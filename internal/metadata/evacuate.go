// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FanOut pushes the snapshot to each datanode's /internal/evacuate endpoint.
// TODO(M10+): parallelize with errgroup.Group + per-node retry, and respect context cancellation.
func (e *Evacuator) FanOut(snap *EvacuationSnapshot, addrs map[string]string, token string) map[string]error {
	errs := make(map[string]error)
	if snap == nil {
		for id := range addrs {
			errs[id] = errors.New("snapshot is nil")
		}
		return errs
	}
	data, err := json.Marshal(snap)
	if err != nil {
		for id := range addrs {
			errs[id] = err
		}
		return errs
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for id, base := range addrs {
		url := fmt.Sprintf("%s/internal/evacuate", strings.TrimRight(base, "/"))
		// Wrap with token envelope
		payload := map[string]any{"token": token, "snapshot": json.RawMessage(data)}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest("POST", url, bytes.NewReader(body))
		if err != nil {
			errs[id] = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			errs[id] = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			errs[id] = fmt.Errorf("status %d", resp.StatusCode)
		}
	}
	return errs
}

// EvacuationSnapshot is the restart-required metadata that the controller
// evacuates to data nodes for master takeover recovery (docs/07).
// In M5 it is written to local dir and each data node's evacuation path
// via the datanode's local file; later it will be pushed via HTTP.
type EvacuationSnapshot struct {
	Version   int       `json:"version"`
	Timestamp time.Time `json:"timestamp"`
	Buckets   []string  `json:"buckets"`
	// Objects is a flat list of object keys per bucket (simplified for M5;
	// Includes bucket listing and minimal object entries.
	Objects []ObjectRecord `json:"objects"`
	// PlacementPolicies captures bucket->policy for recovery.
	PlacementPolicies map[string]string `json:"placement_policies,omitempty"`
	// AuthIdentities captures AccessKey identity list for presence check
	// (secret hashes are not evacuated; new controller must re-seed secrets).
	// Value is existence marker (e.g. "present").
	AuthIdentities map[string]string `json:"auth_identities,omitempty"`
}

// ObjectRecord is a minimal object entry for evacuation.
type ObjectRecord struct {
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ETag        string `json:"etag"`
	ContentType string `json:"content_type"`
}

// Evacuator manages snapshot persistence and fan-out.
type Evacuator struct {
	mu      sync.Mutex
	dir     string // local dir for snapshots (controller side)
	version int
}

// NewEvacuator creates an evacuator rooted at dir.
func NewEvacuator(dir string) (*Evacuator, error) {
	if dir == "" {
		return nil, errors.New("evacuator dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &Evacuator{dir: abs}, nil
}

// Dir returns the evacuation dir.
func (e *Evacuator) Dir() string { return e.dir }

// SnapshotPath returns the primary snapshot file path.
func (e *Evacuator) SnapshotPath() string { return filepath.Join(e.dir, "mirymesh-evacuation.json") }

// Save writes a snapshot atomically and bumps version.
func (e *Evacuator) Save(snap *EvacuationSnapshot) error {
	if snap == nil {
		return errors.New("snapshot is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.version++
	snap.Version = e.version
	snap.Timestamp = time.Now().UTC()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.SnapshotPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, e.SnapshotPath())
}

// Load reads the latest snapshot. Returns error if file missing.
func (e *Evacuator) Load() (*EvacuationSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	data, err := os.ReadFile(e.SnapshotPath())
	if err != nil {
		return nil, err
	}
	var snap EvacuationSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if snap.Version > e.version {
		e.version = snap.Version
	}
	return &snap, nil
}

// LoadAny scans local dir and any additional evacuation paths for the newest snapshot.
// It returns the snapshot with the highest Version among all sources.
func LoadAny(localDir string, remoteDirs []string) (*EvacuationSnapshot, error) {
	paths := []string{filepath.Join(localDir, "mirymesh-evacuation.json")}
	for _, d := range remoteDirs {
		paths = append(paths, filepath.Join(d, "mirymesh-evacuation.json"))
	}
	var best *EvacuationSnapshot
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var snap EvacuationSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			continue
		}
		if best == nil || snap.Version > best.Version || (snap.Version == best.Version && snap.Timestamp.After(best.Timestamp)) {
			c := snap
			best = &c
		}
	}
	if best == nil {
		return nil, errors.New("no evacuation snapshot found in any location")
	}
	return best, nil
}
