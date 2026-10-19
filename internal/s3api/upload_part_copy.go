// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	s := rt.store.getUpload(uploadID)
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
	// Preconditions x-amz-copy-source-if-*
	cond := parseCopyConditions(r)
	if hasAnyCopyCondition(cond) {
		if !evalCopyConditions(w, r, srcMeta, cond) {
			return
		}
	}
	// Range: x-amz-copy-source-range: bytes=start-end
	srcData, err3 := io.ReadAll(rc)
	if err3 != nil {
		writeXMLError(w, r, 500, "InternalError", err3.Error())
		return
	}
	rangeHeader := r.Header.Get("x-amz-copy-source-range")
	if rangeHeader == "" {
		rangeHeader = r.Header.Get("X-Amz-Copy-Source-Range")
	}
	if rangeHeader != "" {
		start, end, ok := parseCopyRange(rangeHeader, int64(len(srcData)))
		if !ok {
			writeError(w, r, APIError{Code: "InvalidRequest", Message: "The x-amz-copy-source-range value must be of the form bytes=start-end", Status: 400})
			return
		}
		srcData = srcData[start : end+1]
	}
	// Write part file
	dir := rt.store.multipartDir(uploadID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	sum := md5.Sum(srcData)
	etag := hex.EncodeToString(sum[:])
	path := filepath.Join(dir, fmt.Sprintf("part-%05d", partNumber))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, srcData, 0o640); err != nil {
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	rec := PartRecord{PartNumber: partNumber, ETag: etag, Size: int64(len(srcData)), Path: path}
	rt.store.mu.Lock()
	if st, ok := rt.store.uploads[uploadID]; ok {
		if st.Parts == nil {
			st.Parts = make(map[int]PartRecord)
		}
		st.Parts[partNumber] = rec
	}
	rt.store.mu.Unlock()
	type resp struct {
		XMLName      xml.Name `xml:"CopyPartResult"`
		Xmlns        string   `xml:"xmlns,attr"`
		ETag         string   `xml:"ETag"`
		LastModified string   `xml:"LastModified"`
	}
	// Use source last modified? S3 uses part copy time; return now.
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", ETag: `"` + etag + `"`, LastModified: srcMeta.LastMod.UTC().Format("2006-01-02T15:04:05.000Z")})
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
