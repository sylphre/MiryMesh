// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Persist writes the job store snapshot to dir/mirymesh-jobs.json.
// It is used for controller restart recovery (docs/07: interrupted-job recovery).
// M5 will replace this with DB/WAL, but the JSON snapshot is sufficient for M4.
func (s *Store) Persist(dir string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "mirymesh-jobs.json.tmp")
	dst := filepath.Join(dir, "mirymesh-jobs.json")
	jobs := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, clone(j))
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Load restores jobs from dir/mirymesh-jobs.json. Missing file is not an error.
func (s *Store) Load(dir string) error {
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, "mirymesh-jobs.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range jobs {
		if j.ID == "" {
			continue
		}
		s.jobs[j.ID] = clone(j)
		if n := seqFromID(j.ID); n > s.seq {
			s.seq = n
		}
	}
	return nil
}

func seqFromID(id string) int64 {
	// id format: bucket/key#seq
	for i := len(id) - 1; i >= 0; i-- {
		if id[i] == '#' {
			var v int64
			for _, c := range id[i+1:] {
				if c < '0' || c > '9' {
					return 0
				}
				v = v*10 + int64(c-'0')
			}
			return v
		}
	}
	return 0
}
