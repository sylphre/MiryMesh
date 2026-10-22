// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

// aws-chunked request bodies (Content-Encoding: aws-chunked).
//
// Recent AWS SDKs upload with X-Amz-Content-Sha256 set to one of
//
//	STREAMING-AWS4-HMAC-SHA256-PAYLOAD           signed chunks
//	STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER   signed chunks + signed trailer
//	STREAMING-UNSIGNED-PAYLOAD-TRAILER           unsigned chunks + trailer
//
// and the body is a sequence of "<hex size>[;chunk-signature=<sig>]\r\n<data>\r\n"
// chunks ending with a zero-size chunk, optionally followed by trailing headers
// (typically x-amz-checksum-*) and a blank line.
//
// The request is authenticated first (sigv4.Verify accepts the markers), then
// prepareStreamingBody replaces r.Body with the decoded payload so handlers see
// the same bytes as for an ordinary PUT. A trailing x-amz-checksum-* is checked
// against the decoded bytes when the stream ends; the handler is told to compute
// and store the same algorithm through x-amz-sdk-checksum-algorithm.

import (
	"bufio"
	"encoding/base64"
	"errors"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/sylphre/MiryMesh/internal/sigv4"
)

const (
	// A signed chunk is held in memory until its signature is verified.
	awsChunkMaxSigned = 16 << 20
	awsChunkMaxLine   = 4096
	awsChunkMaxTrail  = 16 << 10
)

var (
	errChunkNoLength = APIError{
		Code:    "MissingContentLength",
		Message: "The x-amz-decoded-content-length header is required for aws-chunked uploads",
		Status:  http.StatusLengthRequired,
	}
	errChunkIncomplete = APIError{
		Code:    "IncompleteBody",
		Message: "The aws-chunked body does not contain the number of bytes announced by x-amz-decoded-content-length",
		Status:  http.StatusBadRequest,
	}
	errChunkMalformed = APIError{
		Code:    "InvalidRequest",
		Message: "The aws-chunked request body is malformed",
		Status:  http.StatusBadRequest,
	}
)

// awsChunkedReader decodes an aws-chunked body. Errors are APIErrors so the
// handlers answer them like any other failed upload.
type awsChunkedReader struct {
	src        *bufio.Reader
	signer     *sigv4.StreamSigner // nil when the chunks are not signed
	hasTrailer bool
	declared   map[string]bool // lower-case names from x-amz-trailer
	ckAlgo     *ckAlgo         // trailing checksum algorithm, if announced
	ckHash     hash.Hash
	wantLen    int64 // x-amz-decoded-content-length
	total      int64 // decoded bytes accepted so far

	buf    []byte // current verified signed chunk
	pos    int
	remain int64 // unread bytes of the current unsigned chunk
	done   bool
	err    error
}

func (c *awsChunkedReader) fail(err error) error {
	c.err = err
	return err
}

func (c *awsChunkedReader) account(p []byte) {
	c.total += int64(len(p))
	if c.ckHash != nil {
		_, _ = c.ckHash.Write(p)
	}
}

func chunkReadErr(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return errChunkIncomplete
	}
	return err
}

func (c *awsChunkedReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	for {
		switch {
		case c.pos < len(c.buf):
			n := copy(p, c.buf[c.pos:])
			c.pos += n
			return n, nil
		case c.remain > 0:
			if int64(len(p)) > c.remain {
				p = p[:c.remain]
			}
			n, err := c.src.Read(p)
			c.account(p[:n])
			c.remain -= int64(n)
			if err != nil {
				return n, c.fail(chunkReadErr(err))
			}
			if c.remain == 0 {
				if cerr := c.expectCRLF(); cerr != nil {
					return n, c.fail(cerr)
				}
			}
			if n > 0 {
				return n, nil
			}
		case c.done:
			return 0, io.EOF
		default:
			if err := c.nextChunk(); err != nil {
				return 0, c.fail(err)
			}
		}
	}
}

func (c *awsChunkedReader) readLine() (string, error) {
	line, err := c.src.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", errChunkMalformed
		}
		return "", chunkReadErr(err)
	}
	if len(line) > awsChunkMaxLine || len(line) < 2 || line[len(line)-2] != '\r' {
		return "", errChunkMalformed
	}
	return string(line[:len(line)-2]), nil
}

