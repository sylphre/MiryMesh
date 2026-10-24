// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import "sort"

// listEntry is one element of a listing: an object or a CommonPrefix.
type listEntry struct {
	name     string
	isPrefix bool
	obj      ObjectMeta
}

// normalizeMaxKeys applies the same bounds as Storage.ListObjectsV2.
func normalizeMaxKeys(n int) int {
	if n <= 0 || n > 1000 {
		return 1000
	}
	return n
}

// mergeListings merges ListObjectsV2 pages that several sources (the disks of
// one DataNode, or several DataNodes) returned for the SAME prefix, delimiter,
// continuation token and maxKeys.
//
// Each source returns its first maxKeys entries after the token, so the first
// maxKeys entries of the merged listing are always contained in the union of
// those pages. The merge therefore:
//
//   - drops duplicates (the first page wins),
//   - orders entries by UTF-8 byte order, objects and CommonPrefixes together,
//   - keeps the first maxKeys entries,
//   - reports IsTruncated when entries were cut off or when any source still
//     has entries beyond its own page, and
//   - sets NextContinuationToken to the last entry returned, which is what
//     Storage.ListObjectsV2 resumes from (a CommonPrefix skips everything
//     beneath it).
func mergeListings(pages []ListObjectsV2Result, maxKeys int) ListObjectsV2Result {
	maxKeys = normalizeMaxKeys(maxKeys)

	type entryID struct {
		name     string
		isPrefix bool
	}
	seen := make(map[entryID]struct{})
	var entries []listEntry
	truncated := false
	for _, p := range pages {
		if p.IsTruncated {
			truncated = true
		}
		for _, o := range p.Objects {
			id := entryID{name: o.Key}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			entries = append(entries, listEntry{name: o.Key, obj: o})
		}
		for _, cp := range p.CommonPrefixes {
			id := entryID{name: cp, isPrefix: true}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			entries = append(entries, listEntry{name: cp, isPrefix: true})
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].name != entries[j].name {
			return entries[i].name < entries[j].name
		}
		return !entries[i].isPrefix && entries[j].isPrefix
	})
	if len(entries) > maxKeys {
		entries = entries[:maxKeys]
		truncated = true
	}

	var out ListObjectsV2Result
	for _, e := range entries {
		if e.isPrefix {
			out.CommonPrefixes = append(out.CommonPrefixes, e.name)
		} else {
			out.Objects = append(out.Objects, e.obj)
		}
	}
	out.KeyCount = len(entries)
	out.IsTruncated = truncated
	if truncated && len(entries) > 0 {
		out.NextContinuationToken = entries[len(entries)-1].name
	}
	return out
}
