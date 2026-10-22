// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"

	"github.com/sylphre/MiryMesh/internal/s3api"
)

// maxAdminUploadBytes bounds a single upload through the admin API (5 GiB, the
// S3 single-PUT limit). It is a variable so tests can lower it.
var maxAdminUploadBytes int64 = 5 << 30

// handleBucketObject serves a single object for the web console:
//
//	GET    /api/admin/buckets/{bucket}/object?key=...  download (always as attachment)
//	PUT    /api/admin/buckets/{bucket}/object?key=...  upload (raw body)
//	DELETE /api/admin/buckets/{bucket}/object?key=...  delete (idempotent)
//
// Authentication is enforced by handleBucketSub before this is called.
func (h *Handler) handleBucketObject(w http.ResponseWriter, r *http.Request, bucket string) {
	if !h.storage.BucketExists(bucket) {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	key := r.URL.Query().Get("key")
	if err := s3api.ValidateObjectKey(key); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.downloadObject(w, bucket, key)
	case http.MethodPut:
		h.uploadObject(w, r, bucket, key)
	case http.MethodDelete:
		if err := h.storage.DeleteObject(bucket, key); err != nil {
			var apiErr s3api.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "NoSuchKey" {
				writeObjectError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) downloadObject(w http.ResponseWriter, bucket, key string) {
	meta, rc, err := h.storage.GetObject(bucket, key)
	if err != nil {
		writeObjectError(w, err)
		return
	}
	defer func() { _ = rc.Close() }()

	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	// Objects are user-supplied content served from the admin origin: force a
	// download and forbid MIME sniffing so uploaded HTML/SVG can never run here.
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(key)})
	if disposition == "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	if meta.ETag != "" {
		w.Header().Set("ETag", `"`+meta.ETag+`"`)
	}
	if !meta.LastMod.IsZero() {
		w.Header().Set("Last-Modified", meta.LastMod.UTC().Format(http.TimeFormat))
	}
	w.WriteHeader(http.StatusOK)
	// Headers are already sent; a mid-stream failure can only truncate the body.
	if _, err := io.Copy(w, rc); err != nil {
		slog.Warn("admin object download interrupted", "bucket", bucket, "err", err)
	}
}

func (h *Handler) uploadObject(w http.ResponseWriter, r *http.Request, bucket, key string) {
	body := http.MaxBytesReader(w, r.Body, maxAdminUploadBytes)
	meta, err := h.storage.PutObject(bucket, key, r.Header.Get("Content-Type"), nil, body)
	if err != nil {
		writeObjectError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"key":  meta.Key,
		"size": meta.Size,
		"etag": meta.ETag,
	})
}

// writeObjectError maps storage errors to HTTP statuses without leaking
// filesystem details for unexpected failures.
func writeObjectError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "object too large", http.StatusRequestEntityTooLarge)
		return
	}
	var apiErr s3api.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "NoSuchKey", "NoSuchBucket":
			http.Error(w, apiErr.Code, http.StatusNotFound)
			return
		}
	}
	slog.Error("admin object operation failed", "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
