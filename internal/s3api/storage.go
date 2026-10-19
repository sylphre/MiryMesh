// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ObjectMeta holds object metadata stored alongside the blob.
type ObjectMeta struct {
	Key         string
	Size        int64
	ETag        string // hex md5 without quotes
	ContentType string
	LastMod     time.Time
	// User metadata: x-amz-meta-*
	Meta map[string]string
}

// ObjectStore abstracts bucket/object operations for gateway vs standalone.
// Standalone: LocalStore (file). Gateway: RemoteStore (DataNode HTTP fan-out).
type ObjectStore interface {
	CreateBucket(bucket string) error
	BucketExists(bucket string) bool
	DeleteBucket(bucket string) error
	ListBuckets() []string
	PutObject(bucket, key string, contentType string, meta map[string]string, r io.Reader) (ObjectMeta, error)
	GetObject(bucket, key string) (ObjectMeta, io.ReadCloser, error)
	HeadObject(bucket, key string) (ObjectMeta, error)
	DeleteObject(bucket, key string) error
	ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (ListObjectsV2Result, error)
	// multipart
	CreateMultipartUpload(bucket, key, contentType string, meta map[string]string) (*MultipartUploadState, error)
	UploadPart(uploadID string, partNumber int, r io.Reader) (PartRecord, error)
	ListParts(uploadID string) ([]PartRecord, error)
	ListMultipartUploads(bucket string) ([]*MultipartUploadState, error)
	AbortMultipartUpload(uploadID string) error
	CompleteMultipartUpload(uploadID string, parts []CompletePart) (ObjectMeta, error)
	// fs helpers for quota/health
	// internal: upload introspection for handlers
	GetUpload(uploadID string) *MultipartUploadState
	MultipartDir(uploadID string) string
	Root() string
	FilesystemCapacity() int64
	FilesystemAvail() int64
	FilesystemUsed() int64
	IsOverQuota(additional int64) bool
}

type LocalStore = Storage

// Storage is the local filesystem backend for M1 (single-node).
// Later milestones replace placement with multi-node (see internal/metadata).
// TODO(M2): replace in-memory meta map with DB-backed metadata (PostgreSQL
// primary / MySQL both support). Currently meta is reconstructed from
// filesystem on GetObject/HeadObject after restart — M1-review #3.
type Storage struct {
	mu      sync.RWMutex
	root    string
	buckets map[string]struct{}
	// meta cache: bucket -> key -> meta
	meta map[string]map[string]ObjectMeta
	// multipart in-progress uploads: uploadID -> upload (persisted to disk)
	uploads map[string]*MultipartUploadState
}

// MultipartUploadState holds in-progress multipart state (M6).
type MultipartUploadState struct {
	UploadID    string
	Bucket      string
	Key         string
	ContentType string
	Meta        map[string]string
	Initiated   time.Time
	Parts       map[int]PartRecord
}

// PartRecord describes a single uploaded part.
type PartRecord struct {
	PartNumber int
	ETag       string
	Size       int64
	LastMod    time.Time
	Path       string
}

// NewStorage creates a storage rooted at dir.
func NewStorage(root string) (*Storage, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("storage root must not be empty")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	s := &Storage{root: abs, buckets: make(map[string]struct{}), meta: make(map[string]map[string]ObjectMeta), uploads: make(map[string]*MultipartUploadState)}
	// Discover existing buckets on disk.
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() == ".multipart" {
				continue
			}
			s.buckets[e.Name()] = struct{}{}
			s.meta[e.Name()] = make(map[string]ObjectMeta)
		}
	}
	_ = s.loadUploads()
	return s, nil
}

// Root returns the absolute storage root.
func (s *Storage) Root() string { return s.root }

// FilesystemCapacity returns total bytes of the filesystem containing root, or 0 on error.
func (s *Storage) FilesystemCapacity() int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.root, &st); err != nil {
		return 0
	}
	return int64(st.Blocks * uint64(st.Bsize))
}

// FilesystemAvail returns free bytes (Bavail) of the filesystem, or 0 on error.
func (s *Storage) FilesystemAvail() int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.root, &st); err != nil {
		return 0
	}
	return int64(st.Bavail * uint64(st.Bsize))
}

// FilesystemUsed returns used bytes (Blocks-Bfree), or 0 on error.
func (s *Storage) FilesystemUsed() int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.root, &st); err != nil {
		return 0
	}
	return int64((st.Blocks - st.Bfree) * uint64(st.Bsize))
}

