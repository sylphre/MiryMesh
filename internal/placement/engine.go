// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
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
func (e *Engine) Select(reg *datanode.Registry, policy Policy, size int64) ([]Target, error) {
	if reg == nil {
		return nil, errors.New("registry is nil")
	}
	if !policy.IsValid() {
		return nil, fmt.Errorf("invalid policy %q", policy)
	}
	allDisks := reg.ListDisks()
	// Filter to online nodes only and with quota available for size.
	var candidates []datanode.DiskPath
	onlineNodes := make(map[string]bool)
	for _, n := range reg.ListNodes() {
		if n.Status == datanode.StatusOnline {
			onlineNodes[n.ID] = true
		}
	}
	for _, d := range allDisks {
		if !onlineNodes[d.NodeID] {
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
	// For ALL: select one replica per node, picking best (largest avail) path per node.
	if policy == PolicyAll {
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
		var out []Target
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
	need := policy.ReplicaCount(len(onlineNodes))
	if err := policy.ValidateForCluster(len(onlineNodes)); err != nil {
		return nil, err
	}
	// For other policies: weighted random selection without replacement.
	// Weight = EffectiveAvail (or large constant if unknown). This spreads load
	// and avoids starving small disks; still respects quota.
	// For dual/triple on same node we need distinct disks if possible; for
	// now we prefer distinct nodes but allow same node if not enough nodes.
	// (M3 spec: separate disks preferred, separate nodes for durability.)
	selected := e.weightedSelect(candidates, need, size)
	if len(selected) < need {
		return nil, fmt.Errorf("policy %q requires %d replicas but only %d locations available", policy, need, len(selected))
	}
	return selected, nil
}

// weightedSelect picks n distinct disk targets using weighted random (weight = avail).
// It tries to spread across distinct nodes first.
func (e *Engine) weightedSelect(candidates []datanode.DiskPath, need int, _ int64) []Target {
	// Copy to avoid mutating caller slice.
	pool := append([]datanode.DiskPath(nil), candidates...)
	// Shuffle with weight: create weighted list then sample.
	// Simpler: sort by avail descending, then iterate with random choice among top.
	// For M3 we implement proper weighted random without replacement.
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
			// Try to find unused node candidate for next pick; remove chosen and retry.
			// Instead do second chance: search for any unused node in pool.
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
