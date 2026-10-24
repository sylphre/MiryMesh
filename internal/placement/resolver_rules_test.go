// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"errors"
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

func newTestResolver(t *testing.T, reg *datanode.Registry, def string) *Resolver {
	t.Helper()
	r, err := NewResolver(reg, New(1), def, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestResolverReserveAndConflicts(t *testing.T) {
	r := newTestResolver(t, datanode.NewRegistry(0, 0), "")
	a := DiskRef{NodeID: "n1", Path: "/a"}
	b := DiskRef{NodeID: "n1", Path: "/b"}

	if err := r.Reserve("pics", a); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := r.Reserve("pics", DiskRef{NodeID: "n1", Path: "/a/"}); err != nil {
		t.Fatalf("reserving the same disk twice for one bucket must work: %v", err)
	}
	if err := r.Reserve("docs", a); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if _, err := r.SetBucketRule("docs", Rule{Mode: ModeAny, Disks: []DiskRef{a, b}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("allow-list with a reserved disk: want ErrConflict, got %v", err)
	}
	if _, err := r.SetBucketRule("docs", Rule{Mode: ModeAny, Disks: []DiskRef{b, {NodeID: "n2", Path: "/c"}}}); err != nil {
		t.Fatalf("SetBucketRule: %v", err)
	}
	if err := r.Reserve("pics", b); !errors.Is(err, ErrConflict) {
		t.Fatalf("disk in another bucket's allow-list: want ErrConflict, got %v", err)
	}
	if !r.Release(a) || r.Release(a) {
		t.Fatal("Release must succeed once")
	}
	if len(r.Reservations()) != 0 {
		t.Fatalf("reservations left: %v", r.Reservations())
	}
}

func TestResolverResolveHonoursRule(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}, "n2": {"/c"}})
	r := newTestResolver(t, reg, "dual")
	if _, err := r.SetBucketRule("bk", Rule{Mode: ModeSameNode}); err != nil {
		t.Fatalf("SetBucketRule: %v", err)
	}
	for i := 0; i < 20; i++ {
		got, err := r.Resolve("bk", 10)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got[0].NodeID != "n1" || got[1].NodeID != "n1" {
			t.Fatalf("want both replicas on n1, got %v", got)
		}
	}
	// A bucket without a rule keeps using two nodes.
	got, err := r.Resolve("other", 10)
	if err != nil || got[0].NodeID == got[1].NodeID {
		t.Fatalf("default bucket should spread: %v %v", got, err)
	}
}

func TestResolverKeepsReservedDiskAwayFromOtherBuckets(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a"}, "n2": {"/b"}})
	r := newTestResolver(t, reg, "single")
	if err := r.Reserve("pics", DiskRef{NodeID: "n1", Path: "/a"}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	for i := 0; i < 30; i++ {
		got, err := r.Resolve("docs", 10)
		if err != nil || got[0].NodeID != "n2" {
			t.Fatalf("docs must avoid n1:/a: %v %v", got, err)
		}
	}
}

func TestResolverSetBucketPolicyChecksRule(t *testing.T) {
	r := newTestResolver(t, datanode.NewRegistry(0, 0), "")
	two := []DiskRef{{NodeID: "n1", Path: "/a"}, {NodeID: "n1", Path: "/b"}}
	if _, err := r.SetBucketRule("bk", Rule{Mode: ModeSameNode, Disks: two}); err != nil {
		t.Fatalf("SetBucketRule: %v", err)
	}
	if err := r.SetBucketPolicy("bk", PolicyTriple); err == nil {
		t.Fatal("triple cannot fit on two allowed disks")
	}
	if err := r.SetBucketPolicy("bk", PolicyDual); err != nil {
		t.Fatalf("dual fits: %v", err)
	}
}

func TestResolverStateRoundTripAndForget(t *testing.T) {
	r := newTestResolver(t, datanode.NewRegistry(0, 0), "")
	if _, err := r.SetBucketRule("bk", Rule{Mode: ModeAny, Disks: []DiskRef{{NodeID: "n1", Path: "/a"}}}); err != nil {
		t.Fatalf("SetBucketRule: %v", err)
	}
	if err := r.Reserve("bk", DiskRef{NodeID: "n1", Path: "/a"}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	st := r.ExportState()

	r2 := newTestResolver(t, datanode.NewRegistry(0, 0), "")
	if skipped := r2.ImportState(st); skipped != 0 {
		t.Fatalf("skipped=%d", skipped)
	}
	if got := r2.RuleFor("bk"); got.Mode != ModeAny || len(got.Disks) != 1 {
		t.Fatalf("rule lost: %+v", got)
	}
	if len(r2.Reservations()) != 1 {
		t.Fatalf("reservation lost: %v", r2.Reservations())
	}
	if skipped := r2.ImportState(State{Rules: map[string]Rule{"x": {Mode: "bogus"}}}); skipped != 1 {
		t.Fatalf("a broken rule must be skipped, skipped=%d", skipped)
	}

	r.ForgetBucket("bk")
	if got := r.RuleFor("bk"); !got.IsDefault() {
		t.Fatalf("rule still there: %+v", got)
	}
	if len(r.Reservations()) != 0 {
		t.Fatalf("reservation still there: %v", r.Reservations())
	}
}
