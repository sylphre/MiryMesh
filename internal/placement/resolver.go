// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// ErrConflict is wrapped by errors about a disk that is reserved for, or listed by,
// another bucket. The admin API maps it to 409.
var ErrConflict = errors.New("placement conflict")

// State is what the Resolver keeps besides the replica policies. It is saved as JSON.
type State struct {
	Rules        map[string]Rule `json:"rules,omitempty"`
	Reservations []Reservation   `json:"reservations,omitempty"`
}

// Resolver binds placement policy configuration to a registry/engine.
// It is created by the controller and injected into s3api.Router as
// s3api.PlacementResolver. It is safe for concurrent use.
type Resolver struct {
	mu            sync.RWMutex
	engine        *Engine
	registry      *datanode.Registry
	defaultPolicy Policy
	bucketPolicy  map[string]Policy
	bucketRule    map[string]Rule
	reserved      map[DiskRef]string
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
	return &Resolver{
		engine:        engine,
		registry:      reg,
		defaultPolicy: dp,
		bucketPolicy:  bp,
		bucketRule:    make(map[string]Rule),
		reserved:      make(map[DiskRef]string),
	}, nil
}

// Resolve selects targets for bucket, applying bucket-specific or default policy
// and the bucket's placement rule.
func (r *Resolver) Resolve(bucket string, size int64) ([]Target, error) {
	r.mu.RLock()
	policy := r.policyForLocked(bucket)
	opts := r.optionsForLocked(bucket)
	r.mu.RUnlock()
	return r.engine.SelectWith(r.registry, policy, size, opts)
}

// PolicyFor returns the effective policy for a bucket (for introspection/tests).
func (r *Resolver) PolicyFor(bucket string) Policy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policyForLocked(bucket)
}

func (r *Resolver) policyForLocked(bucket string) Policy {
	if p, ok := r.bucketPolicy[bucket]; ok {
		return p
	}
	return r.defaultPolicy
}

