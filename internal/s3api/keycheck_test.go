// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateObjectKey(t *testing.T) {
	cases := []struct {
		key  string
		ok   bool
		code string
	}{
		{"file.txt", true, ""},
		{"dir/sub/file.txt", true, ""},
		{"dir/", true, ""},
		{"a..b/c", true, ""},
		{"../x", false, "InvalidRequest"},
		{"a/../b", false, "InvalidRequest"},
		{"a/..", false, "InvalidRequest"},
		{"a/./b", false, "InvalidRequest"},
		{"bad\x00key", false, "InvalidRequest"},
		{strings.Repeat("k", 1025), false, "KeyTooLongError"},
		{strings.Repeat("k", 1024), true, ""},
	}
	for _, c := range cases {
		e, ok := validateObjectKey(c.key)
		if ok != c.ok {
			t.Fatalf("validateObjectKey(%q) ok=%v want %v", c.key, ok, c.ok)
		}
		if !ok && e.Code != c.code {
			t.Fatalf("validateObjectKey(%q) code=%s want %s", c.key, e.Code, c.code)
		}
	}
}

func TestObjectPathStaysInsideBucket(t *testing.T) {
	root := t.TempDir()
	st, err := NewStorage(root)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	for _, k := range []string{"../../etc/passwd", "a/../../b", "..", "/../x", "a/../../../x", "../bkt2/x"} {
		rel, err := filepath.Rel(st.bucketDir("bkt"), st.objectPath("bkt", k))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Fatalf("key %q escapes bucket: rel=%q err=%v", k, rel, err)
		}
	}
}

func TestPutObjectTraversalDoesNotEscape(t *testing.T) {
	root := t.TempDir()
	st, err := NewStorage(root)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := st.PutObject("bkt", "../../escape.txt", "text/plain", nil, strings.NewReader("x")); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !os.IsNotExist(err) {
		t.Fatalf("file escaped the storage root (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bkt", "escape.txt")); err != nil {
		t.Fatalf("expected file inside bucket: %v", err)
	}
}

func TestBucketCreationDate(t *testing.T) {
	root := t.TempDir()
	st, err := NewStorage(root)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	before := time.Now().UTC().Add(-time.Second)
	if err = st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	got, ok := st.BucketCreationDate("bkt")
	if !ok || got.Before(before) || got.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("creation date %v ok=%v out of range", got, ok)
	}
	if _, ok := st.BucketCreationDate("missing"); ok {
		t.Fatalf("missing bucket must report ok=false")
	}
	st2, err := NewStorage(root) // restart: falls back to the directory mtime
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if d, ok := st2.BucketCreationDate("bkt"); !ok || d.IsZero() {
		t.Fatalf("after restart: date=%v ok=%v", d, ok)
	}
}
