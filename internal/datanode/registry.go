// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// NodeStatus is the node's membership state.
type NodeStatus string

const (
	StatusOnline  NodeStatus = "online"
	StatusOffline NodeStatus = "offline"
)

// Node represents a registered data node.
type Node struct {
	ID       string
	Addr     string
	Status   NodeStatus
	LastSeen time.Time
	Version  string // release version the node reported when it registered ("" = unknown)
}

// DiskPath represents a storage path on a data node.
type DiskPath struct {
	NodeID   string
	Path     string
	Capacity int64 // total bytes (0 = unknown)
	Used     int64 // used bytes (0 = unknown)
	Limit    int64 // quota limit bytes (0 = 95% of capacity)
	Avail    int64 // Limit - Used (derived)
}

// EffectiveLimit returns the quota limit: explicit Limit or 95% of Capacity.
func (d DiskPath) EffectiveLimit() int64 {
	if d.Limit > 0 {
		return d.Limit
	}
	if d.Capacity > 0 {
		return d.Capacity * 95 / 100
	}
	return 0
}

// EffectiveAvail returns bytes available under quota.
func (d DiskPath) EffectiveAvail() int64 {
	lim := d.EffectiveLimit()
	if lim == 0 {
		return 0
	}
	a := lim - d.Used
	if a < 0 {
		return 0
	}
	return a
}

// Registry holds nodes, disk paths and heartbeats. In M2 it is in-memory;
// later it will be backed by metadata DB.
type Registry struct {
	mu        sync.RWMutex
	nodes     map[string]*Node
	disks     map[string]map[string]*DiskPath // nodeID -> path -> DiskPath
	heartbeat time.Duration
	timeout   time.Duration
	tokenSrc  func(nodeID string) (string, bool) // per-node token lookup (Master side, optional)
}

// NewRegistry creates a registry. Heartbeat interval and offline timeout are
// configurable; zero values pick defaults (30s / 90s).
func NewRegistry(heartbeat, timeout time.Duration) *Registry {
	if heartbeat == 0 {
		heartbeat = 30 * time.Second
	}
	if timeout == 0 {
		timeout = 90 * time.Second
	}
	return &Registry{
		nodes:     make(map[string]*Node),
		disks:     make(map[string]map[string]*DiskPath),
		heartbeat: heartbeat,
		timeout:   timeout,
	}
}

// RegisterNode creates or updates a node. ID and Addr must be non-empty.
func (r *Registry) RegisterNode(id, addr string) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(addr) == "" {
		return errors.New("node id and addr must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		n = &Node{ID: id}
		r.nodes[id] = n
	}
	n.Addr = addr
	n.Status = StatusOnline
	n.LastSeen = time.Now()
	return nil
}

// Heartbeat marks a node as alive.
func (r *Registry) Heartbeat(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return errors.New("unknown node")
	}
	n.LastSeen = time.Now()
	n.Status = StatusOnline
	return nil
}

// SetNodeVersion records the release version a node reported. Empty values are ignored,
// so older DataNodes that do not send a version keep showing as unknown.
func (r *Registry) SetNodeVersion(id, ver string) {
	ver = strings.TrimSpace(ver)
	if ver == "" {
		return
	}
	if len(ver) > 64 {
		ver = ver[:64]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if n, ok := r.nodes[id]; ok {
		n.Version = ver
	}
}

// MarkOfflineIfStale marks nodes offline whose LastSeen exceeds timeout.
// Returns IDs of newly offlined nodes.
func (r *Registry) MarkOfflineIfStale(now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, n := range r.nodes {
		if n.Status == StatusOnline && now.Sub(n.LastSeen) > r.timeout {
			n.Status = StatusOffline
			out = append(out, n.ID)
		}
	}
	sort.Strings(out)
	return out
}

// UpsertDisk adds or updates a disk path for a node.
// Capacity may be 0 if unknown; limit 0 means default 95%.
func (r *Registry) UpsertDisk(nodeID, path string, capacity, used, limit int64) error {
	if strings.TrimSpace(nodeID) == "" || strings.TrimSpace(path) == "" {
		return errors.New("nodeID and path must not be empty")
	}
	if capacity < 0 || used < 0 || limit < 0 {
		return errors.New("capacity/used/limit must be non-negative")
	}
	if limit > 0 && capacity > 0 && limit > capacity {
		return errors.New("limit must not exceed capacity")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[nodeID]; !ok {
		return errors.New("unknown node")
	}
	if r.disks[nodeID] == nil {
		r.disks[nodeID] = make(map[string]*DiskPath)
	}
	dp, ok := r.disks[nodeID][path]
	if !ok {
		dp = &DiskPath{NodeID: nodeID, Path: path}
		r.disks[nodeID][path] = dp
	}
	dp.Capacity = capacity
	dp.Used = used
	dp.Limit = limit
	return nil
}

// RemoveDisk removes a disk path.
func (r *Registry) RemoveDisk(nodeID, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.disks[nodeID]
	if !ok {
		return errors.New("disk not found")
	}
	if _, ok := m[path]; !ok {
		return errors.New("disk not found")
	}
	delete(m, path)
	return nil
}

// RemoveNode removes a node and its disks.
func (r *Registry) RemoveNode(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return errors.New("unknown node")
	}
	delete(r.nodes, id)
	delete(r.disks, id)
	return nil
}

// ListNodes returns a snapshot of nodes.
func (r *Registry) ListNodes() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// NodeCount returns number of registered nodes.
func (r *Registry) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// OnlineNodeCount returns number of online nodes.
func (r *Registry) OnlineNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c := 0
	for _, n := range r.nodes {
		if n.Status == StatusOnline {
			c++
		}
	}
	return c
}

// ListDisks returns snapshot of all disks.
func (r *Registry) ListDisks() []DiskPath {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []DiskPath
	for _, m := range r.disks {
		for _, dp := range m {
			out = append(out, *dp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID == out[j].NodeID {
			return out[i].Path < out[j].Path
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

// ListDisksForNode returns disks for a node.
func (r *Registry) ListDisksForNode(nodeID string) []DiskPath {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.disks[nodeID]
	if !ok {
		return nil
	}
	out := make([]DiskPath, 0, len(m))
	for _, dp := range m {
		out = append(out, *dp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// HeartbeatInterval returns the configured heartbeat interval.
func (r *Registry) HeartbeatInterval() time.Duration { return r.heartbeat }

// OfflineTimeout returns the configured offline timeout.
func (r *Registry) OfflineTimeout() time.Duration { return r.timeout }
