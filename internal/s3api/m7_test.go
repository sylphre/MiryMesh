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

func signedPutM7(t *testing.T, signer *sigv4.Signer, method, rawURL string, hdr map[string]string, body []byte) *http.Request {
	t.Helper()
	u, _ := url.Parse(rawURL)
	req := &http.Request{Method: method, URL: u, Header: http.Header{}, Host: u.Host, Body: io.NopCloser(bytes.NewReader(body))}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	hash := sigv4.HashPayload(body)
	if body == nil {
		hash = sigv4.HashPayload(nil)
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, hash)
	// Restore custom headers that signer may have overwritten? Signer only sets auth headers.
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// Re-sign after restoring? Simpler: signer already included host/date/content-sha which are separate; custom headers are not in SignedHeaders (host;x-amz-date;x-amz-content-sha256) so safe.
	if body != nil {
		req.ContentLength = int64(len(body))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	return req
}

func doM7(router *Router, astore *auth.Store, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr, req)
	return rr
}

func TestCopyObject_COPY(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	_ = st.CreateBucket("bkt")
	_, _ = st.PutObject("bkt", "src.txt", "text/plain", map[string]string{"orig": "1"}, strings.NewReader("hello copy"))
	// Copy with COPY directive (default, preserves meta/content-type)
	hdr := map[string]string{"X-Amz-Copy-Source": "/bkt/src.txt"}
	req := signedPutM7(t, &signer, "PUT", "http://example.com/bkt/dst.txt", hdr, nil)
	rr := doM7(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("Copy %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "CopyObjectResult") {
		t.Fatalf("missing CopyObjectResult: %s", rr.Body.String())
	}
	// Verify dest
	meta, rc, _ := st.GetObject("bkt", "dst.txt")
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello copy" {
		t.Fatalf("data %q", string(data))
	}
	if meta.Meta["orig"] != "1" {
		t.Fatalf("meta %v", meta.Meta)
	}
	if meta.ContentType != "text/plain" {
		t.Fatalf("ct %q", meta.ContentType)
	}
}

func TestCopyObject_REPLACE(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	_ = st.CreateBucket("bkt")
	_, _ = st.PutObject("bkt", "src.txt", "text/plain", map[string]string{"orig": "1"}, strings.NewReader("hello"))
	hdr := map[string]string{
		"X-Amz-Copy-Source":        "/bkt/src.txt",
		"x-amz-metadata-directive": "REPLACE",
		"x-amz-meta-new":           "2",
		"Content-Type":             "image/png",
	}
	req := signedPutM7(t, &signer, "PUT", "http://example.com/bkt/dst2.txt", hdr, nil)
	rr := doM7(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("REPLACE %d %s", rr.Code, rr.Body.String())
	}
	meta, rc, _ := st.GetObject("bkt", "dst2.txt")
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "hello" {
		t.Fatalf("data %q", string(data))
	}
	if meta.Meta["new"] != "2" || meta.Meta["orig"] != "" {
		t.Fatalf("replace meta %v", meta.Meta)
	}
	if meta.ContentType != "image/png" {
		t.Fatalf("ct %q", meta.ContentType)
	}
	_ = bytes.Compare(nil, nil)
}

func TestCopyObject_IfMatch(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	_ = st.CreateBucket("bkt")
	m, _ := st.PutObject("bkt", "src.txt", "text/plain", nil, strings.NewReader("hello"))
	// Wrong ETag -> 412
	hdr := map[string]string{"X-Amz-Copy-Source": "/bkt/src.txt", "x-amz-copy-source-if-match": "wrong"}
	req := signedPutM7(t, &signer, "PUT", "http://example.com/bkt/dst.txt", hdr, nil)
	rr := doM7(router, astore, req)
	if rr.Code != 412 {
		t.Fatalf("expected 412, got %d", rr.Code)
	}
	// Correct ETag -> 200
	hdr = map[string]string{"X-Amz-Copy-Source": "/bkt/src.txt", "x-amz-copy-source-if-match": m.ETag}
	req = signedPutM7(t, &signer, "PUT", "http://example.com/bkt/dst2.txt", hdr, nil)
	rr = doM7(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	_ = m
}

func TestDeleteObjects(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	_ = st.CreateBucket("bkt")
	for _, k := range []string{"a.txt", "b.txt", "c.txt"} {
		_, _ = st.PutObject("bkt", k, "text/plain", nil, strings.NewReader("x"))
	}
	body := `<Delete><Object><Key>a.txt</Key></Object><Object><Key>b.txt</Key></Object><Object><Key>missing.txt</Key></Object></Delete>`
	req := signedPutM7(t, &signer, "POST", "http://example.com/bkt?delete", nil, []byte(body))
	// POST ?delete uses bucket-level delete; ensure handler reads body
	_ = xml.Decoder{}
	rr := doM7(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("DeleteObjects %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "DeleteResult") {
		t.Fatalf("missing DeleteResult: %s", rr.Body.String())
	}
	// a.txt and b.txt deleted, missing was no-op but reported as deleted (S3 is idempotent)
	if _, err := st.HeadObject("bkt", "a.txt"); err == nil {
		t.Fatal("a.txt should be deleted")
	}
	if _, err := st.HeadObject("bkt", "b.txt"); err == nil {
		t.Fatal("b.txt should be deleted")
	}
	if _, err := st.HeadObject("bkt", "c.txt"); err != nil {
		t.Fatal("c.txt should remain")
	}
	// Quiet mode
	for _, k := range []string{"c.txt"} {
		_, _ = st.PutObject("bkt", k, "text/plain", nil, strings.NewReader("x"))
	}
	body2 := `<Delete><Quiet>true</Quiet><Object><Key>c.txt</Key></Object></Delete>`
	req = signedPutM7(t, &signer, "POST", "http://example.com/bkt?delete", nil, []byte(body2))
	rr = doM7(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("quiet %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "<Deleted>") {
		t.Fatalf("quiet should not contain Deleted: %s", rr.Body.String())
	}
	// DeleteBucket still 409 if not empty — verify existing
	_, _ = st.PutObject("bkt", "remain.txt", "text/plain", nil, strings.NewReader("x"))
	req = signedPutM7(t, &signer, "DELETE", "http://example.com/bkt", nil, nil)
	rr = doM7(router, astore, req)
	if rr.Code != 409 {
		t.Fatalf("DeleteBucket non-empty should be 409, got %d", rr.Code)
	}
	_ = xml.Decoder{}
}
