// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

// Additional checksums and Content-MD5 for PutObject / UploadPart /
// CompleteMultipartUpload / GetObject / HeadObject.
//
// A request body is wrapped in a ckVerifier: the digest is checked when the
// body reaches EOF and a mismatch surfaces as a read error. Storage writes to
// a temp file and renames only on success, so a BadDigest upload never
// replaces an existing object.

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	ckTypeFullObject = "FULL_OBJECT"
	ckTypeComposite  = "COMPOSITE"
)

var (
	crc32cTable = crc32.MakeTable(crc32.Castagnoli)
	// CRC-64/NVME: reflected polynomial of 0xAD93D23594C93659, init and xorout
	// all ones, which is exactly what hash/crc64 computes for a reflected table.
	crc64NVMETable = crc64.MakeTable(0x9a6c9329ac4bc9b5)
)

// ckAlgo describes one S3 additional-checksum algorithm.
type ckAlgo struct {
	name    string // value of x-amz-checksum-algorithm / x-amz-sdk-checksum-algorithm
	header  string // x-amz-checksum-<algo>
	size    int    // raw digest size in bytes
	newHash func() hash.Hash
	// Reflected polynomial and width, used to combine CRCs for FULL_OBJECT
	// multipart checksums. Zero for the SHA family.
	crcPoly  uint64
	crcWidth int
}

var ckAlgos = []*ckAlgo{
	{name: "CRC32", header: "X-Amz-Checksum-Crc32", size: 4, newHash: func() hash.Hash { return crc32.NewIEEE() }, crcPoly: 0xedb88320, crcWidth: 32},
	{name: "CRC32C", header: "X-Amz-Checksum-Crc32c", size: 4, newHash: func() hash.Hash { return crc32.New(crc32cTable) }, crcPoly: 0x82f63b78, crcWidth: 32},
	{name: "CRC64NVME", header: "X-Amz-Checksum-Crc64nvme", size: 8, newHash: func() hash.Hash { return crc64.New(crc64NVMETable) }, crcPoly: 0x9a6c9329ac4bc9b5, crcWidth: 64},
	{name: "SHA1", header: "X-Amz-Checksum-Sha1", size: 20, newHash: sha1.New},
	{name: "SHA256", header: "X-Amz-Checksum-Sha256", size: 32, newHash: sha256.New},
}

func ckAlgoByName(name string) *ckAlgo {
	name = strings.TrimSpace(name)
	for _, a := range ckAlgos {
		if strings.EqualFold(a.name, name) {
			return a
		}
	}
	return nil
}

