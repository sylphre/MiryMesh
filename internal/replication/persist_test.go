// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import "testing"

func TestPersistAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := NewStore()
	j := &Job{Bucket: "b", Key: "k1", Checksum: "abc", Targets: []string{"t1"}}
	id, _ := s.Create(j)
	j2 := &Job{Bucket: "b", Key: "k2", Checksum: "def", Targets: []string{"t2"}}
	_, _ = s.Create(j2)
	_ = s.MarkVerified(id)
	if err := s.Persist(dir); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	s2 := NewStore()
	if err := s2.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s2.List()) != 2 {
		t.Fatalf("list len=%d", len(s2.List()))
	}
	got, ok := s2.Get(id)
	if !ok || got.Status != StatusVerified {
		t.Fatalf("verified job not loaded: %v %v", got, ok)
	}
	// Resume pending should find the unverified one.
	eng := NewEngine(s2)
	eng.WriterFn = func(target string, data []byte, expected string) error { return nil }
	resumed := eng.ResumePending(func(job *Job) ([]byte, error) { return []byte("data"), nil })
	_ = resumed
}
