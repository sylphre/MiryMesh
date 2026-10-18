// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import (
	"errors"
	"sync"
	"time"
)

// JobStatus is the replication job state.
type JobStatus string

const (
	StatusPending  JobStatus = "pending"
	StatusRunning  JobStatus = "running"
	StatusVerified JobStatus = "verified"
	StatusFailed   JobStatus = "failed"
	StatusStale    JobStatus = "stale"
	StatusMissing  JobStatus = "missing"
)

// Job represents a replication/repair task. In M4 it is in-memory with
// persistence hook for future DB backing (M5 evacuation).
type Job struct {
	ID         string
	Bucket     string
	Key        string
	Size       int64
	Checksum   string // expected SHA256 hex
	Targets    []string
	Status     JobStatus
	Attempts   int
	MaxRetries int
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Store holds jobs. In M4 it is in-memory; M5 will persist to DB/WAL.
type Store struct {
	mu   sync.RWMutex
	jobs map[string]*Job
	seq  int64
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{jobs: make(map[string]*Job)}
}

// Create inserts a job.
func (s *Store) Create(j *Job) (string, error) {
	if j == nil || j.Bucket == "" || j.Key == "" {
		return "", errors.New("bucket and key required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := j.ID
	if id == "" {
		id = newID(s.seq, j.Bucket, j.Key)
	}
	j.ID = id
	if j.Status == "" {
		j.Status = StatusPending
	}
	if j.MaxRetries == 0 {
		j.MaxRetries = 3
	}
	now := time.Now()
	j.CreatedAt = now
	j.UpdatedAt = now
	s.jobs[id] = clone(j)
	return id, nil
}

// Get returns a job by ID.
func (s *Store) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	return clone(j), true
}

// UpdateStatus transitions a job's status.
func (s *Store) UpdateStatus(id string, status JobStatus, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return errors.New("job not found")
	}
	j.Status = status
	j.LastError = lastErr
	j.UpdatedAt = time.Now()
	if status == StatusFailed || status == StatusStale {
		j.Attempts++
	}
	return nil
}

// RequeuePending returns IDs of jobs that need retry (failed with attempts < max, or pending).
func (s *Store) RequeuePending() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for id, j := range s.jobs {
		if j.Status == StatusPending || (j.Status == StatusFailed && j.Attempts < j.MaxRetries) {
			out = append(out, id)
		}
	}
	return out
}

// List returns all jobs.
func (s *Store) List() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, clone(j))
	}
	return out
}

// MarkVerified marks a job as verified.
func (s *Store) MarkVerified(id string) error { return s.UpdateStatus(id, StatusVerified, "") }

// MarkFailed marks a job as failed with error.
func (s *Store) MarkFailed(id string, errStr string) error {
	return s.UpdateStatus(id, StatusFailed, errStr)
}

func clone(j *Job) *Job {
	c := *j
	if j.Targets != nil {
		c.Targets = append([]string(nil), j.Targets...)
	}
	return &c
}

func newID(seq int64, bucket, key string) string {
	// Simple deterministic ID; UUID will replace in DB milestone.
	return bucket + "/" + key + "#" + itoa(seq)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [32]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}