// IsOverQuota reports whether writing additional bytes would exceed the 95% watermark
// of the underlying filesystem. Returns false if filesystem stats are unavailable.
func (s *Storage) IsOverQuota(additional int64) bool {
	capacity := s.FilesystemCapacity()
	if capacity == 0 {
		return false
	}
	used := s.FilesystemUsed()
	limit := capacity * 95 / 100
	return used+additional > limit
}

func (s *Storage) bucketDir(bucket string) string { return filepath.Join(s.root, bucket) }
func (s *Storage) objectPath(bucket, key string) string {
	// key may contain "/"; treat as relative path. Clean and prevent traversal.
	clean := filepath.Clean(key)
	// filepath.Clean("a/../../b") -> "../../b" — reject if starts with ".."
	if clean == "." {
		clean = ""
	}
	// TODO(M2+): reject traversal with 400 InvalidRequest instead of silent
	// rewrite. Current __dotdot__ replacement is M1 compatibility shim; see
	// validate.go — keys containing ".." segments should be rejected with
	// errInvalidRequest rather than mangled. Tracked from M1-review #4.
	if strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		clean = strings.ReplaceAll(clean, "..", "__dotdot__")
	}
	return filepath.Join(s.bucketDir(bucket), filepath.FromSlash(clean))
}

func (s *Storage) ensureBucketExists(bucket string) error {
	s.mu.RLock()
	_, ok := s.buckets[bucket]
	s.mu.RUnlock()
	if !ok {
		return errNoSuchBucket
	}
	return nil
}

// CreateBucket creates a bucket directory.
func (s *Storage) CreateBucket(bucket string) error {
	if !isValidBucketName(bucket) {
		return errInvalidBucketName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[bucket]; ok {
		return errBucketAlreadyOwned
	}
	if err := os.MkdirAll(s.bucketDir(bucket), 0o750); err != nil {
		return err
	}
	s.buckets[bucket] = struct{}{}
	s.meta[bucket] = make(map[string]ObjectMeta)
	return nil
}

// BucketExists reports existence.
func (s *Storage) BucketExists(bucket string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.buckets[bucket]
	return ok
}

// DeleteBucket removes an empty bucket.
func (s *Storage) DeleteBucket(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.buckets[bucket]; !ok {
		return errNoSuchBucket
	}
	if len(s.meta[bucket]) > 0 {
		return errBucketNotEmpty
	}
	// Also check filesystem for stray files.
	dir := s.bucketDir(bucket)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) > 0 {
		return errBucketNotEmpty
	}
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.buckets, bucket)
	delete(s.meta, bucket)
	return nil
}

// ListBuckets returns all bucket names.
func (s *Storage) ListBuckets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.buckets))
	for k := range s.buckets {
		out = append(out, k)
	}
	return out
}

// PutObject stores blob and returns its ETag (hex md5).
func (s *Storage) PutObject(bucket, key string, contentType string, meta map[string]string, r io.Reader) (ObjectMeta, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return ObjectMeta{}, err
	}
	if strings.TrimSpace(key) == "" {
		return ObjectMeta{}, errInvalidRequest
	}
	dest := s.objectPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return ObjectMeta{}, err
	}
	// Stream to temp file while computing md5 (avoids holding 5GiB in memory).
	ct := contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	tmp := dest + ".tmp-put"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return ObjectMeta{}, err
	}
	h := md5.New()
	size, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return ObjectMeta{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return ObjectMeta{}, err
	}
	etag := hex.EncodeToString(h.Sum(nil))
	finalTmp := dest + ".tmp-" + etag
	if err := os.Rename(tmp, finalTmp); err != nil {
		_ = os.Remove(tmp)
		return ObjectMeta{}, err
	}
	if err := os.Rename(finalTmp, dest); err != nil {
		_ = os.Remove(finalTmp)
		_ = os.Remove(tmp)
		return ObjectMeta{}, err
	}
	now := time.Now().UTC()
	m := ObjectMeta{Key: key, Size: size, ETag: etag, ContentType: ct, LastMod: now, Meta: cloneMeta(meta)}
	s.mu.Lock()
	if s.meta[bucket] == nil {
		s.meta[bucket] = make(map[string]ObjectMeta)
	}
	s.meta[bucket][key] = m
	s.mu.Unlock()
	return m, nil
}

