// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"

	"github.com/sylphre/MiryMesh/internal/placement"
)

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if h.setupStore == nil {
		http.Error(w, "setup not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	policy := "single"
	if st := h.setupStore.Get(); st != nil && st.DefaultPolicy != "" {
		policy = st.DefaultPolicy
	}
	disks := h.registry.ListDisks()
	// Collect datanode.paths from disks (unique paths)
	paths := make([]string, 0, len(disks))
	seen := map[string]bool{}
	for _, d := range disks {
		if !seen[d.Path] {
			seen[d.Path] = true
			paths = append(paths, d.Path)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"default_policy": policy,
		"paths":          paths,
		"disks":          disks,
		"initialized":    h.setupStore.IsInitialized(),
	})
}

func (h *Handler) handleConfigPolicy(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if h.setupStore == nil {
		http.Error(w, "setup not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Policy string `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	pol, err := placement.ParsePolicy(req.Policy)
	if err != nil {
		http.Error(w, err.Error(), asStatus(err))
		return
	}
	if err := pol.ValidateForCluster(h.registry.OnlineNodeCount()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Persist to setupStore
	st := h.setupStore.Get()
	if st == nil {
		st = &SetupState{Initialized: true}
	}
	st.DefaultPolicy = pol.String()
	// Save via SetupStore.Save would fail if already initialized (guard) — update directly
	h.setupStore.mu.Lock()
	h.setupStore.state = st
	h.setupStore.mu.Unlock()
	if saveErr := h.setupStore.Save(st); saveErr != nil && saveErr != errAlreadyInitialized {
		h.setupStore.mu.Lock()
		data, _ := json.MarshalIndent(st, "", "  ")
		h.setupStore.mu.Unlock()
		_ = data
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"default_policy": pol.String()})
}
