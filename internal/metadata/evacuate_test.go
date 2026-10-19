// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"path/filepath"
	"testing"
)

func TestEvacuatorSaveLoad(t *testing.T) {
	dir := t.TempDir()
	ev, err := NewEvacuator(dir)
	if err != nil {
		t.Fatalf("NewEvacuator: %v", err)
	}
	snap := &EvacuationSnapshot{Buckets: []string{"bkt"}, Objects: []ObjectRecord{{Bucket: "bkt", Key: "k", Size: 3, ETag: "abc"}}}
	if err := ev.Save(snap); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := ev.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Buckets) != 1 || loaded.Buckets[0] != "bkt" {
		t.Fatalf("buckets=%v", loaded.Buckets)
	}
	if len(loaded.Objects) != 1 || loaded.Objects[0].Key != "k" {
		t.Fatalf("objects=%v", loaded.Objects)
	}
	if loaded.Version != 1 {
		t.Fatalf("version=%d", loaded.Version)
	}
	// Second save bumps version.
	if err := ev.Save(snap); err != nil {
		t.Fatalf("Save2: %v", err)
	}
	loaded2, _ := ev.Load()
	if loaded2.Version != 2 {
		t.Fatalf("version2=%d", loaded2.Version)
	}
}

func TestLoadAny(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	ev1, _ := NewEvacuator(dir1)
	ev2, _ := NewEvacuator(dir2)
	snap1 := &EvacuationSnapshot{Buckets: []string{"bkt1"}}
	snap2 := &EvacuationSnapshot{Buckets: []string{"bkt2"}}
	_ = ev1.Save(snap1)
	_ = ev2.Save(snap2)
	// Make ev2 newer by saving again.
	_ = ev2.Save(snap2)
	best, err := LoadAny(dir1, []string{dir2})
	if err != nil {
		t.Fatalf("LoadAny: %v", err)
	}
	if best.Buckets[0] != "bkt2" {
		t.Fatalf("best=%v", best.Buckets)
	}
	// Missing case.
	if _, err := LoadAny(t.TempDir(), nil); err == nil {
		t.Fatal("expected error for missing snapshot")
	}
	_ = filepath.Join(dir1, "x")
}
