// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/updater"
	"github.com/sylphre/MiryMesh/internal/version"
)

// SetUpdateChecker installs the release checker behind GET /api/admin/update.
func (h *Handler) SetUpdateChecker(c *updater.Checker) { h.updates = c }

func (h *Handler) updateStatus() updater.Status {
	if h.updates == nil {
		return updater.Status{Current: version.Version}
	}
	return h.updates.Status()
}

// HandleUpdateInfo (GET /api/admin/update) reports this Master's version and
// whether a newer release exists.
func (h *Handler) HandleUpdateInfo(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.updateStatus())
}

// HandleUpdateCheck (POST /api/admin/update/check) looks up the newest release now.
func (h *Handler) HandleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st := h.updateStatus()
	if h.updates != nil {
		st = h.updates.Check(r.Context())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}

// updateTarget picks the version a DataNode should move to. DataNodes follow the
// Master, so the default is the Master's own release version. A Master built from
// source ("dev") falls back to the newest published release.
func (h *Handler) updateTarget(requested string) (string, error) {
	if requested != "" {
		if !version.ValidTag(requested) {
			return "", errors.New("バージョンは v1.0.0 や v1.0.0-rc.1 の形式で指定してください")
		}
		return requested, nil
	}
	if version.ValidTag(version.Version) {
		return version.Version, nil
	}
	if latest := h.updateStatus().Latest; version.ValidTag(latest) {
		return latest, nil
	}
	return "", errors.New("更新先のバージョンが分かりません。先に「更新を確認」を実行してください")
}

// HandleNodeUpdate (POST /api/admin/nodes/update) asks one DataNode to update itself.
// Body: {"node_id": "...", "version": "v1.0.0-rc.1"}; version defaults to the Master's.
func (h *Handler) HandleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		NodeID  string `json:"node_id"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	target, err := h.updateTarget(strings.TrimSpace(req.Version))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
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
	switch {
	case !found:
		http.Error(w, "node not found", http.StatusNotFound)
		return
	case !online:
		http.Error(w, "ノードがオフラインです。起動してから更新してください", http.StatusConflict)
		return
	case h.registry.NodeCredential(req.NodeID, h.nodeCredentialSecret()) == "":
		http.Error(w, "internal token is not configured on the Master", http.StatusConflict)
		return
	}

	base := addr
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	body, _ := json.Marshal(map[string]string{"version": target})
	nreq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base+"/internal/update", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid node address", http.StatusBadGateway)
		return
	}
	nreq.Header.Set("Content-Type", "application/json")
	nreq.Header.Set("X-Internal-Token", h.registry.NodeCredential(req.NodeID, h.nodeCredentialSecret()))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(nreq)
	if err != nil {
		http.Error(w, "ノードに接続できません: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	text := strings.TrimSpace(string(msg))

	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusOK:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "updating", "node_id": req.NodeID, "version": target})
	case http.StatusNotFound:
		http.Error(w, "このノードは Master からの更新に未対応です。ノード側で install.sh --update を一度実行してください", http.StatusConflict)
	case http.StatusConflict:
		http.Error(w, text, http.StatusConflict)
	default:
		http.Error(w, fmt.Sprintf("ノードが更新を受け付けませんでした (HTTP %d): %s", resp.StatusCode, text), http.StatusBadGateway)
	}
}