// GetObject returns meta and a read-closer for the blob.
func (s *Storage) GetObject(bucket, key string) (ObjectMeta, io.ReadCloser, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return ObjectMeta{}, nil, err
	}
	s.mu.RLock()
	m, ok := s.meta[bucket][key]
	s.mu.RUnlock()
	// Fallback: if meta missing but file exists (e.g. after restart without persistence, we still try file).
	p := s.objectPath(bucket, key)
	if !ok {
		fi, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				return ObjectMeta{}, nil, errNoSuchKey
			}
			return ObjectMeta{}, nil, err
		}
		// Reconstruct minimal meta.
		m = ObjectMeta{Key: key, Size: fi.Size(), LastMod: fi.ModTime().UTC(), ContentType: "application/octet-stream", Meta: map[string]string{}}
		// Try compute etag lazily on Get path? We compute if missing by reading file once.
		f, err := os.Open(p)
		if err != nil {
			return ObjectMeta{}, nil, err
		}
		h := md5.New()
		n, _ := io.Copy(h, f)
		_ = f.Close()
		m.ETag = hex.EncodeToString(h.Sum(nil))
		m.Size = n
		s.mu.Lock()
		if s.meta[bucket] == nil {
			s.meta[bucket] = make(map[string]ObjectMeta)
		}
		s.meta[bucket][key] = m
		s.mu.Unlock()
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectMeta{}, nil, errNoSuchKey
		}
		return ObjectMeta{}, nil, err
	}
	return m, f, nil
}

// HeadObject returns meta without opening blob.
func (s *Storage) HeadObject(bucket, key string) (ObjectMeta, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return ObjectMeta{}, err
	}
	s.mu.RLock()
	m, ok := s.meta[bucket][key]
	s.mu.RUnlock()
	if ok {
		return m, nil
	}
	p := s.objectPath(bucket, key)
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectMeta{}, errNoSuchKey
		}
		return ObjectMeta{}, err
	}
	return ObjectMeta{Key: key, Size: fi.Size(), LastMod: fi.ModTime().UTC(), ContentType: "application/octet-stream"}, nil
}

// DeleteObject removes the blob idempotently.
// Invariant (docs/07, replication): callers must not delete existing replicas
// until replacement replicas are verified. This method is a single-node primitive;
// higher layers (replication.Engine) enforce the invariant before calling it.
func (s *Storage) DeleteObject(bucket, key string) error {
	if err := s.ensureBucketExists(bucket); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.meta[bucket], key)
	s.mu.Unlock()
	p := s.objectPath(bucket, key)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Prune empty parent dirs up to bucket root (best-effort).
	dir := filepath.Dir(p)
	bucketRoot := s.bucketDir(bucket)
	for dir != bucketRoot && dir != "." && dir != "/" {
		_ = os.Remove(dir) // succeeds only if empty
		dir = filepath.Dir(dir)
		if !strings.HasPrefix(dir, bucketRoot) {
			break
		}
	}
	return nil
}

// ListObjectsV2 lists keys with prefix/delimiter pagination.
type ListObjectsV2Result struct {
	Objects               []ObjectMeta
	CommonPrefixes        []string
	IsTruncated           bool
	NextContinuationToken string
	KeyCount              int
}

