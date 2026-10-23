// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sylphre/MiryMesh/internal/placement"
)

func (h *Handler) handleBuckets(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		buckets := h.storage.ListBuckets()
		if buckets == nil {
			buckets = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"buckets": buckets})
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if err := h.storage.CreateBucket(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"name": req.Name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleBucketSub(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/buckets/")
	parts := strings.Split(rest, "/")
	bucket := parts[0]
	if bucket == "" {
		http.Error(w, "bucket required", http.StatusBadRequest)
		return
	}
	if len(parts) == 1 {
		if r.Method == http.MethodDelete {
			if err := h.storage.DeleteBucket(bucket); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			// Grants must not outlive the bucket: a bucket re-created under the same name starts clean.
			if err := h.userMgr.RemoveBucket(bucket); err != nil {
				slog.Warn("admin: remove user grants of deleted bucket", "bucket", bucket, "err", err)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if parts[1] == "policy" {
		h.handleBucketPolicy(w, r, bucket)
		return
	}
	if parts[1] == "s3-policy" {
		h.handleS3Policy(w, r, bucket)
		return
	}
	if parts[1] == "quota" {
		h.handleBucketQuota(w, r, bucket)
		return
	}
	if parts[1] == "objects" {
		h.handleBucketObjects(w, r, bucket)
		return
	}
	if parts[1] == "object" {
		h.handleBucketObject(w, r, bucket)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) handleBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if h.resolver == nil {
		http.Error(w, "placement not configured", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		pol := h.resolver.PolicyFor(bucket)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"bucket": bucket, "policy": pol.String()})
	case http.MethodPut:
		var req struct {
			Policy string `json:"policy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		pol, err := placement.ParsePolicy(req.Policy)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := pol.ValidateForCluster(h.registry.OnlineNodeCount()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := h.resolver.SetBucketPolicy(bucket, pol); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"bucket": bucket, "policy": pol.String()})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleS3Policy(w http.ResponseWriter, r *http.Request, bucket string) {
	if !h.storage.BucketExists(bucket) {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		b, err := h.storage.GetBucketPolicy(bucket)
		if err != nil {
			http.Error(w, "NoSuchBucketPolicy", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(r.Body, 20*1024+1))
		if err != nil {
			http.Error(w, "MalformedPolicy", http.StatusBadRequest)
			return
		}
		if len(body) > 20*1024 {
			http.Error(w, "MalformedPolicy: too large (max 20KiB)", http.StatusBadRequest)
			return
		}
		// Validate JSON
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			http.Error(w, "MalformedPolicy: invalid JSON", http.StatusBadRequest)
			return
		}
		if err := h.storage.PutBucketPolicy(bucket, body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.storage.DeleteBucketPolicy(bucket); err != nil {
			http.Error(w, "NoSuchBucketPolicy", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleBucketObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.storage.BucketExists(bucket) {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	res, err := h.storage.ListObjectsV2(bucket, prefix, "", "", 1000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bucket":  bucket,
		"objects": res.Objects,
		"prefix":  prefix,
	})
}
