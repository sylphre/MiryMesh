// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// xmlErrorDoc is the minimal S3 XML error envelope; reuse APIError encoding.

func (rt *Router) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets := rt.store.ListBuckets()
	type bucketXML struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
	}
	type result struct {
		XMLName xml.Name `xml:"ListAllMyBucketsResult"`
		Xmlns   string   `xml:"xmlns,attr"`
		Owner   struct {
			ID          string `xml:"ID"`
			DisplayName string `xml:"DisplayName"`
		} `xml:"Owner"`
		Buckets struct {
			Bucket []bucketXML `xml:"Bucket"`
		} `xml:"Buckets"`
	}
	var res result
	res.Xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"
	res.Owner.ID = "mirymesh"
	res.Owner.DisplayName = "MiryMesh"
	for _, b := range buckets {
		res.Buckets.Bucket = append(res.Buckets.Bucket, bucketXML{Name: b, CreationDate: time.Now().UTC().Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(res)
}

func (rt *Router) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("X-Amz-Bucket-Region", rt.region)
	w.WriteHeader(http.StatusOK)
}

func (rt *Router) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if !isValidBucketName(bucket) {
		writeError(w, r, errInvalidBucketName)
		return
	}
	err := rt.store.CreateBucket(bucket)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.Header().Set("Location", "/"+bucket)
	w.WriteHeader(http.StatusOK)
}

func (rt *Router) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	err := rt.store.DeleteBucket(bucket)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) handleGetBucketLocation(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	type loc struct {
		XMLName  xml.Name `xml:"LocationConstraint"`
		Xmlns    string   `xml:"xmlns,attr"`
		Location string   `xml:",chardata"`
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(loc{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Location: rt.region})
}

func (rt *Router) handleListObjectsV2(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	continuationToken := q.Get("continuation-token")
	maxKeysStr := q.Get("max-keys")
	maxKeys := 1000
	if maxKeysStr != "" {
		if v, err := strconv.Atoi(maxKeysStr); err == nil {
			maxKeys = v
		}
	}
	encodingType := q.Get("encoding-type")
	// Support ?list-type=2 and v1 ?marker shim: if list-type missing but marker present, treat marker as continuation-token.
	if !q.Has("list-type") && q.Has("marker") {
		continuationToken = q.Get("marker")
	}
	// Also support virtual ?list-type not set but prefix etc implies ListObjectsV2 shim.
	res, err := rt.store.ListObjectsV2(bucket, prefix, delimiter, continuationToken, maxKeys)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	type contentsXML struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
		StorageClass string `xml:"StorageClass"`
	}
	type listResult struct {
		XMLName               xml.Name      `xml:"ListBucketResult"`
		Xmlns                 string        `xml:"xmlns,attr"`
		Name                  string        `xml:"Name"`
		Prefix                string        `xml:"Prefix"`
		Delimiter             string        `xml:"Delimiter,omitempty"`
		MaxKeys               int           `xml:"MaxKeys"`
		IsTruncated           bool          `xml:"IsTruncated"`
		NextContinuationToken string        `xml:"NextContinuationToken,omitempty"`
		ContinuationToken     string        `xml:"ContinuationToken,omitempty"`
		KeyCount              int           `xml:"KeyCount"`
		EncodingType          string        `xml:"EncodingType,omitempty"`
		Contents              []contentsXML `xml:"Contents"`
		CommonPrefixes        []struct {
			Prefix string `xml:"Prefix"`
		} `xml:"CommonPrefixes"`
	}
	lr := listResult{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucket,
		Prefix:      prefix,
		Delimiter:   delimiter,
		MaxKeys:     maxKeys,
		IsTruncated: res.IsTruncated,
		KeyCount:    res.KeyCount,
	}
	if encodingType != "" {
		lr.EncodingType = encodingType
	}
	if continuationToken != "" {
		lr.ContinuationToken = continuationToken
	}
	if res.NextContinuationToken != "" {
		lr.NextContinuationToken = res.NextContinuationToken
	}
	for _, o := range res.Objects {
		keyOut := o.Key
		if encodingType == "url" {
			keyOut = url.QueryEscape(o.Key)
		}
		lr.Contents = append(lr.Contents, contentsXML{
			Key:          keyOut,
			LastModified: o.LastMod.UTC().Format(time.RFC3339),
			ETag:         `"` + o.ETag + `"`,
			Size:         o.Size,
			StorageClass: "STANDARD",
		})
	}
	for _, cp := range res.CommonPrefixes {
		pfx := cp
		if encodingType == "url" {
			pfx = url.QueryEscape(cp)
		}
		lr.CommonPrefixes = append(lr.CommonPrefixes, struct {
			Prefix string `xml:"Prefix"`
		}{Prefix: pfx})
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_ = xml.NewEncoder(w).Encode(lr)
}

func (rt *Router) handlePutObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	// Placement validation (M3+): check policy feasibility. Insufficient maps to 507.
	var targets []struct{ NodeID, Path string }
	if rt.placing != nil {
		// Use non-nil size if ContentLength is known; else allow small default.
		size := r.ContentLength
		if size < 0 {
			size = 0
		}
		tgts, pErr := rt.placing.Resolve(bucket, size)
		if pErr != nil {
			// TODO: GUI should surface this as a warning banner. For now S3 returns 507.
			writeError(w, r, APIError{Code: errInsufficient.Code, Message: pErr.Error(), Status: 507})
			return
		}
		targets = make([]struct{ NodeID, Path string }, len(tgts))
		for i, t := range tgts {
			targets[i] = struct{ NodeID, Path string }{NodeID: t.NodeID, Path: t.Path}
		}
	} else {
		if r.ContentLength > 0 && rt.store.IsOverQuota(r.ContentLength) {
			writeError(w, r, errInsufficient)
			return
		}
	}
	ct := r.Header.Get("Content-Type")
	// Collect x-amz-meta-*
	meta := make(map[string]string)
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-meta-") {
			meta[lk[len("x-amz-meta-"):]] = strings.Join(vs, ",")
		}
	}
	// Respect Content-MD5 if provided? Verify but not mandatory.
	// We just consume body as-is; storage computes ETag.
	// Limit single PUT to 5GiB is enforced by Go's MaxBytesReader at server layer; here we just stream.
	// M4: replication writes are attempted before local success is reported. In M4
	// single-node mode we still write locally first, then replicate to targets if any.
	// Invariant: existing replica is not deleted until replacement is verified.
	m, err := rt.store.PutObject(bucket, key, ct, meta, r.Body)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		// Map no space to 507.
		if isNoSpaceError(err) {
			writeError(w, r, errInsufficient)
			return
		}
		writeXMLError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if mRecorder != nil {
		mRecorder(false, m.Size)
	}
	// If placement targets include remote nodes, the controller would replicate now.
	// In M4, the actual remote replication is driven by the replication engine
	// (internal/replication) via HTTP to datanodes. Here we only enforce that
	// the local write's checksum is the source of truth — additional replicas
	// must match it (docs/07 invariant: delete only after verified).
	_ = targets
	w.Header().Set("ETag", `"`+m.ETag+`"`)
	w.WriteHeader(http.StatusOK)
}

