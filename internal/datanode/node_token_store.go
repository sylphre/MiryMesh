// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// NodeTokenStore keeps one random credential per DataNode on the Master (the
// database, sealed with the master key). The credential does not depend on the
// Admin / internal token, so changing that token never disconnects a paired node.
type NodeTokenStore interface {
	// GetNodeToken returns the token of nodeID, or false when it has none.
	GetNodeToken(nodeID string) (string, bool)
	// PutNodeToken stores token for nodeID and replaces any previous one.
	PutNodeToken(nodeID, token string) error
}

// newRandomToken returns 32 random bytes, hex encoded.
func newRandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("node token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SetNodeTokenStore makes the Master issue and check random per-node tokens kept in
// s, and lets the Registry hand the same tokens to the code that calls the nodes.
// Without a store the handler keeps the old behavior (token derived from the
// internal token), which is also what lets existing nodes migrate.
func (h *Handler) SetNodeTokenStore(s NodeTokenStore) {
	h.tokens = s
	if h.registry == nil {
		return
	}
	if s == nil {
		h.registry.SetTokenSource(nil)
		return
	}
	h.registry.SetTokenSource(s.GetNodeToken)
}

// legacyNodeTokenOK accepts the token that older Masters derived from the internal
// token. It is only used so that a node paired before per-node tokens existed can
// register once and receive its random token.
func (h *Handler) legacyNodeTokenOK(nodeID, tok string) bool {
	if h.token == "" || nodeID == "" || tok == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(NodeToken(h.token, nodeID))) == 1
}

// issueNodeToken returns the token to hand back after a successful registration.
// A node that presents its stored token keeps it. Any other accepted credential (a
// fresh pair token, the internal token, the legacy derived token) gets a new random
// token that replaces the old one.
func (h *Handler) issueNodeToken(nodeID, presented string, viaPair bool) (string, error) {
	if h.tokens == nil {
		return NodeToken(h.token, nodeID), nil
	}
	if !viaPair {
		if cur, ok := h.tokens.GetNodeToken(nodeID); ok && subtle.ConstantTimeCompare([]byte(presented), []byte(cur)) == 1 {
			return cur, nil
		}
	}
	tok, err := newRandomToken()
	if err != nil {
		return "", err
	}
	if err := h.tokens.PutNodeToken(nodeID, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// SetTokenSource installs the lookup for per-node tokens (Master side).
func (r *Registry) SetTokenSource(fn func(nodeID string) (string, bool)) {
	r.mu.Lock()
	r.tokenSrc = fn
	r.mu.Unlock()
}

// NodeCredential returns the token the Master sends to nodeID: its stored random
// token, or, for a node that has not migrated yet, the token derived from
// legacyMaster. It returns "" when neither is available.
func (r *Registry) NodeCredential(nodeID, legacyMaster string) string {
	r.mu.RLock()
	src := r.tokenSrc
	r.mu.RUnlock()
	if src != nil {
		if tok, ok := src(nodeID); ok && tok != "" {
			return tok
		}
	}
	if legacyMaster == "" {
		return ""
	}
	return NodeToken(legacyMaster, nodeID)
}
