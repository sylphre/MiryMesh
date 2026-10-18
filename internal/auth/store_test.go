// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package auth

import "testing"

func TestStoreBasic(t *testing.T) {
	s := NewStore()
	if err := s.Add("AKIAEXAMPLE", "supersecret123"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	sec, ok := s.Secret("AKIAEXAMPLE")
	if !ok || sec != "supersecret123" {
		t.Fatalf("Secret mismatch: %q %v", sec, ok)
	}
	if !s.Exists("AKIAEXAMPLE") {
		t.Fatal("Exists should be true")
	}
	if s.Exists("UNKNOWN") {
		t.Fatal("Exists should be false for unknown")
	}
}

func TestStoreEmptyRejected(t *testing.T) {
	s := NewStore()
	if err := s.Add("", "secret"); err == nil {
		t.Fatal("empty accessKey should be rejected")
	}
	if err := s.Add("AKIA", ""); err == nil {
		t.Fatal("empty secretKey should be rejected")
	}
}