// ckSumBase64 returns base64(algo(data)).
func ckSumBase64(a *ckAlgo, data []byte) string {
	h := a.newHash()
	_, _ = h.Write(data)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------------------
// Request parsing
// ---------------------------------------------------------------------------

// ckRequest is what a write request asks the gateway to verify and/or store.
type ckRequest struct {
	md5Want []byte  // raw Content-MD5; nil when absent
	algo    *ckAlgo // nil when no additional checksum is requested
	want    []byte  // raw expected digest; nil means "compute and store only"
}

// parseChecksumRequest reads Content-MD5, x-amz-checksum-* and
// x-amz-sdk-checksum-algorithm. It returns nil when nothing is requested.
func parseChecksumRequest(h http.Header) (*ckRequest, *APIError) {
	req := &ckRequest{}
	if v := strings.TrimSpace(h.Get("Content-MD5")); v != "" {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(raw) != md5.Size {
			return nil, p2BadRequest("InvalidDigest", "The Content-MD5 you specified was not valid.")
		}
		req.md5Want = raw
	}
	for _, a := range ckAlgos {
		v := strings.TrimSpace(h.Get(a.header))
		if v == "" {
			continue
		}
		if req.algo != nil {
			return nil, p2BadRequest("InvalidRequest", "Expecting a single x-amz-checksum- header. Multiple checksum Types are not allowed.")
		}
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(raw) != a.size {
			return nil, p2BadRequest("InvalidRequest", "Value for "+strings.ToLower(a.header)+" header is invalid.")
		}
		req.algo, req.want = a, raw
	}
	if v := strings.TrimSpace(h.Get("X-Amz-Sdk-Checksum-Algorithm")); v != "" {
		a := ckAlgoByName(v)
		if a == nil {
			return nil, p2BadRequest("InvalidRequest", "Value for x-amz-sdk-checksum-algorithm header is invalid.")
		}
		if req.algo != nil && req.algo != a {
			return nil, p2BadRequest("InvalidRequest", "Checksum Type mismatch occurred, expected checksum Type: "+strings.ToLower(a.name)+", actual checksum Type: "+strings.ToLower(req.algo.name))
		}
		req.algo = a
	}
	if req.md5Want == nil && req.algo == nil {
		return nil, nil
	}
	return req, nil
}

// parseMultipartChecksum reads x-amz-checksum-algorithm / x-amz-checksum-type
// of CreateMultipartUpload. It returns a nil algo when none was requested.
func parseMultipartChecksum(h http.Header) (*ckAlgo, string, *APIError) {
	name := strings.TrimSpace(h.Get("X-Amz-Checksum-Algorithm"))
	typ := strings.ToUpper(strings.TrimSpace(h.Get("X-Amz-Checksum-Type")))
	if name == "" {
		if typ != "" {
			return nil, "", p2BadRequest("InvalidRequest", "The x-amz-checksum-type header can only be used with the x-amz-checksum-algorithm header.")
		}
		return nil, "", nil
	}
	a := ckAlgoByName(name)
	if a == nil {
		return nil, "", p2BadRequest("InvalidRequest", "Checksum algorithm provided is unsupported. Please try again with any of the valid types: [CRC32, CRC32C, CRC64NVME, SHA1, SHA256]")
	}
	switch typ {
	case "":
		typ = ckTypeComposite
		if a.name == "CRC64NVME" {
			typ = ckTypeFullObject
		}
	case ckTypeComposite:
		if a.name == "CRC64NVME" {
			return nil, "", p2BadRequest("InvalidRequest", "The COMPOSITE checksum type cannot be used with the crc64nvme checksum algorithm.")
		}
	case ckTypeFullObject:
		if a.crcWidth == 0 {
			return nil, "", p2BadRequest("InvalidRequest", "The FULL_OBJECT checksum type cannot be used with the "+strings.ToLower(a.name)+" checksum algorithm.")
		}
	default:
		return nil, "", p2BadRequest("InvalidRequest", "Value for x-amz-checksum-type header is invalid.")
	}
	return a, typ, nil
}

// ---------------------------------------------------------------------------
// Streaming verification
// ---------------------------------------------------------------------------

// ckVerifier hashes everything read through it and, at EOF, compares the
// result with the request. A mismatch is returned as an APIError read error.
type ckVerifier struct {
	src  io.Reader
	req  *ckRequest
	md5h hash.Hash
	h    hash.Hash
	done bool
	sum  []byte
}

func newCkVerifier(src io.Reader, req *ckRequest) *ckVerifier {
	v := &ckVerifier{src: src, req: req}
	if req.md5Want != nil {
		v.md5h = md5.New()
	}
	if req.algo != nil {
		v.h = req.algo.newHash()
	}
	return v
}

func (v *ckVerifier) Read(p []byte) (int, error) {
	n, err := v.src.Read(p)
	if n > 0 {
		if v.md5h != nil {
			_, _ = v.md5h.Write(p[:n])
		}
		if v.h != nil {
			_, _ = v.h.Write(p[:n])
		}
	}
	if err == io.EOF && !v.done {
		v.done = true
		if bad := v.finish(); bad != nil {
			return n, *bad
		}
	}
	return n, err
}

func (v *ckVerifier) finish() *APIError {
	if v.md5h != nil && !bytes.Equal(v.md5h.Sum(nil), v.req.md5Want) {
		return &APIError{Code: "BadDigest", Message: "The Content-MD5 you specified did not match what we received.", Status: http.StatusBadRequest}
	}
	if v.h != nil {
		v.sum = v.h.Sum(nil)
		if v.req.want != nil && !bytes.Equal(v.sum, v.req.want) {
			return &APIError{Code: "BadDigest", Message: "The " + v.req.algo.name + " you specified did not match the calculated checksum.", Status: http.StatusBadRequest}
		}
	}
	return nil
}

// raw returns the raw digest once the body was fully read; nil-safe.
func (v *ckVerifier) raw() []byte {
	if v == nil {
		return nil
	}
	return v.sum
}

// record returns the whole-object checksum record; nil-safe.
func (v *ckVerifier) record() *ckRecord {
	if v == nil || v.sum == nil || v.req.algo == nil {
		return nil
	}
	return &ckRecord{Algo: v.req.algo.name, Value: base64.StdEncoding.EncodeToString(v.sum), Type: ckTypeFullObject}
}

// ---------------------------------------------------------------------------
// Stored checksum record
// ---------------------------------------------------------------------------

// ckRecord is the checksum stored for an object (subresource kind "checksum").
type ckRecord struct {
	Algo  string `json:"algo"`
	Value string `json:"value"` // base64; COMPOSITE values carry the "-N" suffix
	Type  string `json:"type"`
}

// setHeaders emits x-amz-checksum-<algo> and x-amz-checksum-type.
func (c *ckRecord) setHeaders(h http.Header) {
	if c == nil {
		return
	}
	if a := ckAlgoByName(c.Algo); a != nil {
		h.Set(a.header, c.Value)
		h.Set("X-Amz-Checksum-Type", c.Type)
	}
}

// ChecksumXML carries the Checksum* elements of a CompleteMultipartUpload
// request part and of its response.
type ChecksumXML struct {
	ChecksumCRC32     string `xml:"ChecksumCRC32,omitempty"`
	ChecksumCRC32C    string `xml:"ChecksumCRC32C,omitempty"`
	ChecksumCRC64NVME string `xml:"ChecksumCRC64NVME,omitempty"`
	ChecksumSHA1      string `xml:"ChecksumSHA1,omitempty"`
	ChecksumSHA256    string `xml:"ChecksumSHA256,omitempty"`
}

// valueFor returns the client-supplied value for the named algorithm.
func (x ChecksumXML) valueFor(algo string) string {
	switch strings.ToUpper(algo) {
	case "CRC32":
		return x.ChecksumCRC32
	case "CRC32C":
		return x.ChecksumCRC32C
	case "CRC64NVME":
		return x.ChecksumCRC64NVME
	case "SHA1":
		return x.ChecksumSHA1
	case "SHA256":
		return x.ChecksumSHA256
	}
	return ""
}

func (c *ckRecord) toXML() ChecksumXML {
	var out ChecksumXML
	if c == nil {
		return out
	}
	switch strings.ToUpper(c.Algo) {
	case "CRC32":
		out.ChecksumCRC32 = c.Value
	case "CRC32C":
		out.ChecksumCRC32C = c.Value
	case "CRC64NVME":
		out.ChecksumCRC64NVME = c.Value
	case "SHA1":
		out.ChecksumSHA1 = c.Value
	case "SHA256":
		out.ChecksumSHA256 = c.Value
	}
	return out
}

// ---------------------------------------------------------------------------
// Multipart aggregation
// ---------------------------------------------------------------------------

// combineMultipartChecksum derives the object checksum from the raw digests of
// its parts (in Complete order). COMPOSITE is algo(concat(digests)) plus a
// "-N" suffix; FULL_OBJECT combines the CRCs without re-reading the data.
func combineMultipartChecksum(a *ckAlgo, typ string, raws [][]byte, sizes []int64) (string, error) {
	if len(raws) == 0 || len(raws) != len(sizes) {
		return "", errors.New("checksum: no parts")
	}
	for _, r := range raws {
		if len(r) != a.size {
			return "", errors.New("checksum: part digest has the wrong length")
		}
	}
	switch typ {
	case ckTypeComposite:
		h := a.newHash()
		for _, r := range raws {
			_, _ = h.Write(r)
		}
		return base64.StdEncoding.EncodeToString(h.Sum(nil)) + "-" + strconv.Itoa(len(raws)), nil
	case ckTypeFullObject:
		if a.crcWidth == 0 {
			return "", errors.New("checksum: FULL_OBJECT is not supported for " + a.name)
		}
		acc := beUint(raws[0])
		for i := 1; i < len(raws); i++ {
			acc = crcCombine(acc, beUint(raws[i]), sizes[i], a.crcPoly, a.crcWidth)
		}
		return base64.StdEncoding.EncodeToString(putBE(acc, a.size)), nil
	}
	return "", errors.New("checksum: unknown checksum type " + typ)
}

func beUint(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

func putBE(v uint64, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(v >> (8 * uint(n-1-i)))
	}
	return out
}

func gf2MatrixTimes(mat []uint64, vec uint64) uint64 {
	var sum uint64
	for i := 0; vec != 0; i, vec = i+1, vec>>1 {
		if vec&1 != 0 {
			sum ^= mat[i]
		}
	}
	return sum
}

func gf2MatrixSquare(square, mat []uint64) {
	for n := range mat {
		square[n] = gf2MatrixTimes(mat, mat[n])
	}
}

// crcCombine returns the CRC of A||B given crc(A), crc(B) and len(B). It is
// zlib's crc32_combine generalised to any reflected CRC of 32 or 64 bits whose
// init and xorout are all ones (CRC-32, CRC-32C, CRC-64/NVME).
func crcCombine(crc1, crc2 uint64, len2 int64, poly uint64, width int) uint64 {
	if len2 <= 0 {
		return crc1
	}
	even := make([]uint64, width)
	odd := make([]uint64, width)
	odd[0] = poly
	row := uint64(1)
	for n := 1; n < width; n++ {
		odd[n] = row
		row <<= 1
	}
	gf2MatrixSquare(even, odd) // operator for two zero bits
	gf2MatrixSquare(odd, even) // operator for four zero bits
	for {
		gf2MatrixSquare(even, odd)
		if len2&1 != 0 {
			crc1 = gf2MatrixTimes(even, crc1)
		}
		len2 >>= 1
		if len2 == 0 {
			break
		}
		gf2MatrixSquare(odd, even)
		if len2&1 != 0 {
			crc1 = gf2MatrixTimes(odd, crc1)
		}
		len2 >>= 1
		if len2 == 0 {
			break
		}
	}
	return crc1 ^ crc2
}
