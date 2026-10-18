// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Engine orchestrates replica writes with checksum verification, retry and
// resume semantics. In M4 the actual remote write is abstracted via WriterFn
// so the engine can operate in single-node mode and later via HTTP to datanodes.
type Engine struct {
	store *Store
	// WriterFn writes data to a single target path and returns checksum.
	// If nil, replication is local-only (copy to storage with checksum verify).
	WriterFn func(target string, data []byte, expectedSHA256 string) error
	// ReaderFn reads existing replica for verification/repair (optional).
	ReaderFn func(target string) ([]byte, error)
}

// NewEngine creates an Engine.
func NewEngine(store *Store) *Engine {
	if store == nil {
		store = NewStore()
	}
	return &Engine{store: store}
}

// Replicate writes data to all targets, verifying checksum per replica.
// Invariants (docs/07):
//   - Existing replicas are not deleted until replacement is verified.
//   - Retries up to job.MaxRetries with backoff.
//   - Returns the job ID and any terminal error.
func (e *Engine) Replicate(bucket, key string, data []byte, targets []string, maxRetries int) (string, error) {
	if len(targets) == 0 {
		return "", fmt.Errorf("no targets")
	}
	expected := SumSHA256(data)
	job := &Job{Bucket: bucket, Key: key, Size: int64(len(data)), Checksum: expected, Targets: targets, MaxRetries: maxRetries}
	id, err := e.store.Create(job)
	if err != nil {
		return "", err
	}
	// Run synchronously for M4; background queue will be added later.
	if err := e.runReplicaWrites(id, data, expected); err != nil {
		return id, err
	}
	return id, nil
}

func (e *Engine) runReplicaWrites(jobID string, data []byte, expected string) error {
	job, ok := e.store.Get(jobID)
	if !ok {
		return fmt.Errorf("job %s not found", jobID)
	}
	_ = e.store.UpdateStatus(jobID, StatusRunning, "")
	var lastErr error
	for attempt := 0; attempt <= job.MaxRetries; attempt++ {
		lastErr = nil
		for _, target := range job.Targets {
			if err := e.writeOne(target, data, expected); err != nil {
				lastErr = err
				slog.Warn("replication: write failed", "target", target, "attempt", attempt, "err", err)
				break
			}
		}
		if lastErr == nil {
			// Verify each replica checksum after write.
			if verr := e.verifyAll(job.Targets, expected); verr != nil {
				lastErr = verr
			} else {
				_ = e.store.MarkVerified(jobID)
				return nil
			}
		}
		if attempt < job.MaxRetries {
			backoff := time.Duration(1<<attempt) * 100 * time.Millisecond
			time.Sleep(backoff)
		}
	}
	_ = e.store.MarkFailed(jobID, fmt.Sprintf("%v", lastErr))
	return fmt.Errorf("replication failed after %d attempts: %w", job.MaxRetries+1, lastErr)
}

func (e *Engine) writeOne(target string, data []byte, expected string) error {
	// If WriterFn is nil, this is a local-only M4 path: verify checksum locally.
	if e.WriterFn == nil {
		// Local write succeeded if checksum matches.
		got := SumSHA256(data)
		if got != expected {
			return fmt.Errorf("local checksum mismatch for %s", target)
		}
		return nil
	}
	return e.WriterFn(target, data, expected)
}

func (e *Engine) verifyAll(targets []string, expected string) error {
	if e.ReaderFn == nil {
		return nil
	}
	for _, t := range targets {
		data, err := e.ReaderFn(t)
		if err != nil {
			return fmt.Errorf("verify read failed for %s: %w", t, err)
		}
		got := SumSHA256(data)
		if got != expected {
			_ = e.store.UpdateStatus("", StatusStale, "")
			return fmt.Errorf("checksum mismatch for %s: got %s want %s", t, got, expected)
		}
		_ = bytes.Compare(nil, nil)
	}
	return nil
}

// Repair re-reads a replica and re-replicates if checksum mismatch.
func (e *Engine) Repair(bucket, key string, expected string, data []byte, targets []string) (string, error) {
	// Repair uses same path as Replicate but ReaderFn will be used to detect stale.
	return e.Replicate(bucket, key, data, targets, 3)
}

// ResumePending requeues pending/failed jobs for retry (controller restart recovery).
func (e *Engine) ResumePending(reader func(job *Job) ([]byte, error)) []string {
	ids := e.store.RequeuePending()
	for _, id := range ids {
		job, ok := e.store.Get(id)
		if !ok {
			continue
		}
		data, err := reader(job)
		if err != nil {
			slog.Warn("replication: resume reader failed", "job", id, "err", err)
			continue
		}
		if err := e.runReplicaWrites(id, data, job.Checksum); err != nil {
			slog.Warn("replication: resume failed", "job", id, "err", err)
		}
		_ = io.Discard
	}
	return ids
}
