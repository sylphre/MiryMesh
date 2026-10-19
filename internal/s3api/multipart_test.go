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

func signedReqMP(t *testing.T, signer *sigv4.Signer, method, rawURL string, body []byte) *http.Request {
	t.Helper()
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

func doMP(router *Router, astore *auth.Store, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	AuthMiddleware(astore, router).ServeHTTP(rr, req)
	return rr
}

func TestMultipart_FullFlow(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	// Create bucket
	req := signedReqMP(t, &signer, "PUT", "http://example.com/mpbkt", nil)
	rr := doMP(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("CreateBucket %d", rr.Code)
	}
	// Initiate
	req = signedReqMP(t, &signer, "POST", "http://example.com/mpbkt/big.dat?uploads", nil)
	rr = doMP(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("Initiate %d %s", rr.Code, rr.Body.String())
	}
	var initResp struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		UploadID string   `xml:"UploadId"`
	}
	_ = xml.Unmarshal(rr.Body.Bytes(), &initResp)
	if initResp.UploadID == "" {
		t.Fatalf("missing uploadId: %s", rr.Body.String())
	}
	uploadID := initResp.UploadID
	// Upload part 1
	part1 := []byte("hello ")
	u1 := "http://example.com/mpbkt/big.dat?partNumber=1&uploadId=" + url.QueryEscape(uploadID)
	req = signedReqMP(t, &signer, "PUT", u1, part1)
	rr = doMP(router, astore, req)
	if rr.Code != 200 || rr.Header().Get("ETag") == "" {
		t.Fatalf("UploadPart1 %d %s ETag %q", rr.Code, rr.Body.String(), rr.Header().Get("ETag"))
	}
	etag1 := strings.Trim(rr.Header().Get("ETag"), "\"")
	// Upload part 2
	part2 := []byte("world")
	u2 := "http://example.com/mpbkt/big.dat?partNumber=2&uploadId=" + url.QueryEscape(uploadID)
	req = signedReqMP(t, &signer, "PUT", u2, part2)
	rr = doMP(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("UploadPart2 %d", rr.Code)
	}
	etag2 := strings.Trim(rr.Header().Get("ETag"), "\"")
	// ListParts
	uList := "http://example.com/mpbkt/big.dat?uploadId=" + url.QueryEscape(uploadID)
	req = signedReqMP(t, &signer, "GET", uList, nil)
	rr = doMP(router, astore, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), etag1) {
		t.Fatalf("ListParts %d %s", rr.Code, rr.Body.String())
	}
	// ListMultipartUploads
	req = signedReqMP(t, &signer, "GET", "http://example.com/mpbkt?uploads", nil)
	rr = doMP(router, astore, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), uploadID) {
		t.Fatalf("ListUploads %d %s", rr.Code, rr.Body.String())
	}
	// Complete
	completeBody := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"` + etag1 + `"</ETag></Part><Part><PartNumber>2</PartNumber><ETag>"` + etag2 + `"</ETag></Part></CompleteMultipartUpload>`
	uComp := "http://example.com/mpbkt/big.dat?uploadId=" + url.QueryEscape(uploadID)
	req = signedReqMP(t, &signer, "POST", uComp, []byte(completeBody))
	rr = doMP(router, astore, req)
	if rr.Code != 200 {
		t.Fatalf("Complete %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "CompleteMultipartUploadResult") {
		t.Fatalf("complete xml %s", rr.Body.String())
	}
	// Get final object
	req = signedReqMP(t, &signer, "GET", "http://example.com/mpbkt/big.dat", nil)
	rr = doMP(router, astore, req)
	if rr.Code != 200 || rr.Body.String() != "hello world" {
		t.Fatalf("Get after complete %d %q", rr.Code, rr.Body.String())
	}
}

func TestMultipart_Abort(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStorage(dir)
	router := NewRouter(st, "us-east-1")
	astore := auth.NewStoreWith(map[string]string{"K": "secret12345678"})
	signer := sigv4.Signer{AccessKey: "K", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	_ = st.CreateBucket("bkt")
	req := signedReqMP(t, &signer, "POST", "http://example.com/bkt/k?uploads", nil)
	rr := doMP(router, astore, req)
	var initResp struct {
		UploadID string `xml:"UploadId"`
	}
	_ = xml.Unmarshal(rr.Body.Bytes(), &initResp)
	// Abort
	u := "http://example.com/bkt/k?uploadId=" + url.QueryEscape(initResp.UploadID)
	req = signedReqMP(t, &signer, "DELETE", u, nil)
	rr = doMP(router, astore, req)
	if rr.Code != 204 {
		t.Fatalf("Abort %d %s", rr.Code, rr.Body.String())
	}
	// ListParts after abort should be NoSuchUpload
	req = signedReqMP(t, &signer, "GET", u, nil)
	rr = doMP(router, astore, req)
	if rr.Code != 404 {
		t.Fatalf("expected 404 after abort, got %d", rr.Code)
	}
	_ = bytes.Compare(nil, nil)
}
