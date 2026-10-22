// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

func newTestServer(t *testing.T) (*Router, *auth.Store, *sigv4.Signer) {
	t.Helper()
	dir := t.TempDir()
	st, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"TESTKEY": "testsecret12345"})
	signer := &sigv4.Signer{AccessKey: "TESTKEY", SecretKey: "testsecret12345", Region: "us-east-1", Service: "s3"}
	return router, astore, signer
}

func signedReq(t *testing.T, signer *sigv4.Signer, method, rawURL string, body []byte) *http.Request {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("Parse URL: %v", err)
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := &http.Request{Method: method, URL: u, Header: http.Header{}, Host: u.Host, Body: io.NopCloser(bytes.NewReader(body))}
	_ = bodyReader
	payloadHash := sigv4.HashPayload(body)
	if body == nil {
		payloadHash = sigv4.HashPayload(nil)
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, payloadHash)
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	return req
}

func doReq(router *Router, astore *auth.Store, req *http.Request) *httptest.ResponseRecorder {
	handler := AuthMiddleware(astore, router)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestPutGetHeadDeleteObject(t *testing.T) {
	router, astore, signer := newTestServer(t)

	// Create bucket.
	req := signedReq(t, signer, "PUT", "http://example.com/mybucket", nil)
	rr := doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Put object.
	body := []byte("hello mirymesh")
	req = signedReq(t, signer, "PUT", "http://example.com/mybucket/hello.txt", body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PutObject status=%d body=%s", rr.Code, rr.Body.String())
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("PutObject missing ETag")
	}
	// Head.
	req = signedReq(t, signer, "HEAD", "http://example.com/mybucket/hello.txt", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("HeadObject status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("ETag") != etag {
		t.Fatalf("Head ETag mismatch: %q vs %q", rr.Header().Get("ETag"), etag)
	}
	// Get.
	req = signedReq(t, signer, "GET", "http://example.com/mybucket/hello.txt", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GetObject status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != string(body) {
		t.Fatalf("GetObject body=%q want %q", got, string(body))
	}
	// Get with Range.
	req = signedReq(t, signer, "GET", "http://example.com/mybucket/hello.txt", nil)
	req.Header.Set("Range", "bytes=0-4")
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("Range Get status=%d", rr.Code)
	}
	if rr.Body.String() != "hello" {
		t.Fatalf("Range body=%q", rr.Body.String())
	}
	// Delete.
	req = signedReq(t, signer, "DELETE", "http://example.com/mybucket/hello.txt", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DeleteObject status=%d", rr.Code)
	}
	// Get after delete -> 404.
	req = signedReq(t, signer, "GET", "http://example.com/mybucket/hello.txt", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("Get after delete status=%d want 404", rr.Code)
	}
}

func TestBucketOps(t *testing.T) {
	router, astore, signer := newTestServer(t)

	// Invalid bucket name.
	req := signedReq(t, signer, "PUT", "http://example.com/AB", nil)
	rr := doReq(router, astore, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid bucket name status=%d want 400", rr.Code)
	}
	// Create + duplicate.
	req = signedReq(t, signer, "PUT", "http://example.com/mybucket2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket 1 status=%d", rr.Code)
	}
	req = signedReq(t, signer, "PUT", "http://example.com/mybucket2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate CreateBucket status=%d want 409", rr.Code)
	}
	// HeadBucket ok and missing.
	req = signedReq(t, signer, "HEAD", "http://example.com/mybucket2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("HeadBucket ok status=%d", rr.Code)
	}
	req = signedReq(t, signer, "HEAD", "http://example.com/nosuchbucket", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("HeadBucket missing status=%d want 404", rr.Code)
	}
	// ListBuckets contains mybucket2.
	req = signedReq(t, signer, "GET", "http://example.com/", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ListBuckets status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "mybucket2") {
		t.Fatalf("ListBuckets missing bucket: %s", rr.Body.String())
	}
	// GetBucketLocation.
	req = signedReq(t, signer, "GET", "http://example.com/mybucket2?location", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GetBucketLocation status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "us-east-1") {
		t.Fatalf("GetBucketLocation body=%s", rr.Body.String())
	}
	// Delete non-empty -> 409.
	body := []byte("x")
	req = signedReq(t, signer, "PUT", "http://example.com/mybucket2/a.txt", body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("Put for non-empty test status=%d", rr.Code)
	}
	req = signedReq(t, signer, "DELETE", "http://example.com/mybucket2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("Delete non-empty bucket status=%d want 409", rr.Code)
	}
	// Delete object then bucket.
	req = signedReq(t, signer, "DELETE", "http://example.com/mybucket2/a.txt", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DeleteObject status=%d", rr.Code)
	}
	req = signedReq(t, signer, "DELETE", "http://example.com/mybucket2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DeleteBucket after empty status=%d", rr.Code)
	}
}

