// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// AdminTokenRotator is implemented by setup stores that can replace the Admin Token.
// After a successful call the old token no longer authenticates and the new one does.
type AdminTokenRotator interface {
	RotateAdminToken(newToken string) error
}

var errNotInitialized = errors.New("not initialized")

// currentAdminToken returns the token the admin API accepts today.
func (h *Handler) currentAdminToken() string {
	h.tokMu.RLock()
	defer h.tokMu.RUnlock()
	return h.adminToken
}

// nodeCredentialSecret returns the secret used to derive the legacy per-node
// credential. It is fixed when the Master starts and is not changed by a rotation,
// so nodes that still use the derived credential keep working until the Master restarts.
func (h *Handler) nodeCredentialSecret() string {
	h.tokMu.RLock()
	defer h.tokMu.RUnlock()
	return h.nodeSecret
}

// HandleRotateToken is the exported wrapper used by the Gin router.
func (h *Handler) HandleRotateToken(w http.ResponseWriter, r *http.Request) {
	h.handleRotateToken(w, r)
}

// handleRotateToken issues a new Admin Token (POST /api/admin/rotate-token).
// The caller must present a valid token. The response carries the new token; the
// old token and every existing session stop working immediately.
func (h *Handler) handleRotateToken(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.setupStore == nil || !h.setupStore.IsInitialized() {
		http.Error(w, errNotInitialized.Error(), http.StatusBadRequest)
		return
	}
	rot, ok := h.setupStore.(AdminTokenRotator)
	if !ok {
		http.Error(w, "admin token rotation is not supported by this setup store", http.StatusInternalServerError)
		return
	}

	h.rotateMu.Lock()
	defer h.rotateMu.Unlock()
	token := generateToken()
	if err := rot.RotateAdminToken(token); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.tokMu.Lock()
	h.adminToken = token
	h.tokMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"admin_token": token})
}

// RotateAdminToken stores newToken as the Admin Token, revokes every existing
// session and opens a fresh 24h session for newToken, all in one transaction.
func (s *SetupStoreDB) RotateAdminToken(newToken string) error {
	if strings.TrimSpace(newToken) == "" {
		return errors.New("empty admin token")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var username string
	if err := tx.QueryRow(`SELECT admin_username FROM setup WHERE id=`+s.ph(1), 1).Scan(&username); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE setup SET admin_token=`+s.ph(1)+`, updated_at=CURRENT_TIMESTAMP WHERE id=`+s.ph(2), newToken, 1); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET revoked=true`); err != nil {
		return err
	}
	exp := time.Now().Add(24 * time.Hour).UTC()
	if _, err := tx.Exec(`INSERT INTO sessions(token_hash, admin_username, expires_at) VALUES (`+s.ph(1)+`, `+s.ph(2)+`, `+s.ph(3)+`)`, hashToken(newToken), username, exp); err != nil {
		return err
	}
	return tx.Commit()
}

// RotateAdminToken replaces the in-memory Admin Token (dev / ephemeral mode).
func (m *MemSetupStore) RotateAdminToken(newToken string) error {
	if strings.TrimSpace(newToken) == "" {
		return errors.New("empty admin token")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil || !m.state.Initialized {
		return errNotInitialized
	}
	m.state.AdminToken = newToken
	return nil
}

var (
	_ AdminTokenRotator = (*SetupStoreDB)(nil)
	_ AdminTokenRotator = (*MemSetupStore)(nil)
)
