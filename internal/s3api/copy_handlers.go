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
	m, err4 := rt.store.PutObject(destBucket, destKey, ct, dstMeta, rc)
	if err4 != nil {
		if apiErr, ok := err4.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		if isNoSpaceError(err4) {
			writeError(w, r, errInsufficient)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err4.Error())
		return
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
