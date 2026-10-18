// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"fmt"
	"strings"
)

// Policy is the replication placement policy.
type Policy string

const (
	PolicySingle Policy = "single"
	PolicyDual   Policy = "dual"
	PolicyTriple Policy = "triple"
	PolicyAll    Policy = "ALL"
)

var allPolicies = []Policy{PolicySingle, PolicyDual, PolicyTriple, PolicyAll}

// ParsePolicy parses a policy string (case-insensitive).
func ParsePolicy(s string) (Policy, error) {
	norm := strings.TrimSpace(strings.ToLower(s))
	switch norm {
	case "single":
		return PolicySingle, nil
	case "dual":
		return PolicyDual, nil
	case "triple":
		return PolicyTriple, nil
	case "all":
		return PolicyAll, nil
	default:
		return "", fmt.Errorf("policy %q must be one of single/dual/triple/ALL", s)
	}
}

// IsValid reports whether p is a known policy.
func (p Policy) IsValid() bool {
	switch p {
	case PolicySingle, PolicyDual, PolicyTriple, PolicyAll:
		return true
	default:
		return false
	}
}

// ReplicaCount returns the required replica count for a given policy.
// For ALL it returns available (caller-supplied); for others it is static.
func (p Policy) ReplicaCount(available int) int {
	switch p {
	case PolicySingle:
		return 1
	case PolicyDual:
		return 2
	case PolicyTriple:
		return 3
	case PolicyAll:
		return available
	default:
		return 1
	}
}

// ValidateForCluster checks whether the policy can be satisfied by the current
// cluster size. For ALL, at least one node is required.
func (p Policy) ValidateForCluster(nodeCount int) error {
	need := p.ReplicaCount(nodeCount)
	if need == 0 {
		return fmt.Errorf("policy %q requires at least one node", p)
	}
	if nodeCount < need {
		return fmt.Errorf("policy %q requires %d nodes but only %d available", p, need, nodeCount)
	}
	return nil
}

// String returns the canonical string form.
func (p Policy) String() string {
	if p == PolicyAll {
		return "ALL"
	}
	return string(p)
}

// AllPolicies returns the list of valid policies.
func AllPolicies() []Policy { return append([]Policy(nil), allPolicies...) }
