// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"strings"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
)

// Metrics hooks for GET/PUT byte tracking (set by controller).
var mRecorder func(isGet bool, bytes int64)

// SetMetricsRecorder sets the global recorder for S3 GET/PUT.
func SetMetricsRecorder(fn func(bool, int64)) { mRecorder = fn }

// Router routes S3-compatible requests. In M1 it handles path-style only
// (virtual-hosted style is shimmable but not required for Nextcloud/Misskey).
type Router struct {
	store   ObjectStore
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
func NewRouter(store ObjectStore, region string) *Router {
	if region == "" {
		region = "us-east-1"
	}
	return &Router{store: store, region: region}
}

// NewRouterWithPlacement creates a Router with placement resolver.
func NewRouterWithPlacement(store ObjectStore, region string, resolver PlacementResolver) *Router {
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

	// P2 subresources (?acl/tagging/cors/...): read-side answers, 501 for the
	// rest — see p2_subresources.go. ?policy is handled further below.
	q := r.URL.Query()
	if rt.handleP2Subresource(w, r) {
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
		// S3 bucket policy
		if q.Has("policy") {
			switch r.Method {
			case http.MethodGet:
				rt.handleGetBucketPolicy(w, r, bucket)
				return
			case http.MethodPut:
				rt.handlePutBucketPolicy(w, r, bucket)
				return
			case http.MethodDelete:
				rt.handleDeleteBucketPolicy(w, r, bucket)
				return
			}
			writeError(w, r, errNotImplemented)
			return
		}
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
			if q.Has("delete") {
				rt.handleDeleteObjects(w, r, bucket)
				return
			}
		}
	}

	// Object-level operations: /{bucket}/{key}
	if !isBucketOp || key != "" {
		if kerr, ok := validateObjectKey(key); !ok {
			writeError(w, r, kerr)
			return
		}
		// Multipart + UploadPartCopy dispatch: PUT with uploadId+partNumber
		if r.Method == http.MethodPut && q.Has("uploadId") && q.Has("partNumber") {
			if r.Header.Get("X-Amz-Copy-Source") != "" || r.Header.Get("x-amz-copy-source") != "" {
				rt.handleUploadPartCopy(w, r, bucket, key)
				return
			}
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
			if r.Header.Get("X-Amz-Copy-Source") != "" || r.Header.Get("x-amz-copy-source") != "" {
				rt.handleCopyObject(w, r, bucket, key)
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
			if q.Has("delete") {
				// POST /{bucket}?delete is bucket-level, but some clients send it here; handle as DeleteObjects
				rt.handleDeleteObjects(w, r, bucket)
				return
			}
			writeXMLError(w, r, http.StatusNotImplemented, "NotImplemented", "operation is not implemented")
			return
		}
	}

	// Bucket-level POST ?delete fallback (e.g. POST /?delete without key)
	if q.Has("delete") && r.Method == http.MethodPost {
		rt.handleDeleteObjects(w, r, bucket)
		return
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
