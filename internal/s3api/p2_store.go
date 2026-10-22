// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// Subresource kinds persisted in s3_subresources.kind.
const (
	subACL       = "acl"
	subTagging   = "tagging"
	subCORS      = "cors"
	subLifecycle = "lifecycle"
)

// SubresourceStore persists S3 P2 subresource documents. object_key == "" is
// the bucket-level document. It is optional: stores that do not implement it
// (e.g. the gateway placeholder) keep the pre-persistence behaviour.
type SubresourceStore interface {
	GetSubresource(bucket, key, kind string) (string, bool, error)
	PutSubresource(bucket, key, kind, body string) error
	DeleteSubresource(bucket, key, kind string) error
	PurgeObjectSubresources(bucket, key string) error
	PurgeBucketSubresources(bucket string) error
}

var _ SubresourceStore = (*Storage)(nil)

// errSubresourceUnsupported is returned by backends that cannot persist subresources.
var errSubresourceUnsupported = errors.New("subresource storage is not available for this backend")

// noSubresources is used when the object store has no SubresourceStore.
type noSubresources struct{}

func (noSubresources) GetSubresource(string, string, string) (string, bool, error) {
	return "", false, nil
}
func (noSubresources) PutSubresource(string, string, string, string) error {
	return errSubresourceUnsupported
}
func (noSubresources) DeleteSubresource(string, string, string) error { return nil }
func (noSubresources) PurgeObjectSubresources(string, string) error   { return nil }
func (noSubresources) PurgeBucketSubresources(string) error           { return nil }

// subKey indexes the in-memory fallback used when no DB is attached.
type subKey struct{ bucket, key, kind string }

func p2Dialect(driver string) string {
	switch strings.ToLower(driver) {
	case "postgres", "postgresql":
		return "postgres"
	case "sqlite", "sqlite3":
		return "sqlite"
	default:
		return "mysql"
	}
}

// p2Rebind rewrites ? placeholders to $n for PostgreSQL.
func p2Rebind(dialect, q string) string {
	if dialect != "postgres" {
		return q
	}
	var sb strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			sb.WriteString("$" + strconv.Itoa(n))
			continue
		}
		sb.WriteByte(q[i])
	}
	return sb.String()
}

func p2UpsertSQL(dialect string) string {
	const ins = "INSERT INTO s3_subresources(bucket, object_key, kind, body) VALUES(?, ?, ?, ?)"
	switch dialect {
	case "postgres":
		return ins + " ON CONFLICT (bucket, object_key, kind) DO UPDATE SET body = EXCLUDED.body, updated_at = now()"
	case "sqlite":
		return ins + " ON CONFLICT (bucket, object_key, kind) DO UPDATE SET body = excluded.body, updated_at = CURRENT_TIMESTAMP"
	default:
		return ins + " ON DUPLICATE KEY UPDATE body = VALUES(body), updated_at = CURRENT_TIMESTAMP"
	}
}

func (s *Storage) subDB() (*sql.DB, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db, p2Dialect(s.dbDriver)
}

// GetSubresource returns the stored document; found is false when none exists.
func (s *Storage) GetSubresource(bucket, key, kind string) (string, bool, error) {
	db, dialect := s.subDB()
	if db == nil {
		s.mu.RLock()
		defer s.mu.RUnlock()
		v, ok := s.subres[subKey{bucket, key, kind}]
		return v, ok, nil
	}
	var body string
	err := db.QueryRow(p2Rebind(dialect,
		"SELECT body FROM s3_subresources WHERE bucket=? AND object_key=? AND kind=?"),
		bucket, key, kind).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}

// PutSubresource creates or replaces the document.
func (s *Storage) PutSubresource(bucket, key, kind, body string) error {
	db, dialect := s.subDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.subres == nil {
			s.subres = make(map[subKey]string)
		}
		s.subres[subKey{bucket, key, kind}] = body
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect, p2UpsertSQL(dialect)), bucket, key, kind, body)
	return err
}

// DeleteSubresource removes one document; a missing document is not an error.
func (s *Storage) DeleteSubresource(bucket, key, kind string) error {
	db, dialect := s.subDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subres, subKey{bucket, key, kind})
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect,
		"DELETE FROM s3_subresources WHERE bucket=? AND object_key=? AND kind=?"),
		bucket, key, kind)
	return err
}

// PurgeObjectSubresources drops every document of one object (new object version, delete).
func (s *Storage) PurgeObjectSubresources(bucket, key string) error {
	db, dialect := s.subDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for k := range s.subres {
			if k.bucket == bucket && k.key == key {
				delete(s.subres, k)
			}
		}
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect,
		"DELETE FROM s3_subresources WHERE bucket=? AND object_key=?"), bucket, key)
	return err
}

// PurgeBucketSubresources drops the bucket document and every object document of the bucket.
func (s *Storage) PurgeBucketSubresources(bucket string) error {
	db, dialect := s.subDB()
	if db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for k := range s.subres {
			if k.bucket == bucket {
				delete(s.subres, k)
			}
		}
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect, "DELETE FROM s3_subresources WHERE bucket=?"), bucket)
	return err
}
