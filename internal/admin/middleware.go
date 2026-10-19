// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import "net/http"

func (h *Handler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	// Allow setup/login without token
	if r.URL.Path == "/api/admin/setup" || r.URL.Path == "/api/admin/setup/status" || r.URL.Path == "/api/admin/login" || r.URL.Path == "/api/admin/generate-credentials" {
		return true
	}
	if h.adminToken == "" {
		return true
	}
	if r.Header.Get("X-Admin-Token") == h.adminToken {
		return true
	}
	if r.URL.Query().Get("token") == h.adminToken {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}
