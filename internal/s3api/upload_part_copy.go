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
	// Preconditions x-amz-copy-source-if-*
	cond := parseCopyConditions(r)
	if hasAnyCopyCondition(cond) {
		if !evalCopyConditions(w, r, srcMeta, cond) {
			return
		}
	}
	// For range, get size before reading all; validate then slice.
	// TODO: streaming range copy without full ReadAll for large sources.
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
	// Write part file via streaming hash (already have srcData but keep streaming path for consistency).
	dir := rt.store.MultipartDir(uploadID)
	if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
		writeXMLError(w, r, 500, "InternalError", mkErr.Error())
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("part-%05d", partNumber))
	tmp := path + ".tmp"
	f, openErr := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if openErr != nil {
		writeXMLError(w, r, 500, "InternalError", openErr.Error())
		return
	}
	h := md5.New()
	if _, copyErr := io.Copy(io.MultiWriter(f, h), strings.NewReader(string(srcData))); copyErr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		writeXMLError(w, r, 500, "InternalError", copyErr.Error())
		return
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(tmp)
		writeXMLError(w, r, 500, "InternalError", closeErr.Error())
		return
	}
	etag := hex.EncodeToString(h.Sum(nil))
	if renameErr := os.Rename(tmp, path); renameErr != nil {
		_ = os.Remove(tmp)
		writeXMLError(w, r, 500, "InternalError", renameErr.Error())
		return
	}
	rec := PartRecord{PartNumber: partNumber, ETag: etag, Size: int64(len(srcData)), Path: path}
	if st := rt.store.GetUpload(uploadID); st != nil {
		if st.Parts == nil {
			st.Parts = make(map[int]PartRecord)
		}
		st.Parts[partNumber] = rec
	}
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