func (c *awsChunkedReader) expectCRLF() error {
	var b [2]byte
	if _, err := io.ReadFull(c.src, b[:]); err != nil {
		return chunkReadErr(err)
	}
	if b[0] != '\r' || b[1] != '\n' {
		return errChunkMalformed
	}
	return nil
}

// nextChunk reads one chunk header. A signed chunk is read and verified as a
// whole; an unsigned one is streamed through Read.
func (c *awsChunkedReader) nextChunk() error {
	line, err := c.readLine()
	if err != nil {
		return err
	}
	sizeStr, ext, _ := strings.Cut(line, ";")
	size, perr := strconv.ParseUint(strings.TrimSpace(sizeStr), 16, 63)
	if perr != nil {
		return errChunkMalformed
	}
	var gotSig string
	if c.signer != nil {
		ext = strings.TrimSpace(ext)
		if !strings.HasPrefix(ext, "chunk-signature=") {
			return errChunkMalformed
		}
		gotSig = strings.TrimPrefix(ext, "chunk-signature=")
	}
	if size == 0 {
		return c.finish(gotSig)
	}
	if int64(size) > c.wantLen-c.total {
		// More data than x-amz-decoded-content-length announced.
		return errChunkIncomplete
	}
	if c.signer == nil {
		c.remain = int64(size)
		return nil
	}
	if size > awsChunkMaxSigned {
		return *p2BadRequest("InvalidRequest", "An aws-chunked chunk is larger than the supported "+strconv.Itoa(awsChunkMaxSigned)+" bytes")
	}
	if uint64(cap(c.buf)) < size {
		c.buf = make([]byte, size)
	} else {
		c.buf = c.buf[:size]
	}
	c.pos = 0
	if _, rerr := io.ReadFull(c.src, c.buf); rerr != nil {
		c.buf = c.buf[:0]
		return chunkReadErr(rerr)
	}
	if cerr := c.expectCRLF(); cerr != nil {
		c.buf = c.buf[:0]
		return cerr
	}
	if !sigv4.SignatureEqual(c.signer.Chunk(c.buf), gotSig) {
		c.buf = c.buf[:0]
		return errSignatureMismatch
	}
	c.account(c.buf)
	return nil
}

// finish handles the zero-size chunk: its signature, the length check and the
// trailing headers.
func (c *awsChunkedReader) finish(finalSig string) error {
	c.done = true
	if c.signer != nil && !sigv4.SignatureEqual(c.signer.Chunk(nil), finalSig) {
		return errSignatureMismatch
	}
	if c.total != c.wantLen {
		return errChunkIncomplete
	}
	if !c.hasTrailer {
		return c.expectCRLF()
	}
	trailers, err := c.readTrailers()
	if err != nil {
		return err
	}
	for name := range c.declared {
		if _, ok := trailers[name]; !ok {
			return errChunkMalformed
		}
	}
	if c.ckHash != nil {
		got := base64.StdEncoding.EncodeToString(c.ckHash.Sum(nil))
		if trailers[strings.ToLower(c.ckAlgo.header)] != got {
			return APIError{
				Code:    "BadDigest",
				Message: "The " + c.ckAlgo.name + " you specified did not match the calculated checksum.",
				Status:  http.StatusBadRequest,
			}
		}
	}
	return nil
}

// readTrailers reads the trailing headers up to the blank line and checks the
// trailer signature of signed streams.
func (c *awsChunkedReader) readTrailers() (map[string]string, error) {
	trailers := map[string]string{}
	var canonical strings.Builder
	var sig string
	for {
		line, err := c.readLine()
		if err != nil {
			return nil, err
		}
		if line == "" {
			break
		}
		if canonical.Len()+len(line) > awsChunkMaxTrail {
			return nil, errChunkMalformed
		}
		name, val, ok := strings.Cut(line, ":")
		if !ok {
			return nil, errChunkMalformed
		}
		name = strings.ToLower(strings.TrimSpace(name))
		val = strings.TrimSpace(val)
		if name == "x-amz-trailer-signature" {
			sig = val
			continue
		}
		if !c.declared[name] {
			return nil, *p2BadRequest("InvalidRequest", "The trailer "+name+" was not announced in x-amz-trailer")
		}
		trailers[name] = val
		canonical.WriteString(name + ":" + val + "\n")
	}
	if c.signer != nil && !sigv4.SignatureEqual(c.signer.Trailer(canonical.String()), sig) {
		return nil, errSignatureMismatch
	}
	return trailers, nil
}

