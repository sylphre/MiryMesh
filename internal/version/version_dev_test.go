// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package version

import "testing"

// Source builds made by install.sh report "<short commit>-dev". They must behave like "dev":
// not a semantic version, not a release tag, not a pre-release, and never comparable.
func TestCommitDevVersionIsNotSemver(t *testing.T) {
	for _, s := range []string{"1f7b438-dev", "abc1234-dev", "1234567-dev", "0000000-dev", "ca3bef3-dev"} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
		if ValidTag(s) {
			t.Errorf("ValidTag(%q) = true, want false", s)
		}
		if IsPrerelease(s) {
			t.Errorf("IsPrerelease(%q) = true, want false", s)
		}
		if _, ok := Compare(s, "v1.0.0"); ok {
			t.Errorf("Compare(%q, v1.0.0) ok = true, want false", s)
		}
		if _, ok := Compare("v1.0.0", s); ok {
			t.Errorf("Compare(v1.0.0, %q) ok = true, want false", s)
		}
	}
}
