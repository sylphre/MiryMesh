// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"

	"github.com/sylphre/MiryMesh/internal/placement"
)

// mode returns server mode (gateway/standalone) as stored in config via setup.json? For now derive from storage gateway flag if available.
// Admin handler stores mode via config — expose via setupStore? For MVP, read from resolver? Simpler: expose as "standalone" unless gatewayPlaceholder is used.
// We expose mode from setupStore DefaultPolicy? No — mode is in ServerConfig.Mode. Handler doesn't have direct config access, so store it in SetupState extension.
// For MVP, expose mode via env hint: if disks exist, show standalone.

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	policy := "single"
	mode := "standalone"
	dbConfigured := false
	if h.setupStore != nil {
		if st := h.setupStore.Get(); st != nil {
			if st.DefaultPolicy != "" {
				policy = st.DefaultPolicy
			}
			if st.Gateway {
				mode = "gateway"
			}
			dbConfigured = st.DBDriver != "" && st.DBDSN != ""
		}
	}
	// isSetupNeeded: if not initialized, return minimal config بدون auth
	if h.setupStore == nil || !h.setupStore.IsInitialized() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default_policy": policy,
			"mode":           mode,
			"db_configured":  dbConfigured,
			"paths":          []string{},
			"disks":          []interface{}{},
			"initialized":    h.setupStore != nil && h.setupStore.IsInitialized(),
		})
		return
	}
	if !h.checkAuth(w, r) {
		return
	}
	disks := h.registry.ListDisks()
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
		"mode":           mode,
		"db_configured":  dbConfigured,
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
	// Gateway nodes don't store data locally — allow policy even with 0 datanodes
	isGateway := false
	if st := h.setupStore.Get(); st != nil {
		isGateway = st.Gateway
	}
	if !isGateway {
		if err := pol.ValidateForCluster(h.registry.OnlineNodeCount()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := h.setupStore.UpdatePolicy(pol.String()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.resolver != nil {
		_ = h.resolver.SetDefaultPolicy(pol)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"default_policy": pol.String()})
}
