// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import "testing"

func TestJobCreateAndVerify(t *testing.T) {
	s := NewStore()
	j := &Job{Bucket: "b", Key: "k", Targets: []string{"a", "b"}}
	id, err := s.Create(j)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, ok := s.Get(id); !ok {
		t.Fatal("Get failed")
	}
	if err := s.MarkVerified(id); err != nil {
		t.Fatalf("MarkVerified: %v", err)
	}
	got, _ := s.Get(id)
	if got.Status != StatusVerified {
		t.Fatalf("status=%q", got.Status)
	}
}

func TestJobRetryRequeue(t *testing.T) {
	s := NewStore()
	j := &Job{Bucket: "b", Key: "k", Targets: []string{"a"}}
	id, _ := s.Create(j)
	_ = s.MarkFailed(id, "err")
	_ = s.MarkFailed(id, "err2")
	pending := s.RequeuePending()
	if len(pending) != 1 {
		t.Fatalf("pending=%v", pending)
	}
	// Exhaust retries: MaxRetries=3, attempts=2 so still pending; after 2 more fails it should not be pending?
	_ = s.MarkFailed(id, "err3")
	got, _ := s.Get(id)
	if got.Attempts != 3 {
		t.Fatalf("attempts=%d", got.Attempts)
	}
	if len(s.RequeuePending()) != 0 {
		t.Fatalf("should not requeue after max retries")
	}
}
