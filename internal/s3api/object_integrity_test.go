// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type integrityEnv struct {
	t      *testing.T
	rt     *Router
	as     *auth.Store
	sg     *sigv4.Signer
	bucket string
}

func newIntegrityEnv(t *testing.T) *integrityEnv {
	t.Helper()
	rt, as, sg := newTestServer(t)
	e := &integrityEnv{t: t, rt: rt, as: as, sg: sg, bucket: "intbkt"}
	if rr := e.do("PUT", "/"+e.bucket, nil, nil); rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket status=%d body=%s", rr.Code, rr.Body.String())
	}
	return e
}

// do signs and serves one request; extra headers are set after signing, as
// the existing Range test does.
func (e *integrityEnv) do(method, path string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := signedReq(e.t, e.sg, method, "http://example.com"+path, body)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return doReq(e.rt, e.as, req)
}

func (e *integrityEnv) objPath(key string) string { return "/" + e.bucket + "/" + key }

func (e *integrityEnv) mustPut(key string, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	rr := e.do("PUT", e.objPath(key), body, hdr)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("PUT %s status=%d body=%s", key, rr.Code, rr.Body.String())
	}
	return rr
}

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func wantErr(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status=%d want %d (body=%s)", rr.Code, status, rr.Body.String())
	}
	if code != "" && !strings.Contains(rr.Body.String(), "<Code>"+code+"</Code>") {
		t.Fatalf("want error code %s, body=%s", code, rr.Body.String())
	}
}

type testKey struct {
	raw    []byte
	b64    string
	md5b64 string
}

func newTestKey(seed byte) testKey {
	raw := bytes.Repeat([]byte{seed}, 32)
	sum := md5.Sum(raw)
	return testKey{raw: raw, b64: base64.StdEncoding.EncodeToString(raw), md5b64: base64.StdEncoding.EncodeToString(sum[:])}
}

func (k testKey) headers() map[string]string {
	return map[string]string{sseReqHeaders.algo: "AES256", sseReqHeaders.key: k.b64, sseReqHeaders.keyMD5: k.md5b64}
}

