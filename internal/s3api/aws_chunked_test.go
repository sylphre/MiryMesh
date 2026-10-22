// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// chunkedSpec describes one aws-chunked upload.
type chunkedSpec struct {
	payloadHash string
	chunkSize   int
	trailers    [][2]string // name, value — sent in this order
	declare     string      // X-Amz-Trailer
	decodedLen  string      // x-amz-decoded-content-length override; "-" omits the header
	mutate      func(body []byte) []byte
}

func buildAWSChunked(ss *sigv4.StreamSigner, spec chunkedSpec, payload []byte) []byte {
	signed := sigv4.StreamingIsSigned(spec.payloadHash)
	size := spec.chunkSize
	if size <= 0 {
		size = 64 * 1024
	}
	var b bytes.Buffer
	for off := 0; off < len(payload); off += size {
		end := off + size
		if end > len(payload) {
			end = len(payload)
		}
		data := payload[off:end]
		if signed {
			fmt.Fprintf(&b, "%x;chunk-signature=%s\r\n", len(data), ss.Chunk(data))
		} else {
			fmt.Fprintf(&b, "%x\r\n", len(data))
		}
		b.Write(data)
		b.WriteString("\r\n")
	}
	if signed {
		fmt.Fprintf(&b, "0;chunk-signature=%s\r\n", ss.Chunk(nil))
	} else {
		b.WriteString("0\r\n")
	}
	if sigv4.StreamingHasTrailer(spec.payloadHash) {
		var canonical strings.Builder
		for _, t := range spec.trailers {
			fmt.Fprintf(&b, "%s:%s\r\n", t[0], t[1])
			fmt.Fprintf(&canonical, "%s:%s\n", t[0], t[1])
		}
		if signed {
			fmt.Fprintf(&b, "x-amz-trailer-signature:%s\r\n", ss.Trailer(canonical.String()))
		}
	}
	b.WriteString("\r\n")
	return b.Bytes()
}

func (e *integrityEnv) streamDo(method, path string, payload []byte, spec chunkedSpec, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	amzDate := time.Now().UTC().Format("20060102T150405Z")
	req := httptest.NewRequest(method, "http://example.com"+path, nil)
	seed := e.sg.Sign(req, amzDate, spec.payloadHash)
	body := buildAWSChunked(e.sg.NewStreamSigner(amzDate, seed), spec, payload)
	if spec.mutate != nil {
		body = spec.mutate(body)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	decoded := strconv.Itoa(len(payload))
	if spec.decodedLen != "" {
		decoded = spec.decodedLen
	}
	if decoded != "-" {
		req.Header.Set("X-Amz-Decoded-Content-Length", decoded)
	}
	req.Header.Set("Content-Encoding", "aws-chunked")
	if spec.declare != "" {
		req.Header.Set("X-Amz-Trailer", spec.declare)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return doReq(e.rt, e.as, req)
}

func (e *integrityEnv) streamPut(key string, payload []byte, spec chunkedSpec, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.streamDo("PUT", e.objPath(key), payload, spec, hdr)
}

func trailerFor(algoName string, payload []byte) (declare string, trailers [][2]string) {
	a := ckAlgoByName(algoName)
	name := strings.ToLower(a.header)
	return name, [][2]string{{name, ckSumBase64(a, payload)}}
}

func TestAWSChunkedRoundTrip(t *testing.T) {
	e := newIntegrityEnv(t)
	payload := bytes.Repeat([]byte("0123456789"), 5000) // 50000 bytes, several chunks

	crcDecl, crcTrail := trailerFor("CRC32", payload)
	shaDecl, shaTrail := trailerFor("SHA256", payload)
	cases := []struct {
		name string
		spec chunkedSpec
		algo string // checksum that must be stored, "" for none
	}{
		{"signed", chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, chunkSize: 8192}, ""},
		{"signed one chunk", chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned}, ""},
		{"signed+trailer crc32", chunkedSpec{payloadHash: sigv4.PayloadStreamingSignedTrailer, chunkSize: 8192, declare: crcDecl, trailers: crcTrail}, "CRC32"},
		{"unsigned+trailer sha256", chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, chunkSize: 4096, declare: shaDecl, trailers: shaTrail}, "SHA256"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			key := "chunked-" + strings.ReplaceAll(tc.name, " ", "-")
			if rr := e.streamPut(key, payload, tc.spec, nil); rr.Code != http.StatusOK {
				t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
			}
			got := e.do("GET", e.objPath(key), nil, map[string]string{"X-Amz-Checksum-Mode": "ENABLED"})
			if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), payload) {
				t.Fatalf("GET status=%d, decoded payload differs (len=%d want %d)", got.Code, got.Body.Len(), len(payload))
			}
			if tc.algo != "" {
				a := ckAlgoByName(tc.algo)
				if v := got.Header().Get(a.header); v != ckSumBase64(a, payload) {
					t.Fatalf("stored %s=%q want %q", a.header, v, ckSumBase64(a, payload))
				}
			}
		})
	}
}

