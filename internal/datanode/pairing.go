// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
)

// ErrAlreadyPaired is returned by an adopter when the DataNode has already
// been registered with a controller.
var ErrAlreadyPaired = errors.New("datanode is already paired with a controller")

// tokenBox is a tiny mutex-guarded string used for the node token that is
// learned at runtime (after registration) while handlers are already serving.
type tokenBox struct {
	mu sync.RWMutex
	v  string
}

func (b *tokenBox) get() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.v
}

func (b *tokenBox) set(v string) {
	b.mu.Lock()
	b.v = v
	b.mu.Unlock()
}

// SetNodeToken installs the per-node token received from the controller (DataNode
// side). Once set, /internal/objects/* is authenticated with it instead of the
// bootstrap token. An empty value reverts to the bootstrap token.
func (h *Handler) SetNodeToken(tok string) { h.nodeTok.set(tok) }

// objectToken returns the token that /internal/objects/* requests must carry.
func (h *Handler) objectToken() string {
	if t := h.nodeTok.get(); t != "" {
		return t
	}
	return h.token
}

// NodeToken derives the per-node credential that a controller hands back after a
// successful registration. It is an HMAC of the node ID keyed by the controller's
// internal token, so it never reveals the internal token itself and is only valid
// for the node it was issued to.
func NodeToken(masterToken, nodeID string) string {
	m := hmac.New(sha256.New, []byte(masterToken))
	m.Write([]byte("mirymesh/node-token/v1:" + nodeID))
	return hex.EncodeToString(m.Sum(nil))
}

// SetPairConsumer installs the validator for one-time pair tokens (controller side).
// fn must return nil exactly once per valid token.
func (h *Handler) SetPairConsumer(fn func(token string) error) { h.pairConsume = fn }

// SetAdopter enables controller-initiated pairing (DataNode side). Without it,
// /internal/adopt answers 404.
func (h *Handler) SetAdopter(fn func(controllerURL, pairToken string) error) { h.adopter = fn }

// tokenOK reports whether tok authenticates nodeID: either the shared internal
// token or the node token derived from it. An empty handler token means "open".
func (h *Handler) tokenOK(nodeID, tok string) bool {
	if h.token == "" {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(h.token)) == 1 {
		return true
	}
	if nodeID == "" || tok == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(NodeToken(h.token, nodeID))) == 1
}

type adoptReq struct {
	ControllerURL string `json:"controller_url"`
	PairToken     string `json:"pair_token"`
}

// handleAdopt lets a controller ask this (still unpaired) DataNode to register
// itself using a one-time pair token.
func (h *Handler) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.adopter == nil {
		http.Error(w, "adopt is not enabled on this datanode", http.StatusNotFound)
		return
	}
	var req adoptReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := h.adopter(req.ControllerURL, req.PairToken); err != nil {
		if errors.Is(err, ErrAlreadyPaired) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "adopting"})
}
