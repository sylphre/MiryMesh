// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/sylphre/MiryMesh/internal/s3api"
)

// handleBucketQuota serves the storage limit of one bucket:
//
//	GET /api/admin/buckets/{bucket}/quota  -> {"bucket","max_bytes","used_bytes"}
//	PUT /api/admin/buckets/{bucket}/quota  body {"max_bytes": N}  (0 = unlimited)
//
// Authentication is enforced by handleBucketSub before this is called.
func (h *Handler) handleBucketQuota(w http.ResponseWriter, r *http.Request, bucket string) {
	if !h.storage.BucketExists(bucket) {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.writeBucketQuota(w, bucket)
	case http.MethodPut:
		var req struct {
			MaxBytes *int64 `json:"max_bytes"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.MaxBytes == nil {
			http.Error(w, "invalid json: max_bytes is required", http.StatusBadRequest)
			return
		}
		if *req.MaxBytes < 0 {
			http.Error(w, "max_bytes must not be negative", http.StatusBadRequest)
			return
		}
		if err := s3api.SetBucketQuota(h.storage, bucket, *req.MaxBytes); err != nil {
			if errors.Is(err, s3api.ErrQuotaUnsupported) {
				http.Error(w, "quota is not supported by the current storage backend", http.StatusNotImplemented)
				return
			}
			slog.Error("admin: set bucket quota failed", "bucket", bucket, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.writeBucketQuota(w, bucket)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) writeBucketQuota(w http.ResponseWriter, bucket string) {
	limit, err := s3api.GetBucketQuota(h.storage, bucket)
	if err != nil {
		slog.Error("admin: get bucket quota failed", "bucket", bucket, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	resp := map[string]any{"bucket": bucket, "max_bytes": limit, "used_bytes": nil}
	if used, uerr := s3api.BucketUsage(h.storage, bucket); uerr == nil {
		resp["used_bytes"] = used
	} else {
		slog.Warn("admin: bucket usage unavailable", "bucket", bucket, "err", uerr)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
