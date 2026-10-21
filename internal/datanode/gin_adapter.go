// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import "net/http"

func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request)  { h.handleRegister(w, r) }
func (h *Handler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) { h.handleHeartbeat(w, r) }
func (h *Handler) HandleDisks(w http.ResponseWriter, r *http.Request)     { h.handleDisks(w, r) }
func (h *Handler) HandleNodes(w http.ResponseWriter, r *http.Request)     { h.handleListNodes(w, r) }
func (h *Handler) HandleEvacuate(w http.ResponseWriter, r *http.Request)  { h.handleEvacuate(w, r) }
func (h *Handler) HandleRestore(w http.ResponseWriter, r *http.Request)   { h.handleRestore(w, r) }
func (h *Handler) HandleObjects(w http.ResponseWriter, r *http.Request)   { h.handleObjects(w, r) }
func (h *Handler) HandleAny(w http.ResponseWriter, r *http.Request) {
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
	case r.URL.Path == "/internal/evacuate":
		h.handleEvacuate(w, r)
	case r.URL.Path == "/internal/restore":
		h.handleRestore(w, r)
	case len(r.URL.Path) > len("/internal/objects/") && r.URL.Path[:len("/internal/objects/")] == "/internal/objects/":
		h.handleObjects(w, r)
	default:
		http.NotFound(w, r)
	}
}
