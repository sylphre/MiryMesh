// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"strings"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
)

// Router routes S3-compatible requests. In M1 it handles path-style only
// (virtual-hosted style is shimmable but not required for Nextcloud/Misskey).
type Router struct {
	store   *Storage
	region  string
	placing PlacementResolver
}

// PlacementResolver is the placement decision hook used by S3 handlers.
// In M3 it is bound to the controller's datanode.Registry.
type PlacementResolver interface {
	// Resolve selects targets for a new object write. Size is object bytes if known, else 0.
	Resolve(bucket string, size int64) ([]placement.Target, error)
}

// NewRouter creates a Router.
func NewRouter(store *Storage, region string) *Router {
	if region == "" {
		region = "us-east-1"
	}
	return &Router{store: store, region: region}
}

// NewRouterWithPlacement creates a Router with placement resolver.
func NewRouterWithPlacement(store *Storage, region string, resolver PlacementResolver) *Router {
	if region == "" {
		region = "us-east-1"
	}
	return &Router{store: store, region: region, placing: resolver}
}

// SetPlacement sets/overrides the placement resolver (useful in tests).
func (rt *Router) SetPlacement(p PlacementResolver) { rt.placing = p }

// Ensure imports are used even when placing is nil.
var _ = datanode.StatusOnline
var _ = placement.PolicySingle

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
				rt.handleListMultipartUploads(w, r, bucket)
				return
			}
			// ListObjectsV2 or ListObjects v1 shim.
			rt.handleListObjectsV2(w, r, bucket)
			return
		case http.MethodPost:
			if q.Has("uploads") {
				// POST /{bucket}?uploads is not used; object-level uses POST ?uploads
				writeError(w, r, errInvalidRequest)
				return
			}
		}
	}

	// Object-level operations: /{bucket}/{key}
	if !isBucketOp || key != "" {
		// Multipart dispatch
		if q.Has("uploadId") && q.Has("partNumber") && r.Method == http.MethodPut {
			rt.handleUploadPart(w, r, bucket, key)
			return
		}
		if q.Has("uploadId") && r.Method == http.MethodGet {
			rt.handleListParts(w, r, bucket, key)
			return
		}
		if q.Has("uploadId") && (r.Method == http.MethodPost || r.Method == http.MethodDelete) {
			if r.Method == http.MethodDelete {
				rt.handleAbortMultipart(w, r, bucket, key)
				return
			}
			rt.handleCompleteMultipart(w, r, bucket, key)
			return
		}
		if q.Has("uploads") && r.Method == http.MethodPost {
			rt.handleCreateMultipartUpload(w, r, bucket, key)
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
			// POST /{bucket}/{key}?uploads -> create, otherwise Complete already handled
			if q.Has("uploads") {
				rt.handleCreateMultipartUpload(w, r, bucket, key)
				return
			}
			writeXMLError(w, r, http.StatusNotImplemented, "NotImplemented", "operation is not implemented")
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
