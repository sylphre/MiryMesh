// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Multipart uploads on a DataNode, used by the Master in gateway mode.
//
// Routes (all need the node token in X-Internal-Token):
//
//	POST   /internal/multipart/uploads?bucket=&key=&disk=   create (JSON body: content_type, meta)
//	GET    /internal/multipart/uploads?bucket=                list uploads of a bucket
//	GET    /internal/multipart/uploads/{id}                   upload with its parts
//	DELETE /internal/multipart/uploads/{id}                   abort
//	PUT    /internal/multipart/uploads/{id}/parts/{n}         one part (body is streamed to disk)
//	GET    /internal/multipart/uploads/{id}/parts             list parts
//	POST   /internal/multipart/uploads/{id}/complete          assemble the object (JSON body: parts)
//
// Errors are JSON ({"code","message","status"}) with the same HTTP status, so
// the Master can hand the S3 error code (NoSuchUpload, InvalidPart, ...) to the client.

const (
	multipartPathPrefix     = "/internal/multipart/"
	maxMultipartControlBody = 4 << 20
	maxMultipartPartNumber  = 10000
)

// MultipartUploadInfo describes one multipart upload. Field names match
// s3api.MultipartUploadState because the Master decodes the JSON into that type.
type MultipartUploadInfo struct {
	UploadID    string
	Bucket      string
	Key         string
	ContentType string
	Meta        map[string]string
	Initiated   time.Time
	// Parts is filled only by GetMultipartUpload.
	Parts map[int]MultipartPartInfo
}

// MultipartPartInfo describes one uploaded part. Field names match
// s3api.PartRecord, without the local file path.
type MultipartPartInfo struct {
	PartNumber int
	ETag       string
	Size       int64
	LastMod    time.Time
}

// MultipartCompletePart names a part in a complete request.
type MultipartCompletePart struct {
	PartNumber int
	ETag       string
}

// MultipartError is an S3-style error that crosses the DataNode API as JSON.
type MultipartError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *MultipartError) Error() string { return e.Code + ": " + e.Message }

// MultipartAPI is implemented by a DataNode store that can hold multipart
// uploads (s3api.DataNodeStore). It lives here to avoid an import cycle.
// Errors that should reach the S3 client with their code are *MultipartError.
type MultipartAPI interface {
	// CreateMultipartUpload starts an upload on the given disk ("" = default disk).
	CreateMultipartUpload(bucket, key, contentType string, meta map[string]string, disk string) (MultipartUploadInfo, error)
	GetMultipartUpload(uploadID string) (MultipartUploadInfo, error)
	UploadPart(uploadID string, partNumber int, r io.Reader) (MultipartPartInfo, error)
	ListParts(uploadID string) ([]MultipartPartInfo, error)
	ListMultipartUploads(bucket string) ([]MultipartUploadInfo, error)
	AbortMultipartUpload(uploadID string) error
	CompleteMultipartUpload(uploadID string, parts []MultipartCompletePart) (ObjectInfo, error)
}

func (h *Handler) multipartAuthorized(r *http.Request) bool {
	want := h.objectToken()
	if want == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(want)) == 1
}

func writeMultipartJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeMultipartFailure(w http.ResponseWriter, err error) {
	var me *MultipartError
	if !errors.As(err, &me) {
		me = &MultipartError{Code: "InternalError", Message: err.Error(), Status: http.StatusInternalServerError}
	}
	status := me.Status
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	writeMultipartJSON(w, status, me)
}

func multipartBadRequest(code, msg string) *MultipartError {
	return &MultipartError{Code: code, Message: msg, Status: http.StatusBadRequest}
}

