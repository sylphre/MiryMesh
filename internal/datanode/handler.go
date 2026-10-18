// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Handler serves controller-side internal API for datanode registration,
// heartbeat and disk management. It does NOT handle S3 requests.
type Handler struct {
	registry *Registry
	// token is the shared registration token for M2 (single static token).
	// Later milestones will use one-time tokens issued via GUI.
	token string
}

// NewHandler creates a Handler.
func NewHandler(reg *Registry, token string) *Handler {
	return &Handler{registry: reg, token: token}
}

// RegisterRoutes mounts /internal/* endpoints on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/internal/register", h.handleRegister)
	mux.HandleFunc("/internal/heartbeat", h.handleHeartbeat)
	mux.HandleFunc("/internal/disks", h.handleDisks)
	mux.HandleFunc("/internal/nodes", h.handleListNodes)
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
