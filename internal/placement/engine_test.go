// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

func TestEngineSingle(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.RegisterNode("n2", "n2:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	_ = reg.UpsertDisk("n2", "/mnt/b", 1000, 100, 0)
	eng := New(42)
	targets, err := eng.Select(reg, PolicySingle, 10)
	if err != nil {
		t.Fatalf("Select single: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("len=%d", len(targets))
	}
}

func TestEngineDualRequiresTwoNodes(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	eng := New(1)
	if _, err := eng.Select(reg, PolicyDual, 10); err == nil {
		t.Fatal("dual with 1 node should fail")
	}
	_ = reg.RegisterNode("n2", "n2:9001")
	_ = reg.UpsertDisk("n2", "/mnt/b", 1000, 100, 0)
	targets, err := eng.Select(reg, PolicyDual, 10)
	if err != nil {
		t.Fatalf("dual with 2 nodes: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len=%d", len(targets))
	}
	if targets[0].NodeID == targets[1].NodeID {
		t.Fatalf("dual should pick distinct nodes, got %v", targets)
	}
}

func TestEngineTripleAndALL(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	for i := 1; i <= 3; i++ {
		id := string(rune('0'+i)) + "n"
		_ = reg.RegisterNode(id, id+":9001")
		_ = reg.UpsertDisk(id, "/mnt/d", 1000, 100, 0)
	}
	eng := New(7)
	targets, err := eng.Select(reg, PolicyTriple, 10)
	if err != nil {
		t.Fatalf("triple: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("triple len=%d", len(targets))
	}
	// ALL should select 3 (one per node)
	targets, err = eng.Select(reg, PolicyAll, 10)
	if err != nil {
		t.Fatalf("ALL: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("ALL len=%d want 3", len(targets))
	}
}

func TestEngineQuotaFilters(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 940, 0) // avail 10
	_ = reg.RegisterNode("n2", "n2:9001")
	_ = reg.UpsertDisk("n2", "/mnt/b", 1000, 100, 0) // avail 850
	eng := New(2)
	// size 20: only n2 qualifies.
	targets, err := eng.Select(reg, PolicySingle, 20)
	if err != nil {
		t.Fatalf("quota single: %v", err)
	}
	if targets[0].NodeID != "n2" {
		t.Fatalf("expected n2, got %v", targets)
	}
	// size 900: no one has quota (n2 avail 850) -> error -> caller maps to 507
	if _, err := eng.Select(reg, PolicySingle, 900); err == nil {
		t.Fatal("expected insufficient storage error")
	}
}

func TestEngineOfflineFiltered(t *testing.T) {
	reg := datanode.NewRegistry(10*time.Millisecond, 50*time.Millisecond)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	time.Sleep(60 * time.Millisecond)
	_ = reg.RegisterNode("n2", "n2:9001")
	_ = reg.UpsertDisk("n2", "/mnt/b", 1000, 100, 0)
	// Only n1 should be stale (registered 60ms ago, timeout 50ms).
	offlined := reg.MarkOfflineIfStale(time.Now())
	if len(offlined) != 1 || offlined[0] != "n1" {
		t.Fatalf("offlined=%v want [n1]", offlined)
	}
	eng := New(3)
	targets, err := eng.Select(reg, PolicySingle, 10)
	if err != nil {
		t.Fatalf("select with one offline: %v", err)
	}
	_ = targets
	// Dual should fail because only 1 online.
	if _, err := eng.Select(reg, PolicyDual, 10); err == nil {
		t.Fatal("dual with 1 online should fail")
	}
}
