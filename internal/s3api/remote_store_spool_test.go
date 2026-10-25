// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpoolBodyUsesConfiguredDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := SetSpoolDir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetSpoolDir("") })

	f, n, cleanup, err := spoolBody(strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Dir(f.Name()); got != dir {
		t.Fatalf("spool file in %q, want %q", got, dir)
	}
	if n != 5 {
		t.Fatalf("size = %d, want 5", n)
	}
	cleanup()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spool dir not empty after cleanup: %d entries", len(entries))
	}
}

func TestSetSpoolDirRemovesLeftovers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, spoolPrefix+"stale")
	other := filepath.Join(dir, "keep.txt")
	for _, p := range []string{stale, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetSpoolDir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetSpoolDir("") })

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale spool file still present: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
}
