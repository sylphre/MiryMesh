// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import "testing"

func TestEngineReplicateHappy(t *testing.T) {
	s := NewStore()
	eng := NewEngine(s)
	// local writer (nil WriterFn means checksum-only verify)
	data := []byte("hello replication")
	_, err := eng.Replicate("b", "k", data, []string{"n1:/a", "n2:/b"}, 2)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}
}

func TestEngineReplicateWithVerify(t *testing.T) {
	s := NewStore()
	eng := NewEngine(s)
	storage := make(map[string][]byte)
	eng.WriterFn = func(target string, data []byte, expected string) error {
		storage[target] = append([]byte(nil), data...)
		if SumSHA256(data) != expected {
			t.Fatalf("mismatch")
		}
		return nil
	}
	eng.ReaderFn = func(target string) ([]byte, error) { return storage[target], nil }
	data := []byte("verify me")
	id, err := eng.Replicate("b", "k", data, []string{"t1", "t2"}, 2)
	if err != nil {
		t.Fatalf("Replicate: %v", err)
	}
	j, _ := s.Get(id)
	if j.Status != StatusVerified {
		t.Fatalf("status=%q", j.Status)
	}
}

func TestEngineRetryAndFail(t *testing.T) {
	s := NewStore()
	eng := NewEngine(s)
	calls := 0
	eng.WriterFn = func(target string, data []byte, expected string) error {
		calls++
		return &testErr{"fail"}
	}
	_, err := eng.Replicate("b", "k", []byte("x"), []string{"t1"}, 2)
	if err == nil {
		t.Fatal("expected fail")
	}
	if calls != 3 {
		t.Fatalf("calls=%d want 3", calls)
	}
}

func TestEngineResumePending(t *testing.T) {
	s := NewStore()
	eng := NewEngine(s)
	// Create a pending job manually via Replicate that will fail then resume.
	eng.WriterFn = func(target string, data []byte, expected string) error { return &testErr{"fail"} }
	id, _ := eng.Replicate("b", "k", []byte("data"), []string{"t1"}, 0)
	j, _ := s.Get(id)
	if j.Status != StatusFailed {
		t.Fatalf("status=%q", j.Status)
	}
	// Fix writer and resume: but maxRetries is 0 so it won't requeue; create new pending via direct store.
	s2 := NewStore()
	eng2 := NewEngine(s2)
	j2 := &Job{Bucket: "b", Key: "k", Checksum: SumSHA256([]byte("data")), Targets: []string{"t1"}, MaxRetries: 2}
	id2, _ := s2.Create(j2)
	eng2.WriterFn = func(target string, data []byte, expected string) error { return nil }
	eng2.ResumePending(func(job *Job) ([]byte, error) { return []byte("data"), nil })
	got, _ := s2.Get(id2)
	if got.Status != StatusVerified {
		t.Fatalf("resume status=%q", got.Status)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
