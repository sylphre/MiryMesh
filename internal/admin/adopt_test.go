// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import "testing"

func TestNormalizeNodeAddr(t *testing.T) {
	ok := map[string]string{
		"10.0.0.5":        "10.0.0.5:9002",
		"10.0.0.5:9102":   "10.0.0.5:9102",
		"  node1.local  ": "node1.local:9002",
		"::1":             "[::1]:9002",
		"[::1]:9003":      "[::1]:9003",
	}
	for in, want := range ok {
		got, err := normalizeNodeAddr(in)
		if err != nil || got != want {
			t.Errorf("normalizeNodeAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "http://10.0.0.5", "10.0.0.5:abc", "10.0.0.5:0", "10.0.0.5:70000", "a b", "10.0.0.5/x", ":9002", "user@10.0.0.5"} {
		if got, err := normalizeNodeAddr(in); err == nil {
			t.Errorf("normalizeNodeAddr(%q) = %q; want error", in, got)
		}
	}
}

func TestNormalizeControllerURL(t *testing.T) {
	ok := map[string]string{
		"http://master:9001/":  "http://master:9001",
		" https://m.example ":  "https://m.example",
		"http://10.0.0.1:9001": "http://10.0.0.1:9001",
	}
	for in, want := range ok {
		got, err := normalizeControllerURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeControllerURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "master:9001", "ftp://x", "http://"} {
		if got, err := normalizeControllerURL(in); err == nil {
			t.Errorf("normalizeControllerURL(%q) = %q; want error", in, got)
		}
	}
}
