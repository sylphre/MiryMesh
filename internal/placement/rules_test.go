// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import "testing"

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{
		"":          ModeSpread,
		" Spread ":  ModeSpread,
		"same_node": ModeSameNode,
		"ANY":       ModeAny,
	} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMode("everywhere"); err == nil {
		t.Fatal("unknown mode must fail")
	}
}

func TestRuleNormalize(t *testing.T) {
	got, err := Rule{Disks: []DiskRef{
		{NodeID: " n1 ", Path: "/mnt/a/"},
		{NodeID: "n1", Path: "/mnt/a"},
		{NodeID: "n2", Path: "/mnt/b"},
	}}.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.Mode != ModeSpread || len(got.Disks) != 2 || got.Disks[0].Path != "/mnt/a" {
		t.Fatalf("unexpected rule: %+v", got)
	}
	if _, err := (Rule{Disks: []DiskRef{{NodeID: "n1"}}}).Normalize(); err == nil {
		t.Fatal("a disk without path must fail")
	}
	if !(Rule{}).IsDefault() || (Rule{Mode: ModeAny}).IsDefault() {
		t.Fatal("IsDefault is wrong")
	}
}

func TestCheckFeasible(t *testing.T) {
	two := []DiskRef{{NodeID: "n1", Path: "/a"}, {NodeID: "n1", Path: "/b"}}
	cases := []struct {
		name   string
		rule   Rule
		policy Policy
		ok     bool
	}{
		{"no allow-list is always fine", Rule{Mode: ModeSameNode}, PolicyTriple, true},
		{"same_node dual on two disks of one node", Rule{Mode: ModeSameNode, Disks: two}, PolicyDual, true},
		{"same_node triple on two disks", Rule{Mode: ModeSameNode, Disks: two}, PolicyTriple, false},
		{"spread dual on one node", Rule{Mode: ModeSpread, Disks: two}, PolicyDual, false},
		{"any dual on two disks", Rule{Mode: ModeAny, Disks: two}, PolicyDual, true},
		{"ALL with same_node", Rule{Mode: ModeSameNode}, PolicyAll, false},
		{"ALL with spread allow-list", Rule{Mode: ModeSpread, Disks: two}, PolicyAll, true},
	}
	for _, c := range cases {
		err := c.rule.CheckFeasible(c.policy)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}