func (k testKey) copyHeaders() map[string]string {
	return map[string]string{sseCopyHeaders.algo: "AES256", sseCopyHeaders.key: k.b64, sseCopyHeaders.keyMD5: k.md5b64}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func sha256B64(b []byte) string {
	s := sha256.Sum256(b)
	return b64(s[:])
}

func md5B64(b []byte) string {
	s := md5.Sum(b)
	return b64(s[:])
}

// ---------------------------------------------------------------------------
// Unit tests: CRC, combine, SSE-C reader
// ---------------------------------------------------------------------------

func TestCRCKnownValues(t *testing.T) {
	check := []byte("123456789")
	cases := map[string]uint64{
		"CRC32":     0xCBF43926,
		"CRC32C":    0xE3069283,
		"CRC64NVME": 0xAE8B14860A799888,
	}
	for name, want := range cases {
		a := ckAlgoByName(name)
		if a == nil {
			t.Fatalf("algo %s not found", name)
		}
		if got, w := ckSumBase64(a, check), b64(putBE(want, a.size)); got != w {
			t.Errorf("%s(123456789)=%s want %s", name, got, w)
		}
	}
	// Cross-check CRC32 against the standard library.
	if got, w := ckSumBase64(ckAlgoByName("CRC32"), check), b64(putBE(uint64(crc32.ChecksumIEEE(check)), 4)); got != w {
		t.Errorf("CRC32 differs from hash/crc32: %s vs %s", got, w)
	}
}

func TestCRCCombine(t *testing.T) {
	a := bytes.Repeat([]byte("abc"), 1000)
	b := bytes.Repeat([]byte("xyz!"), 777)
	c := []byte("tail")
	whole := append(append(append([]byte{}, a...), b...), c...)
	for _, algo := range ckAlgos {
		if algo.crcWidth == 0 {
			continue
		}
		raw := func(p []byte) []byte {
			h := algo.newHash()
			_, _ = h.Write(p)
			return h.Sum(nil)
		}
		got, err := combineMultipartChecksum(algo, ckTypeFullObject,
			[][]byte{raw(a), raw(b), raw(c)}, []int64{int64(len(a)), int64(len(b)), int64(len(c))})
		if err != nil {
			t.Fatalf("%s: %v", algo.name, err)
		}
		if want := ckSumBase64(algo, whole); got != want {
			t.Errorf("%s FULL_OBJECT combine=%s want %s", algo.name, got, want)
		}
	}
	if _, err := combineMultipartChecksum(ckAlgoByName("SHA1"), ckTypeFullObject, [][]byte{make([]byte, 20)}, []int64{1}); err == nil {
		t.Error("FULL_OBJECT with SHA1 must be rejected")
	}
}

type seekBuf struct{ *bytes.Reader }

func (seekBuf) Close() error { return nil }

func TestSSEDecReaderSegmentsAndSeek(t *testing.T) {
	k := newTestKey(7)
	sk := &sseKey{key: k.raw, md5: k.md5b64}
	p1 := bytes.Repeat([]byte("0123456789abcdef"), 5)[:37]
	p2 := bytes.Repeat([]byte("ZYXWVUTSRQ"), 5)

	e1, err := newSSEEncrypter(sk)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := newSSEEncrypter(sk)
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := io.ReadAll(e1.wrap(bytes.NewReader(p1)))
	c2, _ := io.ReadAll(e2.wrap(bytes.NewReader(p2)))
	if len(c1) != len(p1) || bytes.Equal(c1, p1) {
		t.Fatal("ciphertext must have the plaintext length and differ from it")
	}
	blob := append(append([]byte{}, c1...), c2...)
	plain := append(append([]byte{}, p1...), p2...)
	rec := &sseRecord{KeyMD5: k.md5b64, Segments: []sseSegment{e1.segment(0), e2.segment(int64(len(p1)))}}

	rc, err := newSSEDecReader(seekBuf{bytes.NewReader(blob)}, k.raw, rec, int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("full read mismatch err=%v", err)
	}
	sk2, ok := rc.(io.Seeker)
	if !ok {
		t.Fatal("decrypting reader over a seekable source must be seekable")
	}
	for _, off := range []int64{0, 5, 16, 20, 36, 37, 40, 60} {
		if _, err := sk2.Seek(off, io.SeekStart); err != nil {
			t.Fatalf("seek %d: %v", off, err)
		}
		rest, err := io.ReadAll(rc)
		if err != nil || !bytes.Equal(rest, plain[off:]) {
			t.Fatalf("read after seek %d mismatch err=%v", off, err)
		}
	}
}

func TestSSEKeyHeaderValidation(t *testing.T) {
	k := newTestKey(1)
	h := http.Header{}
	if got, err := sseReqHeaders.parse(h); got != nil || err != nil {
		t.Fatalf("no headers: got=%v err=%v", got, err)
	}
	for name, mutate := range map[string]func(http.Header){
		"missing md5":   func(h http.Header) { h.Del(sseReqHeaders.keyMD5) },
		"wrong algo":    func(h http.Header) { h.Set(sseReqHeaders.algo, "aws:kms") },
		"short key":     func(h http.Header) { h.Set(sseReqHeaders.key, b64([]byte("short"))) },
		"md5 mismatch":  func(h http.Header) { h.Set(sseReqHeaders.keyMD5, md5B64([]byte("other"))) },
		"key not b64":   func(h http.Header) { h.Set(sseReqHeaders.key, "%%%") },
		"md5 not b64":   func(h http.Header) { h.Set(sseReqHeaders.keyMD5, "%%%") },
		"only the algo": func(h http.Header) { h.Del(sseReqHeaders.key); h.Del(sseReqHeaders.keyMD5) },
	} {
		hh := http.Header{}
		for hk, hv := range k.headers() {
			hh.Set(hk, hv)
		}
		mutate(hh)
		if got, err := sseReqHeaders.parse(hh); got != nil || err == nil || err.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got key=%v err=%v", name, got, err)
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP tests: Content-MD5 and checksums
// ---------------------------------------------------------------------------

func TestIntegrityContentMD5(t *testing.T) {
	e := newIntegrityEnv(t)
	v1 := []byte("version one")
	e.mustPut("md5.txt", v1, map[string]string{"Content-MD5": md5B64(v1)})

	// A wrong digest is BadDigest and must not replace the stored object.
	v2 := []byte("version two")
	rr := e.do("PUT", e.objPath("md5.txt"), v2, map[string]string{"Content-MD5": md5B64([]byte("something else"))})
	wantErr(t, rr, http.StatusBadRequest, "BadDigest")
	if got := e.do("GET", e.objPath("md5.txt"), nil, nil); got.Body.String() != string(v1) {
		t.Fatalf("object was replaced by a BadDigest upload: %q", got.Body.String())
	}

	// A BadDigest upload of a new key leaves nothing behind.
	rr = e.do("PUT", e.objPath("never.txt"), v2, map[string]string{"Content-MD5": md5B64([]byte("nope"))})
	wantErr(t, rr, http.StatusBadRequest, "BadDigest")
	if head := e.do("HEAD", e.objPath("never.txt"), nil, nil); head.Code != http.StatusNotFound {
		t.Fatalf("HEAD after BadDigest status=%d want 404", head.Code)
	}

	// A malformed digest is InvalidDigest.
	rr = e.do("PUT", e.objPath("bad.txt"), v2, map[string]string{"Content-MD5": "not-base64!"})
	wantErr(t, rr, http.StatusBadRequest, "InvalidDigest")
}

func TestIntegrityChecksumRoundTrip(t *testing.T) {
	e := newIntegrityEnv(t)
	body := []byte("checksum me, please")
	for _, algo := range ckAlgos {
		algo := algo
		t.Run(algo.name, func(t *testing.T) {
			key := "ck-" + strings.ToLower(algo.name)
			want := ckSumBase64(algo, body)
			rr := e.mustPut(key, body, map[string]string{algo.header: want})
			if got := rr.Header().Get(algo.header); got != want {
				t.Fatalf("PUT echo %s=%q want %q", algo.header, got, want)
			}
			for _, method := range []string{"GET", "HEAD"} {
				rr := e.do(method, e.objPath(key), nil, map[string]string{"X-Amz-Checksum-Mode": "ENABLED"})
				if rr.Code != http.StatusOK {
					t.Fatalf("%s status=%d", method, rr.Code)
				}
				if got := rr.Header().Get(algo.header); got != want {
					t.Fatalf("%s %s=%q want %q", method, algo.header, got, want)
				}
				if got := rr.Header().Get("X-Amz-Checksum-Type"); got != ckTypeFullObject {
					t.Fatalf("%s checksum type=%q", method, got)
				}
			}
			// Without x-amz-checksum-mode nothing is returned.
			if rr := e.do("GET", e.objPath(key), nil, nil); rr.Header().Get(algo.header) != "" {
				t.Fatal("checksum header returned without x-amz-checksum-mode")
			}
			// A mismatching digest is rejected.
			bad := ckSumBase64(algo, []byte("different"))
			wantErr(t, e.do("PUT", e.objPath(key+"-bad"), body, map[string]string{algo.header: bad}), http.StatusBadRequest, "BadDigest")
			if head := e.do("HEAD", e.objPath(key+"-bad"), nil, nil); head.Code != http.StatusNotFound {
				t.Fatalf("object stored despite BadDigest: HEAD=%d", head.Code)
			}
		})
	}
}

func TestIntegrityChecksumRequestErrors(t *testing.T) {
	e := newIntegrityEnv(t)
	body := []byte("x")
	cases := map[string]map[string]string{
		"wrong length":    {"X-Amz-Checksum-Sha256": b64([]byte("short"))},
		"not base64":      {"X-Amz-Checksum-Crc32": "***"},
		"two headers":     {"X-Amz-Checksum-Crc32": ckSumBase64(ckAlgoByName("CRC32"), body), "X-Amz-Checksum-Sha1": ckSumBase64(ckAlgoByName("SHA1"), body)},
		"unknown sdk alg": {"X-Amz-Sdk-Checksum-Algorithm": "MD6"},
		"alg mismatch":    {"X-Amz-Checksum-Crc32": ckSumBase64(ckAlgoByName("CRC32"), body), "X-Amz-Sdk-Checksum-Algorithm": "SHA256"},
	}
	for name, hdr := range cases {
		wantErr(t, e.do("PUT", e.objPath("req-err"), body, hdr), http.StatusBadRequest, "InvalidRequest")
		_ = name
	}
	// The SDK algorithm header alone means "compute and store".
	e.mustPut("sdk-only", body, map[string]string{"X-Amz-Sdk-Checksum-Algorithm": "SHA256"})
	rr := e.do("HEAD", e.objPath("sdk-only"), nil, map[string]string{"X-Amz-Checksum-Mode": "ENABLED"})
	if got := rr.Header().Get("X-Amz-Checksum-Sha256"); got != sha256B64(body) {
		t.Fatalf("stored SHA256=%q want %q", got, sha256B64(body))
	}
}

// ---------------------------------------------------------------------------
// HTTP tests: SSE-C
// ---------------------------------------------------------------------------

func TestIntegritySSEC(t *testing.T) {
	e := newIntegrityEnv(t)
	k := newTestKey(3)
	other := newTestKey(4)
	body := []byte("the quick brown fox jumps over the lazy dog")

	rr := e.mustPut("enc.txt", body, k.headers())
	if rr.Header().Get(sseReqHeaders.algo) != "AES256" || rr.Header().Get(sseReqHeaders.keyMD5) != k.md5b64 {
		t.Fatalf("PUT did not echo the SSE-C headers: %v", rr.Header())
	}

	// The blob on disk is ciphertext of the same length.
	meta, rc, err := e.rt.store.GetObject(e.bucket, "enc.txt")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	if int(meta.Size) != len(body) || len(raw) != len(body) || bytes.Equal(raw, body) {
		t.Fatalf("stored blob is not same-length ciphertext (size=%d len=%d)", meta.Size, len(raw))
	}

	// Right key: plaintext.
	got := e.do("GET", e.objPath("enc.txt"), nil, k.headers())
	if got.Code != http.StatusOK || got.Body.String() != string(body) {
		t.Fatalf("GET with key status=%d body=%q", got.Code, got.Body.String())
	}
	if got.Header().Get(sseReqHeaders.keyMD5) != k.md5b64 {
		t.Fatal("GET did not echo the key MD5")
	}
	// Range reads decrypt from the middle of the stream.
	part := e.do("GET", e.objPath("enc.txt"), nil, merge(k.headers(), map[string]string{"Range": "bytes=4-14"}))
	if part.Code != http.StatusPartialContent || part.Body.String() != string(body[4:15]) {
		t.Fatalf("Range GET status=%d body=%q", part.Code, part.Body.String())
	}
	// No key / wrong key / HEAD without key.
	wantErr(t, e.do("GET", e.objPath("enc.txt"), nil, nil), http.StatusBadRequest, "InvalidRequest")
	wantErr(t, e.do("GET", e.objPath("enc.txt"), nil, other.headers()), http.StatusForbidden, "AccessDenied")
	if h := e.do("HEAD", e.objPath("enc.txt"), nil, nil); h.Code != http.StatusBadRequest {
		t.Fatalf("HEAD without key status=%d want 400", h.Code)
	}
	if h := e.do("HEAD", e.objPath("enc.txt"), nil, k.headers()); h.Code != http.StatusOK {
		t.Fatalf("HEAD with key status=%d want 200", h.Code)
	}
	// A key sent for an unencrypted object is rejected.
	e.mustPut("plain.txt", body, nil)
	wantErr(t, e.do("GET", e.objPath("plain.txt"), nil, k.headers()), http.StatusBadRequest, "InvalidRequest")
	// Invalid headers on PUT.
	bad := k.headers()
	bad[sseReqHeaders.keyMD5] = md5B64([]byte("other"))
	wantErr(t, e.do("PUT", e.objPath("bad-enc"), body, bad), http.StatusBadRequest, "InvalidArgument")

	// Overwriting with a plaintext PUT drops the encryption record.
	e.mustPut("enc.txt", []byte("now plain"), nil)
	if got := e.do("GET", e.objPath("enc.txt"), nil, nil); got.Code != http.StatusOK || got.Body.String() != "now plain" {
		t.Fatalf("GET after plaintext overwrite status=%d body=%q", got.Code, got.Body.String())
	}
}

func TestIntegritySSECWithChecksum(t *testing.T) {
	e := newIntegrityEnv(t)
	k := newTestKey(5)
	body := []byte("checksum is computed over the plaintext")
	want := sha256B64(body)
	e.mustPut("both", body, merge(k.headers(), map[string]string{"X-Amz-Checksum-Sha256": want}))
	rr := e.do("GET", e.objPath("both"), nil, merge(k.headers(), map[string]string{"X-Amz-Checksum-Mode": "ENABLED"}))
	if rr.Body.String() != string(body) || rr.Header().Get("X-Amz-Checksum-Sha256") != want {
		t.Fatalf("status=%d sha256=%q body=%q", rr.Code, rr.Header().Get("X-Amz-Checksum-Sha256"), rr.Body.String())
	}
}

func TestIntegritySSECCopy(t *testing.T) {
	e := newIntegrityEnv(t)
	src := newTestKey(8)
	dst := newTestKey(9)
	body := []byte("copy between keys")
	e.mustPut("src", body, src.headers())
	from := map[string]string{"X-Amz-Copy-Source": "/" + e.bucket + "/src"}

	// Without the copy-source key the copy is refused.
	wantErr(t, e.do("PUT", e.objPath("dst-nokey"), nil, from), http.StatusBadRequest, "InvalidRequest")
	if h := e.do("HEAD", e.objPath("dst-nokey"), nil, nil); h.Code != http.StatusNotFound {
		t.Fatalf("destination created by a refused copy: HEAD=%d", h.Code)
	}
	// Wrong copy-source key.
	wantErr(t, e.do("PUT", e.objPath("dst-wrong"), nil, merge(from, dst.copyHeaders())), http.StatusForbidden, "AccessDenied")

	// Encrypted source to plaintext destination.
	if rr := e.do("PUT", e.objPath("dst-plain"), nil, merge(from, src.copyHeaders())); rr.Code != http.StatusOK {
		t.Fatalf("copy to plaintext status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := e.do("GET", e.objPath("dst-plain"), nil, nil); got.Body.String() != string(body) {
		t.Fatalf("plaintext copy body=%q", got.Body.String())
	}

	// Encrypted source to a destination with another key.
	if rr := e.do("PUT", e.objPath("dst-enc"), nil, merge(from, src.copyHeaders(), dst.headers())); rr.Code != http.StatusOK {
		t.Fatalf("copy to encrypted status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := e.do("GET", e.objPath("dst-enc"), nil, dst.headers()); got.Body.String() != string(body) {
		t.Fatalf("re-encrypted copy body=%q", got.Body.String())
	}
	wantErr(t, e.do("GET", e.objPath("dst-enc"), nil, src.headers()), http.StatusForbidden, "AccessDenied")
}

func TestIntegrityCopyCarriesFullObjectChecksum(t *testing.T) {
	e := newIntegrityEnv(t)
	body := []byte("carry my checksum")
	want := ckSumBase64(ckAlgoByName("CRC32C"), body)
	e.mustPut("csrc", body, map[string]string{"X-Amz-Checksum-Crc32c": want})
	if rr := e.do("PUT", e.objPath("cdst"), nil, map[string]string{"X-Amz-Copy-Source": "/" + e.bucket + "/csrc"}); rr.Code != http.StatusOK {
		t.Fatalf("copy status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr := e.do("HEAD", e.objPath("cdst"), nil, map[string]string{"X-Amz-Checksum-Mode": "ENABLED"})
	if got := rr.Header().Get("X-Amz-Checksum-Crc32c"); got != want {
		t.Fatalf("copied CRC32C=%q want %q", got, want)
	}
	// An explicit algorithm computes a new checksum over the copied bytes.
	if cp := e.do("PUT", e.objPath("cdst2"), nil, map[string]string{"X-Amz-Copy-Source": "/" + e.bucket + "/csrc", "X-Amz-Checksum-Algorithm": "SHA1"}); cp.Code != http.StatusOK {
		t.Fatalf("copy with algorithm status=%d body=%s", cp.Code, cp.Body.String())
	}
	rr = e.do("HEAD", e.objPath("cdst2"), nil, map[string]string{"X-Amz-Checksum-Mode": "ENABLED"})
	if got, w := rr.Header().Get("X-Amz-Checksum-Sha1"), ckSumBase64(ckAlgoByName("SHA1"), body); got != w {
		t.Fatalf("copied SHA1=%q want %q", got, w)
	}
}

// ---------------------------------------------------------------------------
// HTTP tests: multipart
// ---------------------------------------------------------------------------

// multipartFlow runs Create, UploadPart for every part and returns the
// upload id plus the per-part responses. It does not complete the upload.
func (e *integrityEnv) multipartFlow(key string, createHdr, partHdr map[string]string, parts [][]byte) (string, []*httptest.ResponseRecorder) {
	e.t.Helper()
	rr := e.do("POST", e.objPath(key)+"?uploads", nil, createHdr)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("CreateMultipartUpload status=%d body=%s", rr.Code, rr.Body.String())
	}
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(rr.Body.Bytes(), &init); err != nil || init.UploadID == "" {
		e.t.Fatalf("cannot read UploadId: %v body=%s", err, rr.Body.String())
	}
	var out []*httptest.ResponseRecorder
	for i, p := range parts {
		path := fmt.Sprintf("%s?partNumber=%d&uploadId=%s", e.objPath(key), i+1, init.UploadID)
		pr := e.do("PUT", path, p, partHdr)
		if pr.Code != http.StatusOK {
			e.t.Fatalf("UploadPart %d status=%d body=%s", i+1, pr.Code, pr.Body.String())
		}
		out = append(out, pr)
	}
	return init.UploadID, out
}

func (e *integrityEnv) complete(key, uploadID string, partRRs []*httptest.ResponseRecorder, hdr map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	var sb strings.Builder
	sb.WriteString("<CompleteMultipartUpload>")
	for i, pr := range partRRs {
		fmt.Fprintf(&sb, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", i+1, pr.Header().Get("ETag"))
	}
	sb.WriteString("</CompleteMultipartUpload>")
	return e.do("POST", e.objPath(key)+"?uploadId="+uploadID, []byte(sb.String()), hdr)
}

// The first part is above the 5 MiB minimum so the test does not depend on
// whether the store enforces it.
func multipartTestParts() [][]byte {
	p1 := bytes.Repeat([]byte("0123456789abcdef"), (5*1024*1024+16)/16)
	p1 = append(p1, []byte("odd")...)
	p2 := []byte("the final, short part")
	return [][]byte{p1, p2}
}

func TestIntegrityMultipartSSECFullObjectCRC(t *testing.T) {
	e := newIntegrityEnv(t)
	k := newTestKey(11)
	parts := multipartTestParts()
	whole := append(append([]byte{}, parts[0]...), parts[1]...)
	crc := ckAlgoByName("CRC32")

	createHdr := merge(k.headers(), map[string]string{"X-Amz-Checksum-Algorithm": "CRC32", "X-Amz-Checksum-Type": ckTypeFullObject})
	id, prs := e.multipartFlow("mp-full", createHdr, k.headers(), parts)
	for i, pr := range prs {
		if got, want := pr.Header().Get(crc.header), ckSumBase64(crc, parts[i]); got != want {
			t.Fatalf("part %d CRC32=%q want %q", i+1, got, want)
		}
	}
	rr := e.complete("mp-full", id, prs, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Complete status=%d body=%s", rr.Code, rr.Body.String())
	}
	wantSum := ckSumBase64(crc, whole)
	if !strings.Contains(rr.Body.String(), "<ChecksumCRC32>"+wantSum+"</ChecksumCRC32>") ||
		!strings.Contains(rr.Body.String(), "<ChecksumType>FULL_OBJECT</ChecksumType>") {
		t.Fatalf("Complete response lacks the FULL_OBJECT checksum %s: %s", wantSum, rr.Body.String())
	}

	got := e.do("GET", e.objPath("mp-full"), nil, merge(k.headers(), map[string]string{"X-Amz-Checksum-Mode": "ENABLED"}))
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), whole) {
		t.Fatalf("GET status=%d, plaintext mismatch (len=%d want %d)", got.Code, got.Body.Len(), len(whole))
	}
	if got.Header().Get(crc.header) != wantSum || got.Header().Get("X-Amz-Checksum-Type") != ckTypeFullObject {
		t.Fatalf("GET checksum headers: %v", got.Header())
	}
	// A range spanning the part boundary decrypts with both IVs.
	from := int64(len(parts[0])) - 6
	rng := e.do("GET", e.objPath("mp-full"), nil, merge(k.headers(), map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", from, from+14)}))
	if rng.Code != http.StatusPartialContent || !bytes.Equal(rng.Body.Bytes(), whole[from:from+15]) {
		t.Fatalf("cross-part Range status=%d body=%q", rng.Code, rng.Body.String())
	}
	wantErr(t, e.do("GET", e.objPath("mp-full"), nil, nil), http.StatusBadRequest, "InvalidRequest")
}

func TestIntegrityMultipartCompositeSHA256(t *testing.T) {
	e := newIntegrityEnv(t)
	parts := multipartTestParts()
	sha := ckAlgoByName("SHA256")

	id, prs := e.multipartFlow("mp-comp", map[string]string{"X-Amz-Checksum-Algorithm": "SHA256"}, nil, parts)
	d0, d1 := sha256.Sum256(parts[0]), sha256.Sum256(parts[1])
	want := sha256B64(append(append([]byte{}, d0[:]...), d1[:]...)) + "-2"

	// A wrong checksum for a part in the Complete body is BadDigest and the
	// upload stays usable.
	badBody := fmt.Sprintf("<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag><ChecksumSHA256>%s</ChecksumSHA256></Part><Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>",
		prs[0].Header().Get("ETag"), sha256B64([]byte("wrong")), prs[1].Header().Get("ETag"))
	wantErr(t, e.do("POST", e.objPath("mp-comp")+"?uploadId="+id, []byte(badBody), nil), http.StatusBadRequest, "BadDigest")

	rr := e.complete("mp-comp", id, prs, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Complete status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<ChecksumSHA256>"+want+"</ChecksumSHA256>") ||
		!strings.Contains(rr.Body.String(), "<ChecksumType>COMPOSITE</ChecksumType>") {
		t.Fatalf("Complete response lacks the COMPOSITE checksum %s: %s", want, rr.Body.String())
	}
	head := e.do("HEAD", e.objPath("mp-comp"), nil, map[string]string{"X-Amz-Checksum-Mode": "ENABLED"})
	if head.Header().Get(sha.header) != want || head.Header().Get("X-Amz-Checksum-Type") != ckTypeComposite {
		t.Fatalf("HEAD checksum headers: %v", head.Header())
	}
}

func TestIntegrityMultipartKeyAndAlgorithmEnforced(t *testing.T) {
	e := newIntegrityEnv(t)
	k := newTestKey(21)
	other := newTestKey(22)
	small := []byte("small part")

	// An upload created with a key refuses parts without it or with another.
	rr := e.do("POST", e.objPath("mp-key")+"?uploads", nil, k.headers())
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(rr.Body.Bytes(), &init); err != nil || init.UploadID == "" {
		t.Fatalf("Create failed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	path := e.objPath("mp-key") + "?partNumber=1&uploadId=" + init.UploadID
	wantErr(t, e.do("PUT", path, small, nil), http.StatusBadRequest, "InvalidRequest")
	wantErr(t, e.do("PUT", path, small, other.headers()), http.StatusForbidden, "AccessDenied")
	if pr := e.do("PUT", path, small, k.headers()); pr.Code != http.StatusOK {
		t.Fatalf("part with the right key status=%d body=%s", pr.Code, pr.Body.String())
	}
	// A key on a part of an unencrypted upload is refused too.
	rr = e.do("POST", e.objPath("mp-nokey")+"?uploads", nil, nil)
	if err := xml.Unmarshal(rr.Body.Bytes(), &init); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.do("PUT", e.objPath("mp-nokey")+"?partNumber=1&uploadId="+init.UploadID, small, k.headers()), http.StatusBadRequest, "InvalidRequest")

	// A part checksum of another algorithm than the upload's is refused.
	rr = e.do("POST", e.objPath("mp-alg")+"?uploads", nil, map[string]string{"X-Amz-Checksum-Algorithm": "CRC32"})
	if err := xml.Unmarshal(rr.Body.Bytes(), &init); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.do("PUT", e.objPath("mp-alg")+"?partNumber=1&uploadId="+init.UploadID, small,
		map[string]string{"X-Amz-Checksum-Sha1": ckSumBase64(ckAlgoByName("SHA1"), small)}), http.StatusBadRequest, "InvalidRequest")

	// Invalid algorithm / type combinations on Create.
	wantErr(t, e.do("POST", e.objPath("mp-bad")+"?uploads", nil, map[string]string{"X-Amz-Checksum-Algorithm": "MD6"}), http.StatusBadRequest, "InvalidRequest")
	wantErr(t, e.do("POST", e.objPath("mp-bad")+"?uploads", nil, map[string]string{"X-Amz-Checksum-Algorithm": "SHA256", "X-Amz-Checksum-Type": ckTypeFullObject}), http.StatusBadRequest, "InvalidRequest")
	wantErr(t, e.do("POST", e.objPath("mp-bad")+"?uploads", nil, map[string]string{"X-Amz-Checksum-Algorithm": "CRC64NVME", "X-Amz-Checksum-Type": ckTypeComposite}), http.StatusBadRequest, "InvalidRequest")
	wantErr(t, e.do("POST", e.objPath("mp-bad")+"?uploads", nil, map[string]string{"X-Amz-Checksum-Type": ckTypeComposite}), http.StatusBadRequest, "InvalidRequest")
}
