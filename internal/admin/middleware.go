// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import "net/http"

func (h *Handler) checkAuth(w http.ResponseWriter, r *http.Request) bool {
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
