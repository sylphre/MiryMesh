// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"
)

func (rt *Router) handleCopyObject(w http.ResponseWriter, r *http.Request, destBucket, destKey string) {
	srcHeader := r.Header.Get("X-Amz-Copy-Source")
	if srcHeader == "" {
		srcHeader = r.Header.Get("x-amz-copy-source")
	}
	srcBucket, srcKey, err := parseCopySource(srcHeader)
	if err != nil {
		writeError(w, r, errInvalidRequest)
		return
	}
	if !rt.store.BucketExists(destBucket) || !rt.store.BucketExists(srcBucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	// The copy overwrites the destination: a protected object must stay intact.
	if rt.rejectLocked(w, r, destBucket, destKey) {
		return
	}
	srcMeta, rc, err2 := rt.store.GetObject(srcBucket, srcKey)
	if err2 != nil {
		if apiErr, ok := err2.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err2.Error())
		return
	}
	defer rc.Close()
	// SSE-C source: the copy-source headers decrypt it (and reject a wrong key).
	rc, _, srcSSEErr := rt.decryptForRead(r.Header, sseCopyHeaders, srcBucket, srcKey, srcMeta.Size, rc)
	if srcSSEErr != nil {
		writeFailure(w, r, srcSSEErr)
		return
	}
	// 5GiB copy limit (docs/06): S3 says copy source must be <=5GiB. Enforce before reading.
	if srcMeta.Size > 5*1024*1024*1024 {
		writeError(w, r, APIError{Code: "InvalidRequest", Message: "The specified copy source is larger than the maximum allowable size for a copy source (5 GiB)", Status: 400})
		return
	}
	// Preconditions x-amz-copy-source-if-*
	cond := parseCopyConditions(r)
	if hasAnyCopyCondition(cond) {
		if !evalCopyConditions(w, r, srcMeta, cond) {
			return
		}
	}
	// Metadata directive: COPY (default) or REPLACE.
	directive := strings.ToUpper(r.Header.Get("x-amz-metadata-directive"))
	if directive == "" {
		directive = "COPY"
	}
	var dstMeta map[string]string
	var dstCT string
	if directive == "REPLACE" {
		dstCT = r.Header.Get("Content-Type")
		dstMeta = map[string]string{}
		for k, vs := range r.Header {
			lk := strings.ToLower(k)
			if strings.HasPrefix(lk, "x-amz-meta-") {
				dstMeta[lk[len("x-amz-meta-"):]] = strings.Join(vs, ",")
			}
		}
	} else if directive == "COPY" {
		dstMeta = srcMeta.Meta
		dstCT = srcMeta.ContentType
	} else {
		writeError(w, r, errInvalidRequest)
		return
	}
	// Per-bucket storage quota of the destination bucket.
	if rt.rejectOverQuota(w, r, destBucket, destKey, srcMeta.Size) {
		return
	}
	// Stream copy: validate placement using src size, then stream via PutObject.
	if rt.placing != nil {
		if _, pErr := rt.placing.Resolve(destBucket, srcMeta.Size); pErr != nil {
			writeError(w, r, APIError{Code: errInsufficient.Code, Message: pErr.Error(), Status: 507})
			return
		}
	} else {
		if srcMeta.Size > 0 && rt.store.IsOverQuota(srcMeta.Size) {
			writeError(w, r, errInsufficient)
			return
		}
	}
	ct := dstCT
	if ct == "" {
		ct = "application/octet-stream"
	}
	// Tags: COPY (default) carries the source tags over, REPLACE takes x-amz-tagging.
	// Resolved before the write because the destination may be the source object.
	destTags := ""
	if strings.EqualFold(r.Header.Get("X-Amz-Tagging-Directive"), "REPLACE") {
		t, tagErr := requestTagging(r)
		if tagErr != nil {
			writeError(w, r, *tagErr)
			return
		}
		destTags = t
	} else {
		doc, found, gerr := rt.subStore().GetSubresource(srcBucket, srcKey, subTagging)
		if gerr != nil {
			writeStoreFailure(w, r, gerr)
			return
		}
		if found {
			destTags = doc
		}
	}
	// Destination SSE-C and checksum, resolved before the write because the
	// destination may be the source object. An explicit x-amz-checksum-algorithm
	// is computed over the plaintext; otherwise a FULL_OBJECT source checksum
	// carries over (the bytes are identical).
	destCK, destSSEErr := sseReqHeaders.parse(r.Header)
	if destSSEErr != nil {
		writeError(w, r, *destSSEErr)
		return
	}
	var copyBody io.Reader = rc
	var copyVer *ckVerifier
	var carriedCk *ckRecord
	if name := strings.TrimSpace(r.Header.Get("X-Amz-Checksum-Algorithm")); name != "" {
		algo := ckAlgoByName(name)
		if algo == nil {
			writeError(w, r, *p2BadRequest("InvalidRequest", "Checksum algorithm provided is unsupported. Please try again with any of the valid types: [CRC32, CRC32C, CRC64NVME, SHA1, SHA256]"))
			return
		}
		copyVer = newCkVerifier(copyBody, &ckRequest{algo: algo})
		copyBody = copyVer
	} else {
		var ckLoadErr error
		if carriedCk, ckLoadErr = rt.loadChecksum(srcBucket, srcKey); ckLoadErr != nil {
			writeFailure(w, r, ckLoadErr)
			return
		}
		if carriedCk != nil && carriedCk.Type != ckTypeFullObject {
			carriedCk = nil
		}
	}
	var destEnc *sseEncrypter
	if destCK != nil {
		var encErr error
		if destEnc, encErr = newSSEEncrypter(destCK); encErr != nil {
			writeFailure(w, r, encErr)
			return
		}
		copyBody = destEnc.wrap(copyBody)
	}
	m, err4 := rt.store.PutObject(destBucket, destKey, ct, dstMeta, copyBody)
	if err4 != nil {
		writeFailure(w, r, err4)
		return
	}
	// The copy is a new object: reset its subresources and apply the resolved tags.
	if !rt.commitObjectTags(w, r, destBucket, destKey, destTags) {
		return
	}
	destCk := carriedCk
	if copyVer != nil {
		destCk = copyVer.record()
	}
	if !rt.commitObjectDocs(w, r, destBucket, destKey, newSSERecord(destEnc, destCK), destCk) {
		return
	}
	if destCK != nil {
		sseEcho(w.Header(), destCK.md5)
	}
	type resp struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		Xmlns        string   `xml:"xmlns,attr"`
		LastModified string   `xml:"LastModified"`
		ETag         string   `xml:"ETag"`
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", LastModified: m.LastMod.UTC().Format(time.RFC3339), ETag: `"` + m.ETag + `"`})
	_ = io.Discard
}

