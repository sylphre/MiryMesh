// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListObjectsV2 implements S3 ListObjectsV2 semantics.
//
//   - Keys are the union of the in-memory meta cache and the files on disk, so
//     objects remain visible after a restart.
//   - Keys are ordered by UTF-8 byte order, as S3 specifies.
//   - The continuation token is the last entry returned (an object key or a
//     CommonPrefix). Resuming from a CommonPrefix skips every key under it, so
//     a prefix is never returned twice.
//   - KeyCount and max-keys count objects and CommonPrefixes together.
func (s *Storage) ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (ListObjectsV2Result, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return ListObjectsV2Result{}, err
	}
	if maxKeys <= 0 || maxKeys > 1000 {
		maxKeys = 1000
	}

	keys := s.collectKeys(bucket, prefix)
	sort.Strings(keys)
	start := listStart(keys, delimiter, continuationToken)

	var (
		objects    []ObjectMeta
		prefixes   []string
		count      int
		lastEntry  string
		lastPrefix string
		truncated  bool
	)
	for _, k := range keys[start:] {
		if delimiter != "" {
			// k is guaranteed to start with prefix (filtered in collectKeys).
			rest := k[len(prefix):]
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := k[:len(prefix)+idx+len(delimiter)]
				if cp == lastPrefix {
					continue // keys of one prefix are contiguous after sorting
				}
				if count >= maxKeys {
					truncated = true
					break
				}
				lastPrefix = cp
				prefixes = append(prefixes, cp)
				lastEntry = cp
				count++
				continue
			}
		}
		if count >= maxKeys {
			truncated = true
			break
		}
		m, ok := s.statObject(bucket, k)
		if !ok {
			continue // removed concurrently
		}
		objects = append(objects, m)
		lastEntry = k
		count++
	}

	res := ListObjectsV2Result{
		Objects:        objects,
		CommonPrefixes: prefixes,
		IsTruncated:    truncated,
		KeyCount:       count,
	}
	if truncated {
		res.NextContinuationToken = lastEntry
	}
	return res, nil
}

// collectKeys returns the unsorted keys of bucket that start with prefix.
func (s *Storage) collectKeys(bucket, prefix string) []string {
	set := make(map[string]struct{})
	s.mu.RLock()
	for k := range s.meta[bucket] {
		set[k] = struct{}{}
	}
	s.mu.RUnlock()

	root := s.bucketDir(bucket)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil // unreadable entry: skip, keep listing
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if _, known := set[key]; known {
			return nil
		}
		// In-flight PutObject temp files are not objects.
		if strings.Contains(d.Name(), ".tmp-") {
			return nil
		}
		set[key] = struct{}{}
		return nil
	})

	keys := make([]string, 0, len(set))
	for k := range set {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys
}

// listStart returns the index of the first key to emit after token.
func listStart(keys []string, delimiter, token string) int {
	if token == "" {
		return 0
	}
	if delimiter != "" && strings.HasSuffix(token, delimiter) {
		// token is a CommonPrefix: skip everything beneath it.
		i := sort.SearchStrings(keys, token)
		for i < len(keys) && strings.HasPrefix(keys[i], token) {
			i++
		}
		return i
	}
	return sort.Search(len(keys), func(i int) bool { return keys[i] > token })
}

// statObject returns cached metadata, or rebuilds it from the file on disk
// (and caches it) when the object was written before the last restart.
func (s *Storage) statObject(bucket, key string) (ObjectMeta, bool) {
	s.mu.RLock()
	m, ok := s.meta[bucket][key]
	s.mu.RUnlock()
	if ok {
		return m, true
	}
	p := s.objectPath(bucket, key)
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return ObjectMeta{}, false
	}
	f, err := os.Open(p)
	if err != nil {
		return ObjectMeta{}, false
	}
	h := md5.New()
	n, err := io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		return ObjectMeta{}, false
	}
	m = ObjectMeta{
		Key:         key,
		Size:        n,
		ETag:        hex.EncodeToString(h.Sum(nil)),
		ContentType: "application/octet-stream",
		LastMod:     fi.ModTime().UTC(),
		Meta:        map[string]string{},
	}
	s.mu.Lock()
	if s.meta[bucket] == nil {
		s.meta[bucket] = make(map[string]ObjectMeta)
	}
	s.meta[bucket][key] = m
	s.mu.Unlock()
	return m, true
}
