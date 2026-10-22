// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"net/http"
	"strconv"
	"strings"
)

// Output side of SelectObjectContent: row encoders (CSV / JSON) and the
// application/vnd.amazon.eventstream framing of the response.

type selRowEncoder interface {
	encode(buf *bytes.Buffer, row selRow)
}

type selJSONEnc struct{ rd string }

func (e selJSONEnc) encode(buf *bytes.Buffer, row selRow) {
	if row.raw != nil {
		var compact bytes.Buffer
		if json.Compact(&compact, row.raw) == nil {
			buf.Write(compact.Bytes())
		} else {
			buf.Write(row.raw)
		}
	} else {
		buf.WriteByte('{')
		for i, v := range row.vals {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(selMarshal(row.names[i]))
			buf.WriteByte(':')
			buf.Write(selJSONValue(v))
		}
		buf.WriteByte('}')
	}
	buf.WriteString(e.rd)
}

func selJSONValue(v selVal) []byte {
	switch v.kind {
	case selKNull:
		return []byte("null")
	case selKBool:
		return []byte(strconv.FormatBool(v.b))
	case selKNum:
		if v.s != "" && json.Valid([]byte(v.s)) {
			return []byte(v.s)
		}
		return []byte(selNumVal(v.f).numText())
	case selKJSON:
		return selMarshal(v.j)
	}
	return selMarshal(v.s)
}

type selCSVEnc struct {
	fd, rd, quote, esc string
	always             bool
}

func (e selCSVEnc) encode(buf *bytes.Buffer, row selRow) {
	for i, v := range row.vals {
		if i > 0 {
			buf.WriteString(e.fd)
		}
		if v.kind == selKNull {
			if e.always {
				buf.WriteString(e.quote + e.quote)
			}
			continue
		}
		text := v.text()
		if e.always || strings.Contains(text, e.fd) || strings.Contains(text, e.rd) ||
			strings.Contains(text, e.quote) || strings.ContainsAny(text, "\r\n") {
			buf.WriteString(e.quote)
			buf.WriteString(strings.ReplaceAll(text, e.quote, e.esc+e.quote))
			buf.WriteString(e.quote)
			continue
		}
		buf.WriteString(text)
	}
	buf.WriteString(e.rd)
}

// selEventMessage frames one event-stream message:
//
//	total length (4) | headers length (4) | CRC32 of those 8 bytes (4)
//	headers | payload | CRC32 of everything before it (4)
//
// Every header is name length (1) | name | type 7 (string) | value length (2) | value.
func selEventMessage(headers [][2]string, payload []byte) []byte {
	var hb bytes.Buffer
	for _, h := range headers {
		hb.WriteByte(byte(len(h[0])))
		hb.WriteString(h[0])
		hb.WriteByte(7)
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(h[1])))
		hb.Write(l[:])
		hb.WriteString(h[1])
	}
	total := 16 + hb.Len() + len(payload)
	msg := make([]byte, 0, total)
	var pre [12]byte
	binary.BigEndian.PutUint32(pre[0:4], uint32(total))
	binary.BigEndian.PutUint32(pre[4:8], uint32(hb.Len()))
	binary.BigEndian.PutUint32(pre[8:12], crc32.ChecksumIEEE(pre[:8]))
	msg = append(msg, pre[:]...)
	msg = append(msg, hb.Bytes()...)
	msg = append(msg, payload...)
	var tail [4]byte
	binary.BigEndian.PutUint32(tail[:], crc32.ChecksumIEEE(msg))
	return append(msg, tail[:]...)
}

func selEventHeaders(kind, contentType string) [][2]string {
	h := [][2]string{{":message-type", "event"}, {":event-type", kind}}
	if contentType != "" {
		h = append(h, [2]string{":content-type", contentType})
	}
	return h
}

const selFlushSize = 64 << 10

// selStream writes the event-stream response. Nothing is sent to the client
// before the first Records flush (or the end), so an error that happens early
// can still be answered with a normal HTTP error.
type selStream struct {
	w         http.ResponseWriter
	flusher   http.Flusher
	enc       selRowEncoder
	progress  bool
	scanned   *selCounter
	processed *selCounter
	pending   bytes.Buffer
	returned  int64
	started   bool
	werr      error
}

func newSelStream(w http.ResponseWriter, enc selRowEncoder, progress bool, scanned, processed *selCounter) *selStream {
	s := &selStream{w: w, enc: enc, progress: progress, scanned: scanned, processed: processed}
	if f, ok := w.(http.Flusher); ok {
		s.flusher = f
	}
	return s
}

func (s *selStream) send(msg []byte) {
	if s.werr != nil {
		return
	}
	if !s.started {
		s.w.Header().Set("Content-Type", "application/octet-stream")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	if _, err := s.w.Write(msg); err != nil {
		s.werr = err
		return
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *selStream) details(root string) []byte {
	return []byte("<" + root + "><Details><BytesScanned>" + strconv.FormatInt(s.scanned.n, 10) +
		"</BytesScanned><BytesProcessed>" + strconv.FormatInt(s.processed.n, 10) +
		"</BytesProcessed><BytesReturned>" + strconv.FormatInt(s.returned, 10) +
		"</BytesReturned></Details></" + root + ">")
}

func (s *selStream) emit(row selRow) error {
	if s.werr != nil {
		return s.werr
	}
	s.enc.encode(&s.pending, row)
	if s.pending.Len() >= selFlushSize {
		s.flushRecords()
	}
	return s.werr
}

func (s *selStream) flushRecords() {
	if s.pending.Len() == 0 {
		return
	}
	payload := append([]byte(nil), s.pending.Bytes()...)
	s.pending.Reset()
	s.returned += int64(len(payload))
	s.send(selEventMessage(selEventHeaders("Records", "application/octet-stream"), payload))
	if s.progress {
		s.send(selEventMessage(selEventHeaders("Progress", "text/xml"), s.details("Progress")))
	}
}

// finish completes the response after selExecute returned runErr.
func (s *selStream) finish(r *http.Request, runErr error) {
	if runErr != nil {
		if s.werr != nil || r.Context().Err() != nil {
			return // the client went away
		}
		apiErr := selAPIError(runErr)
		if !s.started {
			writeError(s.w, r, apiErr)
			return
		}
		s.send(selEventMessage([][2]string{
			{":message-type", "error"},
			{":error-code", apiErr.Code},
			{":error-message", apiErr.Message},
		}, nil))
		return
	}
	s.flushRecords()
	s.send(selEventMessage(selEventHeaders("Stats", "text/xml"), s.details("Stats")))
	s.send(selEventMessage(selEventHeaders("End", ""), nil))
}
