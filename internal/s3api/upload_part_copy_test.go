// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
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

func signedPutUPC(t *testing.T, signer *sigv4.Signer, method, rawURL string, hdr map[string]string, body string) *http.Request {
	t.Helper()
	u, _ := url.Parse(rawURL)
	req := &http.Request{Method: method, URL: u, Header: http.Header{}, Host: u.Host, Body: io.NopCloser(strings.NewReader(body))}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	hash := sigv4.HashPayload([]byte(body))
	if body == "" {
		hash = sigv4.HashPayload(nil)
	}
	now := time.Now().UTC().Format("20060102T150405Z")
	signer.Sign(req, now, hash)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.Body = io.NopCloser(strings.NewReader(body))
	return req
}

func doUPC(router *Router, astore *auth.Store, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr, req)
	return rr
}

func TestUploadPartCopy(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	_ = st.CreateBucket("bkt")
	_, _ = st.PutObject("bkt", "src.txt", "text/plain", nil, strings.NewReader("hello world for copy"))
	// Initiate multipart
	req := signedPutUPC(t, &signer, "POST", "http://example.com/bkt/dst.txt?uploads", nil, "")
	rr := doUPC(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("init %d %s", rr.Code, rr.Body.String())
	}
	var initResp struct {
		UploadID string `xml:"UploadId"`
	}
	_ = xml.Unmarshal(rr.Body.Bytes(), &initResp)
	// UploadPartCopy full object as part 1
	hdr := map[string]string{"X-Amz-Copy-Source": "/bkt/src.txt"}
	u := "http://example.com/bkt/dst.txt?partNumber=1&uploadId=" + url.QueryEscape(initResp.UploadID)
	req = signedPutUPC(t, &signer, "PUT", u, hdr, "")
	rr = doUPC(router, astore, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "CopyPartResult") {
		t.Fatalf("copy part %d %s", rr.Code, rr.Body.String())
	}
	var cpResp struct {
		ETag string `xml:"ETag"`
	}
	_ = xml.Unmarshal(rr.Body.Bytes(), &cpResp)
	if cpResp.ETag == "" {
		t.Fatalf("missing ETag in CopyPartResult")
	}
	// UploadPartCopy with range bytes=0-4 -> "hello"
	hdr2 := map[string]string{"X-Amz-Copy-Source": "/bkt/src.txt", "x-amz-copy-source-range": "bytes=0-4"}
	u2 := "http://example.com/bkt/dst.txt?partNumber=2&uploadId=" + url.QueryEscape(initResp.UploadID)
	req = signedPutUPC(t, &signer, "PUT", u2, hdr2, "")
	rr = doUPC(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("range copy %d %s", rr.Code, rr.Body.String())
	}
	// Complete with 2 parts
	etag1 := strings.Trim(cpResp.ETag, "\"")
	// Get second part ETag from last response
	var cpResp2 struct {
		ETag string `xml:"ETag"`
	}
	_ = xml.Unmarshal(rr.Body.Bytes(), &cpResp2)
	etag2 := strings.Trim(cpResp2.ETag, "\"")
	body := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"` + etag1 + `"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"` + etag2 + `"</ETag></Part></CompleteMultipartUpload>`
	compURL := "http://example.com/bkt/dst.txt?uploadId=" + url.QueryEscape(initResp.UploadID)
	req = signedPutUPC(t, &signer, "POST", compURL, nil, body)
	rr = doUPC(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("complete %d %s", rr.Code, rr.Body.String())
	}
	// Verify final object is "hello world for copy" + "hello" (range)
	req = signedPutUPC(t, &signer, "GET", "http://example.com/bkt/dst.txt", nil, "")
	rr = doUPC(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("get %d", rr.Code)
	}
	expected := "hello world for copyhello"
	if rr.Body.String() != expected {
		t.Fatalf("expected %q got %q", expected, rr.Body.String())
	}
}
