// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultDatanodePort = "9002"

// normalizeNodeAddr accepts "host", "host:port", "ip", "[v6]:port" and returns
// a canonical "host:port" (default port 9002). URLs and paths are rejected.
func normalizeNodeAddr(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("addr is required")
	}
	if strings.Contains(s, "://") || strings.ContainsAny(s, "/?#@ \t") {
		return "", errors.New("addr must be host or host:port")
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		// No port (or a bare IPv6 address).
		host, port = strings.Trim(s, "[]"), defaultDatanodePort
	}
	if !validNodeHost(host) {
		return "", errors.New("addr has an invalid host")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", errors.New("addr has an invalid port")
	}
	return net.JoinHostPort(host, port), nil
}

func validNodeHost(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, c := range host {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_':
		default:
			return false
		}
	}
	return true
}

// normalizeControllerURL returns "scheme://host[:port]" for an http(s) URL.
func normalizeControllerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("controller_url must be http(s)://host:port")
	}
	return u.Scheme + "://" + u.Host, nil
}

// HandleAdoptNode (POST /api/admin/nodes/adopt) asks the DataNode at addr to
// register itself with this controller. A fresh one-time pair token is issued
// and sent to the DataNode's /internal/adopt endpoint; the DataNode then runs
// the normal register flow with it.
func (h *Handler) HandleAdoptNode(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Addr          string `json:"addr"`
		ControllerURL string `json:"controller_url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	addr, err := normalizeNodeAddr(req.Addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctrl, err := normalizeControllerURL(req.ControllerURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sdb, ok := h.setupStore.(*SetupStoreDB)
	if !ok || sdb == nil {
		http.Error(w, "adopt requires DB setup", http.StatusInternalServerError)
		return
	}
	token, err := sdb.CreatePairToken(15 * time.Minute)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	payload, _ := json.Marshal(map[string]string{"controller_url": ctrl, "pair_token": token})
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Post("http://"+addr+"/internal/adopt", "application/json", bytes.NewReader(payload))
	if err != nil {
		http.Error(w, fmt.Sprintf("DataNode (%s) に接続できません: %v", addr, err), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		http.Error(w, "DataNode が adopt を受け付けていません（DataNode を --allow-adopt 付きで起動してください）", http.StatusBadGateway)
		return
	case http.StatusConflict:
		http.Error(w, "この DataNode は既に Master に登録済みです", http.StatusConflict)
		return
	default:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		http.Error(w, fmt.Sprintf("DataNode が登録依頼を拒否しました: %s %s", resp.Status, strings.TrimSpace(string(msg))), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "requested", "addr": addr})
}
