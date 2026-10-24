// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"errors"
	"fmt"
	"math/rand"
	"path"
	"sort"
	"sync"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// Target is a selected storage location.
type Target struct {
	NodeID string
	Path   string
}

// Engine selects storage locations based on registry state, policy and quota.
type Engine struct {
	mu  sync.Mutex // guards rnd: *rand.Rand is not safe for concurrent use
	rnd *rand.Rand
}

// New creates an Engine. If seed is 0, it uses time-based seed.
func New(seed int64) *Engine {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &Engine{rnd: rand.New(rand.NewSource(seed))}
}

// Select chooses replica targets for a new object write.
//
// Params:
//   - reg: cluster registry (used to list online nodes and disks).
//   - policy: desired replication policy (single/dual/triple/ALL).
//   - size: object size in bytes (used for quota estimation).
//
// Returns error if the policy cannot be satisfied (e.g. not enough nodes,
// not enough disks with capacity, or quota exceeded). This maps to S3
// 507 InsufficientStorage at the caller.
//
// Select ignores per-bucket rules; use SelectWith for those.
func (e *Engine) Select(reg *datanode.Registry, policy Policy, size int64) ([]Target, error) {
	return e.SelectWith(reg, policy, size, SelectOptions{})
}

// SelectWith is Select plus the per-bucket constraints in opts:
//   - disks reserved for another bucket are never used,
//   - a non-empty opts.Allow limits the choice to those disks,
//   - opts.Mode decides whether replicas go to different nodes (spread),
//     to different disks of one node (same_node) or to different disks
//     with a preference for different nodes (any).
//
// With the zero SelectOptions it behaves exactly like the original Select.
func (e *Engine) SelectWith(reg *datanode.Registry, policy Policy, size int64, opts SelectOptions) ([]Target, error) {
	if reg == nil {
		return nil, errors.New("registry is nil")
	}
	if !policy.IsValid() {
		return nil, fmt.Errorf("invalid policy %q", policy)
	}
	mode, err := ParseMode(string(opts.Mode))
	if err != nil {
		return nil, err
	}
	if policy == PolicyAll && mode != ModeSpread {
		return nil, fmt.Errorf("policy %q cannot be combined with placement mode %q", policy, mode)
	}
	allow := make(map[DiskRef]bool, len(opts.Allow))
	for _, d := range opts.Allow {
		allow[DiskRef{NodeID: d.NodeID, Path: path.Clean(d.Path)}] = true
	}

	onlineNodes := make(map[string]bool)
	for _, n := range reg.ListNodes() {
		if n.Status == datanode.StatusOnline {
			onlineNodes[n.ID] = true
		}
	}
	// constrained is true when rules removed disks of online nodes, so the number
	// of usable nodes has to be counted from the candidates instead of the cluster.
	constrained := len(allow) > 0
	var candidates []datanode.DiskPath
	for _, d := range reg.ListDisks() {
		if !onlineNodes[d.NodeID] {
			continue
		}
		ref := refOf(d)
		if owner, ok := opts.Reserved[ref]; ok && owner != opts.Bucket {
			constrained = true
			continue
		}
		if len(allow) > 0 && !allow[ref] {
			continue
		}
		if d.Capacity > 0 {
			avail := d.EffectiveAvail()
			if avail < size && size > 0 {
				continue
			}
		}
		candidates = append(candidates, d)
	}
	if len(candidates) == 0 {
		return nil, errors.New("no available storage locations")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if policy == PolicyAll {
		return selectAll(candidates)
	}
	switch mode {
	case ModeSameNode:
		return e.selectSameNode(candidates, policy)
	case ModeAny:
		return e.selectAny(candidates, policy, size)
	case ModeSpread:
		// handled below
	}
	nodeCount := len(onlineNodes)
	if constrained {
		nodeCount = countNodes(candidates)
	}
	need := policy.ReplicaCount(nodeCount)
	if err := policy.ValidateForCluster(nodeCount); err != nil {
		return nil, err
	}
	// Weighted random selection without replacement. Weight = EffectiveAvail (or a
	// large constant if unknown). This spreads load and avoids starving small
	// disks; still respects quota. Distinct nodes are preferred.
	selected := e.weightedSelect(candidates, need, size)
	if len(selected) < need {
		return nil, fmt.Errorf("policy %q requires %d replicas but only %d locations available", policy, need, len(selected))
	}
	return selected, nil
}

// refOf is the cluster-wide identity of a registry disk (path cleaned).
func refOf(d datanode.DiskPath) DiskRef {
	return DiskRef{NodeID: d.NodeID, Path: path.Clean(d.Path)}
}

// selectAll picks one replica per node, taking the path with the most room.
func selectAll(candidates []datanode.DiskPath) ([]Target, error) {
	byNode := make(map[string]datanode.DiskPath)
	for _, d := range candidates {
		cur, ok := byNode[d.NodeID]
		if !ok || d.EffectiveAvail() > cur.EffectiveAvail() {
			byNode[d.NodeID] = d
		}
	}
	if len(byNode) == 0 {
		return nil, errors.New("no online nodes for ALL policy")
	}
	out := make([]Target, 0, len(byNode))
	for _, d := range byNode {
		out = append(out, Target{NodeID: d.NodeID, Path: d.Path})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID == out[j].NodeID {
			return out[i].Path < out[j].Path
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out, nil
}

// selectSameNode puts every replica on one node, each on a different disk.
// The caller holds e.mu.
func (e *Engine) selectSameNode(candidates []datanode.DiskPath, policy Policy) ([]Target, error) {
	need := policy.ReplicaCount(0)
	byNode := make(map[string][]datanode.DiskPath)
	for _, d := range candidates {
		byNode[d.NodeID] = append(byNode[d.NodeID], d)
	}
	var nodes []string
	for id, ds := range byNode {
		if len(ds) >= need {
			nodes = append(nodes, id)
		}
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("policy %q in mode %q needs a node with %d usable disks", policy, ModeSameNode, need)
	}
	sort.Strings(nodes)
	node := e.pickNode(nodes, byNode)
	return e.pickDisks(byNode[node], need), nil
}

// selectAny uses different disks and prefers different nodes, but accepts
// several disks of one node. The caller holds e.mu.
func (e *Engine) selectAny(candidates []datanode.DiskPath, policy Policy, size int64) ([]Target, error) {
	need := policy.ReplicaCount(0)
	if len(candidates) < need {
		return nil, fmt.Errorf("policy %q in mode %q requires %d disks but only %d available", policy, ModeAny, need, len(candidates))
	}
	return e.weightedSelect(candidates, need, size), nil
}

// pickNode chooses one node, weighted by the room its disks have.
func (e *Engine) pickNode(nodes []string, byNode map[string][]datanode.DiskPath) string {
	weights := make([]float64, len(nodes))
	var total float64
	for i, id := range nodes {
		var w float64
		for _, d := range byNode[id] {
			a := d.EffectiveAvail()
			if a <= 0 && d.Capacity == 0 {
				a = 1 << 30 // unknown capacity: treat as large
			}
			w += float64(a)
		}
		if w <= 0 {
			w = 1
		}
		weights[i] = w
		total += w
	}
	r := e.rnd.Float64() * total
	cum := 0.0
	for i, w := range weights {
		cum += w
		if r < cum {
			return nodes[i]
		}
	}
	return nodes[len(nodes)-1]
}

// pickDisks takes n distinct disks from pool (weighted by room).
func (e *Engine) pickDisks(pool []datanode.DiskPath, n int) []Target {
	pool = append([]datanode.DiskPath(nil), pool...)
	out := make([]Target, 0, n)
	for len(out) < n && len(pool) > 0 {
		i := e.weightedIndex(pool)
		out = append(out, Target{NodeID: pool[i].NodeID, Path: pool[i].Path})
		pool = removeAt(pool, i)
	}
	return out
}

// weightedSelect picks n distinct disk targets using weighted random (weight = avail).
// It tries to spread across distinct nodes first.
func (e *Engine) weightedSelect(candidates []datanode.DiskPath, need int, _ int64) []Target {
	// Copy to avoid mutating caller slice.
	pool := append([]datanode.DiskPath(nil), candidates...)
	var out []Target
	usedNodes := make(map[string]bool)
	// First pass: prefer distinct nodes.
	for len(out) < need && len(pool) > 0 {
		idx := e.weightedIndex(pool)
		chosen := pool[idx]
		// If we still need more and there exists unused node, skip picks that would duplicate.
		if !usedNodes[chosen.NodeID] || countUnusedNodes(pool, usedNodes) == 0 {
			out = append(out, Target{NodeID: chosen.NodeID, Path: chosen.Path})
			usedNodes[chosen.NodeID] = true
		} else {
			// Second chance: search for any unused node in pool.
			found := -1
			for i, d := range pool {
				if !usedNodes[d.NodeID] {
					found = i
					break
				}
			}
			if found >= 0 {
				out = append(out, Target{NodeID: pool[found].NodeID, Path: pool[found].Path})
				usedNodes[pool[found].NodeID] = true
				pool = removeAt(pool, found)
				continue
			}
			out = append(out, Target{NodeID: chosen.NodeID, Path: chosen.Path})
			usedNodes[chosen.NodeID] = true
		}
		pool = removeAt(pool, idx)
	}
	return out
}

func (e *Engine) weightedIndex(pool []datanode.DiskPath) int {
	if len(pool) == 1 {
		return 0
	}
	weights := make([]float64, len(pool))
	var total float64
	for i, d := range pool {
		w := float64(d.EffectiveAvail())
		if w <= 0 {
			if d.Capacity == 0 {
				w = 1e9 // unknown capacity: treat as large
			} else {
				w = 0
			}
		}
		weights[i] = w
		total += w
	}
	if total == 0 {
		return e.rnd.Intn(len(pool))
	}
	r := e.rnd.Float64() * total
	cum := 0.0
	for i, w := range weights {
		cum += w
		if r < cum {
			return i
		}
	}
	return len(pool) - 1
}

func removeAt(pool []datanode.DiskPath, idx int) []datanode.DiskPath {
	pool[idx] = pool[len(pool)-1]
	return pool[:len(pool)-1]
}

func countUnusedNodes(pool []datanode.DiskPath, used map[string]bool) int {
	seen := make(map[string]bool)
	for _, d := range pool {
		if !used[d.NodeID] && !seen[d.NodeID] {
			seen[d.NodeID] = true
		}
	}
	return len(seen)
}

// countNodes returns the number of distinct nodes among disks.
func countNodes(disks []datanode.DiskPath) int {
	seen := make(map[string]bool)
	for _, d := range disks {
		seen[d.NodeID] = true
	}
	return len(seen)
}
