// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"sync"
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// regWith builds a registry from node -> disk paths. Every disk has 1000 bytes and 100 used.
func regWith(t *testing.T, layout map[string][]string) *datanode.Registry {
	t.Helper()
	reg := datanode.NewRegistry(0, 0)
	for node, paths := range layout {
		if err := reg.RegisterNode(node, node+":9002"); err != nil {
			t.Fatalf("RegisterNode(%s): %v", node, err)
		}
		for _, p := range paths {
			if err := reg.UpsertDisk(node, p, 1000, 100, 0); err != nil {
				t.Fatalf("UpsertDisk(%s,%s): %v", node, p, err)
			}
		}
	}
	return reg
}

func TestSameNodeUsesOneNodeDifferentDisks(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}, "n2": {"/c"}})
	for seed := int64(1); seed <= 40; seed++ {
		got, err := New(seed).SelectWith(reg, PolicyDual, 10, SelectOptions{Mode: ModeSameNode})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if len(got) != 2 || got[0].NodeID != "n1" || got[1].NodeID != "n1" || got[0].Path == got[1].Path {
			t.Fatalf("seed %d: want two different disks of n1, got %v", seed, got)
		}
	}
}

func TestSameNodeNeedsEnoughDisksOnOneNode(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a"}, "n2": {"/b"}})
	if _, err := New(1).SelectWith(reg, PolicyDual, 10, SelectOptions{Mode: ModeSameNode}); err == nil {
		t.Fatal("same_node dual with one disk per node must fail")
	}
}

func TestAnyModeAcceptsOneNode(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}})
	got, err := New(1).SelectWith(reg, PolicyDual, 10, SelectOptions{Mode: ModeAny})
	if err != nil {
		t.Fatalf("any: %v", err)
	}
	if len(got) != 2 || got[0].Path == got[1].Path {
		t.Fatalf("want two different disks, got %v", got)
	}
	// The default mode still needs two nodes.
	if _, err := New(1).SelectWith(reg, PolicyDual, 10, SelectOptions{}); err == nil {
		t.Fatal("spread dual with one node must fail")
	}
}

func TestAnyModePrefersDistinctNodes(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}, "n2": {"/c"}})
	for seed := int64(1); seed <= 40; seed++ {
		got, err := New(seed).SelectWith(reg, PolicyDual, 10, SelectOptions{Mode: ModeAny})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if got[0].NodeID == got[1].NodeID {
			t.Fatalf("seed %d: any should use two nodes when it can, got %v", seed, got)
		}
	}
}

func TestAllowListRestrictsDisks(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}, "n2": {"/c"}})
	allow := []DiskRef{{NodeID: "n1", Path: "/b"}, {NodeID: "n2", Path: "/c"}}
	for seed := int64(1); seed <= 40; seed++ {
		got, err := New(seed).SelectWith(reg, PolicySingle, 10, SelectOptions{Allow: allow})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if got[0] == (Target{NodeID: "n1", Path: "/a"}) {
			t.Fatalf("seed %d: picked a disk outside the allow-list: %v", seed, got)
		}
	}
	// Explicit pair on one node: "/a" and "/b" of n1.
	pair := []DiskRef{{NodeID: "n1", Path: "/a"}, {NodeID: "n1", Path: "/b"}}
	got, err := New(1).SelectWith(reg, PolicyDual, 10, SelectOptions{Mode: ModeSameNode, Allow: pair})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if len(got) != 2 || got[0].NodeID != "n1" || got[1].NodeID != "n1" {
		t.Fatalf("want both on n1, got %v", got)
	}
}

func TestAllowListMatchesUncleanedPath(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/mnt/a/"}, "n2": {"/mnt/b"}})
	allow := []DiskRef{{NodeID: "n1", Path: "/mnt/a"}}
	got, err := New(1).SelectWith(reg, PolicySingle, 10, SelectOptions{Allow: allow})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got[0].NodeID != "n1" || got[0].Path != "/mnt/a/" {
		t.Fatalf("want the registered path /mnt/a/ on n1, got %v", got)
	}
}

func TestReservedDiskOnlyForItsBucket(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a"}, "n2": {"/b"}})
	reserved := map[DiskRef]string{{NodeID: "n1", Path: "/a"}: "pics"}
	for seed := int64(1); seed <= 40; seed++ {
		got, err := New(seed).SelectWith(reg, PolicySingle, 10, SelectOptions{Bucket: "docs", Reserved: reserved})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if got[0].NodeID != "n2" {
			t.Fatalf("seed %d: docs must not use the disk reserved for pics, got %v", seed, got)
		}
	}
	got, err := New(1).SelectWith(reg, PolicySingle, 10, SelectOptions{
		Bucket:   "pics",
		Allow:    []DiskRef{{NodeID: "n1", Path: "/a"}},
		Reserved: reserved,
	})
	if err != nil || got[0].NodeID != "n1" {
		t.Fatalf("pics should reach its own disk: %v %v", got, err)
	}
}

func TestReservationShrinksNodeCount(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a"}, "n2": {"/b"}})
	reserved := map[DiskRef]string{{NodeID: "n1", Path: "/a"}: "pics"}
	if _, err := New(1).SelectWith(reg, PolicyDual, 10, SelectOptions{Bucket: "docs", Reserved: reserved}); err == nil {
		t.Fatal("docs has only one usable node left, dual must fail")
	}
	got, err := New(1).SelectWith(reg, PolicyDual, 10, SelectOptions{Bucket: "pics", Reserved: reserved})
	if err != nil || len(got) != 2 {
		t.Fatalf("pics can use both nodes: %v %v", got, err)
	}
}

func TestPolicyAllRejectsMode(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}, "n2": {"/c"}})
	if _, err := New(1).SelectWith(reg, PolicyAll, 10, SelectOptions{Mode: ModeSameNode}); err == nil {
		t.Fatal("ALL with same_node must fail")
	}
	got, err := New(1).SelectWith(reg, PolicyAll, 10, SelectOptions{})
	if err != nil || len(got) != 2 {
		t.Fatalf("ALL spread: %v %v", got, err)
	}
}

func TestSelectWithIsSafeForConcurrentUse(t *testing.T) {
	reg := regWith(t, map[string][]string{"n1": {"/a", "/b"}, "n2": {"/c", "/d"}})
	eng := New(5)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := eng.SelectWith(reg, PolicyDual, 10, SelectOptions{Mode: ModeAny}); err != nil {
					t.Errorf("select: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
