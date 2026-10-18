// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import "regexp"

var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func isValidBucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	if !bucketNameRe.MatchString(name) {
		return false
	}
	// No double dots, no dot-dash adjacency leading to IP-like.
	for i := 0; i < len(name)-1; i++ {
		if name[i] == '.' && name[i+1] == '.' {
			return false
		}
	}
	// Must not look like IPv4.
	dots := 0
	for _, c := range name {
		if c == '.' {
			dots++
		}
	}
	if dots == 3 {
		allDotOrDigit := true
		for _, c := range name {
			if c != '.' && (c < '0' || c > '9') {
				allDotOrDigit = false
				break
			}
		}
		if allDotOrDigit {
			return false
		}
	}
	return true
}
