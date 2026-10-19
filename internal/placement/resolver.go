// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"strings"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// Resolver binds placement policy configuration to a registry/engine.
// It is created by the controller and injected into s3api.Router as
// s3api.PlacementResolver.
type Resolver struct {
	engine        *Engine
	registry      *datanode.Registry
	defaultPolicy Policy
	bucketPolicy  map[string]Policy
}

// NewResolver creates a Resolver.
func NewResolver(reg *datanode.Registry, engine *Engine, defaultPolicy string, bucketPolicy map[string]string) (*Resolver, error) {
	dp := PolicySingle
	if strings.TrimSpace(defaultPolicy) != "" {
		var err error
		dp, err = ParsePolicy(defaultPolicy)
		if err != nil {
			return nil, err
		}
	}
	bp := make(map[string]Policy, len(bucketPolicy))
	for k, v := range bucketPolicy {
		p, err := ParsePolicy(v)
		if err != nil {
			return nil, err
		}
		bp[k] = p
	}
	if engine == nil {
		engine = New(0)
	}
	return &Resolver{engine: engine, registry: reg, defaultPolicy: dp, bucketPolicy: bp}, nil
}

// Resolve selects targets for bucket, applying bucket-specific or default policy.
func (r *Resolver) Resolve(bucket string, size int64) ([]Target, error) {
	policy := r.defaultPolicy
	if p, ok := r.bucketPolicy[bucket]; ok {
		policy = p
	}
	return r.engine.Select(r.registry, policy, size)
}

// PolicyFor returns the effective policy for a bucket (for introspection/tests).
func (r *Resolver) PolicyFor(bucket string) Policy {
	if p, ok := r.bucketPolicy[bucket]; ok {
		return p
	}
	return r.defaultPolicy
}

// SetBucketPolicy updates a bucket's policy at runtime (future GUI path).
// TODO: GUI will call this via management API; wire to persistence later.
func (r *Resolver) SetBucketPolicy(bucket string, policy Policy) error {
	if !policy.IsValid() {
		return errInvalidPolicy
	}
	if r.bucketPolicy == nil {
		r.bucketPolicy = make(map[string]Policy)
	}
	r.bucketPolicy[bucket] = policy
	return nil
}

// SetDefaultPolicy updates the default placement policy at runtime.
func (r *Resolver) SetDefaultPolicy(policy Policy) error {
	if !policy.IsValid() {
		return errInvalidPolicy
	}
	r.defaultPolicy = policy
	return nil
}

// DefaultPolicy returns the current default policy.
func (r *Resolver) DefaultPolicy() Policy { return r.defaultPolicy }

var errInvalidPolicy = errPolicyInvalid

// errPolicyInvalid is a sentinel for invalid policy.
var errPolicyInvalid = &policyError{"invalid policy"}

type policyError struct{ msg string }

func (e *policyError) Error() string { return e.msg }