func multipartMethodNotAllowed(w http.ResponseWriter, allow ...string) {
	w.Header().Set("Allow", strings.Join(allow, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// decodeMultipartBody decodes a size-limited JSON body. An empty body is not an error.
func decodeMultipartBody(r *http.Request, v any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, maxMultipartControlBody)).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (h *Handler) handleMultipart(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "DataNode storage not configured", http.StatusInternalServerError)
		return
	}
	if !h.multipartAuthorized(r) {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	mp, ok := h.store.(MultipartAPI)
	if !ok {
		writeMultipartFailure(w, &MultipartError{Code: "NotImplemented", Message: "this DataNode storage has no multipart support", Status: http.StatusNotImplemented})
		return
	}
	segs := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, multipartPathPrefix), "/"), "/")
	if segs[0] != "uploads" {
		http.NotFound(w, r)
		return
	}
	if len(segs) == 1 {
		h.multipartCollection(w, r, mp)
		return
	}
	id := segs[1]
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch {
	case len(segs) == 2:
		h.multipartItem(w, r, mp, id)
	case len(segs) == 3 && segs[2] == "parts":
		if r.Method != http.MethodGet {
			multipartMethodNotAllowed(w, http.MethodGet)
			return
		}
		parts, err := mp.ListParts(id)
		if err != nil {
			writeMultipartFailure(w, err)
			return
		}
		if parts == nil {
			parts = []MultipartPartInfo{}
		}
		writeMultipartJSON(w, http.StatusOK, parts)
	case len(segs) == 4 && segs[2] == "parts":
		if r.Method != http.MethodPut {
			multipartMethodNotAllowed(w, http.MethodPut)
			return
		}
		n, err := strconv.Atoi(segs[3])
		if err != nil || n < 1 || n > maxMultipartPartNumber {
			writeMultipartFailure(w, multipartBadRequest("InvalidArgument", "part number must be between 1 and 10000"))
			return
		}
		// The body goes straight to a file on this node; it is never held in memory.
		rec, err := mp.UploadPart(id, n, r.Body)
		if err != nil {
			writeMultipartFailure(w, err)
			return
		}
		writeMultipartJSON(w, http.StatusOK, rec)
	case len(segs) == 3 && segs[2] == "complete":
		if r.Method != http.MethodPost {
			multipartMethodNotAllowed(w, http.MethodPost)
			return
		}
		var req struct {
			Parts []MultipartCompletePart `json:"parts"`
		}
		if err := decodeMultipartBody(r, &req); err != nil {
			writeMultipartFailure(w, multipartBadRequest("MalformedJSON", "invalid JSON body"))
			return
		}
		obj, err := mp.CompleteMultipartUpload(id, req.Parts)
		if err != nil {
			writeMultipartFailure(w, err)
			return
		}
		writeMultipartJSON(w, http.StatusOK, obj)
	default:
		http.NotFound(w, r)
	}
}

// multipartCollection serves /internal/multipart/uploads.
func (h *Handler) multipartCollection(w http.ResponseWriter, r *http.Request, mp MultipartAPI) {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodPost:
		bucket, key := q.Get("bucket"), q.Get("key")
		if bucket == "" || key == "" {
			writeMultipartFailure(w, multipartBadRequest("InvalidRequest", "bucket and key are required"))
			return
		}
		var req struct {
			ContentType string            `json:"content_type"`
			Meta        map[string]string `json:"meta"`
		}
		if err := decodeMultipartBody(r, &req); err != nil {
			writeMultipartFailure(w, multipartBadRequest("MalformedJSON", "invalid JSON body"))
			return
		}
		info, err := mp.CreateMultipartUpload(bucket, key, req.ContentType, req.Meta, q.Get("disk"))
		if err != nil {
			writeMultipartFailure(w, err)
			return
		}
		writeMultipartJSON(w, http.StatusOK, info)
	case http.MethodGet:
		bucket := q.Get("bucket")
		if bucket == "" {
			writeMultipartFailure(w, multipartBadRequest("InvalidRequest", "bucket is required"))
			return
		}
		list, err := mp.ListMultipartUploads(bucket)
		if err != nil {
			writeMultipartFailure(w, err)
			return
		}
		if list == nil {
			list = []MultipartUploadInfo{}
		}
		writeMultipartJSON(w, http.StatusOK, list)
	default:
		multipartMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

// multipartItem serves /internal/multipart/uploads/{id}.
func (h *Handler) multipartItem(w http.ResponseWriter, r *http.Request, mp MultipartAPI, id string) {
	switch r.Method {
	case http.MethodGet:
		info, err := mp.GetMultipartUpload(id)
		if err != nil {
			writeMultipartFailure(w, err)
			return
		}
		writeMultipartJSON(w, http.StatusOK, info)
	case http.MethodDelete:
		if err := mp.AbortMultipartUpload(id); err != nil {
			writeMultipartFailure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		multipartMethodNotAllowed(w, http.MethodGet, http.MethodDelete)
	}
}
