// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

func TestHandleNodeDisks(t *testing.T) {
	const adminToken = "admintok"
	var gotToken, gotBody string
	status := http.StatusOK
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/paths" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		gotToken = r.Header.Get("X-Internal-Token")
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		if status != http.StatusOK {
			http.Error(w, "nope", status)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","path":"/mnt/disk2","added":true}`))
	}))
	defer node.Close()

	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	if err := reg.RegisterNode("n1", node.URL); err != nil {
		t.Fatal(err)
	}
	h := New(reg, nil, nil, nil, adminToken)

	do := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/nodes/disks", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleNodeDisks(rec, req)
		return rec
	}

	rec := do(`{"node_id":"n1","path":"/mnt/disk2"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"added":true`) {
		t.Fatalf("ok: %d %s", rec.Code, rec.Body.String())
	}
	if want := datanode.NodeToken(adminToken, "n1"); gotToken != want {
		t.Fatalf("node got token %q; want the node credential", gotToken)
	}
	if !strings.Contains(gotBody, "/mnt/disk2") {
		t.Fatalf("node got body %q", gotBody)
	}

	if rec := do(`{"node_id":"n1","path":"relative"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("relative path: %d", rec.Code)
	}
	if rec := do(`{"node_id":"missing","path":"/mnt/disk2"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown node: %d", rec.Code)
	}
	status = http.StatusNotFound
	if rec := do(`{"node_id":"n1","path":"/mnt/disk2"}`); rec.Code != http.StatusConflict {
		t.Fatalf("old node: %d", rec.Code)
	}
	status = http.StatusBadRequest
	if rec := do(`{"node_id":"n1","path":"/mnt/disk2"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("rejected path: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/nodes/disks", nil)
	get := httptest.NewRecorder()
	h.HandleNodeDisks(get, req)
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", get.Code)
	}
}
