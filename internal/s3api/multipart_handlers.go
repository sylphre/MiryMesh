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
	mpAlgo, mpType, mpCkErr := parseMultipartChecksum(r.Header)
	if mpCkErr != nil {
		writeError(w, r, *mpCkErr)
		return
	}
	customerKey, custErr := sseReqHeaders.parse(r.Header)
	if custErr != nil {
		writeError(w, r, *custErr)
		return
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
	// Remember the upload-wide settings: every part must use the same key and
	// checksum algorithm, and Complete needs them to build the object records.
	var cfg mpConfig
	if customerKey != nil {
		cfg.KeyMD5 = customerKey.md5
	}
	if mpAlgo != nil {
		cfg.Algo, cfg.Type = mpAlgo.name, mpType
	}
	if cfg != (mpConfig{}) {
		if perr := rt.putDoc(bucket, mpConfigKey(st.UploadID), subMPConfig, cfg); perr != nil {
			_ = rt.store.AbortMultipartUpload(st.UploadID)
			writeFailure(w, r, perr)
			return
		}
	}
	if mpAlgo != nil {
		w.Header().Set("X-Amz-Checksum-Algorithm", mpAlgo.name)
		w.Header().Set("X-Amz-Checksum-Type", mpType)
	}
	if customerKey != nil {
		sseEcho(w.Header(), customerKey.md5)
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
	// The upload must belong to this bucket/key before the body is consumed.
	up := rt.store.GetUpload(uploadID)
	if up == nil || up.Bucket != bucket || up.Key != key {
		writeError(w, r, errUploadGone)
		return
	}
	cfg, cfgErr := rt.loadMPConfig(bucket, uploadID)
	if cfgErr != nil {
		writeFailure(w, r, cfgErr)
		return
	}
	pw, prepErr := preparePart(r.Header, cfg, r.Body)
	if prepErr != nil {
		writeFailure(w, r, prepErr)
		return
	}
	rec, err2 := rt.store.UploadPart(uploadID, partNumber, pw.body)
	if err2 != nil {
		writeFailure(w, r, err2)
		return
	}
	if doc := pw.doc(cfg); doc != (mpPart{}) {
		if perr := rt.putDoc(bucket, mpPartKey(uploadID, partNumber), subMPPart, doc); perr != nil {
			writeFailure(w, r, perr)
			return
		}
	}
	pw.ver.setHeader(w.Header())
	if pw.key != nil {
		sseEcho(w.Header(), pw.key.md5)
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
	up := rt.store.GetUpload(uploadID)
	if err := rt.store.AbortMultipartUpload(uploadID); err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
			return
		}
		writeXMLError(w, r, 500, "InternalError", err.Error())
		return
	}
	rt.dropUploadDocs(up)
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
		ChecksumXML
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
	partSums := make([]ChecksumXML, 0, len(req.Parts))
	for _, p := range req.Parts {
		parts = append(parts, CompletePart{PartNumber: p.PartNumber, ETag: p.ETag})
		partSums = append(partSums, p.ChecksumXML)
	}
	// Completing replaces the object at key: a protected object must stay intact.
	if rt.rejectLocked(w, r, bucket, key) {
		return
	}
	// Work out the SSE-C segments and aggregate checksum first: this only reads
	// state, so a bad part list leaves the upload intact.
	plan, planErr := rt.planComplete(bucket, key, uploadID, parts, partSums, r.Header)
	if planErr != nil {
		writeFailure(w, r, planErr)
		return
	}
	meta, err2 := rt.store.CompleteMultipartUpload(uploadID, parts)
	if err2 != nil {
		writeFailure(w, r, err2)
		return
	}
	// The completed upload is a new object: it must not inherit old tags/ACL.
	if !rt.commitObjectTags(w, r, bucket, key, "") {
		return
	}
	if !rt.commitObjectDocs(w, r, bucket, key, plan.sse, plan.ck) {
		return
	}
	rt.dropUploadDocs(plan.up)
	if plan.sse != nil {
		sseEcho(w.Header(), plan.sse.KeyMD5)
	}
	type resp struct {
		XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
		Xmlns    string   `xml:"xmlns,attr"`
		Location string   `xml:"Location"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		ETag     string   `xml:"ETag"`
		ChecksumXML
		ChecksumType string `xml:"ChecksumType,omitempty"`
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/", Bucket: bucket, Key: key, Location: "/" + bucket + "/" + key, ETag: `"` + meta.ETag + `"`, ChecksumXML: plan.ck.toXML(), ChecksumType: plan.ck.typ()})
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
