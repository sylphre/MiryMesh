// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3compat_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/s3api"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

func signedM7(t *testing.T, signer *sigv4.Signer, method, rawURL string, hdr map[string]string, body []byte) *http.Request {
	t.Helper()
	u, _ := url.Parse(rawURL)
	req := &http.Request{Method: method, URL: u, Header: http.Header{}, Host: u.Host, Body: io.NopCloser(strings.NewReader(string(body)))}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	hash := sigv4.HashPayload(body)
	if body == nil {
		hash = sigv4.HashPayload(nil)
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, hash)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Body = io.NopCloser(strings.NewReader(string(body)))
	}
	return req
}

func TestE2E_M7_CopyAndDeleteObjects(t *testing.T) {
	dir := t.TempDir()
	st, _ := s3api.NewStorage(dir)
	router := s3api.NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	do := func(req *http.Request) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s3api.AuthMiddleware(astore, router).ServeHTTP(rr, req)
		return rr
	}
	// Setup bucket + source
	req := signedM7(t, &signer, "PUT", "http://example.com/m7bkt", nil, nil)
	rr := do(req)
	if rr.Code != 200 {
		t.Fatalf("CreateBucket %d", rr.Code)
	}
	req = signedM7(t, &signer, "PUT", "http://example.com/m7bkt/src.txt", map[string]string{"Content-Type": "text/plain", "x-amz-meta-foo": "bar"}, []byte("m7data"))
	rr = do(req)
	if rr.Code != 200 {
		t.Fatalf("Put src %d", rr.Code)
	}
	// CopyObject COPY
	req = signedM7(t, &signer, "PUT", "http://example.com/m7bkt/dst.txt", map[string]string{"X-Amz-Copy-Source": "/m7bkt/src.txt"}, nil)
	rr = do(req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "CopyObjectResult") {
		t.Fatalf("Copy %d %s", rr.Code, rr.Body.String())
	}
	// Get dest
	req = signedM7(t, &signer, "GET", "http://example.com/m7bkt/dst.txt", nil, nil)
	rr = do(req)
	if rr.Code != 200 || rr.Body.String() != "m7data" {
		t.Fatalf("Get dst %d %q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Amz-Meta-Foo") != "bar" {
		t.Fatalf("meta %q", rr.Header().Get("X-Amz-Meta-Foo"))
	}
	// DeleteObjects batch
	body := `<Delete><Object><Key>src.txt</Key></Object><Object><Key>dst.txt</Key></Object></Delete>`
	req = signedM7(t, &signer, "POST", "http://example.com/m7bkt?delete", nil, []byte(body))
	rr = do(req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "DeleteResult") {
		t.Fatalf("DeleteObjects %d %s", rr.Code, rr.Body.String())
	}
	// Both gone
	req = signedM7(t, &signer, "GET", "http://example.com/m7bkt/src.txt", nil, nil)
	rr = do(req)
	if rr.Code != 404 {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
	// DeleteBucket still 409 if not empty (create remain then try)
	req = signedM7(t, &signer, "PUT", "http://example.com/m7bkt/remain.txt", nil, []byte("x"))
	rr = do(req)
	if rr.Code != 200 {
		t.Fatalf("Put remain %d", rr.Code)
	}
	req = signedM7(t, &signer, "DELETE", "http://example.com/m7bkt", nil, nil)
	rr = do(req)
	if rr.Code != 409 {
		t.Fatalf("DeleteBucket non-empty should be 409, got %d", rr.Code)
	}
}
