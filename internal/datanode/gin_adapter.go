// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import "net/http"

func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request)  { h.handleRegister(w, r) }
func (h *Handler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) { h.handleHeartbeat(w, r) }
func (h *Handler) HandleAny(w http.ResponseWriter, r *http.Request) {
	// Dispatch by path prefix
	switch {
	case r.URL.Path == "/internal/register":
		h.handleRegister(w, r)
	case r.URL.Path == "/internal/heartbeat":
		h.handleHeartbeat(w, r)
	case r.URL.Path == "/internal/disks" || r.URL.Path == "/internal/nodes":
		if r.URL.Path == "/internal/disks" {
			h.handleDisks(w, r)
		} else {
			h.handleListNodes(w, r)
		}
	case len(r.URL.Path) > len("/internal/objects/") && r.URL.Path[:len("/internal/objects/")] == "/internal/objects/":
		h.handleObjects(w, r)
	default:
		http.NotFound(w, r)
	}
}
