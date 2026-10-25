// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// HandleNodeDisks (POST /api/admin/nodes/disks) adds a data path to one DataNode.
// Body: {"node_id": "...", "path": "/mnt/disk2"}. Disks can only be added; there
// is no way to remove or shrink one through this endpoint.
func (h *Handler) HandleNodeDisks(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeID string `json:"node_id"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" || !strings.HasPrefix(path, "/") || len(path) > 4096 || strings.ContainsRune(path, 0) {
		http.Error(w, "パスは / から始まる絶対パスで指定してください", http.StatusBadRequest)
		return
	}

	var (
		found  bool
		addr   string
		online bool
	)
	for _, n := range h.registry.ListNodes() {
		if n.ID == req.NodeID {
			found, addr, online = true, n.Addr, n.Status == datanode.StatusOnline
			break
		}
	}
	cred := h.registry.NodeCredential(req.NodeID, h.nodeCredentialSecret())
	switch {
	case !found:
		http.Error(w, "node not found", http.StatusNotFound)
		return
	case !online:
		http.Error(w, "ノードがオフラインです。起動してからディスクを追加してください", http.StatusConflict)
		return
	case cred == "":
		http.Error(w, "internal token is not configured on the Master", http.StatusConflict)
		return
	}

	base := addr
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	body, _ := json.Marshal(map[string]string{"path": path})
	nreq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/internal/paths", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid node address", http.StatusBadGateway)
		return
	}
	nreq.Header.Set("Content-Type", "application/json")
	nreq.Header.Set("X-Internal-Token", cred)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(nreq)
	if err != nil {
		http.Error(w, "ノードに接続できません: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(msg))

	switch resp.StatusCode {
	case http.StatusOK:
		out := struct {
			Path  string `json:"path"`
			Added bool   `json:"added"`
		}{Path: path, Added: true}
		_ = json.Unmarshal(msg, &out)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"node_id": req.NodeID,
			"path":    out.Path,
			"added":   out.Added,
		})
	case http.StatusNotFound:
		http.Error(w, "このノードは Master からのディスク追加に未対応です。ノード側で install.sh --update を一度実行してください", http.StatusConflict)
	case http.StatusBadRequest:
		http.Error(w, text, http.StatusBadRequest)
	default:
		http.Error(w, "ノードがディスク追加を受け付けませんでした (HTTP "+http.StatusText(resp.StatusCode)+"): "+text, http.StatusBadGateway)
	}
}