func ckAlgoByHeader(name string) *ckAlgo {
	for _, a := range ckAlgos {
		if strings.EqualFold(a.header, name) {
			return a
		}
	}
	return nil
}

// declareTrailers reads x-amz-trailer. At most one checksum trailer is
// supported.
func (c *awsChunkedReader) declareTrailers(h http.Header) *APIError {
	c.declared = map[string]bool{}
	for _, v := range h.Values("X-Amz-Trailer") {
		for _, name := range strings.Split(v, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			c.declared[name] = true
			if !strings.HasPrefix(name, "x-amz-checksum-") {
				continue
			}
			a := ckAlgoByHeader(name)
			if a == nil || c.ckAlgo != nil {
				return p2BadRequest("InvalidRequest", "Unsupported or duplicate trailing checksum "+name)
			}
			c.ckAlgo = a
			c.ckHash = a.newHash()
		}
	}
	return nil
}

func hasChecksumHeader(h http.Header) bool {
	for _, a := range ckAlgos {
		if h.Get(a.header) != "" {
			return true
		}
	}
	return false
}

// stripAWSChunked removes the aws-chunked token from Content-Encoding: the
// body handed to the handlers is no longer chunk-encoded.
func stripAWSChunked(h http.Header) {
	var keep []string
	for _, v := range h.Values("Content-Encoding") {
		for _, tok := range strings.Split(v, ",") {
			tok = strings.TrimSpace(tok)
			if tok != "" && !strings.EqualFold(tok, "aws-chunked") {
				keep = append(keep, tok)
			}
		}
	}
	if len(keep) == 0 {
		h.Del("Content-Encoding")
		return
	}
	h.Set("Content-Encoding", strings.Join(keep, ", "))
}

type streamBody struct {
	io.Reader
	io.Closer
}

// prepareStreamingBody decodes an authenticated aws-chunked request in place.
// Requests that are not aws-chunked are left alone. Malformed streams surface
// as errors while the handler reads the body.
func prepareStreamingBody(r *http.Request, res sigv4.VerifyResult) *APIError {
	payload := res.PayloadHash
	if !sigv4.IsStreamingPayload(payload) {
		return nil
	}
	raw := strings.TrimSpace(r.Header.Get("X-Amz-Decoded-Content-Length"))
	if raw == "" {
		e := errChunkNoLength
		return &e
	}
	want, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil || want < 0 {
		return p2BadRequest("InvalidRequest", "Invalid x-amz-decoded-content-length")
	}
	cr := &awsChunkedReader{wantLen: want, hasTrailer: sigv4.StreamingHasTrailer(payload)}
	if sigv4.StreamingIsSigned(payload) {
		if cr.signer = res.StreamSigner(); cr.signer == nil {
			return p2BadRequest("InvalidRequest", "The request cannot be used for signed streaming")
		}
	}
	if cr.hasTrailer {
		if aerr := cr.declareTrailers(r.Header); aerr != nil {
			return aerr
		}
		// The handler computes and stores the checksum the trailer announces.
		if cr.ckAlgo != nil && !hasChecksumHeader(r.Header) {
			r.Header.Set("X-Amz-Sdk-Checksum-Algorithm", cr.ckAlgo.name)
		}
	}

	var closer io.Closer = http.NoBody
	var body io.Reader = http.NoBody
	if r.Body != nil {
		closer, body = r.Body, r.Body
	}
	cr.src = bufio.NewReaderSize(body, 32*1024)
	r.Body = streamBody{Reader: cr, Closer: closer}
	r.ContentLength = want
	r.Header.Set("Content-Length", strconv.FormatInt(want, 10))
	stripAWSChunked(r.Header)
	return nil
}