// SetBucketPolicy updates a bucket's policy at runtime. It fails when the bucket's
// placement rule cannot satisfy the new policy.
func (r *Resolver) SetBucketPolicy(bucket string, policy Policy) error {
	if !policy.IsValid() {
		return errInvalidPolicy
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule, ok := r.bucketRule[bucket]; ok {
		if err := rule.CheckFeasible(policy); err != nil {
			return err
		}
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
	r.mu.Lock()
	r.defaultPolicy = policy
	r.mu.Unlock()
	return nil
}

// DefaultPolicy returns the current default policy.
func (r *Resolver) DefaultPolicy() Policy {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultPolicy
}

// RuleFor returns the placement rule of a bucket. A bucket without a rule gets
// the default one (spread, any disk).
func (r *Resolver) RuleFor(bucket string) Rule {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.bucketRule[bucket]
	if !ok {
		return Rule{Mode: ModeSpread}
	}
	rule.Disks = append([]DiskRef(nil), rule.Disks...)
	return rule
}

// SetBucketRule validates and stores the rule of a bucket and returns the cleaned
// rule. A default rule removes the entry. It fails with ErrConflict when the
// allow-list names a disk reserved for another bucket.
func (r *Resolver) SetBucketRule(bucket string, rule Rule) (Rule, error) {
	if strings.TrimSpace(bucket) == "" {
		return Rule{}, errors.New("bucket must not be empty")
	}
	nr, err := rule.Normalize()
	if err != nil {
		return Rule{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ferr := nr.CheckFeasible(r.policyForLocked(bucket)); ferr != nil {
		return Rule{}, ferr
	}
	for _, d := range nr.Disks {
		if owner, ok := r.reserved[d]; ok && owner != bucket {
			return Rule{}, fmt.Errorf("disk %s:%s is reserved for bucket %q: %w", d.NodeID, d.Path, owner, ErrConflict)
		}
	}
	if nr.IsDefault() {
		delete(r.bucketRule, bucket)
	} else {
		r.bucketRule[bucket] = nr
	}
	return nr, nil
}

// OptionsFor returns the engine options for a bucket: its mode, its allow-list and
// the disks reserved for buckets.
func (r *Resolver) OptionsFor(bucket string) SelectOptions {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.optionsForLocked(bucket)
}

func (r *Resolver) optionsForLocked(bucket string) SelectOptions {
	opts := SelectOptions{Bucket: bucket, Mode: ModeSpread}
	if rule, ok := r.bucketRule[bucket]; ok {
		opts.Mode = rule.Mode
		opts.Allow = append([]DiskRef(nil), rule.Disks...)
	}
	if len(r.reserved) > 0 {
		opts.Reserved = make(map[DiskRef]string, len(r.reserved))
		for k, v := range r.reserved {
			opts.Reserved[k] = v
		}
	}
	return opts
}

// Reserve dedicates a disk to bucket. It fails with ErrConflict when the disk is
// already reserved for another bucket or is in another bucket's allow-list.
func (r *Resolver) Reserve(bucket string, disk DiskRef) error {
	if strings.TrimSpace(bucket) == "" {
		return errors.New("bucket must not be empty")
	}
	nd, err := disk.Normalize()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if owner, ok := r.reserved[nd]; ok && owner != bucket {
		return fmt.Errorf("disk %s:%s is already reserved for bucket %q: %w", nd.NodeID, nd.Path, owner, ErrConflict)
	}
	for b, rule := range r.bucketRule {
		if b == bucket {
			continue
		}
		for _, d := range rule.Disks {
			if d == nd {
				return fmt.Errorf("disk %s:%s is in the allow-list of bucket %q: %w", nd.NodeID, nd.Path, b, ErrConflict)
			}
		}
	}
	r.reserved[nd] = bucket
	return nil
}

// Release removes a reservation. It reports whether there was one.
func (r *Resolver) Release(disk DiskRef) bool {
	nd, err := disk.Normalize()
	if err != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.reserved[nd]; !ok {
		return false
	}
	delete(r.reserved, nd)
	return true
}

// Reservations lists all reservations sorted by bucket, node and path.
func (r *Resolver) Reservations() []Reservation {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.reservationsLocked()
}

func (r *Resolver) reservationsLocked() []Reservation {
	out := make([]Reservation, 0, len(r.reserved))
	for d, b := range r.reserved {
		out = append(out, Reservation{NodeID: d.NodeID, Path: d.Path, Bucket: b})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bucket != out[j].Bucket {
			return out[i].Bucket < out[j].Bucket
		}
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// ForgetBucket drops the rule, the policy override and the reservations of a deleted bucket.
func (r *Resolver) ForgetBucket(bucket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.bucketRule, bucket)
	delete(r.bucketPolicy, bucket)
	for d, b := range r.reserved {
		if b == bucket {
			delete(r.reserved, d)
		}
	}
}

// ExportState returns the rules and reservations for saving.
func (r *Resolver) ExportState() State {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := State{Reservations: r.reservationsLocked()}
	if len(r.bucketRule) > 0 {
		st.Rules = make(map[string]Rule, len(r.bucketRule))
		for b, rule := range r.bucketRule {
			rule.Disks = append([]DiskRef(nil), rule.Disks...)
			st.Rules[b] = rule
		}
	}
	return st
}

// ImportState replaces the rules and reservations with a saved state. Entries that no
// longer validate are skipped; their number is returned. Disks are not checked against
// the registry, because nodes register after the controller starts.
func (r *Resolver) ImportState(s State) (skipped int) {
	rules := make(map[string]Rule, len(s.Rules))
	for b, rule := range s.Rules {
		nr, err := rule.Normalize()
		if err != nil || strings.TrimSpace(b) == "" {
			skipped++
			continue
		}
		if !nr.IsDefault() {
			rules[b] = nr
		}
	}
	reserved := make(map[DiskRef]string, len(s.Reservations))
	for _, rv := range s.Reservations {
		d, err := rv.Disk().Normalize()
		if err != nil || strings.TrimSpace(rv.Bucket) == "" {
			skipped++
			continue
		}
		reserved[d] = rv.Bucket
	}
	r.mu.Lock()
	r.bucketRule = rules
	r.reserved = reserved
	r.mu.Unlock()
	return skipped
}

var errInvalidPolicy = errPolicyInvalid

// errPolicyInvalid is a sentinel for invalid policy.
var errPolicyInvalid = &policyError{"invalid policy"}

type policyError struct{ msg string }

func (e *policyError) Error() string { return e.msg }
