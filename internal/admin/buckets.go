// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
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
