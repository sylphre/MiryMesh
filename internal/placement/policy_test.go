// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import "testing"

func TestParsePolicy(t *testing.T) {
	tests := []struct {
		in      string
		want    Policy
		wantErr bool
	}{
		{"single", PolicySingle, false},
		{"Single", PolicySingle, false},
		{"DUAL", PolicyDual, false},
		{"triple", PolicyTriple, false},
		{"ALL", PolicyAll, false},
		{"all", PolicyAll, false},
		{"", "", true},
		{"quorum", "", true},
	}
	for _, tc := range tests {
		got, err := ParsePolicy(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("ParsePolicy(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
		}
		if err == nil && got != tc.want {
			t.Fatalf("ParsePolicy(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestPolicyValidateForCluster(t *testing.T) {
	if err := PolicyDual.ValidateForCluster(1); err == nil {
		t.Fatal("dual with 1 node should fail")
	}
	if err := PolicyTriple.ValidateForCluster(2); err == nil {
		t.Fatal("triple with 2 nodes should fail")
	}
	if err := PolicyAll.ValidateForCluster(0); err == nil {
		t.Fatal("ALL with 0 nodes should fail")
	}
	if err := PolicySingle.ValidateForCluster(1); err != nil {
		t.Fatalf("single with 1 should pass: %v", err)
	}
	if err := PolicyAll.ValidateForCluster(3); err != nil {
		t.Fatalf("ALL with 3 should pass: %v", err)
	}
}