func evalCopyConditions(w http.ResponseWriter, r *http.Request, srcMeta ObjectMeta, cond copyCondition) bool {
	// x-amz-copy-source-if-match: if ETag matches, copy proceeds; else 412.
	if cond.matchETag != "" {
		want := strings.Trim(cond.matchETag, "\" ")
		if want != "*" && strings.Trim(srcMeta.ETag, "\"") != want {
			writeError(w, r, APIError{Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold", Status: 412})
			return false
		}
	}
	if cond.noneMatch != "" {
		want := strings.Trim(cond.noneMatch, "\" ")
		if want == "*" || strings.Trim(srcMeta.ETag, "\"") == want {
			writeError(w, r, APIError{Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold", Status: 412})
			return false
		}
	}
	if cond.modifiedSince != "" {
		if t, err := time.Parse(http.TimeFormat, cond.modifiedSince); err == nil {
			if !srcMeta.LastMod.After(t) {
				writeError(w, r, APIError{Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold", Status: 412})
				return false
			}
		}
	}
	if cond.unmodifiedSince != "" {
		if t, err := time.Parse(http.TimeFormat, cond.unmodifiedSince); err == nil {
			if srcMeta.LastMod.After(t) {
				writeError(w, r, APIError{Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold", Status: 412})
				return false
			}
		}
	}
	return true
}

// getDir removed: use path/filepath.Dir.
