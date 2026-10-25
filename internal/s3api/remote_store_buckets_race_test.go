// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
)

// Run with -race: before the bucket set was guarded by r.mu, concurrent
// CreateBucket/ListBuckets crashed with "concurrent map writes".
func TestRemoteStoreBucketsConcurrentAccess(t *testing.T) {
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	rs := NewRemoteStore(reg, placement.New(0), "tok")

	const workers = 16
	const perWorker = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("bucket-%d-%d", w, i)
				if err := rs.CreateBucket(name); err != nil {
					t.Errorf("CreateBucket(%s): %v", name, err)
					return
				}
				if !rs.BucketExists(name) {
					t.Errorf("BucketExists(%s) = false after create", name)
					return
				}
				if err := rs.PutBucketPolicy(name, []byte(`{}`)); err != nil {
					t.Errorf("PutBucketPolicy(%s): %v", name, err)
					return
				}
				_ = rs.ListBuckets()
				if _, err := rs.GetBucketPolicy(name); err != nil {
					t.Errorf("GetBucketPolicy(%s): %v", name, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if got, want := len(rs.ListBuckets()), workers*perWorker; got != want {
		t.Fatalf("ListBuckets returned %d buckets, want %d", got, want)
	}
}

func TestRemoteStoreCreateBucketDuplicateAndMissingDelete(t *testing.T) {
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	rs := NewRemoteStore(reg, placement.New(0), "tok")

	if err := rs.CreateBucket("dup-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if err := rs.CreateBucket("dup-bucket"); !errors.Is(err, errBucketAlreadyOwned) {
		t.Fatalf("second CreateBucket err = %v, want errBucketAlreadyOwned", err)
	}
	if err := rs.DeleteBucket("no-such-bucket"); !errors.Is(err, errNoSuchBucket) {
		t.Fatalf("DeleteBucket(missing) err = %v, want errNoSuchBucket", err)
	}
}
