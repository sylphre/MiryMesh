// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

func TestPlacementValidationInHandler(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	_ = st.CreateBucket("bkt")
	reg := datanode.NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	// Only one node, dual should fail with 507.
	resolver, _ := placement.NewResolver(reg, placement.New(1), "dual", nil)
	router := NewRouterWithPlacement(st, "us-east-1", resolver)
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	body := []byte("hello")
	u, _ := url.Parse("http://example.com/bkt/key.txt")
	req := &http.Request{Method: "PUT", URL: u, Header: http.Header{}, Host: "example.com", Body: io.NopCloser(bytes.NewReader(body))}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, sigv4.HashPayload(body))
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rr := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr, req)
	if rr.Code != 507 {
		t.Fatalf("dual with 1 node should be 507, got %d body=%s", rr.Code, rr.Body.String())
	}
	// Single should pass.
	resolver2, _ := placement.NewResolver(reg, placement.New(1), "single", nil)
	router.SetPlacement(resolver2)
	req2 := &http.Request{Method: "PUT", URL: u, Header: http.Header{}, Host: "example.com", Body: io.NopCloser(bytes.NewReader(body))}
	signer.Sign(req2, now, sigv4.HashPayload(body))
	req2.Body = io.NopCloser(bytes.NewReader(body))
	req2.ContentLength = int64(len(body))
	rr2 := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("single should be 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	// Bucket-specific policy overrides default.
	_ = reg.RegisterNode("n2", "n2:9001")
	_ = reg.UpsertDisk("n2", "/mnt/b", 1000, 100, 0)
	resolver3, _ := placement.NewResolver(reg, placement.New(1), "single", map[string]string{"bkt": "dual"})
	router.SetPlacement(resolver3)
	// Now dual should succeed with 2 nodes.
	body3 := []byte("world")
	u3, _ := url.Parse("http://example.com/bkt/another.txt")
	req3 := &http.Request{Method: "PUT", URL: u3, Header: http.Header{}, Host: "example.com", Body: io.NopCloser(bytes.NewReader(body3))}
	signer.Sign(req3, now, sigv4.HashPayload(body3))
	req3.Body = io.NopCloser(bytes.NewReader(body3))
	req3.ContentLength = int64(len(body3))
	rr3 := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("bucket dual with 2 nodes should be 200, got %d body=%s", rr3.Code, rr3.Body.String())
	}
	// ALL with 2 nodes should also succeed.
	resolver4, _ := placement.NewResolver(reg, placement.New(1), "ALL", nil)
	router.SetPlacement(resolver4)
	body4 := []byte("all")
	u4, _ := url.Parse("http://example.com/bkt/for-all.txt")
	req4 := &http.Request{Method: "PUT", URL: u4, Header: http.Header{}, Host: "example.com", Body: io.NopCloser(bytes.NewReader(body4))}
	signer.Sign(req4, now, sigv4.HashPayload(body4))
	req4.Body = io.NopCloser(bytes.NewReader(body4))
	req4.ContentLength = int64(len(body4))
	rr4 := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Fatalf("ALL with 2 nodes should be 200, got %d", rr4.Code)
	}
	_ = bytes.Compare(nil, nil)
}
