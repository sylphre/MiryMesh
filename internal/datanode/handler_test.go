// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerRegisterAndHeartbeat(t *testing.T) {
	reg := NewRegistry(0, 0)
	h := NewHandler(reg, "secrettoken")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Bad token -> 403.
	body, _ := json.Marshal(map[string]string{"node_id": "n1", "addr": "10.0.0.1:9001", "token": "bad"})
	req := httptest.NewRequest("POST", "/internal/register", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad token status=%d", rr.Code)
	}
	// Good register.
	body, _ = json.Marshal(map[string]string{"node_id": "n1", "addr": "10.0.0.1:9001", "token": "secrettoken"})
	req = httptest.NewRequest("POST", "/internal/register", bytes.NewReader(body))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Heartbeat.
	body, _ = json.Marshal(map[string]string{"node_id": "n1"})
	req = httptest.NewRequest("POST", "/internal/heartbeat", bytes.NewReader(body))
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d", rr.Code)
	}
	// List nodes.
	req = httptest.NewRequest("GET", "/internal/nodes", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list nodes status=%d", rr.Code)
	}
}

func TestHandlerDisks(t *testing.T) {
	reg := NewRegistry(0, 0)
	h := NewHandler(reg, "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	_ = reg.RegisterNode("n1", "10.0.0.1:9001")
	body, _ := json.Marshal(map[string]any{"node_id": "n1", "path": "/mnt/disk1", "capacity": 1000, "used": 100})
	req := httptest.NewRequest("POST", "/internal/disks", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("upsert disk status=%d body=%s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest("GET", "/internal/disks", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list disks status=%d", rr.Code)
	}
	req = httptest.NewRequest("DELETE", "/internal/disks?node_id=n1&path=/mnt/disk1", nil)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete disk status=%d", rr.Code)
	}
}
