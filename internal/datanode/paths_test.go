// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCleanDiskPath(t *testing.T) {
	good := map[string]string{
		"/mnt/disk2":     "/mnt/disk2",
		"  /mnt/disk2/ ": "/mnt/disk2",
		"/mnt/a/../b":    "/mnt/b",
	}
	for in, want := range good {
		got, err := CleanDiskPath(in)
		if err != nil || got != want {
			t.Errorf("CleanDiskPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "relative/path", "/", "/..", "/a\x00b", "/" + strings.Repeat("a", 5000)} {
		if _, err := CleanDiskPath(in); !errors.Is(err, ErrPathRejected) {
			t.Errorf("CleanDiskPath(%q) error = %v; want ErrPathRejected", in, err)
		}
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/mnt/disk1", "/mnt/disk1", true},
		{"/mnt/disk1", "/mnt/disk1/sub", true},
		{"/mnt/disk1/sub", "/mnt/disk1", true},
		{"/mnt/disk1", "/mnt/disk10", false},
		{"/mnt/disk1", "/srv/disk1", false},
	}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("PathsOverlap(%q, %q) = %v; want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestMergePathsKeepsBaseAsGiven(t *testing.T) {
	got := MergePaths([]string{"/mnt/disk1/", "/mnt/disk2"}, []string{"/mnt/disk1", "/mnt/disk3", "/mnt/disk3"})
	want := []string{"/mnt/disk1/", "/mnt/disk2", "/mnt/disk3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergePaths = %v; want %v", got, want)
	}
}

func TestExtraPathsRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "datanode.paths.json")
	if got, err := LoadExtraPaths(file); err != nil || got != nil {
		t.Fatalf("missing file: got %v, %v; want nil, nil", got, err)
	}
	if err := SaveExtraPaths(file, []string{"/mnt/disk2", "/mnt/disk3"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadExtraPaths(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/mnt/disk2", "/mnt/disk3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadExtraPaths = %v; want %v", got, want)
	}
}

func newPathsTestHandler() *Handler {
	return NewHandler(NewRegistry(30*time.Second, 90*time.Second), "tok")
}

func postPaths(h *Handler, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/internal/paths", strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	rec := httptest.NewRecorder()
	h.handlePaths(rec, req)
	return rec
}

func TestHandlePaths(t *testing.T) {
	h := newPathsTestHandler()
	if rec := postPaths(h, "wrong", `{"path":"/mnt/d"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	if rec := postPaths(h, "tok", `{"path":"/mnt/d"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("no adder: %d", rec.Code)
	}
	var seen string
	h.SetPathAdder(func(p string) (string, bool, error) {
		if p == "bad" {
			return "", false, ErrPathRejected
		}
		if p == "boom" {
			return "", false, errors.New("disk on fire")
		}
		seen = p
		return p, true, nil
	})
	rec := postPaths(h, "tok", `{"path":"/mnt/d"}`)
	if rec.Code != http.StatusOK || seen != "/mnt/d" || !strings.Contains(rec.Body.String(), `"added":true`) {
		t.Fatalf("ok: %d %s seen=%q", rec.Code, rec.Body.String(), seen)
	}
	if rec := postPaths(h, "tok", `{"path":"bad"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("rejected: %d", rec.Code)
	}
	if rec := postPaths(h, "tok", `{"path":"boom"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("internal: %d", rec.Code)
	}
	if rec := postPaths(h, "tok", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/paths", nil)
	req.Header.Set("X-Internal-Token", "tok")
	get := httptest.NewRecorder()
	h.handlePaths(get, req)
	if get.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", get.Code)
	}
}
