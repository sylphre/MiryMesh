// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// listAllPages walks every page and returns objects and prefixes in S3 order.
func listAllPages(t *testing.T, st *Storage, bucket, prefix, delim string, max int) []string {
	t.Helper()
	var out []string
	token := ""
	for i := 0; i < 100; i++ {
		res, err := st.ListObjectsV2(bucket, prefix, delim, token, max)
		if err != nil {
			t.Fatalf("ListObjectsV2: %v", err)
		}
		var page []string
		for _, o := range res.Objects {
			page = append(page, o.Key)
		}
		page = append(page, res.CommonPrefixes...)
		sort.Strings(page)
		if res.KeyCount != len(page) {
			t.Fatalf("KeyCount=%d want %d (page=%v)", res.KeyCount, len(page), page)
		}
		if len(page) > max {
			t.Fatalf("page has %d entries, max-keys=%d", len(page), max)
		}
		out = append(out, page...)
		if !res.IsTruncated {
			return out
		}
		if res.NextContinuationToken == "" {
			t.Fatalf("truncated page without NextContinuationToken")
		}
		token = res.NextContinuationToken
	}
	t.Fatalf("pagination did not terminate")
	return nil
}

func newListFixture(t *testing.T) (*Storage, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	for _, k := range []string{"a.txt", "a/b.txt", "a/c.txt", "b.txt", "c/d.txt"} {
		if _, err := st.PutObject("bkt", k, "text/plain", nil, strings.NewReader("data-"+k)); err != nil {
			t.Fatalf("PutObject %s: %v", k, err)
		}
	}
	return st, dir
}

func TestStorageListPaginationFlat(t *testing.T) {
	st, _ := newListFixture(t)
	got := listAllPages(t, st, "bkt", "", "", 2)
	want := []string{"a.txt", "a/b.txt", "a/c.txt", "b.txt", "c/d.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestStorageListPaginationDelimiterNoDuplicatePrefix(t *testing.T) {
	st, _ := newListFixture(t)
	got := listAllPages(t, st, "bkt", "", "/", 2)
	want := []string{"a.txt", "a/", "b.txt", "c/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestStorageListPrefixFilter(t *testing.T) {
	st, _ := newListFixture(t)
	got := listAllPages(t, st, "bkt", "a/", "/", 10)
	want := []string{"a/b.txt", "a/c.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestStorageListAfterRestart(t *testing.T) {
	_, dir := newListFixture(t)
	st2, err := NewStorage(dir) // fresh process: empty meta cache
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	res, err := st2.ListObjectsV2("bkt", "", "", "", 1000)
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	if len(res.Objects) != 5 || res.KeyCount != 5 || res.IsTruncated {
		t.Fatalf("after restart: objects=%d keycount=%d truncated=%v", len(res.Objects), res.KeyCount, res.IsTruncated)
	}
	for _, o := range res.Objects {
		if o.ETag == "" || o.Size == 0 || o.LastMod.IsZero() {
			t.Fatalf("incomplete meta for %s: %+v", o.Key, o)
		}
	}
}
