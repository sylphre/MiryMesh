// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

// Glue between the object handlers and the SSE-C / checksum building blocks
// (sse.go, checksum.go): what is stored next to each object, how a multipart
// upload remembers its settings, and how Complete aggregates them.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Subresource kinds (s3_subresources.kind) used by SSE-C and checksums.
const (
	subSSEC     = "sse-c"     // per-object: key MD5 + IV segments
	subChecksum = "checksum"  // per-object: additional checksum
	subMPConfig = "mp-config" // per-upload: SSE-C key MD5, checksum algorithm/type
	subMPPart   = "mp-part"   // per-part: IV and raw checksum
)

var errUploadGone = APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: http.StatusNotFound}

// mpConfig is what CreateMultipartUpload decided for the whole upload.
type mpConfig struct {
	KeyMD5 string `json:"key_md5,omitempty"`
	Algo   string `json:"algo,omitempty"`
	Type   string `json:"type,omitempty"`
}

// mpPart is what one UploadPart left behind.
type mpPart struct {
	IV  string `json:"iv,omitempty"`
	Sum string `json:"sum,omitempty"` // base64 of the raw part digest
}

// Documents of an in-progress upload live under keys no real object can have:
// the API edge rejects ".." path segments (validateObjectKey).
func mpConfigKey(uploadID string) string { return "../mpu/" + uploadID }

