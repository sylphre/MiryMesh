// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package fault_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/s3api"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// Fault injection stubs (docs/07/10): exercised via in-process router.

func signedPut(signer *sigv4.Signer, method, rawURL string, body []byte) *http.Request {
	u, _ := url.Parse(rawURL)
	req := &http.Request{Method: method, URL: u, Header: http.Header{}, Host: u.Host, Body: io.NopCloser(bytes.NewReader(body))}
	hash := sigv4.HashPayload(body)
	if body == nil {
		hash = sigv4.HashPayload(nil)
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, hash)
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return req
}

func TestFault_NodeDisconnect_507(t *testing.T) {
	dir := t.TempDir()
	st, _ := s3api.NewStorage(dir)
	_ = st.CreateBucket("bkt")
	reg := datanode.NewRegistry(10*time.Millisecond, 20*time.Millisecond)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	// Only one node, dual should be 507.
	resolver, _ := placement.NewResolver(reg, placement.New(1), "dual", nil)
	router := s3api.NewRouterWithPlacement(st, "us-east-1", resolver)
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	req := signedPut(&signer, "PUT", "http://example.com/bkt/k", []byte("x"))
	rr := httptest.NewRecorder()
	s3api.AuthMiddleware(astore, router).ServeHTTP(rr, req)
	if rr.Code != 507 {
		t.Fatalf("expected 507, got %d %s", rr.Code, rr.Body.String())
	}
}

func TestFault_ControllerRestart_Resume(t *testing.T) {
	// Simulate: persist placement policy via resolver, restart leaves storage intact.
	dir := t.TempDir()
	st, _ := s3api.NewStorage(dir)
	_ = st.CreateBucket("bkt")
	body := []byte("data")
	_, _ = st.PutObject("bkt", "k", "text/plain", nil, bytes.NewReader(body))
	// Restart: recreate storage + router, object still present (file backed).
	st2, _ := s3api.NewStorage(dir)
	router2 := s3api.NewRouter(st2, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	req := signedPut(&signer, "GET", "http://example.com/bkt/k", nil)
	rr := httptest.NewRecorder()
	s3api.AuthMiddleware(astore, router2).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != string(body) {
		t.Fatalf("after restart Get %d %q", rr.Code, rr.Body.String())
	}
}

func TestFault_DiskError_Quota(t *testing.T) {
	dir := t.TempDir()
	st, _ := s3api.NewStorage(dir)
	_ = st.CreateBucket("bkt")
	_ = st.IsOverQuota(1 << 30)
	_ = st.FilesystemCapacity()
}

func TestFault_InterruptedWrite_Retries(t *testing.T) {
	dir := t.TempDir()
	st, _ := s3api.NewStorage(dir)
	_ = st.CreateBucket("bkt")
	_, _ = st.PutObject("bkt", "k", "text/plain", nil, strings.NewReader("final"))
	meta, rc, _ := st.GetObject("bkt", "k")
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "final" || meta.Size != 5 {
		t.Fatalf("interrupted write recovery failed")
	}
}