func TestListObjectsV2(t *testing.T) {
	router, astore, signer := newTestServer(t)
	req := signedReq(t, signer, "PUT", "http://example.com/bkt", nil)
	rr := doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket status=%d", rr.Code)
	}
	for _, key := range []string{"a.txt", "a/b.txt", "a/c.txt", "b.txt"} {
		body := []byte("data-" + key)
		req = signedReq(t, signer, "PUT", "http://example.com/bkt/"+key, body)
		req.Body = io.NopCloser(bytes.NewReader(body))
		rr = doReq(router, astore, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("Put %s status=%d", key, rr.Code)
		}
	}
	// List all.
	req = signedReq(t, signer, "GET", "http://example.com/bkt?list-type=2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("ListObjectsV2 status=%d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "a.txt") || !strings.Contains(body, "b.txt") {
		t.Fatalf("ListObjectsV2 missing keys: %s", body)
	}
	// Pagination max-keys=2.
	req = signedReq(t, signer, "GET", "http://example.com/bkt?list-type=2&max-keys=2", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("List pagination status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "IsTruncated>true") {
		t.Fatalf("expected IsTruncated true, got %s", rr.Body.String())
	}
	// Delimiter.
	req = signedReq(t, signer, "GET", "http://example.com/bkt?list-type=2&delimiter=%2F", nil)
	rr = doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("List delimiter status=%d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "CommonPrefixes") {
		t.Fatalf("expected CommonPrefixes, got %s", rr.Body.String())
	}
	// Verify XML namespace.
	if !strings.Contains(rr.Body.String(), "http://s3.amazonaws.com/doc/2006-03-01/") {
		t.Fatalf("missing xmlns 2006-03-01")
	}
}

func TestPresignedGet(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	_ = st.CreateBucket("bkt")
	_, _ = st.PutObject("bkt", "hello.txt", "text/plain", nil, strings.NewReader("hello presigned"))
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"AKID": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "AKID", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	now := time.Now().UTC().Format("20060102T150405Z")
	u, _ := url.Parse("http://example.com/bkt/hello.txt")
	req := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "example.com"}
	signer.Presign(req, now, 3600)
	rr := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("presigned GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "hello presigned" {
		t.Fatalf("presigned body=%q", rr.Body.String())
	}
}

func TestUnsignedPayload(t *testing.T) {
	router, astore, signer := newTestServer(t)
	req := signedReq(t, signer, "PUT", "http://example.com/bkt2", nil)
	rr := doReq(router, astore, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket status=%d", rr.Code)
	}
	// Put with UNSIGNED-PAYLOAD.
	body := []byte("streaming body")
	u, _ := url.Parse("http://example.com/bkt2/obj.bin")
	req2 := &http.Request{Method: "PUT", URL: u, Header: http.Header{}, Host: "example.com", Body: io.NopCloser(bytes.NewReader(body))}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req2, now, "UNSIGNED-PAYLOAD")
	req2.Body = io.NopCloser(bytes.NewReader(body))
	rr = doReq(router, astore, req2)
	if rr.Code != http.StatusOK {
		t.Fatalf("Put UNSIGNED-PAYLOAD status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAuthRejected(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"REAL": "realsecret12345"})
	// Request signed with wrong key.
	badSigner := sigv4.Signer{AccessKey: "FAKE", SecretKey: "fakesecret12345", Region: "us-east-1", Service: "s3"}
	req := signedReq(t, &badSigner, "GET", "http://example.com/", nil)
	rr := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bad key status=%d want 403", rr.Code)
	}
	// Unsigned request when store non-empty -> 403.
	u, _ := url.Parse("http://example.com/")
	req2 := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "example.com"}
	rr2 := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Fatalf("unsigned with creds status=%d want 403", rr2.Code)
	}
}

func TestP2NotImplemented(t *testing.T) {
	router, astore, signer := newTestServer(t)
	req := signedReq(t, signer, "GET", "http://example.com/bkt?analytics", nil)
	rr := doReq(router, astore, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("P2 analytics status=%d want 501", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "NotImplemented") {
		t.Fatalf("expected NotImplemented code")
	}
}

func TestXMLErrorFormat(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStore() // empty -> anonymous allowed
	_ = astore
	// Get from missing bucket -> NoSuchBucket XML.
	req := &http.Request{Method: "GET", URL: mustParse("http://example.com/nosuch/b.txt"), Header: http.Header{}, Host: "example.com"}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
	var errResp APIError
	if err := xml.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&errResp); err != nil {
		t.Fatalf("decode error xml: %v body=%s", err, rr.Body.String())
	}
	if errResp.Code != "NoSuchBucket" {
		t.Fatalf("Code=%q want NoSuchBucket", errResp.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/xml") {
		t.Fatalf("Content-Type=%q want xml", ct)
	}
}

func mustParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
