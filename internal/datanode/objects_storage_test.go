// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

// objectsServer serves the DataNode object API over a real s3api.Storage,
// the same wiring cmd/datanode uses.
func objectsServer(t *testing.T, store *s3api.Storage) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	datanode.NewHandlerWithObjects(reg, "", s3api.NewDataNodeStore(store)).RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// objectsDo sends one request and returns the status code and the body.
func objectsDo(t *testing.T, method, target, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, target, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

func TestObjectsEndpointWithStorage(t *testing.T) {
	store, err := s3api.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if err = store.CreateBucket("probe-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	srv := objectsServer(t, store)
	obj := srv.URL + "/internal/objects/probe-bucket/dir%2Ffile.txt"

	if code, body := objectsDo(t, http.MethodPut, obj, "hello"); code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", code, body)
	}
	if code, body := objectsDo(t, http.MethodGet, obj, ""); code != http.StatusOK || body != "hello" {
		t.Fatalf("GET status=%d body=%q", code, body)
	}
	if code, _ := objectsDo(t, http.MethodHead, obj, ""); code != http.StatusOK {
		t.Fatalf("HEAD status=%d", code)
	}
	if code, body := objectsDo(t, http.MethodGet, srv.URL+"/internal/objects/probe-bucket?prefix=dir", ""); code != http.StatusOK || !strings.Contains(body, `"Key":"dir/file.txt"`) {
		t.Fatalf("LIST status=%d body=%s", code, body)
	}
	if code, body := objectsDo(t, http.MethodDelete, obj, ""); code != http.StatusNoContent {
		t.Fatalf("DELETE status=%d body=%s", code, body)
	}
	if code, _ := objectsDo(t, http.MethodGet, obj, ""); code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status=%d want 404", code)
	}
}
