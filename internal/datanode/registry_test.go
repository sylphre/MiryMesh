// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"testing"
	"time"
)

func TestRegistryBasics(t *testing.T) {
	reg := NewRegistry(0, 0)
	if err := reg.RegisterNode("n1", "10.0.0.1:9001"); err != nil {
		t.Fatalf("RegisterNode: %v", err)
	}
	if reg.NodeCount() != 1 || reg.OnlineNodeCount() != 1 {
		t.Fatalf("counts: %d %d", reg.NodeCount(), reg.OnlineNodeCount())
	}
	if err := reg.UpsertDisk("n1", "/mnt/disk1", 1000, 200, 0); err != nil {
		t.Fatalf("UpsertDisk: %v", err)
	}
	disks := reg.ListDisksForNode("n1")
	if len(disks) != 1 {
		t.Fatalf("disks len=%d", len(disks))
	}
	if disks[0].EffectiveLimit() != 950 {
		t.Fatalf("EffectiveLimit=%d want 950", disks[0].EffectiveLimit())
	}
	if disks[0].EffectiveAvail() != 750 {
		t.Fatalf("EffectiveAvail=%d want 750", disks[0].EffectiveAvail())
	}
	// Custom limit overrides 95%.
	if err := reg.UpsertDisk("n1", "/mnt/disk1", 1000, 200, 500); err != nil {
		t.Fatalf("UpsertDisk custom: %v", err)
	}
	disks = reg.ListDisksForNode("n1")
	if disks[0].EffectiveLimit() != 500 {
		t.Fatalf("custom limit=%d want 500", disks[0].EffectiveLimit())
	}
	if disks[0].EffectiveAvail() != 300 {
		t.Fatalf("custom avail=%d want 300", disks[0].EffectiveAvail())
	}
	// Remove disk.
	if err := reg.RemoveDisk("n1", "/mnt/disk1"); err != nil {
		t.Fatalf("RemoveDisk: %v", err)
	}
	if len(reg.ListDisks()) != 0 {
		t.Fatal("expected no disks after remove")
	}
}

func TestRegistryOfflineTimeout(t *testing.T) {
	reg := NewRegistry(10*time.Millisecond, 20*time.Millisecond)
	_ = reg.RegisterNode("n1", "10.0.0.1:9001")
	// Fresh heartbeat.
	_ = reg.Heartbeat("n1")
	// Future scan should mark offline.
	offlined := reg.MarkOfflineIfStale(time.Now().Add(100 * time.Millisecond))
	if len(offlined) != 1 || offlined[0] != "n1" {
		t.Fatalf("offlined=%v", offlined)
	}
	if reg.OnlineNodeCount() != 0 {
		t.Fatal("should be offline")
	}
	// Re-register brings back online.
	_ = reg.RegisterNode("n1", "10.0.0.1:9001")
	if reg.OnlineNodeCount() != 1 {
		t.Fatal("should be online after re-register")
	}
}

func TestRegistryQuotaValidation(t *testing.T) {
	reg := NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "10.0.0.1:9001")
	if err := reg.UpsertDisk("n1", "/mnt/disk1", 100, 0, 200); err == nil {
		t.Fatal("limit > capacity should be rejected")
	}
	if err := reg.UpsertDisk("n1", "/mnt/disk1", -1, 0, 0); err == nil {
		t.Fatal("negative capacity should be rejected")
	}
}