func mpPartKey(uploadID string, n int) string {
	return "../mpu/" + uploadID + "/" + strconv.Itoa(n)
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// asAPIError finds an APIError in err, whether wrapped, a value or a pointer.
func asAPIError(err error) (APIError, bool) {
	var v APIError
	if errors.As(err, &v) {
		return v, true
	}
	var p *APIError
	if errors.As(err, &p) && p != nil {
		return *p, true
	}
	return APIError{}, false
}

// writeFailure renders any error of a write or read path.
func writeFailure(w http.ResponseWriter, r *http.Request, err error) {
	if apiErr, ok := asAPIError(err); ok {
		writeError(w, r, apiErr)
		return
	}
	if errors.Is(err, errSubresourceUnsupported) {
		writeSubresourceError(w, r, err)
		return
	}
	if isNoSpaceError(err) {
		writeError(w, r, errInsufficient)
		return
	}
	writeXMLError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
}

func invalidPart(n int, why string) APIError {
	return APIError{Code: "InvalidPart", Message: "Part " + strconv.Itoa(n) + " " + why, Status: http.StatusBadRequest}
}

// ---------------------------------------------------------------------------
// Subresource documents
// ---------------------------------------------------------------------------

// loadDoc reads and decodes one JSON document. A backend that cannot store
// documents is the same as "no document".
func (rt *Router) loadDoc(bucket, key, kind string, out any) (bool, error) {
	body, found, err := rt.subStore().GetSubresource(bucket, key, kind)
	if errors.Is(err, errSubresourceUnsupported) {
		return false, nil
	}
	if err != nil || !found {
		return false, err
	}
	if uerr := json.Unmarshal([]byte(body), out); uerr != nil {
		return false, uerr
	}
	return true, nil
}

func (rt *Router) putDoc(bucket, key, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return rt.subStore().PutSubresource(bucket, key, kind, string(b))
}

func (rt *Router) loadSSE(bucket, key string) (*sseRecord, error) {
	var rec sseRecord
	found, err := rt.loadDoc(bucket, key, subSSEC, &rec)
	if err != nil || !found {
		return nil, err
	}
	return &rec, nil
}

func (rt *Router) loadChecksum(bucket, key string) (*ckRecord, error) {
	var rec ckRecord
	found, err := rt.loadDoc(bucket, key, subChecksum, &rec)
	if err != nil || !found {
		return nil, err
	}
	return &rec, nil
}

func (rt *Router) loadMPConfig(bucket, uploadID string) (mpConfig, error) {
	var cfg mpConfig
	_, err := rt.loadDoc(bucket, mpConfigKey(uploadID), subMPConfig, &cfg)
	return cfg, err
}

// dropUploadDocs removes the bookkeeping of a finished or aborted upload.
func (rt *Router) dropUploadDocs(up *MultipartUploadState) {
	if up == nil {
		return
	}
	ss := rt.subStore()
	_ = ss.DeleteSubresource(up.Bucket, mpConfigKey(up.UploadID), subMPConfig)
	for n := range up.Parts {
		_ = ss.DeleteSubresource(up.Bucket, mpPartKey(up.UploadID, n), subMPPart)
	}
}

// newSSERecord is the record of a single-PUT object; nil when not encrypted.
func newSSERecord(enc *sseEncrypter, k *sseKey) *sseRecord {
	if enc == nil || k == nil {
		return nil
	}
	return &sseRecord{KeyMD5: k.md5, Segments: []sseSegment{enc.segment(0)}}
}

// commitObjectDocs stores the SSE-C and checksum documents of a freshly
// written object. Call it after commitObjectTags (which purges old documents).
// It writes the error response itself.
func (rt *Router) commitObjectDocs(w http.ResponseWriter, r *http.Request, bucket, key string, sse *sseRecord, ck *ckRecord) bool {
	if sse == nil && ck == nil {
		return true
	}
	if sse != nil {
		if err := rt.putDoc(bucket, key, subSSEC, sse); err != nil {
			// Without its IVs the ciphertext is unreadable: do not keep it.
			_ = rt.store.DeleteObject(bucket, key)
			_ = rt.subStore().PurgeObjectSubresources(bucket, key)
			writeFailure(w, r, err)
			return false
		}
	}
	if ck != nil {
		if err := rt.putDoc(bucket, key, subChecksum, ck); err != nil && !errors.Is(err, errSubresourceUnsupported) {
			writeFailure(w, r, err)
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// decryptForRead authorizes the SSE-C headers hs against the stored record and
// wraps rc so reads return plaintext. Unencrypted objects pass through.
func (rt *Router) decryptForRead(h http.Header, hs sseHeaders, bucket, key string, size int64, rc io.ReadCloser) (io.ReadCloser, *sseKey, error) {
	rec, err := rt.loadSSE(bucket, key)
	if err != nil {
		return nil, nil, err
	}
	k, aerr := sseAuthorize(h, hs, rec)
	if aerr != nil {
		return nil, nil, *aerr
	}
	if k == nil {
		return rc, nil, nil
	}
	dec, derr := newSSEDecReader(rc, k.key, rec, size)
	if derr != nil {
		return nil, nil, derr
	}
	return dec, k, nil
}

func checksumEnabled(h http.Header) bool {
	return strings.EqualFold(strings.TrimSpace(h.Get("X-Amz-Checksum-Mode")), "ENABLED")
}

// setChecksumHeaders adds x-amz-checksum-* to a GET/HEAD response when the
// request asked for it with x-amz-checksum-mode: ENABLED.
func (rt *Router) setChecksumHeaders(out, req http.Header, bucket, key string) error {
	if !checksumEnabled(req) {
		return nil
	}
	ck, err := rt.loadChecksum(bucket, key)
	if err != nil {
		return err
	}
	ck.setHeaders(out)
	return nil
}

func (c *ckRecord) typ() string {
	if c == nil {
		return ""
	}
	return c.Type
}

// setHeader echoes the computed digest as x-amz-checksum-<algo>; nil-safe.
func (v *ckVerifier) setHeader(h http.Header) {
	if v == nil || v.sum == nil || v.req.algo == nil {
		return
	}
	h.Set(v.req.algo.header, base64.StdEncoding.EncodeToString(v.sum))
}

// ---------------------------------------------------------------------------
// UploadPart
// ---------------------------------------------------------------------------

// partWrite is an UploadPart body wrapped with verification and encryption.
type partWrite struct {
	body io.Reader
	ver  *ckVerifier
	enc  *sseEncrypter
	key  *sseKey
}

func checkUploadKey(cfg mpConfig, k *sseKey) *APIError {
	var e APIError
	switch {
	case cfg.KeyMD5 == "" && k == nil:
		return nil
	case cfg.KeyMD5 == "":
		e = errSSENotApplied
	case k == nil:
		e = errSSEMissingKey
	case k.md5 != cfg.KeyMD5:
		e = errSSEWrongKey
	default:
		return nil
	}
	return &e
}

// preparePart validates the part's headers against the upload settings and
// wraps src: verifier first (plaintext), encryption on top.
func preparePart(h http.Header, cfg mpConfig, src io.Reader) (*partWrite, error) {
	ckReq, perr := parseChecksumRequest(h)
	if perr != nil {
		return nil, *perr
	}
	if cfg.Algo != "" {
		want := ckAlgoByName(cfg.Algo)
		if want == nil {
			return nil, errors.New("multipart checksum: unknown algorithm " + cfg.Algo)
		}
		switch {
		case ckReq == nil:
			ckReq = &ckRequest{algo: want}
		case ckReq.algo == nil:
			ckReq.algo = want
		case ckReq.algo != want:
			return nil, *p2BadRequest("InvalidRequest", "Checksum Type mismatch occurred, expected checksum Type: "+strings.ToLower(want.name)+", actual checksum Type: "+strings.ToLower(ckReq.algo.name))
		}
	}
	k, kerr := sseReqHeaders.parse(h)
	if kerr != nil {
		return nil, *kerr
	}
	if aerr := checkUploadKey(cfg, k); aerr != nil {
		return nil, *aerr
	}
	pw := &partWrite{body: src, key: k}
	if ckReq != nil {
		pw.ver = newCkVerifier(pw.body, ckReq)
		pw.body = pw.ver
	}
	if k != nil {
		enc, eerr := newSSEEncrypter(k)
		if eerr != nil {
			return nil, eerr
		}
		pw.enc = enc
		pw.body = enc.wrap(pw.body)
	}
	return pw, nil
}

// doc is the bookkeeping to store once the part was written.
func (pw *partWrite) doc(cfg mpConfig) mpPart {
	var d mpPart
	if pw.enc != nil {
		d.IV = pw.enc.segment(0).IV
	}
	if cfg.Algo != "" && pw.ver != nil && pw.ver.sum != nil {
		d.Sum = base64.StdEncoding.EncodeToString(pw.ver.sum)
	}
	return d
}

// ---------------------------------------------------------------------------
// CompleteMultipartUpload
// ---------------------------------------------------------------------------

// completePlan is what Complete will store once the blob is assembled.
type completePlan struct {
	up  *MultipartUploadState
	sse *sseRecord
	ck  *ckRecord
}

// planComplete checks the part list against the upload and works out the
// object's SSE-C segments and aggregate checksum. It only reads state, so a
// failure leaves the upload intact.
func (rt *Router) planComplete(bucket, key, uploadID string, parts []CompletePart, sums []ChecksumXML, h http.Header) (*completePlan, error) {
	up := rt.store.GetUpload(uploadID)
	if up == nil || up.Bucket != bucket || up.Key != key {
		return nil, errUploadGone
	}
	for i := 1; i < len(parts); i++ {
		if parts[i].PartNumber <= parts[i-1].PartNumber {
			return nil, APIError{Code: "InvalidPartOrder", Message: "The list of parts was not in ascending order. The parts list must be specified in order by part number.", Status: http.StatusBadRequest}
		}
	}
	plan := &completePlan{up: up}
	cfg, err := rt.loadMPConfig(bucket, uploadID)
	if err != nil {
		return nil, err
	}
	if cfg == (mpConfig{}) {
		return plan, nil
	}
	var algo *ckAlgo
	if cfg.Algo != "" {
		algo = ckAlgoByName(cfg.Algo)
		if algo == nil {
			return nil, errors.New("multipart checksum: unknown algorithm " + cfg.Algo)
		}
	}
	var (
		segs  []sseSegment
		raws  [][]byte
		sizes []int64
		off   int64
	)
	for i, p := range parts {
		rec, ok := up.Parts[p.PartNumber]
		if !ok {
			return nil, invalidPart(p.PartNumber, "not found")
		}
		var doc mpPart
		found, derr := rt.loadDoc(bucket, mpPartKey(uploadID, p.PartNumber), subMPPart, &doc)
		if derr != nil {
			return nil, derr
		}
		if cfg.KeyMD5 != "" {
			if !found || doc.IV == "" {
				return nil, invalidPart(p.PartNumber, "was not uploaded with the encryption key of the upload")
			}
			seg := sseSegment{Off: off, IV: doc.IV}
			// An empty part owns no bytes: the next part takes over its offset.
			if n := len(segs); n > 0 && segs[n-1].Off == off {
				segs[n-1] = seg
			} else {
				segs = append(segs, seg)
			}
		}
		if algo != nil {
			if !found || doc.Sum == "" {
				return nil, invalidPart(p.PartNumber, "was uploaded without a "+algo.name+" checksum")
			}
			raw, berr := base64.StdEncoding.DecodeString(doc.Sum)
			if berr != nil || len(raw) != algo.size {
				return nil, invalidPart(p.PartNumber, "has an unreadable checksum")
			}
			if given := sums[i].valueFor(algo.name); given != "" && given != doc.Sum {
				return nil, APIError{Code: "BadDigest", Message: "The " + algo.name + " you specified for part " + strconv.Itoa(p.PartNumber) + " did not match what we received.", Status: http.StatusBadRequest}
			}
			raws = append(raws, raw)
			sizes = append(sizes, rec.Size)
		}
		off += rec.Size
	}
	if cfg.KeyMD5 != "" {
		plan.sse = &sseRecord{KeyMD5: cfg.KeyMD5, Segments: segs}
	}
	if algo != nil {
		val, cerr := combineMultipartChecksum(algo, cfg.Type, raws, sizes)
		if cerr != nil {
			return nil, cerr
		}
		if given := strings.TrimSpace(h.Get(algo.header)); given != "" && given != val {
			return nil, APIError{Code: "BadDigest", Message: "The " + algo.name + " you specified did not match the calculated checksum.", Status: http.StatusBadRequest}
		}
		plan.ck = &ckRecord{Algo: algo.name, Value: val, Type: cfg.Type}
	}
	return plan, nil
}
