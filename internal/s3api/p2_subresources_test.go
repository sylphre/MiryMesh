// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newP2Router(t *testing.T) *Router {
	t.Helper()
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := st.PutObject("bkt", "obj.txt", "text/plain", nil, strings.NewReader("hello")); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	return NewRouter(st, "us-east-1")
}

func TestP2Subresources(t *testing.T) {
	rt := newP2Router(t)
	cases := []struct {
		name   string
		method string
		target string
		hdr    map[string]string
		status int
		want   string // substring expected in the body
	}{
		{"versioning get", http.MethodGet, "/bkt?versioning", nil, http.StatusOK, "VersioningConfiguration"},
		{"versioning put", http.MethodPut, "/bkt?versioning", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"versioning missing bucket", http.MethodGet, "/nobucket?versioning", nil, http.StatusNotFound, "<Code>NoSuchBucket</Code>"},
		{"versioning bad method", http.MethodPost, "/bkt?versioning", nil, http.StatusMethodNotAllowed, "<Code>MethodNotAllowed</Code>"},
		{"versioning on object", http.MethodGet, "/bkt/obj.txt?versioning", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"bucket acl get", http.MethodGet, "/bkt?acl", nil, http.StatusOK, "FULL_CONTROL"},
		{"object acl get", http.MethodGet, "/bkt/obj.txt?acl", nil, http.StatusOK, "FULL_CONTROL"},
		{"object acl missing key", http.MethodGet, "/bkt/missing.txt?acl", nil, http.StatusNotFound, "<Code>NoSuchKey</Code>"},
		{"acl put private", http.MethodPut, "/bkt?acl", map[string]string{"X-Amz-Acl": "private"}, http.StatusOK, ""},
		{"acl put public-read", http.MethodPut, "/bkt?acl", map[string]string{"X-Amz-Acl": "public-read"}, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"acl put without header", http.MethodPut, "/bkt?acl", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"bucket tagging get", http.MethodGet, "/bkt?tagging", nil, http.StatusNotFound, "<Code>NoSuchTagSet</Code>"},
		{"object tagging get", http.MethodGet, "/bkt/obj.txt?tagging", nil, http.StatusOK, "TagSet"},
		{"object tagging missing key", http.MethodGet, "/bkt/missing.txt?tagging", nil, http.StatusNotFound, "<Code>NoSuchKey</Code>"},
		{"tagging delete", http.MethodDelete, "/bkt?tagging", nil, http.StatusNoContent, ""},
		{"tagging put", http.MethodPut, "/bkt?tagging", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"cors get", http.MethodGet, "/bkt?cors", nil, http.StatusNotFound, "<Code>NoSuchCORSConfiguration</Code>"},
		{"cors delete", http.MethodDelete, "/bkt?cors", nil, http.StatusNoContent, ""},
		{"cors put", http.MethodPut, "/bkt?cors", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"cors on object", http.MethodGet, "/bkt/obj.txt?cors", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"lifecycle get", http.MethodGet, "/bkt?lifecycle", nil, http.StatusNotFound, "<Code>NoSuchLifecycleConfiguration</Code>"},
		{"lifecycle put", http.MethodPut, "/bkt?lifecycle", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"analytics stays 501", http.MethodGet, "/bkt?analytics", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"select stays 501", http.MethodPost, "/bkt/obj.txt?select&select-type=2", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"root subresource", http.MethodGet, "/?acl", nil, http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, nil)
			for k, v := range tc.hdr {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			rt.ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status=%d want %d body=%s", rr.Code, tc.status, rr.Body.String())
			}
			if tc.want != "" && !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("body missing %q: %s", tc.want, rr.Body.String())
			}
		})
	}
}

// Normal routing must be unaffected when no P2 subresource is present.
func TestP2DoesNotShadowObjectGet(t *testing.T) {
	rt := newP2Router(t)
	req := httptest.NewRequest(http.MethodGet, "/bkt/obj.txt", nil)
	rr := httptest.NewRecorder()
	rt.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "hello" {
		t.Fatalf("GetObject status=%d body=%q", rr.Code, rr.Body.String())
	}
}
