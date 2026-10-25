// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (rt *Router) handleUploadPartCopy(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	partStr := q.Get("partNumber")
	partNumber, err := strconv.Atoi(partStr)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		writeError(w, r, errInvalidRequest)
		return
	}
	if uploadID == "" {
		writeError(w, r, errInvalidRequest)
		return
	}
	s := rt.store.GetUpload(uploadID)
	if s == nil || s.Bucket != bucket || s.Key != key {
		writeError(w, r, APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: 404})
		return
	}
	srcHeader := r.Header.Get("X-Amz-Copy-Source")
	if srcHeader == "" {
		srcHeader = r.Header.Get("x-amz-copy-source")
	}
	srcBucket, srcKey, err := parseCopySource(srcHeader)
	if err != nil {
		writeError(w, r, errInvalidRequest)
		return
	}
	if !rt.store.BucketExists(srcBucket) {
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
	// SSE-C source: the copy-source headers decrypt it (and reject a wrong key).
	rc, _, srcSSEErr := rt.decryptForRead(r.Header, sseCopyHeaders, srcBucket, srcKey, srcMeta.Size, rc)
	if srcSSEErr != nil {
		writeFailure(w, r, srcSSEErr)
		return
	}
	// Preconditions x-amz-copy-source-if-*
	cond := parseCopyConditions(r)
	if hasAnyCopyCondition(cond) {
		if !evalCopyConditions(w, r, srcMeta, cond) {
			return
		}
	}
	// Stream the source: skip to the start of the range and read only the range,
	// so a large source object is never held in memory.
	partSize := srcMeta.Size
	var partSrc io.Reader = rc
	rangeHeader := r.Header.Get("x-amz-copy-source-range")
	if rangeHeader == "" {
		rangeHeader = r.Header.Get("X-Amz-Copy-Source-Range")
	}
	if rangeHeader != "" {
		start, end, ok := parseCopyRange(rangeHeader, srcMeta.Size)
		if !ok {
			writeError(w, r, APIError{Code: "InvalidRequest", Message: "The x-amz-copy-source-range value must be of the form bytes=start-end", Status: 400})
			return
		}
		if _, skipErr := io.CopyN(io.Discard, rc, int64(start)); skipErr != nil {
			writeXMLError(w, r, 500, "InternalError", skipErr.Error())
			return
		}
		partSize = int64(end) - int64(start) + 1
		partSrc = io.LimitReader(rc, partSize)
	}
	// Pending parts count towards the bucket quota.
	if rt.rejectOverQuota(w, r, bucket, "", partSize) {
		return
	}
	// The part follows the upload's settings: same SSE-C key, same checksum algorithm.
	cfg, cfgErr := rt.loadMPConfig(bucket, uploadID)
	if cfgErr != nil {
		writeFailure(w, r, cfgErr)
		return
	}
	pw, prepErr := preparePart(r.Header, cfg, partSrc)
	if prepErr != nil {
		writeFailure(w, r, prepErr)
		return
	}
	// The store writes the part (a local file, or a DataNode in gateway mode) and records it.
	rec, upErr := rt.store.UploadPart(uploadID, partNumber, pw.body)
	if upErr != nil {
		writeFailure(w, r, upErr)
		return
	}
	etag := rec.ETag
	if doc := pw.doc(cfg); doc != (mpPart{}) {
		if perr := rt.putDoc(bucket, mpPartKey(uploadID, partNumber), subMPPart, doc); perr != nil {
			writeFailure(w, r, perr)
			return
		}
	}
	if pw.key != nil {
		sseEcho(w.Header(), pw.key.md5)
	}
	type resp struct {
		XMLName      xml.Name `xml:"CopyPartResult"`
		Xmlns        string   `xml:"xmlns,attr"`
		ETag         string   `xml:"ETag"`
		LastModified string   `xml:"LastModified"`
		ChecksumXML
	}
	// Use source last modified? S3 uses part copy time; return now.
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", ETag: `"` + etag + `"`, LastModified: srcMeta.LastMod.UTC().Format("2006-01-02T15:04:05.000Z"), ChecksumXML: pw.ver.record().toXML()})
	_ = io.Discard
}

func parseCopyRange(v string, size int64) (int64, int64, bool) {
	if !strings.HasPrefix(v, "bytes=") {
		return 0, 0, false
	}
	rest := strings.TrimPrefix(v, "bytes=")
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	endStr := strings.TrimSpace(parts[1])
	var end int64
	if endStr == "" {
		end = size - 1
	} else {
		var err2 error
		end, err2 = strconv.ParseInt(endStr, 10, 64)
		if err2 != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
}
