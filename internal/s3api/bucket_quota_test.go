// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"net/http"
	"testing"
)

func TestBucketQuotaRejectsWritesOverLimit(t *testing.T) {
	router, astore, signer := newTestServer(t)
	const bucket = "quota-limit"
	if err := router.store.CreateBucket(bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := SetBucketQuota(router.store, bucket, 100); err != nil {
		t.Fatalf("SetBucketQuota: %v", err)
	}
	if got, err := GetBucketQuota(router.store, bucket); err != nil || got != 100 {
		t.Fatalf("GetBucketQuota = %d, %v", got, err)
	}
	put := func(key string, n int) int {
		t.Helper()
		req := signedReq(t, signer, http.MethodPut, "http://localhost:9000/"+bucket+"/"+key, bytes.Repeat([]byte("a"), n))
		return doReq(router, astore, req).Code
	}

	if code := put("a", 60); code != http.StatusOK {
		t.Fatalf("first put within quota: status %d", code)
	}
	if code := put("b", 60); code != http.StatusInsufficientStorage {
		t.Fatalf("put over quota: status %d, want 507", code)
	}
	// Replacing an object only counts the size difference.
	if code := put("a", 60); code != http.StatusOK {
		t.Fatalf("overwrite with same size: status %d", code)
	}
	if code := put("a", 101); code != http.StatusInsufficientStorage {
		t.Fatalf("overwrite growing past quota: status %d, want 507", code)
	}

	// 0 removes the limit.
	if err := SetBucketQuota(router.store, bucket, 0); err != nil {
		t.Fatalf("SetBucketQuota(0): %v", err)
	}
	if got, _ := GetBucketQuota(router.store, bucket); got != 0 {
		t.Fatalf("quota after removal = %d", got)
	}
	if code := put("b", 60); code != http.StatusOK {
		t.Fatalf("put without quota: status %d", code)
	}
}

func TestBucketQuotaRejectsNegativeLimit(t *testing.T) {
	router, _, _ := newTestServer(t)
	const bucket = "quota-neg"
	if err := router.store.CreateBucket(bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := SetBucketQuota(router.store, bucket, -1); err == nil {
		t.Fatal("expected an error for a negative limit")
	}
}

func TestBucketUsageCountsObjects(t *testing.T) {
	router, astore, signer := newTestServer(t)
	const bucket = "quota-usage"
	if err := router.store.CreateBucket(bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	for _, k := range []string{"x", "y"} {
		req := signedReq(t, signer, http.MethodPut, "http://localhost:9000/"+bucket+"/"+k, bytes.Repeat([]byte("z"), 10))
		if code := doReq(router, astore, req).Code; code != http.StatusOK {
			t.Fatalf("put %s: status %d", k, code)
		}
	}
	got, err := computeBucketUsage(router.store, bucket)
	if err != nil || got != 20 {
		t.Fatalf("computeBucketUsage = %d, %v; want 20", got, err)
	}
}
