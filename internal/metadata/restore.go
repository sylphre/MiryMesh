// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Restorer rebuilds controller state from an evacuation snapshot.
// In M5 it restores buckets and object metadata into the local filesystem
// storage (s3api.Storage). Later it will also restore DB state, placement
// policies and auth.
type Restorer struct {
	storageRoot string
}

// NewRestorer creates a Restorer rooted at storageDir.
func NewRestorer(storageRoot string) (*Restorer, error) {
	if storageRoot == "" {
		return nil, errors.New("storage root required")
	}
	abs, err := filepath.Abs(storageRoot)
	if err != nil {
		return nil, err
	}
	return &Restorer{storageRoot: abs}, nil
}

// Restore applies snapshot to storageRoot: creates buckets, validates objects.
// It is idempotent — existing buckets/objects are left as-is.
// Objects whose blob files are missing are reported but not treated as fatal
// (they will be repaired via replication after restore).
func (r *Restorer) Restore(snap *EvacuationSnapshot) (*RestoreResult, error) {
	if snap == nil {
		return nil, errors.New("snapshot is nil")
	}
	res := &RestoreResult{Version: snap.Version}
	// Buckets: ensure directories exist.
	for _, b := range snap.Buckets {
		dir := filepath.Join(r.storageRoot, b)
		if _, err := os.Stat(dir); err == nil {
			res.BucketsRestored++
		} else if os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("bucket %s: %v", b, err))
				continue
			}
			res.BucketsCreated++
		} else {
			res.Errors = append(res.Errors, fmt.Sprintf("bucket %s stat: %v", b, err))
		}
	}
	// Objects: count present vs missing blobs.
	for _, obj := range snap.Objects {
		path := filepath.Join(r.storageRoot, obj.Bucket, filepath.FromSlash(obj.Key))
		if _, err := os.Stat(path); err == nil {
			res.ObjectsPresent++
		} else if os.IsNotExist(err) {
			res.ObjectsMissing++
			res.MissingKeys = append(res.MissingKeys, obj.Bucket+"/"+obj.Key)
		} else {
			res.Errors = append(res.Errors, fmt.Sprintf("obj %s/%s stat: %v", obj.Bucket, obj.Key, err))
		}
	}
	return res, nil
}

// RestoreResult summarizes a restore operation.
type RestoreResult struct {
	Version         int
	BucketsCreated  int
	BucketsRestored int
	ObjectsPresent  int
	ObjectsMissing  int
	MissingKeys     []string
	Errors          []string
}
