// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3compat_test

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
	"github.com/sylphre/MiryMesh/internal/s3api"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// In-process E2E: real S3 router + SigV4, exercises Nextcloud/Misskey P0 set.
func newE2E(t *testing.T) (*s3api.Router, *auth.Store, *sigv4.Signer) {
	t.Helper()
	dir := t.TempDir()
	st, err := s3api.NewStorage(dir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	router := s3api.NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"TESTKEY": "testsecret12345"})
	signer := &sigv4.Signer{AccessKey: "TESTKEY", SecretKey: "testsecret12345", Region: "us-east-1", Service: "s3"}
	return router, astore, signer
}

func signedPut(t *testing.T, signer *sigv4.Signer, method, rawURL string, body []byte) *http.Request {
	t.Helper()
	u, _ := url.Parse(rawURL)
	req := &http.Request{Method: method, URL: u, Header: http.Header{}, Host: u.Host, Body: io.NopCloser(bytes.NewReader(body))}
	payloadHash := sigv4.HashPayload(body)
	if body == nil {
		payloadHash = sigv4.HashPayload(nil)
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, payloadHash)
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return req
}

func doE2E(router *s3api.Router, astore *auth.Store, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s3api.AuthMiddleware(astore, router).ServeHTTP(rr, req)
	return rr
}

// P0 baseline: Put/Get/Delete/List/Head (+ Range, error format, xmlns).
func TestE2E_P0(t *testing.T) {
	router, astore, signer := newE2E(t)
	// CreateBucket (Nextcloud autocreate)
	req := signedPut(t, signer, "PUT", "http://example.com/e2e-bkt", nil)
	rr := doE2E(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket %d %s", rr.Code, rr.Body.String())
	}
	// HeadBucket verify_bucket_exists
	req = signedPut(t, signer, "HEAD", "http://example.com/e2e-bkt", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("HeadBucket %d", rr.Code)
	}
	// GetBucketLocation
	req = signedPut(t, signer, "GET", "http://example.com/e2e-bkt?location", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "us-east-1") {
		t.Fatalf("Location %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "http://s3.amazonaws.com/doc/2006-03-01/") {
		t.Fatalf("missing xmlns")
	}
	// PutObject
	body := []byte("nextcloud-p0")
	req = signedPut(t, signer, "PUT", "http://example.com/e2e-bkt/file.txt", body)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK || rr.Header().Get("ETag") == "" {
		t.Fatalf("PutObject %d ETag=%q", rr.Code, rr.Header().Get("ETag"))
	}
	etag := rr.Header().Get("ETag")
	// HeadObject
	req = signedPut(t, signer, "HEAD", "http://example.com/e2e-bkt/file.txt", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK || rr.Header().Get("ETag") != etag {
		t.Fatalf("HeadObject %d ETag %q", rr.Code, rr.Header().Get("ETag"))
	}
	// GetObject + Range
	req = signedPut(t, signer, "GET", "http://example.com/e2e-bkt/file.txt", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK || rr.Body.String() != string(body) {
		t.Fatalf("Get %d %q", rr.Code, rr.Body.String())
	}
	req = signedPut(t, signer, "GET", "http://example.com/e2e-bkt/file.txt", nil)
	req.Header.Set("Range", "bytes=0-3")
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("Range %d", rr.Code)
	}
	// ListObjectsV2
	req = signedPut(t, signer, "GET", "http://example.com/e2e-bkt?list-type=2", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "file.txt") {
		t.Fatalf("List %d %s", rr.Code, rr.Body.String())
	}
	// ListBuckets
	req = signedPut(t, signer, "GET", "http://example.com/", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "e2e-bkt") {
		t.Fatalf("ListBuckets %d %s", rr.Code, rr.Body.String())
	}
	// DeleteObject
	req = signedPut(t, signer, "DELETE", "http://example.com/e2e-bkt/file.txt", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DeleteObject %d", rr.Code)
	}
	// Error format: missing key -> NoSuchKey with XML
	req = signedPut(t, signer, "GET", "http://example.com/e2e-bkt/missing", nil)
	rr = doE2E(router, astore, req)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "NoSuchKey") {
		t.Fatalf("NoSuchKey %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("Content-Type %q", rr.Header().Get("Content-Type"))
	}
}

func TestE2E_SigV4_Variants(t *testing.T) {
	router, astore, signer := newE2E(t)
	// Unsigned payload: PUT to a bucket via SigV4 with UNSIGNED-PAYLOAD.
	// Use e2e-bkt consistent with TestE2E_P0 bucket naming.
	// Ensure bucket exists first.
	createReq := signedPut(t, signer, "PUT", "http://example.com/e2e-bkt2", nil)
	rrCreate := doE2E(router, astore, createReq)
	if rrCreate.Code != http.StatusOK {
		t.Fatalf("CreateBucket for UNSIGNED-PAYLOAD test %d %s", rrCreate.Code, rrCreate.Body.String())
	}
	u, _ := url.Parse("http://example.com/e2e-bkt2/k")
	req := &http.Request{Method: "PUT", URL: u, Header: http.Header{}, Host: "example.com", Body: io.NopCloser(bytes.NewReader([]byte("x")))}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, "UNSIGNED-PAYLOAD")
	req.Body = io.NopCloser(bytes.NewReader([]byte("x")))
	req.ContentLength = 1
	rr := doE2E(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("UNSIGNED-PAYLOAD PUT %d %s", rr.Code, rr.Body.String())
	}
	// Presigned
	client := sigv4.Signer{AccessKey: "TESTKEY", SecretKey: "testsecret12345", Region: "us-east-1", Service: "s3"}
	st, _ := s3api.NewStorage(t.TempDir())
	_ = st.CreateBucket("e2e-bkt2")
	_, _ = st.PutObject("e2e-bkt2", "hello.txt", "text/plain", nil, strings.NewReader("presigned"))
	router2 := s3api.NewRouter(st, "us-east-1")
	now2 := time.Now().UTC().Format("20060102T150405Z")
	u2, _ := url.Parse("http://example.com/e2e-bkt2/hello.txt")
	req2 := &http.Request{Method: "GET", URL: u2, Header: http.Header{}, Host: "example.com"}
	client.Presign(req2, now2, 3600)
	rr2 := httptest.NewRecorder()
	s3api.AuthMiddleware(astore, router2).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("presigned %d %s", rr2.Code, rr2.Body.String())
	}
}
