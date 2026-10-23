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

// handleBucketDomains serves the domain allow-list of one bucket:
//
//	GET /api/admin/buckets/{bucket}/domains -> {"bucket","domains":[...],"max":10}
//	PUT /api/admin/buckets/{bucket}/domains body {"domains": [...]}  ([] = accept any host)
//
// Authentication is enforced by handleBucketSub before this is called.
func (h *Handler) handleBucketDomains(w http.ResponseWriter, r *http.Request, bucket string) {
	if !h.storage.BucketExists(bucket) {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		domains, err := s3api.GetBucketDomains(h.storage, bucket)
		if err != nil {
			slog.Error("admin: get bucket domains failed", "bucket", bucket, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeBucketDomains(w, bucket, domains)
	case http.MethodPut:
		var req struct {
			Domains *[]string `json:"domains"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.Domains == nil {
			http.Error(w, "invalid json: domains is required", http.StatusBadRequest)
			return
		}
		domains, err := s3api.SetBucketDomains(h.storage, bucket, *req.Domains)
		switch {
		case err == nil:
		case errors.Is(err, s3api.ErrInvalidDomain), errors.Is(err, s3api.ErrTooManyDomains):
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		case errors.Is(err, s3api.ErrDomainsUnsupported):
			http.Error(w, "domains are not supported by the current storage backend", http.StatusNotImplemented)
			return
		default:
			slog.Error("admin: set bucket domains failed", "bucket", bucket, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeBucketDomains(w, bucket, domains)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeBucketDomains(w http.ResponseWriter, bucket string, domains []string) {
	if domains == nil {
		domains = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bucket":  bucket,
		"domains": domains,
		"max":     s3api.MaxBucketDomains,
	})
}
