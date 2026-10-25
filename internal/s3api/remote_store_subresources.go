// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"database/sql"
	"errors"
	"sync"
)

// RemoteStore stores subresource documents on the Master, not on a DataNode:
// the multipart config and part records, SSE-C and checksum records, ACL,
// tagging, CORS, lifecycle and bucket quota. Without this the Router falls back
// to noSubresources and every upload that needs a document fails.
var _ SubresourceStore = (*RemoteStore)(nil)

// subresDB persists subresource documents in the s3_subresources table when a
// database is attached with SetDB, and in memory otherwise. It uses the same
// SQL as Storage. RemoteStore embeds it, so its methods are promoted.
type subresDB struct {
	smu    sync.RWMutex
	db     *sql.DB
	driver string
	mem    map[subKey]string
}

// SetDB attaches the database that holds s3_subresources. Call it once at
// startup, before the store serves requests; documents already held in memory
// are not copied over.
func (s *subresDB) SetDB(db *sql.DB, driver string) {
	s.smu.Lock()
	s.db = db
	s.driver = driver
	s.smu.Unlock()
}

func (s *subresDB) handle() (*sql.DB, string) {
	s.smu.RLock()
	defer s.smu.RUnlock()
	return s.db, p2Dialect(s.driver)
}

// GetSubresource returns the stored document; found is false when none exists.
func (s *subresDB) GetSubresource(bucket, key, kind string) (string, bool, error) {
	db, dialect := s.handle()
	if db == nil {
		s.smu.RLock()
		defer s.smu.RUnlock()
		v, ok := s.mem[subKey{bucket, key, kind}]
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
func (s *subresDB) PutSubresource(bucket, key, kind, body string) error {
	db, dialect := s.handle()
	if db == nil {
		s.smu.Lock()
		defer s.smu.Unlock()
		if s.mem == nil {
			s.mem = make(map[subKey]string)
		}
		s.mem[subKey{bucket, key, kind}] = body
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect, p2UpsertSQL(dialect)), bucket, key, kind, body)
	return err
}

// DeleteSubresource removes one document; a missing document is not an error.
func (s *subresDB) DeleteSubresource(bucket, key, kind string) error {
	db, dialect := s.handle()
	if db == nil {
		s.smu.Lock()
		defer s.smu.Unlock()
		delete(s.mem, subKey{bucket, key, kind})
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect,
		"DELETE FROM s3_subresources WHERE bucket=? AND object_key=? AND kind=?"),
		bucket, key, kind)
	return err
}

// PurgeObjectSubresources drops every document of one object.
func (s *subresDB) PurgeObjectSubresources(bucket, key string) error {
	db, dialect := s.handle()
	if db == nil {
		s.smu.Lock()
		defer s.smu.Unlock()
		for k := range s.mem {
			if k.bucket == bucket && k.key == key {
				delete(s.mem, k)
			}
		}
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect,
		"DELETE FROM s3_subresources WHERE bucket=? AND object_key=?"), bucket, key)
	return err
}

// PurgeBucketSubresources drops the bucket document and every object document of the bucket.
func (s *subresDB) PurgeBucketSubresources(bucket string) error {
	db, dialect := s.handle()
	if db == nil {
		s.smu.Lock()
		defer s.smu.Unlock()
		for k := range s.mem {
			if k.bucket == bucket {
				delete(s.mem, k)
			}
		}
		return nil
	}
	_, err := db.Exec(p2Rebind(dialect, "DELETE FROM s3_subresources WHERE bucket=?"), bucket)
	return err
}
