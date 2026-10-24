// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

// multiDiskServer serves the DataNode object API over two disks, the same
// wiring cmd/datanode uses. The first disk is the default.
func multiDiskServer(t *testing.T) (srv *httptest.Server, disk1, disk2 string) {
	t.Helper()
	disk1, disk2 = t.TempDir(), t.TempDir()
	s1, err := s3api.NewStorage(disk1)
	if err != nil {
		t.Fatalf("NewStorage disk1: %v", err)
	}
	s2, err := s3api.NewStorage(disk2)
	if err != nil {
		t.Fatalf("NewStorage disk2: %v", err)
	}
	store := s3api.NewDataNodeStoreWithDisks(s1, map[string]s3api.ObjectStore{disk1: s1, disk2: s2})
	mux := http.NewServeMux()
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	datanode.NewHandlerWithObjects(reg, "", store).RegisterRoutes(mux)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, disk1, disk2
}

func multiDiskDo(t *testing.T, method, target, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, target, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestObjectsMultiDisk(t *testing.T) {
	srv, disk1, disk2 := multiDiskServer(t)
	obj := srv.URL + "/internal/objects/bkt/k.txt"

	// Write to the second disk, as the placement engine would.
	if code, body := multiDiskDo(t, http.MethodPut, obj+"?disk="+url.QueryEscape(disk2), "hello"); code != http.StatusOK {
		t.Fatalf("PUT ?disk=disk2: got %d %q", code, body)
	}

	// The object lives on disk2 only.
	if code, _ := multiDiskDo(t, http.MethodGet, obj+"?disk="+url.QueryEscape(disk1), ""); code != http.StatusNotFound {
		t.Fatalf("GET ?disk=disk1: got %d, want 404", code)
	}
	if code, body := multiDiskDo(t, http.MethodGet, obj+"?disk="+url.QueryEscape(disk2), ""); code != http.StatusOK || body != "hello" {
		t.Fatalf("GET ?disk=disk2: got %d %q", code, body)
	}

	// Without ?disk= every disk is searched.
	if code, body := multiDiskDo(t, http.MethodGet, obj, ""); code != http.StatusOK || body != "hello" {
		t.Fatalf("GET without disk: got %d %q", code, body)
	}
	if code, _ := multiDiskDo(t, http.MethodHead, obj, ""); code != http.StatusOK {
		t.Fatalf("HEAD without disk: got %d, want 200", code)
	}
	if code, body := multiDiskDo(t, http.MethodGet, srv.URL+"/internal/objects/bkt", ""); code != http.StatusOK || !strings.Contains(body, "k.txt") {
		t.Fatalf("list without disk: got %d %q", code, body)
	}

	// A path that is not a configured disk is rejected.
	if code, _ := multiDiskDo(t, http.MethodPut, obj+"?disk="+url.QueryEscape("/not/a/configured/disk"), "x"); code != http.StatusBadRequest {
		t.Fatalf("PUT unknown disk: got %d, want 400", code)
	}

	// Delete without ?disk= removes it from the disk that has it.
	if code, body := multiDiskDo(t, http.MethodDelete, obj, ""); code != http.StatusNoContent {
		t.Fatalf("DELETE without disk: got %d %q", code, body)
	}
	if code, _ := multiDiskDo(t, http.MethodGet, obj, ""); code != http.StatusNotFound {
		t.Fatalf("GET after delete: got %d, want 404", code)
	}
}
