// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"reflect"
	"testing"
)

func objs(keys ...string) []ObjectMeta {
	out := make([]ObjectMeta, 0, len(keys))
	for _, k := range keys {
		out = append(out, ObjectMeta{Key: k})
	}
	return out
}

func objKeys(res ListObjectsV2Result) []string {
	var out []string
	for _, o := range res.Objects {
		out = append(out, o.Key)
	}
	return out
}

func TestMergeListingsDedupeAndOrder(t *testing.T) {
	a := ListObjectsV2Result{Objects: objs("a", "c"), CommonPrefixes: []string{"d/"}}
	b := ListObjectsV2Result{Objects: objs("b", "c"), CommonPrefixes: []string{"d/", "e/"}}
	got := mergeListings([]ListObjectsV2Result{a, b}, 10)
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(objKeys(got), want) {
		t.Fatalf("objects=%v want %v", objKeys(got), want)
	}
	if want := []string{"d/", "e/"}; !reflect.DeepEqual(got.CommonPrefixes, want) {
		t.Fatalf("prefixes=%v want %v", got.CommonPrefixes, want)
	}
	if got.KeyCount != 5 || got.IsTruncated || got.NextContinuationToken != "" {
		t.Fatalf("count=%d truncated=%v token=%q", got.KeyCount, got.IsTruncated, got.NextContinuationToken)
	}
}

func TestMergeListingsCutsAtMaxKeys(t *testing.T) {
	a := ListObjectsV2Result{Objects: objs("a", "c", "e")}
	b := ListObjectsV2Result{Objects: objs("b", "d", "f")}
	got := mergeListings([]ListObjectsV2Result{a, b}, 4)
	if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(objKeys(got), want) {
		t.Fatalf("objects=%v want %v", objKeys(got), want)
	}
	if !got.IsTruncated || got.NextContinuationToken != "d" || got.KeyCount != 4 {
		t.Fatalf("truncated=%v token=%q count=%d", got.IsTruncated, got.NextContinuationToken, got.KeyCount)
	}
}

// A source that is truncated has entries beyond its page, so the merge is
// truncated even when exactly maxKeys entries remain after de-duplication.
func TestMergeListingsSourceTruncated(t *testing.T) {
	a := ListObjectsV2Result{Objects: objs("a", "b"), IsTruncated: true, NextContinuationToken: "b"}
	b := ListObjectsV2Result{Objects: objs("a")}
	got := mergeListings([]ListObjectsV2Result{a, b}, 2)
	if !got.IsTruncated || got.NextContinuationToken != "b" || got.KeyCount != 2 {
		t.Fatalf("truncated=%v token=%q count=%d", got.IsTruncated, got.NextContinuationToken, got.KeyCount)
	}
}

func TestMergeListingsPrefixIsTokenAndSortsWithObjects(t *testing.T) {
	a := ListObjectsV2Result{Objects: objs("a.txt", "b.txt"), CommonPrefixes: []string{"a/"}}
	got := mergeListings([]ListObjectsV2Result{a}, 2)
	if want := []string{"a.txt"}; !reflect.DeepEqual(objKeys(got), want) {
		t.Fatalf("objects=%v want %v", objKeys(got), want)
	}
	if want := []string{"a/"}; !reflect.DeepEqual(got.CommonPrefixes, want) {
		t.Fatalf("prefixes=%v want %v", got.CommonPrefixes, want)
	}
	if !got.IsTruncated || got.NextContinuationToken != "a/" {
		t.Fatalf("truncated=%v token=%q", got.IsTruncated, got.NextContinuationToken)
	}
}

func TestMergeListingsEmpty(t *testing.T) {
	got := mergeListings([]ListObjectsV2Result{{}, {}}, 0)
	if got.KeyCount != 0 || got.IsTruncated || len(got.Objects) != 0 || len(got.CommonPrefixes) != 0 {
		t.Fatalf("unexpected result %+v", got)
	}
}
