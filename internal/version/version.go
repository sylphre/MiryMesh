// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package version holds the build version and compares release tags.
package version

import (
	"cmp"
	"strconv"
	"strings"
)

// Version is the release tag this binary was built from. Release builds set it with
//
//	-ldflags "-X github.com/sylphre/MiryMesh/internal/version.Version=v1.0.0-rc.1"
//
// Local builds keep "dev".
var Version = "dev"

type semver struct {
	major, minor, patch uint64
	pre                 []string
}

func parse(s string) (semver, bool) {
	var out semver
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return out, false
	}
	var nums [3]uint64
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return out, false
		}
		nums[i] = n
	}
	out.major, out.minor, out.patch = nums[0], nums[1], nums[2]
	if hasPre {
		out.pre = strings.Split(pre, ".")
		for _, id := range out.pre {
			if id == "" {
				return out, false
			}
		}
	}
	return out, true
}

// Valid reports whether s is a semantic version such as v1.2.3 or 1.0.0-rc.1.
func Valid(s string) bool {
	_, ok := parse(s)
	return ok
}

// IsPrerelease reports whether s is a semantic version with a pre-release part (for example v1.0.0-rc.1).
func IsPrerelease(s string) bool {
	v, ok := parse(s)
	return ok && len(v.pre) > 0
}

// ValidTag reports whether s can be used as a release tag in a download URL:
// a leading "v", a semantic version, and only [0-9A-Za-z.-] characters.
func ValidTag(s string) bool {
	if !strings.HasPrefix(s, "v") || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-':
		default:
			return false
		}
	}
	return Valid(s)
}

// Compare returns -1, 0 or 1 when a is older than, equal to or newer than b.
// ok is false when either argument is not a semantic version (for example "dev").
func Compare(a, b string) (result int, ok bool) {
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return 0, false
	}
	if c := cmp.Compare(x.major, y.major); c != 0 {
		return c, true
	}
	if c := cmp.Compare(x.minor, y.minor); c != 0 {
		return c, true
	}
	if c := cmp.Compare(x.patch, y.patch); c != 0 {
		return c, true
	}
	return comparePre(x.pre, y.pre), true
}

// comparePre follows semver precedence: a release outranks any of its pre-releases.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareID(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func compareID(a, b string) int {
	na, errA := strconv.ParseUint(a, 10, 64)
	nb, errB := strconv.ParseUint(b, 10, 64)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}
