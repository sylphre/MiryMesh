// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"strings"
)

// Router routes S3-compatible requests. In M1 it handles path-style only
// (virtual-hosted style is shimmable but not required for Nextcloud/Misskey).
type Router struct {
	store  *Storage
	region string
}

// NewRouter creates a Router.
func NewRouter(store *Storage, region string) *Router {
	if region == "" {
		region = "us-east-1"
	}
	return &Router{store: store, region: region}
}

// ServeHTTP dispatches to handlers based on method + path + query.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Health / root passthrough for non-S3 probes.
	if r.URL.Path == "/healthz" || r.URL.Path == "/health" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// P2 operations that are explicitly NotImplemented in M1.
	q := r.URL.Query()
	if q.Has("versioning") || q.Has("acl") || q.Has("tagging") || q.Has("policy") || q.Has("cors") || q.Has("lifecycle") || q.Has("analytics") || q.Has("inventory") || q.Has("metrics") || q.Has("retention") || q.Has("legal-hold") || q.Has("select") {
		writeError(w, r, errNotImplemented)
		return
	}

	bucket, key, isBucketOp := splitPath(r.URL.Path)

	// ListBuckets: GET /
	if isBucketOp && bucket == "" && r.Method == http.MethodGet && !q.Has("uploads") {
		// Distinguish ListBuckets vs ListObjectsV2? ListBuckets is GET / without list-type.
		if !q.Has("list-type") {
			rt.handleListBuckets(w, r)
			return
		}
	}

	// Bucket-level operations: /{bucket}
	if isBucketOp && key == "" {
		switch r.Method {
		case http.MethodHead:
			rt.handleHeadBucket(w, r, bucket)
			return
		case http.MethodPut:
			rt.handleCreateBucket(w, r, bucket)
			return
		case http.MethodDelete:
			rt.handleDeleteBucket(w, r, bucket)
			return
		case http.MethodGet:
			// GetBucketLocation ?location, ListObjectsV2 ?list-type=2, ListMultipartUploads ?uploads, etc.
			if q.Has("location") {
				rt.handleGetBucketLocation(w, r, bucket)
				return
			}
			if q.Has("uploads") {
				// M1: abort/pending multipart not yet — return empty.
				writeXMLError(w, r, http.StatusNotImplemented, "NotImplemented", "ListMultipartUploads is not implemented")
				return
			}
			// ListObjectsV2 or ListObjects v1 shim.
			rt.handleListObjectsV2(w, r, bucket)
			return
		}
	}

	// Object-level operations: /{bucket}/{key}
	if !isBucketOp || key != "" {
		// Multipart query params imply NotImplemented for now unless it's simple abort.
		if q.Has("uploadId") || q.Has("uploads") || q.Has("partNumber") {
			// For M1, all multipart ops are NotImplemented except we could handle single-part Put via regular Put.
			// But UploadPart etc must be 501.
			writeError(w, r, errNotImplemented)
			return
		}
		switch r.Method {
		case http.MethodHead:
			rt.handleHeadObject(w, r, bucket, key)
			return
		case http.MethodGet:
			rt.handleGetObject(w, r, bucket, key)
			return
		case http.MethodPut:
			// CopyObject is PUT with x-amz-copy-source.
			if r.Header.Get("X-Amz-Copy-Source") != "" || r.Header.Get("x-amz-copy-source") != "" {
				writeError(w, r, errNotImplemented)
				return
			}
			rt.handlePutObject(w, r, bucket, key)
			return
		case http.MethodDelete:
			rt.handleDeleteObject(w, r, bucket, key)
			return
		case http.MethodPost:
			// CompleteMultipartUpload / Select etc.
			writeError(w, r, errNotImplemented)
			return
		}
	}

	writeXMLError(w, r, http.StatusNotImplemented, "NotImplemented", "operation is not implemented")
}

func splitPath(p string) (bucket, key string, isBucketOp bool) {
	if p == "" || p == "/" {
		return "", "", true
	}
	trimmed := strings.TrimPrefix(p, "/")
	idx := strings.Index(trimmed, "/")
	if idx < 0 {
		return trimmed, "", true
	}
	bucket = trimmed[:idx]
	key = trimmed[idx+1:]
	// Leading slash removed; key may be empty if path ends with "/".
	return bucket, key, false
}