func TestAWSChunkedEmptyPayload(t *testing.T) {
	e := newIntegrityEnv(t)
	for _, marker := range []string{sigv4.PayloadStreamingSigned, sigv4.PayloadStreamingUnsignedTrailer} {
		key := "empty-" + strings.ToLower(marker)
		if rr := e.streamPut(key, nil, chunkedSpec{payloadHash: marker}, nil); rr.Code != http.StatusOK {
			t.Fatalf("%s: PUT status=%d body=%s", marker, rr.Code, rr.Body.String())
		}
		if head := e.do("HEAD", e.objPath(key), nil, nil); head.Code != http.StatusOK || head.Header().Get("Content-Length") != "0" {
			t.Fatalf("%s: HEAD status=%d length=%q", marker, head.Code, head.Header().Get("Content-Length"))
		}
	}
}

func TestAWSChunkedRejectsBadStreams(t *testing.T) {
	e := newIntegrityEnv(t)
	payload := bytes.Repeat([]byte("abcdefghij"), 1000)
	crcDecl, crcTrail := trailerFor("CRC32", payload)
	_, wrongTrail := trailerFor("CRC32", []byte("something else"))

	cases := []struct {
		name   string
		spec   chunkedSpec
		status int
		code   string
	}{
		{
			"tampered chunk data",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, chunkSize: 4096, mutate: func(b []byte) []byte {
				i := bytes.Index(b, []byte("\r\n")) + 2 // first byte of the first chunk
				b[i] ^= 0xFF
				return b
			}},
			http.StatusForbidden, "SignatureDoesNotMatch",
		},
		{
			"signed trailer tampered",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSignedTrailer, declare: crcDecl, trailers: crcTrail, mutate: func(b []byte) []byte {
				return bytes.Replace(b, []byte("x-amz-checksum-crc32:"), []byte("x-amz-checksum-crc32:A"), 1)
			}},
			http.StatusForbidden, "SignatureDoesNotMatch",
		},
		{
			"wrong trailing checksum",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, declare: crcDecl, trailers: wrongTrail},
			http.StatusBadRequest, "BadDigest",
		},
		{
			"truncated body",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, chunkSize: 4096, mutate: func(b []byte) []byte { return b[:len(b)-30] }},
			http.StatusBadRequest, "IncompleteBody",
		},
		{
			"decoded length too large",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, decodedLen: strconv.Itoa(len(payload) + 5)},
			http.StatusBadRequest, "IncompleteBody",
		},
		{
			"decoded length too small",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, decodedLen: strconv.Itoa(len(payload) - 1)},
			http.StatusBadRequest, "IncompleteBody",
		},
		{
			"missing decoded length",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, decodedLen: "-"},
			http.StatusLengthRequired, "MissingContentLength",
		},
		{
			"invalid decoded length",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, decodedLen: "many"},
			http.StatusBadRequest, "InvalidRequest",
		},
		{
			"trailer not announced",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, trailers: crcTrail},
			http.StatusBadRequest, "InvalidRequest",
		},
		{
			"announced trailer missing",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, declare: crcDecl},
			http.StatusBadRequest, "InvalidRequest",
		},
		{
			"two trailing checksums",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, declare: "x-amz-checksum-crc32,x-amz-checksum-sha256"},
			http.StatusBadRequest, "InvalidRequest",
		},
		{
			"malformed chunk size",
			chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, mutate: func(b []byte) []byte {
				return append([]byte("zz\r\n"), b[bytes.Index(b, []byte("\r\n"))+2:]...)
			}},
			http.StatusBadRequest, "InvalidRequest",
		},
	}
	for i, tc := range cases {
		key := fmt.Sprintf("bad-%d", i)
		rr := e.streamPut(key, payload, tc.spec, nil)
		if rr.Code != tc.status || !strings.Contains(rr.Body.String(), "<Code>"+tc.code+"</Code>") {
			t.Errorf("%s: status=%d want %d code %s, body=%s", tc.name, rr.Code, tc.status, tc.code, rr.Body.String())
			continue
		}
		// A rejected upload must not leave an object behind.
		if head := e.do("HEAD", e.objPath(key), nil, nil); head.Code != http.StatusNotFound {
			t.Errorf("%s: object stored despite the error (HEAD=%d)", tc.name, head.Code)
		}
	}
}

func TestAWSChunkedKeepsExistingObjectOnFailure(t *testing.T) {
	e := newIntegrityEnv(t)
	e.mustPut("keep", []byte("original"), nil)
	payload := []byte("replacement that never completes")
	rr := e.streamPut("keep", payload, chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, mutate: func(b []byte) []byte {
		b[bytes.Index(b, []byte("\r\n"))+2] ^= 0xFF
		return b
	}}, nil)
	wantErr(t, rr, http.StatusForbidden, "SignatureDoesNotMatch")
	if got := e.do("GET", e.objPath("keep"), nil, nil); got.Body.String() != "original" {
		t.Fatalf("object replaced by a failed upload: %q", got.Body.String())
	}
}

