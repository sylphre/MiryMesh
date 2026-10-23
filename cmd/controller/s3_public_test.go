// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

const publicReadPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::pub/*"]}]}`

func newPublicTestStorage(t *testing.T) *s3api.Storage {
	t.Helper()
	st, err := s3api.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	for _, b := range []string{"pub", "priv", "locked"} {
		if err := st.CreateBucket(b); err != nil {
			t.Fatalf("CreateBucket(%s): %v", b, err)
		}
	}
	if err := st.PutBucketPolicy("pub", []byte(publicReadPolicy)); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	if _, err := s3api.SetBucketDomains(st, "locked", []string{"example.com"}); err != nil {
		t.Fatalf("SetBucketDomains: %v", err)
	}
	return st
}

func TestS3HostGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st := newPublicTestStorage(t)
	r := gin.New()
	r.Use(s3HostGuard(st))
	r.NoRoute(func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	cases := []struct {
		name   string
		target string
		host   string
		want   int
	}{
		{"allowed domain", "/locked/a.txt", "example.com", http.StatusOK},
		{"allowed subdomain", "/locked/a.txt", "cdn.example.com:443", http.StatusOK},
		{"other host", "/locked/a.txt", "evil.test", http.StatusForbidden},
		{"bucket without domains", "/priv/a.txt", "evil.test", http.StatusOK},
		{"no bucket", "/", "evil.test", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.target, nil)
			req.Host = c.host
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != c.want {
				t.Fatalf("status=%d body=%s; want %d", rr.Code, rr.Body.String(), c.want)
			}
		})
	}
}

func TestS3AnonymousReadMarksOnlyPublicObjects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	st := newPublicTestStorage(t)
	r := gin.New()
	r.Use(s3AnonymousRead(st))
	r.NoRoute(func(c *gin.Context) {
		if _, ok := c.Get(anonReadKey); ok {
			c.String(http.StatusOK, "anon")
			return
		}
		c.String(http.StatusOK, "signed")
	})

	cases := []struct {
		name   string
		method string
		target string
		auth   string
		want   string
	}{
		{"public GET", http.MethodGet, "/pub/a.txt", "", "anon"},
		{"public HEAD", http.MethodHead, "/pub/a.txt", "", "anon"},
		{"public PUT", http.MethodPut, "/pub/a.txt", "", "signed"},
		{"private bucket", http.MethodGet, "/priv/a.txt", "", "signed"},
		{"public with Authorization", http.MethodGet, "/pub/a.txt", "AWS4-HMAC-SHA256 Credential=x", "signed"},
		{"public ?acl", http.MethodGet, "/pub/a.txt?acl", "", "signed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.target, nil)
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			// HEAD responses carry no body, so only check the body for the other methods.
			if c.method != http.MethodHead && rr.Body.String() != c.want {
				t.Fatalf("body=%q; want %q", rr.Body.String(), c.want)
			}
			if c.method == http.MethodHead && rr.Code != http.StatusOK {
				t.Fatalf("status=%d; want 200", rr.Code)
			}
		})
	}
}