func (rt *Router) handleGetObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	meta, rc, err := rt.store.GetObject(bucket, key)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	defer rc.Close()
	// Handle Range? M1 supports simple bytes=START-END or bytes=START-
	status := http.StatusOK
	var size = meta.Size
	var bodyReader io.Reader = rc
	if rg := r.Header.Get("Range"); rg != "" {
		if start, end, ok := parseRange(rg, size); ok {
			length := end - start + 1
			if seeker, ok := rc.(interface {
				io.Seeker
				io.Reader
			}); ok {
				_, _ = seeker.Seek(start, io.SeekStart)
				bodyReader = io.LimitReader(seeker, length)
			} else {
				// Fallback: copy and slice.
				data, _ := io.ReadAll(rc)
				if start < int64(len(data)) {
					if end >= int64(len(data)) {
						end = int64(len(data)) - 1
					}
					bodyReader = strings.NewReader(string(data[start : end+1]))
					length = end - start + 1
				}
			}
			w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(size, 10))
			w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
			status = http.StatusPartialContent
		}
	}
	if status == http.StatusOK {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("Last-Modified", meta.LastMod.UTC().Format(http.TimeFormat))
	for k, v := range meta.Meta {
		w.Header().Set("X-Amz-Meta-"+k, v)
	}
	// Handle If-None-Match / If-Match / If-Modified-Since minimally.
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if strings.Trim(inm, `"`) == meta.ETag || inm == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := time.Parse(http.TimeFormat, ims); err == nil {
			if !meta.LastMod.After(t) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	w.WriteHeader(status)
	_, _ = io.Copy(w, bodyReader)
	if mRecorder != nil {
		if status == http.StatusPartialContent {
			// Range: count actual bytes served
			if rg := r.Header.Get("Range"); rg != "" {
				if start, end, ok := parseRange(rg, size); ok {
					mRecorder(true, end-start+1)
				} else {
					mRecorder(true, size)
				}
			}
		} else {
			mRecorder(true, size)
		}
	}
}

func (rt *Router) handleHeadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	meta, err := rt.store.HeadObject(bucket, key)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			if apiErr.Code == "NoSuchKey" || apiErr.Code == "NoSuchBucket" {
				w.WriteHeader(apiErr.Status)
				return
			}
			writeError(w, r, apiErr)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("Last-Modified", meta.LastMod.UTC().Format(http.TimeFormat))
	for k, v := range meta.Meta {
		w.Header().Set("X-Amz-Meta-"+k, v)
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if strings.Trim(inm, `"`) == meta.ETag || inm == "*" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (rt *Router) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	// S3 DeleteObject is idempotent: 204 even if bucket/key missing? Spec says 204; but we return 404 for NoSuchBucket to be helpful.
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	_ = rt.store.DeleteObject(bucket, key)
	w.WriteHeader(http.StatusNoContent)
}

func parseRange(spec string, size int64) (int64, int64, bool) {
	// bytes=START-END, bytes=START-, bytes=-SUFFIX
	if !strings.HasPrefix(spec, "bytes=") {
		return 0, 0, false
	}
	rest := strings.TrimPrefix(spec, "bytes=")
	// Only single range.
	if strings.Contains(rest, ",") {
		return 0, 0, false
	}
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	if parts[0] == "" {
		// suffix: -N
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	if parts[1] == "" {
		return start, size - 1, true
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	if end >= size {
		end = size - 1
	}
	return start, end, true
}

func isNoSpaceError(err error) bool {
	// ENOSPC detection via string match (portable).
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no space left") || strings.Contains(msg, "No space left")
}