func TestAWSChunkedWithSSECAndTrailerChecksum(t *testing.T) {
	e := newIntegrityEnv(t)
	k := newTestKey(31)
	payload := bytes.Repeat([]byte("secret and checksummed "), 300)
	decl, trailers := trailerFor("CRC32C", payload)
	spec := chunkedSpec{payloadHash: sigv4.PayloadStreamingSignedTrailer, chunkSize: 1000, declare: decl, trailers: trailers}
	if rr := e.streamPut("enc-chunked", payload, spec, k.headers()); rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	got := e.do("GET", e.objPath("enc-chunked"), nil, merge(k.headers(), map[string]string{"X-Amz-Checksum-Mode": "ENABLED"}))
	a := ckAlgoByName("CRC32C")
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), payload) || got.Header().Get(a.header) != ckSumBase64(a, payload) {
		t.Fatalf("GET status=%d, plaintext/checksum mismatch (checksum=%q)", got.Code, got.Header().Get(a.header))
	}
	wantErr(t, e.do("GET", e.objPath("enc-chunked"), nil, nil), http.StatusBadRequest, "InvalidRequest")
}

func TestAWSChunkedUploadPart(t *testing.T) {
	e := newIntegrityEnv(t)
	rr := e.do("POST", e.objPath("mp-chunked")+"?uploads", nil, map[string]string{"X-Amz-Checksum-Algorithm": "CRC32"})
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(rr.Body.Bytes(), &init); err != nil || init.UploadID == "" {
		t.Fatalf("Create failed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	payload := bytes.Repeat([]byte("part body "), 400)
	decl, trailers := trailerFor("CRC32", payload)
	spec := chunkedSpec{payloadHash: sigv4.PayloadStreamingUnsignedTrailer, chunkSize: 700, declare: decl, trailers: trailers}
	part := e.streamDo("PUT", e.objPath("mp-chunked")+"?partNumber=1&uploadId="+init.UploadID, payload, spec, nil)
	if part.Code != http.StatusOK {
		t.Fatalf("UploadPart status=%d body=%s", part.Code, part.Body.String())
	}
	if done := e.complete("mp-chunked", init.UploadID, []*httptest.ResponseRecorder{part}, nil); done.Code != http.StatusOK {
		t.Fatalf("Complete status=%d body=%s", done.Code, done.Body.String())
	}
	if got := e.do("GET", e.objPath("mp-chunked"), nil, nil); !bytes.Equal(got.Body.Bytes(), payload) {
		t.Fatalf("multipart object differs from the decoded part (len=%d want %d)", got.Body.Len(), len(payload))
	}
}

func TestAWSChunkedContentEncodingStripped(t *testing.T) {
	req := httptest.NewRequest("PUT", "http://example.com/b/k", nil)
	req.Header.Add("Content-Encoding", "gzip, aws-chunked")
	stripAWSChunked(req.Header)
	if got := req.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding=%q want gzip", got)
	}
	req.Header.Set("Content-Encoding", "aws-chunked")
	stripAWSChunked(req.Header)
	if _, ok := req.Header["Content-Encoding"]; ok {
		t.Fatal("Content-Encoding must be removed when only aws-chunked was listed")
	}
}

// A signed chunk larger than awsChunkBufMax is streamed and verified at its end.
func TestAWSChunkedLargeSignedChunk(t *testing.T) {
	e := newIntegrityEnv(t)
	payload := bytes.Repeat([]byte("0123456789abcdef"), (awsChunkBufMax+(1<<20))/16) // 17 MiB
	decl, trailers := trailerFor("CRC32", payload)

	cases := []struct {
		name string
		spec chunkedSpec
	}{
		{"signed one chunk", chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, chunkSize: len(payload)}},
		{"signed two chunks", chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, chunkSize: len(payload)/2 + 1}},
		{"signed+trailer", chunkedSpec{payloadHash: sigv4.PayloadStreamingSignedTrailer, chunkSize: len(payload), declare: decl, trailers: trailers}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			key := "large-" + strings.ReplaceAll(tc.name, " ", "-")
			if rr := e.streamPut(key, payload, tc.spec, nil); rr.Code != http.StatusOK {
				t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
			}
			got := e.do("GET", e.objPath(key), nil, nil)
			if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), payload) {
				t.Fatalf("GET status=%d, payload differs (len=%d want %d)", got.Code, got.Body.Len(), len(payload))
			}
		})
	}

	t.Run("tampered data is rejected and the old object is kept", func(t *testing.T) {
		e.mustPut("large-keep", []byte("original"), nil)
		spec := chunkedSpec{payloadHash: sigv4.PayloadStreamingSigned, chunkSize: len(payload), mutate: func(b []byte) []byte {
			b[len(b)/2] ^= 0xFF
			return b
		}}
		wantErr(t, e.streamPut("large-keep", payload, spec, nil), http.StatusForbidden, "SignatureDoesNotMatch")
		if got := e.do("GET", e.objPath("large-keep"), nil, nil); got.Body.String() != "original" {
			t.Fatalf("object replaced by a failed upload (len=%d)", got.Body.Len())
		}
	})
}
