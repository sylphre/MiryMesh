// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"strings"
	"testing"
)

func TestBucketNameValidation(t *testing.T) {
	valid := []string{"my-bucket", "my.bucket-123", "abc", "a1b2c3"}
	for _, v := range valid {
		if !isValidBucketName(v) {
			t.Fatalf("expected valid: %q", v)
		}
	}
	invalid := []string{"AB", "ab", "192.168.1.1", "my..bucket", "-mybucket", "mybucket-", "MyBucket"}
	for _, v := range invalid {
		if isValidBucketName(v) {
			t.Fatalf("expected invalid: %q", v)
		}
	}
}

func TestPutGetDeleteViaStorage(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if cErr := st.CreateBucket("test-bucket"); cErr != nil {
		t.Fatalf("CreateBucket: %v", cErr)
	}
	meta, err := st.PutObject("test-bucket", "a/b/c.txt", "text/plain", map[string]string{"foo": "bar"}, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if meta.ETag == "" || meta.Size != 7 {
		t.Fatalf("meta unexpected: %+v", meta)
	}
	if _, hErr := st.HeadObject("test-bucket", "a/b/c.txt"); hErr != nil {
		t.Fatalf("HeadObject: %v", hErr)
	}
	gotMeta, rc, err := st.GetObject("test-bucket", "a/b/c.txt")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()
	if gotMeta.Meta["foo"] != "bar" {
		t.Fatalf("meta foo=%q", gotMeta.Meta["foo"])
	}
	if dErr := st.DeleteObject("test-bucket", "a/b/c.txt"); dErr != nil {
		t.Fatalf("DeleteObject: %v", dErr)
	}
	if _, hErr2 := st.HeadObject("test-bucket", "a/b/c.txt"); hErr2 == nil {
		t.Fatal("expected Head after delete to fail")
	}
}
