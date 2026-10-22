// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

// SSE-C (server-side encryption with customer-provided keys).
//
// The blob on disk is AES-256-CTR ciphertext, so its length equals the
// plaintext length (Size, Range and Content-Length keep working). The random
// per-object (or per-part) IVs live in the object's "sse-c" subresource next
// to the MD5 of the key; the key itself is never stored.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
)

// sseKey is a validated customer key.
type sseKey struct {
	key []byte
	md5 string // base64(md5(key))
}

// sseHeaders names the three headers of one SSE-C header family.
type sseHeaders struct{ algo, key, keyMD5 string }

var (
	sseReqHeaders = sseHeaders{
		"X-Amz-Server-Side-Encryption-Customer-Algorithm",
		"X-Amz-Server-Side-Encryption-Customer-Key",
		"X-Amz-Server-Side-Encryption-Customer-Key-Md5",
	}
	sseCopyHeaders = sseHeaders{
		"X-Amz-Copy-Source-Server-Side-Encryption-Customer-Algorithm",
		"X-Amz-Copy-Source-Server-Side-Encryption-Customer-Key",
		"X-Amz-Copy-Source-Server-Side-Encryption-Customer-Key-Md5",
	}
)

// parse validates the header triple. It returns (nil, nil) when none of the
// three headers is present.
func (hs sseHeaders) parse(h http.Header) (*sseKey, *APIError) {
	algo, key, sum := h.Get(hs.algo), h.Get(hs.key), h.Get(hs.keyMD5)
	if algo == "" && key == "" && sum == "" {
		return nil, nil
	}
	if algo == "" || key == "" || sum == "" {
		return nil, p2BadRequest("InvalidArgument", "Requests specifying Server Side Encryption with Customer provided keys must provide an appropriate secret key, algorithm and key MD5.")
	}
	if algo != "AES256" {
		return nil, p2BadRequest("InvalidArgument", "The Encryption request you specified is not valid. Supported value: AES256.")
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 {
		return nil, p2BadRequest("InvalidArgument", "The secret key was invalid for the specified algorithm.")
	}
	want, err := base64.StdEncoding.DecodeString(sum)
	got := md5.Sum(raw)
	if err != nil || subtle.ConstantTimeCompare(got[:], want) != 1 {
		return nil, p2BadRequest("InvalidArgument", "The calculated MD5 hash of the key did not match the hash that was provided.")
	}
	return &sseKey{key: raw, md5: base64.StdEncoding.EncodeToString(got[:])}, nil
}

// sseEcho sets the SSE-C response headers.
func sseEcho(h http.Header, md5b64 string) {
	h.Set(sseReqHeaders.algo, "AES256")
	h.Set(sseReqHeaders.keyMD5, md5b64)
}

// ---------------------------------------------------------------------------
// Stored record
// ---------------------------------------------------------------------------

// sseSegment is one contiguous stretch of the blob encrypted under its own IV.
// A single PUT has one segment; a multipart object has one per part. A
// segment ends where the next one starts (the last one at the object size).
type sseSegment struct {
	Off int64  `json:"off"`
	IV  string `json:"iv"` // base64, 16 bytes
}

// sseRecord is the object's "sse-c" subresource document.
type sseRecord struct {
	KeyMD5   string       `json:"key_md5"`
	Segments []sseSegment `json:"segments"`
}

var (
	errSSEMissingKey = APIError{Code: "InvalidRequest", Message: "The object was stored using a form of Server Side Encryption. The correct parameters must be provided to retrieve the object.", Status: http.StatusBadRequest}
	errSSENotApplied = APIError{Code: "InvalidRequest", Message: "The encryption parameters are not applicable to this object.", Status: http.StatusBadRequest}
	errSSEWrongKey   = APIError{Code: "AccessDenied", Message: "The provided encryption key does not match the key used to encrypt the object.", Status: http.StatusForbidden}
)

// sseAuthorize checks the SSE-C headers of a read against the stored record.
// It returns the key to decrypt with, or nil for an unencrypted object.
func sseAuthorize(h http.Header, hs sseHeaders, rec *sseRecord) (*sseKey, *APIError) {
	k, perr := hs.parse(h)
	if perr != nil {
		return nil, perr
	}
	if rec == nil {
		if k != nil {
			e := errSSENotApplied
			return nil, &e
		}
		return nil, nil
	}
	if k == nil {
		e := errSSEMissingKey
		return nil, &e
	}
	if subtle.ConstantTimeCompare([]byte(k.md5), []byte(rec.KeyMD5)) != 1 {
		e := errSSEWrongKey
		return nil, &e
	}
	return k, nil
}

// ---------------------------------------------------------------------------
// Encryption
// ---------------------------------------------------------------------------

// sseEncrypter encrypts one stream (a whole object or a single part).
type sseEncrypter struct {
	iv     []byte
	stream cipher.Stream
}

func newSSEEncrypter(k *sseKey) (*sseEncrypter, error) {
	block, err := aes.NewCipher(k.key)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	return &sseEncrypter{iv: iv, stream: cipher.NewCTR(block, iv)}, nil
}

// segment describes this stream when it starts at off in the blob.
func (e *sseEncrypter) segment(off int64) sseSegment {
	return sseSegment{Off: off, IV: base64.StdEncoding.EncodeToString(e.iv)}
}

// wrap returns a reader yielding the ciphertext of src.
func (e *sseEncrypter) wrap(src io.Reader) io.Reader {
	return &sseEncReader{src: src, stream: e.stream}
}

type sseEncReader struct {
	src    io.Reader
	stream cipher.Stream
}

func (e *sseEncReader) Read(p []byte) (int, error) {
	n, err := e.src.Read(p)
	if n > 0 {
		e.stream.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

// ---------------------------------------------------------------------------
// Decryption
// ---------------------------------------------------------------------------

// ivAdd returns iv + n, treating iv as a 128-bit big-endian counter (the same
// way cipher.NewCTR increments it).
func ivAdd(iv []byte, n uint64) []byte {
	out := make([]byte, len(iv))
	copy(out, iv)
	carry := n
	for i := len(out) - 1; i >= 0 && carry > 0; i-- {
		sum := uint64(out[i]) + (carry & 0xff)
		out[i] = byte(sum)
		carry = (carry >> 8) + (sum >> 8)
	}
	return out
}

type sseDecReader struct {
	rc     io.ReadCloser
	block  cipher.Block
	segs   []sseSegment
	ivs    [][]byte
	size   int64
	pos    int64
	stream cipher.Stream
	cur    int // segment backing stream; -1 when none
}

// sseDecSeeker adds Seek when the underlying reader supports it, so Range
// reads keep using the seek path of the GetObject handler.
type sseDecSeeker struct{ *sseDecReader }

// newSSEDecReader wraps rc (the raw blob) so reads return plaintext.
func newSSEDecReader(rc io.ReadCloser, key []byte, rec *sseRecord, size int64) (io.ReadCloser, error) {
	if rec == nil || len(rec.Segments) == 0 {
		return nil, errors.New("sse-c: corrupt encryption record")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ivs := make([][]byte, len(rec.Segments))
	for i, s := range rec.Segments {
		iv, derr := base64.StdEncoding.DecodeString(s.IV)
		if derr != nil || len(iv) != aes.BlockSize || s.Off < 0 || (i > 0 && s.Off <= rec.Segments[i-1].Off) {
			return nil, errors.New("sse-c: corrupt encryption record")
		}
		ivs[i] = iv
	}
	d := &sseDecReader{rc: rc, block: block, segs: rec.Segments, ivs: ivs, size: size, cur: -1}
	if _, ok := rc.(io.Seeker); ok {
		return &sseDecSeeker{d}, nil
	}
	return d, nil
}

func (d *sseDecReader) segEnd(i int) int64 {
	if i+1 < len(d.segs) {
		return d.segs[i+1].Off
	}
	return d.size
}

func (d *sseDecReader) segmentAt(pos int64) int {
	for i := range d.segs {
		if pos >= d.segs[i].Off && pos < d.segEnd(i) {
			return i
		}
	}
	return -1
}

// startStream builds the keystream for segment idx positioned at d.pos.
func (d *sseDecReader) startStream(idx int) {
	off := d.pos - d.segs[idx].Off
	d.stream = cipher.NewCTR(d.block, ivAdd(d.ivs[idx], uint64(off/aes.BlockSize)))
	if skip := int(off % aes.BlockSize); skip > 0 {
		var scratch [aes.BlockSize]byte
		d.stream.XORKeyStream(scratch[:skip], scratch[:skip])
	}
	d.cur = idx
}

func (d *sseDecReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.pos >= d.size {
		return 0, io.EOF
	}
	idx := d.segmentAt(d.pos)
	if idx < 0 {
		return 0, errors.New("sse-c: corrupt encryption record")
	}
	if d.stream == nil || d.cur != idx {
		d.startStream(idx)
	}
	if remain := d.segEnd(idx) - d.pos; int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := d.rc.Read(p)
	if n > 0 {
		d.stream.XORKeyStream(p[:n], p[:n])
		d.pos += int64(n)
	}
	if err == io.EOF && d.pos < d.size {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (d *sseDecReader) Close() error { return d.rc.Close() }

func (s *sseDecSeeker) Seek(offset int64, whence int) (int64, error) {
	var np int64
	switch whence {
	case io.SeekStart:
		np = offset
	case io.SeekCurrent:
		np = s.pos + offset
	case io.SeekEnd:
		np = s.size + offset
	default:
		return 0, errors.New("sse-c: invalid whence")
	}
	if np < 0 {
		return 0, errors.New("sse-c: negative position")
	}
	if _, err := s.rc.(io.Seeker).Seek(np, io.SeekStart); err != nil {
		return 0, err
	}
	s.pos, s.stream, s.cur = np, nil, -1
	return np, nil
}
