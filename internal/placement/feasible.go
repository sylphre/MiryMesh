// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import "fmt"

// CheckFeasible reports whether policy can ever be satisfied under the rule, looking
// only at the rule itself (mode and allow-list), not at which disks are online.
// A rule without an allow-list is always feasible here: the cluster decides at write time.
func (r Rule) CheckFeasible(policy Policy) error {
	mode := r.Mode
	if mode == "" {
		mode = ModeSpread
	}
	if policy == PolicyAll {
		if mode != ModeSpread {
			return fmt.Errorf("policy %q cannot be combined with placement mode %q", policy, mode)
		}
		return nil
	}
	if len(r.Disks) == 0 {
		return nil
	}
	need := policy.ReplicaCount(0)
	perNode := make(map[string]int)
	for _, d := range r.Disks {
		perNode[d.NodeID]++
	}
	switch mode {
	case ModeSameNode:
		for _, n := range perNode {
			if n >= need {
				return nil
			}
		}
		return fmt.Errorf("policy %q in mode %q needs %d allowed disks on one node", policy, mode, need)
	case ModeAny:
		if len(r.Disks) < need {
			return fmt.Errorf("policy %q needs %d allowed disks but only %d are listed", policy, need, len(r.Disks))
		}
	case ModeSpread:
		if len(perNode) < need {
			return fmt.Errorf("policy %q needs disks on %d different nodes but the allow-list covers %d", policy, need, len(perNode))
		}
	}
	return nil
}
