// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"testing"
)

func TestSplitPath(t *testing.T) {
	cases := []struct {
		in         string
		bucket     string
		key        string
		isBucketOp bool
	}{
		{"", "", "", true},
		{"/", "", "", true},
		{"/b", "b", "", true},
		{"/b/", "b", "", true},
		{"/b/k", "b", "k", false},
		{"/b/dir/", "b", "dir/", false},
	}
	for _, c := range cases {
		bucket, key, isBucketOp := splitPath(c.in)
		if bucket != c.bucket || key != c.key || isBucketOp != c.isBucketOp {
			t.Errorf("splitPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, bucket, key, isBucketOp, c.bucket, c.key, c.isBucketOp)
		}
	}
}

// minio-go (used by Gitea) addresses bucket operations as "/bucket/" in path style.
func TestBucketOperationsWithTrailingSlash(t *testing.T) {
	router, astore, signer := newTestServer(t)

	steps := []struct {
		method string
		url    string
		want   int
	}{
		{"PUT", "http://example.com/slashbucket/", http.StatusOK},
		{"HEAD", "http://example.com/slashbucket/", http.StatusOK},
		{"HEAD", "http://example.com/slashbucket", http.StatusOK},
		{"HEAD", "http://example.com/nosuchbucket/", http.StatusNotFound},
		{"GET", "http://example.com/slashbucket/?list-type=2", http.StatusOK},
	}
	for _, s := range steps {
		rr := doReq(router, astore, signedReq(t, signer, s.method, s.url, nil))
		if rr.Code != s.want {
			t.Errorf("%s %s = %d, want %d (body: %s)", s.method, s.url, rr.Code, s.want, rr.Body.String())
		}
	}
}
