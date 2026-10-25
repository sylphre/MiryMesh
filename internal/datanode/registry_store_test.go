// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"errors"
	"sync"
	"testing"
)

// memNodeStore is an in-memory NodeStore standing in for the database.
type memNodeStore struct {
	mu      sync.Mutex
	nodes   map[string]Node
	disks   map[string]DiskPath
	loadErr error
}

func newMemNodeStore() *memNodeStore {
	return &memNodeStore{nodes: map[string]Node{}, disks: map[string]DiskPath{}}
}

func memDiskKey(nodeID, path string) string { return nodeID + "\x00" + path }

func (m *memNodeStore) SaveNode(n Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[n.ID] = n
	return nil
}

func (m *memNodeStore) DeleteNode(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nodes, id)
	for k, d := range m.disks {
		if d.NodeID == id {
			delete(m.disks, k)
		}
	}
	return nil
}

func (m *memNodeStore) SaveDisk(d DiskPath) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disks[memDiskKey(d.NodeID, d.Path)] = d
	return nil
}

func (m *memNodeStore) DeleteDisk(nodeID, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.disks, memDiskKey(nodeID, path))
	return nil
}

func (m *memNodeStore) LoadNodes() ([]Node, []DiskPath, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadErr != nil {
		return nil, nil, m.loadErr
	}
	var nodes []Node
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	var disks []DiskPath
	for _, d := range m.disks {
		disks = append(disks, d)
	}
	return nodes, disks, nil
}

func TestRegistryRestoresNodesAfterRestart(t *testing.T) {
	store := newMemNodeStore()
	r1 := NewRegistry(0, 0)
	if err := r1.SetStore(store); err != nil {
		t.Fatal(err)
	}
	if err := r1.RegisterNode("n1", "n1:9002"); err != nil {
		t.Fatal(err)
	}
	r1.SetNodeVersion("n1", "v1.2.3")
	if err := r1.UpsertDisk("n1", "/mnt/a", 1000, 100, 0); err != nil {
		t.Fatal(err)
	}

	// A new Registry stands for the restarted Master.
	r2 := NewRegistry(0, 0)
	if err := r2.SetStore(store); err != nil {
		t.Fatal(err)
	}
	nodes := r2.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("restored %d nodes, want 1", len(nodes))
	}
	got := nodes[0]
	if got.ID != "n1" || got.Addr != "n1:9002" || got.Version != "v1.2.3" {
		t.Fatalf("restored node = %+v", got)
	}
	if got.Status != StatusOffline {
		t.Fatalf("restored node status = %q, want offline", got.Status)
	}
	if r2.OnlineNodeCount() != 0 {
		t.Fatalf("OnlineNodeCount = %d, want 0 before the first heartbeat", r2.OnlineNodeCount())
	}
	disks := r2.ListDisksForNode("n1")
	if len(disks) != 1 || disks[0].Path != "/mnt/a" || disks[0].Capacity != 1000 || disks[0].Used != 100 {
		t.Fatalf("restored disks = %+v", disks)
	}

	// A DataNode that does not register again still comes back with a plain heartbeat.
	if err := r2.Heartbeat("n1"); err != nil {
		t.Fatalf("heartbeat of a restored node: %v", err)
	}
	if r2.OnlineNodeCount() != 1 {
		t.Fatalf("OnlineNodeCount = %d, want 1 after the heartbeat", r2.OnlineNodeCount())
	}
}

func TestRegistryForgetsRemovedDiskAndNode(t *testing.T) {
	store := newMemNodeStore()
	r := NewRegistry(0, 0)
	if err := r.SetStore(store); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterNode("n1", "n1:9002"); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertDisk("n1", "/mnt/a", 1000, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertDisk("n1", "/mnt/b", 1000, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveDisk("n1", "/mnt/a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadNodes(); err != nil {
		t.Fatal(err)
	}
	if len(store.disks) != 1 {
		t.Fatalf("store holds %d disks after RemoveDisk, want 1", len(store.disks))
	}
	if err := r.RemoveNode("n1"); err != nil {
		t.Fatal(err)
	}
	if len(store.nodes) != 0 || len(store.disks) != 0 {
		t.Fatalf("store still holds %d nodes and %d disks after RemoveNode", len(store.nodes), len(store.disks))
	}
}

func TestRegistrySetStoreLoadErrorKeepsRegistryUsable(t *testing.T) {
	store := newMemNodeStore()
	store.loadErr = errors.New("table missing")
	r := NewRegistry(0, 0)
	if err := r.SetStore(store); err == nil {
		t.Fatal("SetStore must report the load error")
	}
	if err := r.RegisterNode("n1", "n1:9002"); err != nil {
		t.Fatal(err)
	}
	if len(store.nodes) != 0 {
		t.Fatal("a store that failed to load must not be attached")
	}
}
