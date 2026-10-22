// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (rt *Router) handleCreateMultipartUpload(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	ct := r.Header.Get("Content-Type")
	meta := map[string]string{}
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-meta-") {
			meta[lk[len("x-amz-meta-"):]] = strings.Join(vs, ",")
		}
	}
	st, err := rt.store.CreateMultipartUpload(bucket, key, ct, meta)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	type resp struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Xmlns    string   `xml:"xmlns,attr"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Bucket: bucket, Key: key, UploadID: st.UploadID})
}

func (rt *Router) handleUploadPart(w http.ResponseWriter, r *http.Request, bucket, key string) {
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
	rec, err2 := rt.store.UploadPart(uploadID, partNumber, r.Body)
	if err2 != nil {
		if apiErr, ok := err2.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 400, "InvalidRequest", err2.Error())
		return
	}
	// Validate bucket/key match upload?
	s := rt.store.GetUpload(uploadID)
	if s != nil && (s.Bucket != bucket || s.Key != key) {
		writeError(w, r, APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: 404})
		return
	}
	w.Header().Set("ETag", `"`+rec.ETag+`"`)
	w.WriteHeader(200)
}

func (rt *Router) handleListParts(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	if uploadID == "" {
		writeError(w, r, errInvalidRequest)
		return
	}
	parts, err := rt.store.ListParts(uploadID)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	type partXML struct {
		PartNumber   int    `xml:"PartNumber"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	}
	type resp struct {
		XMLName     xml.Name  `xml:"ListPartsResult"`
		Xmlns       string    `xml:"xmlns,attr"`
		Bucket      string    `xml:"Bucket"`
		Key         string    `xml:"Key"`
		UploadID    string    `xml:"UploadId"`
		IsTruncated bool      `xml:"IsTruncated"`
		Parts       []partXML `xml:"Part"`
	}
	var pxml []partXML
	for _, p := range parts {
		pxml = append(pxml, partXML{PartNumber: p.PartNumber, ETag: `"` + p.ETag + `"`, Size: p.Size, LastModified: p.LastMod.UTC().Format("2006-01-02T15:04:05.000Z")})
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Bucket: bucket, Key: key, UploadID: uploadID, Parts: pxml})
}

func (rt *Router) handleAbortMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	if uploadID == "" {
		writeError(w, r, errInvalidRequest)
		return
	}
	if err := rt.store.AbortMultipartUpload(uploadID); err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	w.WriteHeader(204)
}

func (rt *Router) handleCompleteMultipart(w http.ResponseWriter, r *http.Request, bucket, key string) {
	q := r.URL.Query()
	uploadID := q.Get("uploadId")
	if uploadID == "" {
		writeError(w, r, errInvalidRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeXMLError(w, r, 400, "MalformedXML", err.Error())
		return
	}
	type completePartXML struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}
	type completeReq struct {
		XMLName xml.Name          `xml:"CompleteMultipartUpload"`
		Parts   []completePartXML `xml:"Part"`
	}
	var req completeReq
	if err := xml.Unmarshal(body, &req); err != nil {
		// Also try namespace-stripped? Accept without validation for now.
		writeXMLError(w, r, 400, "MalformedXML", err.Error())
		return
	}
	if len(req.Parts) == 0 {
		writeXMLError(w, r, 400, "MalformedXML", "no parts")
		return
	}
	var parts []CompletePart
	for _, p := range req.Parts {
		parts = append(parts, CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	// Completing replaces the object at key: a protected object must stay intact.
	if rt.rejectLocked(w, r, bucket, key) {
		return
	}
	meta, err2 := rt.store.CompleteMultipartUpload(uploadID, parts)
	if err2 != nil {
		if apiErr, ok := err2.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err2.Error())
		return
	}
	// The completed upload is a new object: it must not inherit old tags/ACL.
	if !rt.commitObjectTags(w, r, bucket, key, "") {
		return
	}
	type resp struct {
		XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
		Xmlns    string   `xml:"xmlns,attr"`
		Location string   `xml:"Location"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		ETag     string   `xml:"ETag"`
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Bucket: bucket, Key: key, Location: "/" + bucket + "/" + key, ETag: `"` + meta.ETag + `"`})
}

func (rt *Router) handleListMultipartUploads(w http.ResponseWriter, r *http.Request, bucket string) {
	uploads, err := rt.store.ListMultipartUploads(bucket)
	if err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	type uploadXML struct {
		Key       string `xml:"Key"`
		UploadID  string `xml:"UploadId"`
		Initiated string `xml:"Initiated"`
	}
	type resp struct {
		XMLName     xml.Name    `xml:"ListMultipartUploadsResult"`
		Xmlns       string      `xml:"xmlns,attr"`
		Bucket      string      `xml:"Bucket"`
		Uploads     []uploadXML `xml:"Upload"`
		IsTruncated bool        `xml:"IsTruncated"`
	}
	var uxml []uploadXML
	for _, u := range uploads {
		uxml = append(uxml, uploadXML{Key: u.Key, UploadID: u.UploadID, Initiated: u.Initiated.UTC().Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Bucket: bucket, Uploads: uxml})
}
