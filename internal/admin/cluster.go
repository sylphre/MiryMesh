// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/replication"
)

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
}

func (h *Handler) handleNodes(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodes := h.registry.ListNodes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes})
}

func (h *Handler) handleDisks(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodeID := r.URL.Query().Get("node_id")
	var disks []datanode.DiskPath
	if nodeID != "" {
		disks = h.registry.ListDisksForNode(nodeID)
	} else {
		disks = h.registry.ListDisks()
	}
	if disks == nil {
		disks = []datanode.DiskPath{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"disks": disks})
}

func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobs := h.jobs.List()
	if jobs == nil {
		jobs = []*replication.Job{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
}

func (h *Handler) handleCluster(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	nodes := h.registry.ListNodes()
	disks := h.registry.ListDisks()
	buckets := h.storage.ListBuckets()
	jobs := h.jobs.List()
	var totalCap, totalUsed int64
	for _, d := range disks {
		totalCap += d.Capacity
		totalUsed += d.Used
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"nodes":        nodes,
		"disks":        disks,
		"buckets":      buckets,
		"jobs":         jobs,
		"totalCap":     totalCap,
		"totalUsed":    totalUsed,
		"onlineNodes":  h.registry.OnlineNodeCount(),
		"totalNodes":   h.registry.NodeCount(),
		"totalBuckets": len(buckets),
		"totalJobs":    len(jobs),
	})
}
