// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// Handler serves controller-side internal API for datanode registration,
// heartbeat and disk management. It does NOT handle S3 requests.
type Handler struct {
	registry *Registry
	// token is the shared registration token for M2 (single static token).
	// TODO: replace with GUI-issued one-time tokens (M4+). See M2-review #2.
	token string
	// evacDir is the controller-side evacuation dir for backup/restore (M5).
	evacDir string
}

// NewHandler creates a Handler.
func NewHandler(reg *Registry, token string) *Handler {
	return &Handler{registry: reg, token: token}
}

// NewHandlerWithEvacDir creates a Handler with evacuation dir.
func NewHandlerWithEvacDir(reg *Registry, token, evacDir string) *Handler {
	return &Handler{registry: reg, token: token, evacDir: evacDir}
}

// RegisterRoutes mounts /internal/* endpoints on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/internal/register", h.handleRegister)
	mux.HandleFunc("/internal/heartbeat", h.handleHeartbeat)
	mux.HandleFunc("/internal/disks", h.handleDisks)
	mux.HandleFunc("/internal/nodes", h.handleListNodes)
	mux.HandleFunc("/internal/evacuate", h.handleEvacuate)
	mux.HandleFunc("/internal/restore", h.handleRestore)
}

type registerReq struct {
	NodeID string `json:"node_id"`
	Addr   string `json:"addr"`
	Token  string `json:"token"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if h.token != "" && req.Token != h.token {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if strings.TrimSpace(req.NodeID) == "" || strings.TrimSpace(req.Addr) == "" {
		http.Error(w, "node_id and addr required", http.StatusBadRequest)
		return
	}
	if err := h.registry.RegisterNode(req.NodeID, req.Addr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type heartbeatReq struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"` // optional: same token check if configured
}

func (h *Handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req heartbeatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if h.token != "" && req.Token != "" && req.Token != h.token {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if err := h.registry.Heartbeat(req.NodeID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// diskReq represents upsert/delete payload for a disk path.
type diskReq struct {
	NodeID   string `json:"node_id"`
	Path     string `json:"path"`
	Capacity int64  `json:"capacity"`
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
	Token    string `json:"token"`
}

func (h *Handler) handleDisks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListDisks(w, r)
	case http.MethodPost:
		var req diskReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if h.token != "" && req.Token != "" && req.Token != h.token {
			http.Error(w, "invalid token", http.StatusForbidden)
			return
		}
		if err := h.registry.UpsertDisk(req.NodeID, req.Path, req.Capacity, req.Used, req.Limit); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	case http.MethodDelete:
		nodeID := r.URL.Query().Get("node_id")
		path := r.URL.Query().Get("path")
		if nodeID == "" || path == "" {
			http.Error(w, "node_id and path required", http.StatusBadRequest)
			return
		}
		if err := h.registry.RemoveDisk(nodeID, path); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleListDisks(w http.ResponseWriter, _ *http.Request) {
	disks := h.registry.ListDisks()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"disks": disks})
}

func (h *Handler) handleListNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := h.registry.ListNodes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes})
}

func (h *Handler) handleEvacuate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Token string `json:"token"`
		// Snapshot payload: if empty, evacuates current registry snapshot.
		Snapshot *metadata.EvacuationSnapshot `json:"snapshot,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if h.token != "" && req.Token != h.token {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	dir := h.evacDir
	if dir == "" {
		dir = os.TempDir()
	}
	ev, err := metadata.NewEvacuator(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	snap := req.Snapshot
	if snap == nil {
		// Build from registry: we record buckets from disks? For M5 we record node list.
		snap = &metadata.EvacuationSnapshot{Buckets: []string{}}
		for _, n := range h.registry.ListNodes() {
			snap.Buckets = append(snap.Buckets, n.ID)
		}
	}
	if err := ev.Save(snap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": snap.Version})
}

func (h *Handler) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dir := h.evacDir
	if dir == "" {
		dir = os.TempDir()
	}
	snap, err := metadata.LoadAny(dir, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("no snapshot: %v", err), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}
