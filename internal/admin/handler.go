// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"net/http"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/replication"
	"github.com/sylphre/MiryMesh/internal/s3api"
	"github.com/sylphre/MiryMesh/internal/auth"
)

// Handler serves management API under /api/admin/*.
// It is intended for the GUI and CLI; S3 API remains under / (SigV4).
// Auth is placeholder: if adminToken is non-empty, requests must carry
// X-Admin-Token header with same value. TODO(Phase4): integrate GUI session
// auth and AccessKey management.
type Handler struct {
	registry   *datanode.Registry
	storage    *s3api.Storage
	resolver   *placement.Resolver
	jobs       *replication.Store
	adminToken string
	setupStore *SetupStore
	authStore  *auth.Store
}

// New creates a Handler.
func New(reg *datanode.Registry, storage *s3api.Storage, resolver *placement.Resolver, jobs *replication.Store, adminToken string) *Handler {
	return NewWithSetup(reg, storage, resolver, jobs, adminToken, nil, nil)
}

// NewWithSetup creates a Handler with optional setup/auth stores.
func NewWithSetup(reg *datanode.Registry, storage *s3api.Storage, resolver *placement.Resolver, jobs *replication.Store, adminToken string, setup *SetupStore, authStore *auth.Store) *Handler {
	if jobs == nil {
		jobs = replication.NewStore()
	}
	return &Handler{registry: reg, storage: storage, resolver: resolver, jobs: jobs, adminToken: adminToken, setupStore: setup, authStore: authStore}
}

// RegisterRoutes mounts handlers on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/health", h.handleHealth)
	mux.HandleFunc("/api/admin/nodes", h.handleNodes)
	mux.HandleFunc("/api/admin/disks", h.handleDisks)
	mux.HandleFunc("/api/admin/buckets", h.handleBuckets)
	mux.HandleFunc("/api/admin/buckets/", h.handleBucketSub)
	mux.HandleFunc("/api/admin/jobs", h.handleJobs)
	mux.HandleFunc("/api/admin/cluster", h.handleCluster)
	h.setupRoutes(mux)
	if h.setupStore != nil {
		// Also expose config endpoints
		mux.HandleFunc("/api/admin/config", h.handleConfig)
		mux.HandleFunc("/api/admin/config/policy", h.handleConfigPolicy)
	}
}
