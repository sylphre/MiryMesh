// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestAdminObjectLifecycle(t *testing.T) {
	h := newAdminHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	if err := h.storage.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	do := func(method, bucket, key string, body []byte, ct string) *httptest.ResponseRecorder {
		target := "/api/admin/buckets/" + bucket + "/object"
		if key != "" {
			target += "?key=" + url.QueryEscape(key)
		}
		req := httptest.NewRequest(method, target, bytes.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	// Upload.
	rr := do(http.MethodPut, "bkt", "docs/a b.txt", []byte("hello"), "text/plain")
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload status=%d body=%s", rr.Code, rr.Body.String())
	}
	var up struct {
		Key  string `json:"key"`
		Size int64  `json:"size"`
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &up); err != nil {
		t.Fatalf("upload response: %v", err)
	}
	if up.Key != "docs/a b.txt" || up.Size != 5 || up.ETag == "" {
		t.Fatalf("unexpected upload response: %+v", up)
	}

	// Download is always an attachment, with sniffing disabled.
	rr = do(http.MethodGet, "bkt", "docs/a b.txt", nil, "")
	if rr.Code != http.StatusOK || rr.Body.String() != "hello" {
		t.Fatalf("download status=%d body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}
	disp, params, err := mime.ParseMediaType(rr.Header().Get("Content-Disposition"))
	if err != nil || disp != "attachment" || params["filename"] != "a b.txt" {
		t.Fatalf("Content-Disposition=%q (%v)", rr.Header().Get("Content-Disposition"), err)
	}

	// Missing key, bad key, unknown bucket, wrong method.
	if rr = do(http.MethodGet, "bkt", "nope.txt", nil, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("missing key status=%d", rr.Code)
	}
	if rr = do(http.MethodGet, "bkt", "", nil, ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("no key param status=%d", rr.Code)
	}
	if rr = do(http.MethodPut, "bkt", "a/../b", []byte("x"), ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("traversal key status=%d", rr.Code)
	}
	if rr = do(http.MethodGet, "nobkt", "a.txt", nil, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown bucket status=%d", rr.Code)
	}
	if rr = do(http.MethodPost, "bkt", "a.txt", nil, ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", rr.Code)
	}

	// Delete is idempotent.
	if rr = do(http.MethodDelete, "bkt", "docs/a b.txt", nil, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", rr.Code)
	}
	if rr = do(http.MethodGet, "bkt", "docs/a b.txt", nil, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d", rr.Code)
	}
	if rr = do(http.MethodDelete, "bkt", "docs/a b.txt", nil, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("second delete status=%d", rr.Code)
	}
}

func TestAdminObjectUploadTooLarge(t *testing.T) {
	old := maxAdminUploadBytes
	maxAdminUploadBytes = 4
	t.Cleanup(func() { maxAdminUploadBytes = old })

	h := newAdminHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	if err := h.storage.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/admin/buckets/bkt/object?key=big.bin", bytes.NewReader([]byte("0123456789")))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
