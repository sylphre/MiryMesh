// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"crypto/subtle"
	"net"
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
	if token != "" && h.validToken(token) {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// validToken reports whether token is a live session or the configured admin token.
// An empty adminToken never matches anything: a missing secret must not mean "open".
func (h *Handler) validToken(token string) bool {
	switch s := h.setupStore.(type) {
	case *SetupStoreDB:
		if s.ValidateSession(token) {
			return true
		}
	case *MemSetupStore:
		if s.ValidateSession(token) {
			return true
		}
	}
	cur := h.currentAdminToken()
	return cur != "" && subtle.ConstantTimeCompare([]byte(token), []byte(cur)) == 1
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

// clientIP returns the peer address. X-Forwarded-For is deliberately ignored:
// it is client-controlled and would let an attacker dodge the login rate limit.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
