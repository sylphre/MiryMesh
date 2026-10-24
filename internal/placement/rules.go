// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"fmt"
	"path"
	"strings"
)

// Mode says where the replicas of one object may sit relative to each other.
type Mode string

const (
	// ModeSpread puts every replica on a different node. This is the behavior
	// before per-bucket rules existed and stays the default.
	ModeSpread Mode = "spread"
	// ModeSameNode puts every replica on one node, each on a different disk.
	ModeSameNode Mode = "same_node"
	// ModeAny uses different disks and prefers different nodes, but accepts
	// several disks of one node when there are not enough nodes.
	ModeAny Mode = "any"
)

// ParseMode parses a mode name. An empty string means ModeSpread.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(s))); m {
	case "", ModeSpread:
		return ModeSpread, nil
	case ModeSameNode, ModeAny:
		return m, nil
	default:
		return "", fmt.Errorf("unknown placement mode %q (spread, same_node, any)", s)
	}
}

// DiskRef names one disk (a DataNode storage path) in the cluster.
type DiskRef struct {
	NodeID string `json:"node_id"`
	Path   string `json:"path"`
}

// Normalize trims the fields and cleans the path so "/mnt/a/" and "/mnt/a" are the same disk.
func (d DiskRef) Normalize() (DiskRef, error) {
	d.NodeID = strings.TrimSpace(d.NodeID)
	p := strings.TrimSpace(d.Path)
	if d.NodeID == "" || p == "" {
		return DiskRef{}, fmt.Errorf("a disk needs both node_id and path")
	}
	d.Path = path.Clean(p)
	return d, nil
}

// Rule is the placement rule of one bucket, on top of its replica policy.
//
// Disks is an allow-list: when it is not empty, the bucket only writes to
// those disks. The zero Rule is the old behavior (spread, any disk).
type Rule struct {
	Mode  Mode      `json:"mode"`
	Disks []DiskRef `json:"disks,omitempty"`
}

// IsDefault reports whether the rule changes nothing.
func (r Rule) IsDefault() bool {
	return (r.Mode == "" || r.Mode == ModeSpread) && len(r.Disks) == 0
}

// Normalize validates the rule and returns a cleaned copy (default mode filled in,
// duplicate disks removed, order kept).
func (r Rule) Normalize() (Rule, error) {
	mode, err := ParseMode(string(r.Mode))
	if err != nil {
		return Rule{}, err
	}
	out := Rule{Mode: mode}
	seen := make(map[DiskRef]bool, len(r.Disks))
	for _, d := range r.Disks {
		nd, err := d.Normalize()
		if err != nil {
			return Rule{}, err
		}
		if seen[nd] {
			continue
		}
		seen[nd] = true
		out.Disks = append(out.Disks, nd)
	}
	return out, nil
}

// Reservation dedicates one disk to one bucket: no other bucket may write to it.
type Reservation struct {
	NodeID string `json:"node_id"`
	Path   string `json:"path"`
	Bucket string `json:"bucket"`
}

// Disk returns the reserved disk.
func (r Reservation) Disk() DiskRef { return DiskRef{NodeID: r.NodeID, Path: r.Path} }

// SelectOptions carries the per-bucket constraints into Engine.SelectWith.
type SelectOptions struct {
	// Bucket is the bucket being written. It decides which reserved disks are usable.
	Bucket string
	// Mode is the replica layout. Empty means ModeSpread.
	Mode Mode
	// Allow restricts the choice to these disks. Empty means every disk.
	Allow []DiskRef
	// Reserved maps a disk to the only bucket allowed to use it.
	Reserved map[DiskRef]string
}
