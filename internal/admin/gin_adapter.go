// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import "net/http"

// Exported wrappers for Gin integration (gin.go calls these).

func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request)              { h.handleHealth(w, r) }
func (h *Handler) HandleNodes(w http.ResponseWriter, r *http.Request)              { h.handleNodes(w, r) }
func (h *Handler) HandleDisks(w http.ResponseWriter, r *http.Request)              { h.handleDisks(w, r) }
func (h *Handler) HandleCluster(w http.ResponseWriter, r *http.Request)            { h.handleCluster(w, r) }
func (h *Handler) HandleMetrics(w http.ResponseWriter, r *http.Request)            { h.handleMetrics(w, r) }
func (h *Handler) HandleBuckets(w http.ResponseWriter, r *http.Request)            { h.handleBuckets(w, r) }
func (h *Handler) HandleBucketSub(w http.ResponseWriter, r *http.Request)          { h.handleBucketSub(w, r) }
func (h *Handler) HandleJobs(w http.ResponseWriter, r *http.Request)               { h.handleJobs(w, r) }
func (h *Handler) HandleSetupStatus(w http.ResponseWriter, r *http.Request)        { h.handleSetupStatus(w, r) }
func (h *Handler) HandleSetup(w http.ResponseWriter, r *http.Request)              { h.handleSetup(w, r) }
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request)              { h.handleLogin(w, r) }
func (h *Handler) HandleGenerateCredentials(w http.ResponseWriter, r *http.Request) { h.handleGenerateCredentials(w, r) }
func (h *Handler) HandlePairToken(w http.ResponseWriter, r *http.Request)          { h.handlePairToken(w, r) }
func (h *Handler) HandleConfig(w http.ResponseWriter, r *http.Request)             { h.handleConfig(w, r) }
func (h *Handler) HandleConfigPolicy(w http.ResponseWriter, r *http.Request)       { h.handleConfigPolicy(w, r) }
func (h *Handler) CheckAuth(w http.ResponseWriter, r *http.Request) bool           { return h.checkAuth(w, r) }

// Internal datanode handlers (controller side) — delegated
func (h *Handler) HandleInternalRegister(w http.ResponseWriter, r *http.Request) {
	// Will be wired via datanode handler; placeholder
	http.Error(w, "not wired", http.StatusNotImplemented)
}
func (h *Handler) HandleInternalHeartbeat(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not wired", http.StatusNotImplemented)
}
func (h *Handler) HandleInternalDisks(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not wired", http.StatusNotImplemented)
}
func (h *Handler) HandleInternalNodes(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not wired", http.StatusNotImplemented)
}
func (h *Handler) HandleInternalEvacuate(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not wired", http.StatusNotImplemented)
}
