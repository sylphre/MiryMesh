// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"os"
	"time"
)

// BucketCreationDate returns when bucket was created. Buckets created by this
// process report the exact creation time; buckets discovered on disk after a
// restart fall back to the bucket directory's modification time.
func (s *Storage) BucketCreationDate(bucket string) (time.Time, bool) {
	s.mu.RLock()
	t, recorded := s.created[bucket]
	_, exists := s.buckets[bucket]
	s.mu.RUnlock()
	if recorded {
		return t, true
	}
	if !exists {
		return time.Time{}, false
	}
	fi, err := os.Stat(s.bucketDir(bucket))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime().UTC(), true
}
