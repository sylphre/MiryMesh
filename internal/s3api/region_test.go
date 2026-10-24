// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"strings"
	"sync"
	"testing"
)

func TestRouterRegionDefaultAndChange(t *testing.T) {
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	rt := NewRouter(st, "")
	if got := rt.Region(); got != DefaultRegion {
		t.Fatalf("Region() = %q, want %q", got, DefaultRegion)
	}
	if err = rt.SetRegion("ap-northeast-1"); err != nil {
		t.Fatalf("SetRegion: %v", err)
	}
	if got := rt.Region(); got != "ap-northeast-1" {
		t.Fatalf("Region() = %q, want ap-northeast-1", got)
	}
	// An empty value restores the default.
	if err = rt.SetRegion(""); err != nil {
		t.Fatalf("SetRegion(empty): %v", err)
	}
	if got := rt.Region(); got != DefaultRegion {
		t.Fatalf("Region() = %q, want %q", got, DefaultRegion)
	}
}

func TestRouterSetRegionRejectsInvalid(t *testing.T) {
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	rt := NewRouter(st, "eu-west-1")
	for _, bad := range []string{"US_EAST_1", "us east", "us-east-1\n", "ü", strings.Repeat("a", 65)} {
		if err = rt.SetRegion(bad); err == nil {
			t.Fatalf("SetRegion(%q) = nil, want error", bad)
		}
		if got := rt.Region(); got != "eu-west-1" {
			t.Fatalf("after rejected %q, Region() = %q, want eu-west-1", bad, got)
		}
	}
}

func TestRouterRegionConcurrent(t *testing.T) {
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	rt := NewRouter(st, "")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = rt.SetRegion("us-west-2")
		}()
		go func() {
			defer wg.Done()
			_ = rt.Region()
		}()
	}
	wg.Wait()
	if got := rt.Region(); got != "us-west-2" {
		t.Fatalf("Region() = %q, want us-west-2", got)
	}
}
