// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.1", "v1.0.0-rc.2", -1},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", -1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-alpha.1", "v1.0.0-alpha.beta", -1},
		{"1.2.3", "v1.2.3", 0},
		{"v1.10.0", "v1.9.9", 1},
		{"v1.0.0+build5", "v1.0.0", 0},
	}
	for _, c := range cases {
		got, ok := Compare(c.a, c.b)
		if !ok || got != c.want {
			t.Errorf("Compare(%q, %q) = %d, %v; want %d, true", c.a, c.b, got, ok, c.want)
		}
	}
}

func TestCompareRejectsNonSemver(t *testing.T) {
	for _, s := range []string{"dev", "", "v1.2", "master", "v1.2.x", "v1.0.0-"} {
		if _, ok := Compare(s, "v1.0.0"); ok {
			t.Errorf("Compare(%q, v1.0.0) should not be comparable", s)
		}
	}
}

func TestValidTag(t *testing.T) {
	for _, s := range []string{"v1.0.0", "v1.0.0-rc.1", "v0.9.12"} {
		if !ValidTag(s) {
			t.Errorf("ValidTag(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"1.0.0", "v1.0.0/../x", "v1.0.0+b", "dev", "", "v1.0.0 "} {
		if ValidTag(s) {
			t.Errorf("ValidTag(%q) = true, want false", s)
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	if !IsPrerelease("v1.0.0-rc.1") || IsPrerelease("v1.0.0") || IsPrerelease("dev") {
		t.Fatal("IsPrerelease gave an unexpected answer")
	}
}
