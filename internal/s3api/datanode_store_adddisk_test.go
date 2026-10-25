// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"strings"
	"testing"
)

func TestDataNodeStoreAddDisk(t *testing.T) {
	const bucket = "disk-add-test"
	d1, d2 := t.TempDir(), t.TempDir()
	s1, err := NewStorage(d1)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewStorage(d2)
	if err != nil {
		t.Fatal(err)
	}
	ds := NewDataNodeStoreWithDisks(s1, map[string]ObjectStore{d1: s1})

	if _, err := ds.Disk(d2, bucket); err == nil {
		t.Fatal("a disk that was not added must be rejected")
	}
	if err := ds.AddDisk(d2, s2); err != nil {
		t.Fatal(err)
	}
	if err := ds.AddDisk(d2, s2); err != nil {
		t.Fatalf("adding the same disk twice must be a no-op: %v", err)
	}
	if err := ds.AddDisk(d2, nil); err == nil {
		t.Fatal("nil store must be rejected")
	}

	api, err := ds.Disk(d2, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.PutObject(bucket, "k", "text/plain", map[string]string{}, strings.NewReader("hi")); err != nil {
		t.Fatal(err)
	}
	// A request without ?disk= must now find the object on the added disk.
	_, rc, err := ds.GetObject(bucket, "k")
	if err != nil {
		t.Fatalf("object on the added disk not found: %v", err)
	}
	_ = rc.Close()
	if err := ds.DeleteObject(bucket, "k"); err != nil {
		t.Fatalf("delete on the added disk: %v", err)
	}
}
