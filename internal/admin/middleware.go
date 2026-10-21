// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// checkAuth validates X-Admin-Token. Uninitialized => setup POST allowed without token.
// After init: all admin routes require valid session token (or legacy adminToken for compat).
func (h *Handler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	// No setupStore (tests) — allow all for backward compat
	if h.setupStore == nil {
		return true
	}
	// Uninitialized: allow setup/status and setup POST.
	if !h.setupStore.IsInitialized() {
		if r.URL.Path == "/api/admin/setup/status" || r.URL.Path == "/api/admin/config" {
			return true
		}
		if r.URL.Path == "/api/admin/setup" && r.Method == http.MethodPost {
			return true
		}
	}
	// login is allowed but rate-limited; auth check itself passes for login route
	if r.URL.Path == "/api/admin/login" {
		return true
	}
	token := strings.TrimSpace(r.Header.Get("X-Admin-Token"))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	// If no token required (empty adminToken and no DB sessions), allow.
	if token == "" {
		if h.adminToken == "" {
			// Check if setupStore is Mem or nil with no sessions -> allow
			if _, ok := h.setupStore.(*MemSetupStore); ok {
				return true
			}
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	// Prefer session validation for any store that implements it.
	if sdb, ok := h.setupStore.(*SetupStoreDB); ok {
		if sdb.ValidateSession(token) {
			return true
		}
	} else if ms, ok := h.setupStore.(*MemSetupStore); ok {
		if ms.ValidateSession(token) {
			return true
		}
	}
	if h.adminToken != "" && token == h.adminToken {
		return true
	}
	// If adminToken empty and no DB, allow (test mode)
	if h.adminToken == "" {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// Gin-compatible middleware (used after migration).
func (h *Handler) AuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !h.checkAuth(w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- Rate limit for login (5/min per IP) ---

var loginLimiters sync.Map // map[string]*rate.Limiter

func allowLogin(ip string) bool {
	v, _ := loginLimiters.LoadOrStore(ip, rate.NewLimiter(rate.Every(time.Minute/5), 5))
	return v.(*rate.Limiter).Allow()
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return host
}
