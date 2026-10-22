// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"testing"
	"time"
)

func TestParseByteRange(t *testing.T) {
	cases := []struct {
		spec  string
		size  int64
		start int64
		end   int64
		res   rangeResult
	}{
		{"bytes=0-3", 10, 0, 3, rangeOK},
		{"bytes=5-", 10, 5, 9, rangeOK},
		{"bytes=-4", 10, 6, 9, rangeOK},
		{"bytes=-20", 10, 0, 9, rangeOK},
		{"bytes=2-100", 10, 2, 9, rangeOK},
		{"bytes=9-9", 10, 9, 9, rangeOK},
		{"bytes=10-", 10, 0, 0, rangeUnsatisfiable},
		{"bytes=10-12", 10, 0, 0, rangeUnsatisfiable},
		{"bytes=-0", 10, 0, 0, rangeUnsatisfiable},
		{"bytes=-5", 0, 0, 0, rangeUnsatisfiable},
		{"bytes=0-0", 0, 0, 0, rangeUnsatisfiable},
		{"bytes=5-2", 10, 0, 0, rangeIgnore},
		{"bytes=0-1,3-4", 10, 0, 0, rangeIgnore},
		{"bytes=a-b", 10, 0, 0, rangeIgnore},
		{"bytes=-", 10, 0, 0, rangeIgnore},
		{"bytes=", 10, 0, 0, rangeIgnore},
		{"items=0-1", 10, 0, 0, rangeIgnore},
	}
	for _, c := range cases {
		start, end, res := parseByteRange(c.spec, c.size)
		if res != c.res {
			t.Fatalf("parseByteRange(%q,%d) result=%v want %v", c.spec, c.size, res, c.res)
		}
		if res == rangeOK && (start != c.start || end != c.end) {
			t.Fatalf("parseByteRange(%q,%d) = %d-%d want %d-%d", c.spec, c.size, start, end, c.start, c.end)
		}
	}
}

func TestEvalReadPreconditions(t *testing.T) {
	mod := time.Date(2026, 1, 1, 12, 0, 0, 500_000_000, time.UTC)
	meta := ObjectMeta{Key: "k", ETag: "abc", LastMod: mod}
	same := mod.Truncate(time.Second).Format(http.TimeFormat)
	older := mod.Add(-time.Hour).Format(http.TimeFormat)
	newer := mod.Add(time.Hour).Format(http.TimeFormat)

	cases := []struct {
		name string
		h    map[string]string
		want precondResult
	}{
		{"none", nil, precondOK},
		{"if-match hit", map[string]string{"If-Match": `"abc"`}, precondOK},
		{"if-match star", map[string]string{"If-Match": "*"}, precondOK},
		{"if-match miss", map[string]string{"If-Match": `"zzz"`}, precondFailed},
		{"if-none-match hit", map[string]string{"If-None-Match": `"abc"`}, precondNotModified},
		{"if-none-match weak", map[string]string{"If-None-Match": `W/"abc"`}, precondNotModified},
		{"if-none-match list", map[string]string{"If-None-Match": `"x", "abc"`}, precondNotModified},
		{"if-none-match star", map[string]string{"If-None-Match": "*"}, precondNotModified},
		{"if-none-match miss", map[string]string{"If-None-Match": `"zzz"`}, precondOK},
		{"if-modified-since same second", map[string]string{"If-Modified-Since": same}, precondNotModified},
		{"if-modified-since older", map[string]string{"If-Modified-Since": older}, precondOK},
		{"if-modified-since newer", map[string]string{"If-Modified-Since": newer}, precondNotModified},
		{"if-modified-since garbage", map[string]string{"If-Modified-Since": "yesterday"}, precondOK},
		{"if-unmodified-since older", map[string]string{"If-Unmodified-Since": older}, precondFailed},
		{"if-unmodified-since newer", map[string]string{"If-Unmodified-Since": newer}, precondOK},
		{"if-match overrides if-unmodified-since", map[string]string{"If-Match": `"abc"`, "If-Unmodified-Since": older}, precondOK},
		{"if-none-match overrides if-modified-since", map[string]string{"If-None-Match": `"zzz"`, "If-Modified-Since": newer}, precondOK},
	}
	for _, c := range cases {
		h := http.Header{}
		for k, v := range c.h {
			h.Set(k, v)
		}
		if got := evalReadPreconditions(h, meta); got != c.want {
			t.Fatalf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
