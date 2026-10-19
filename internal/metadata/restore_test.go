// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestore(t *testing.T) {
	storageRoot := t.TempDir()
	evacDir := t.TempDir()
	// Create a bucket with one object file.
	bktDir := filepath.Join(storageRoot, "bkt")
	if err := os.MkdirAll(bktDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bktDir, "present.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	ev, _ := NewEvacuator(evacDir)
	snap := &EvacuationSnapshot{
		Buckets: []string{"bkt", "newbkt"},
		Objects: []ObjectRecord{
			{Bucket: "bkt", Key: "present.txt", Size: 5, ETag: "abc"},
			{Bucket: "bkt", Key: "missing.txt", Size: 7, ETag: "def"},
		},
	}
	_ = ev.Save(snap)
	loaded, _ := ev.Load()
	restorer, _ := NewRestorer(storageRoot)
	res, err := restorer.Restore(loaded)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.BucketsCreated != 1 {
		t.Fatalf("BucketsCreated=%d want 1", res.BucketsCreated)
	}
	if res.ObjectsMissing != 1 {
		t.Fatalf("ObjectsMissing=%d", res.ObjectsMissing)
	}
	if res.ObjectsPresent != 1 {
		t.Fatalf("ObjectsPresent=%d", res.ObjectsPresent)
	}
	if _, err := os.Stat(filepath.Join(storageRoot, "newbkt")); err != nil {
		t.Fatalf("new bucket not created: %v", err)
	}
}
