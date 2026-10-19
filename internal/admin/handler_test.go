// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/replication"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

func newAdminHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	st, _ := s3api.NewStorage(dir)
	reg := datanode.NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	resolver, _ := placement.NewResolver(reg, placement.New(1), "single", nil)
	jobs := replication.NewStore()
	return New(reg, st, resolver, jobs, "")
}

func TestAdminNodes(t *testing.T) {
	h := newAdminHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "/api/admin/nodes", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("nodes status=%d", rr.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(rr.Body).Decode(&body)
	if body["nodes"] == nil {
		t.Fatal("missing nodes")
	}
}

func TestAdminBucketsAndPolicy(t *testing.T) {
	h := newAdminHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	// Create bucket.
	payload, _ := json.Marshal(map[string]string{"name": "mybkt"})
	req := httptest.NewRequest("POST", "/api/admin/buckets", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create bucket status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Get policy (default single).
	req = httptest.NewRequest("GET", "/api/admin/buckets/mybkt/policy", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get policy status=%d", rr.Code)
	}
	// Set policy to dual should fail with 1 node.
	payload, _ = json.Marshal(map[string]string{"policy": "dual"})
	req = httptest.NewRequest("PUT", "/api/admin/buckets/mybkt/policy", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("dual with 1 node should be 400, got %d body=%s", rr.Code, rr.Body.String())
	}
	// Add second node, then dual succeeds.
	_ = h.registry.RegisterNode("n2", "n2:9001")
	_ = h.registry.UpsertDisk("n2", "/mnt/b", 1000, 100, 0)
	req = httptest.NewRequest("PUT", "/api/admin/buckets/mybkt/policy", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dual with 2 nodes should be 200, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminClusterAndJobs(t *testing.T) {
	h := newAdminHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest("GET", "/api/admin/cluster", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("cluster status=%d", rr.Code)
	}
	req = httptest.NewRequest("GET", "/api/admin/jobs", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("jobs status=%d", rr.Code)
	}
}
