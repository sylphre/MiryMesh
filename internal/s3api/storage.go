// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
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
	// bucket policy (S3 ?policy) — raw JSON bytes, validated by handler
	GetBucketPolicy(bucket string) ([]byte, error)
	PutBucketPolicy(bucket string, policyJSON []byte) error
	DeleteBucketPolicy(bucket string) error
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
	mu           sync.RWMutex
	root         string
	db           *sql.DB
	dbDriver     string
	buckets      map[string]struct{}
	bucketPolicy map[string][]byte // bucket -> raw policy JSON (fallback when db==nil)
	// meta cache: bucket -> key -> meta
	meta map[string]map[string]ObjectMeta
	// multipart in-progress uploads: uploadID -> upload (persisted to disk)
	uploads map[string]*MultipartUploadState
	// bucket creation times recorded by this process
	created map[string]time.Time
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
	return NewStorageWithDB(root, nil, "")
}

func (s *Storage) SetDB(db *sql.DB, driver string) {
	s.mu.Lock()
	s.db = db
	s.dbDriver = driver
	s.mu.Unlock()
}

// NewStorageWithDB creates a storage with optional DB-backed bucket policy persistence.
func NewStorageWithDB(root string, db *sql.DB, driver string) (*Storage, error) {
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
	s := &Storage{root: abs, db: db, dbDriver: driver, buckets: make(map[string]struct{}), bucketPolicy: make(map[string][]byte), meta: make(map[string]map[string]ObjectMeta), uploads: make(map[string]*MultipartUploadState)}
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
	// The API edge rejects "." / ".." segments (validateObjectKey). As defense
	// in depth for paths that bypass it (CopyObject source, DeleteObjects body),
	// anchor the key at "/" before cleaning so ".." can never climb out of the
	// bucket directory.
	clean := path.Clean("/" + key)
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

// CreateBucket creates a bucket directory and DB row (if DB available).
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
	if s.created == nil {
		s.created = make(map[string]time.Time)
	}
	s.created[bucket] = time.Now().UTC()
	if s.db != nil {
		if err := s.insertBucketRow(bucket, "admin"); err != nil {
			delete(s.buckets, bucket)
			delete(s.meta, bucket)
			delete(s.created, bucket)
			_ = os.Remove(s.bucketDir(bucket))
			return err
		}
	}
	return nil
}

// insertBucketRow records the bucket in the DB with a driver-specific
// "ignore duplicate" statement (exactly one statement per driver).
func (s *Storage) insertBucketRow(bucket, owner string) error {
	var q string
	switch s.dbDriver {
	case "postgres", "postgresql":
		q = "INSERT INTO buckets(name, owner) VALUES($1, $2) ON CONFLICT (name) DO NOTHING"
	case "sqlite", "sqlite3":
		q = "INSERT OR IGNORE INTO buckets(name, owner) VALUES(?, ?)"
	default:
		q = "INSERT IGNORE INTO buckets(name, owner) VALUES(?, ?)"
	}
	_, err := s.db.Exec(q, bucket, owner)
	return err
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
	delete(s.bucketPolicy, bucket)
	if s.db != nil {
		if s.dbDriver == "postgres" || s.dbDriver == "postgresql" {
			_, _ = s.db.Exec("DELETE FROM buckets WHERE name=$1", bucket)
		} else {
			_, _ = s.db.Exec("DELETE FROM buckets WHERE name=?", bucket)
		}
	}
	return nil
}

// Bucket policy (S3 ?policy) — DB-backed when db != nil, in-memory fallback.

func (s *Storage) GetBucketPolicy(bucket string) ([]byte, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return nil, err
	}
	if s.db != nil {
		var raw []byte
		var err error
		if s.dbDriver == "postgres" || s.dbDriver == "postgresql" {
			err = s.db.QueryRow("SELECT policy FROM buckets WHERE name=$1", bucket).Scan(&raw)
		} else {
			err = s.db.QueryRow("SELECT policy FROM buckets WHERE name=?", bucket).Scan(&raw)
		}
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, errNoSuchBucket
			}
			return nil, err
		}
		if raw == nil {
			return nil, errNoSuchBucketPolicy
		}
		out := make([]byte, len(raw))
		copy(out, raw)
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bucketPolicy[bucket]
	if !ok {
		return nil, errNoSuchBucketPolicy
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func (s *Storage) PutBucketPolicy(bucket string, policyJSON []byte) error {
	if err := s.ensureBucketExists(bucket); err != nil {
		return err
	}
	if s.db != nil {
		if s.dbDriver == "postgres" || s.dbDriver == "postgresql" {
			_, err := s.db.Exec("UPDATE buckets SET policy=$1 WHERE name=$2", string(policyJSON), bucket)
			if err == nil {
				cp := make([]byte, len(policyJSON))
				copy(cp, policyJSON)
				s.mu.Lock()
				s.bucketPolicy[bucket] = cp
				s.mu.Unlock()
			}
			return err
		}
		_, err := s.db.Exec("UPDATE buckets SET policy=? WHERE name=?", string(policyJSON), bucket)
		if err == nil {
			cp := make([]byte, len(policyJSON))
			copy(cp, policyJSON)
			s.mu.Lock()
			s.bucketPolicy[bucket] = cp
			s.mu.Unlock()
		}
		return err
	}
	cp := make([]byte, len(policyJSON))
	copy(cp, policyJSON)
	s.mu.Lock()
	s.bucketPolicy[bucket] = cp
	s.mu.Unlock()
	return nil
}

func (s *Storage) DeleteBucketPolicy(bucket string) error {
	if err := s.ensureBucketExists(bucket); err != nil {
		return err
	}
	if s.db != nil {
		var cur []byte
		var err error
		if s.dbDriver == "postgres" || s.dbDriver == "postgresql" {
			err = s.db.QueryRow("SELECT policy FROM buckets WHERE name=$1", bucket).Scan(&cur)
		} else {
			err = s.db.QueryRow("SELECT policy FROM buckets WHERE name=?", bucket).Scan(&cur)
		}
		if err != nil {
			return errNoSuchBucket
		}
		if cur == nil {
			return errNoSuchBucketPolicy
		}
		if s.dbDriver == "postgres" || s.dbDriver == "postgresql" {
			_, err = s.db.Exec("UPDATE buckets SET policy=NULL WHERE name=$1", bucket)
		} else {
			_, err = s.db.Exec("UPDATE buckets SET policy=NULL WHERE name=?", bucket)
		}
		if err == nil {
			s.mu.Lock()
			delete(s.bucketPolicy, bucket)
			s.mu.Unlock()
		}
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.bucketPolicy[bucket]; !ok {
		return errNoSuchBucketPolicy
	}
	delete(s.bucketPolicy, bucket)
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