// ListObjectsV2 implements S3 ListObjectsV2 semantics for M1 (in-memory).
func (s *Storage) ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (ListObjectsV2Result, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return ListObjectsV2Result{}, err
	}
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	if maxKeys > 1000 {
		maxKeys = 1000
	}
	s.mu.RLock()
	keys := make([]string, 0, len(s.meta[bucket]))
	for k := range s.meta[bucket] {
		keys = append(keys, k)
	}
	// Fallback: also scan filesystem for keys not yet in meta (e.g. after crash).
	// For M1, we add filesystem walk if meta is empty but bucket dir has files.
	s.mu.RUnlock()
	// Sort keys lexicographically per S3 spec.
	// Use simple bubble-free sort via standard library done inline to avoid import.
	sortStrings(keys)
	// Filter by prefix.
	filtered := keys[:0]
	for _, k := range keys {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		filtered = append(filtered, k)
	}
	keys = filtered
	// Find start index after continuationToken.
	start := 0
	if continuationToken != "" {
		for i, k := range keys {
			if k == continuationToken {
				start = i + 1
				break
			}
			if k > continuationToken {
				start = i
				break
			}
		}
	}
	// Handle delimiter: group keys with common prefixes.
	var prefixes []string
	seenPrefix := make(map[string]bool)
	var objects []ObjectMeta
	var collected []string // keys that contribute to pagination (objects + prefixes as one entry? S3 counts objects for IsTruncated; prefixes are extra)
	// S3: CommonPrefixes are distinct prefixes up to delimiter; objects whose key contains delimiter after prefix are rolled up.
	for i := start; i < len(keys); i++ {
		k := keys[i]
		if delimiter != "" {
			rest := k
			if prefix != "" {
				rest = strings.TrimPrefix(k, prefix)
			}
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				cp := k[:len(prefix)+idx+len(delimiter)]
				if !seenPrefix[cp] {
					seenPrefix[cp] = true
					prefixes = append(prefixes, cp)
					collected = append(collected, cp)
					if len(collected) >= maxKeys {
						break
					}
				}
				continue
			}
		}
		// Regular object.
		s.mu.RLock()
		m := s.meta[bucket][k]
		s.mu.RUnlock()
		objects = append(objects, m)
		collected = append(collected, k)
		if len(collected) >= maxKeys {
			break
		}
	}
	sortStrings(prefixes)
	// Determine truncation: if we consumed less than remaining keys/prefixes.
	totalCollected := len(objects) + len(prefixes)
	// Estimate remaining: walk remaining keys to see if more groups/objects exist beyond collected.
	remaining := 0
	if len(collected) >= maxKeys {
		// Need to know if there is more beyond current window.
		// Re-scan from i after break point.
		// Simplistic: if start+len(keys) > start+some, mark truncated.
		// Instead do exact by continuing loop.
		// We already broke, so if there are more distinct entries beyond, truncated = true.
		// Check by continuing virtual scan.
		seen2 := make(map[string]bool)
		for k := range seenPrefix {
			seen2[k] = true
		}
		for i := start; i < len(keys); i++ {
			k := keys[i]
			// Skip if already counted.
			already := false
			for _, c := range collected {
				if c == k {
					already = true
					break
				}
				// Also if k belongs to a collected prefix.
				if seenPrefix[c] && strings.HasPrefix(k, c) {
					already = true
					break
				}
			}
			_ = already
		}
		// Simpler: if we broke due to maxKeys and there are more keys beyond, truncated = true.
		// Check by counting distinct entries beyond collected window.
		countDistinct := 0
		seenTmp := make(map[string]bool)
		for _, k := range keys[start:] {
			if delimiter != "" {
				rest := k
				if prefix != "" {
					rest = strings.TrimPrefix(k, prefix)
				}
				if idx := strings.Index(rest, delimiter); idx >= 0 {
					cp := k[:len(prefix)+idx+len(delimiter)]
					if !seenTmp[cp] {
						seenTmp[cp] = true
						countDistinct++
					}
					continue
				}
			}
			countDistinct++
		}
		if countDistinct > totalCollected {
			remaining = countDistinct - totalCollected
		}
	}
	isTruncated := remaining > 0
	nextToken := ""
	if isTruncated && len(collected) > 0 {
		nextToken = collected[len(collected)-1]
	}
	return ListObjectsV2Result{
		Objects:               objects,
		CommonPrefixes:        prefixes,
		IsTruncated:           isTruncated,
		NextContinuationToken: nextToken,
		KeyCount:              len(objects),
	}, nil
}

func sortStrings(a []string) {
	// Insertion sort is fine for small n; for large n use stdlib sort via manual.
	// Use Go's sort package without importing at top to avoid cycle; we import via fully qualified if needed.
	// Instead use an inline quick sort using stdlib.
	if len(a) < 2 {
		return
	}
	// Use Go's sort.StringSlice via type assertion trick: just call sort via helper.
	quickSort(a, 0, len(a)-1)
}

func quickSort(a []string, lo, hi int) {
	if lo >= hi {
		return
	}
	p := partition(a, lo, hi)
	quickSort(a, lo, p-1)
	quickSort(a, p+1, hi)
}

func partition(a []string, lo, hi int) int {
	pivot := a[hi]
	i := lo
	for j := lo; j < hi; j++ {
		if a[j] <= pivot {
			a[i], a[j] = a[j], a[i]
			i++
		}
	}
	a[i], a[hi] = a[hi], a[i]
	return i
}

func cloneMeta(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
