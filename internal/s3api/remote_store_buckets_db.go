// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"database/sql"
	"fmt"
)

// RemoteStore keeps its bucket list and bucket policies in the buckets table of the
// Master database when SetDB has attached one, the same table Storage uses. Without a
// database the list lives in memory only. LoadBuckets reads the table back at startup,
// so a Master restart or update does not forget the buckets.

// bucketOwnerOf returns the owner to record for a new bucket. buckets.owner has a
// foreign key on setup(admin_username), so it is the administrator name chosen on the
// Setup page. defaultBucketOwner is used when the setup row cannot be read.
func bucketOwnerOf(db *sql.DB) string {
	var owner string
	if err := db.QueryRow("SELECT admin_username FROM setup WHERE id = 1").Scan(&owner); err != nil || owner == "" {
		return defaultBucketOwner
	}
	return owner
}

// insertBucketRow records a bucket. An existing row is left untouched: a REPLACE would
// delete and recreate it, and the placement table cascades from buckets.
func (r *RemoteStore) insertBucketRow(bucket string) error {
	db, dialect := r.handle()
	if db == nil {
		return nil
	}
	var q string
	switch dialect {
	case "postgres":
		q = "INSERT INTO buckets(name, owner) VALUES($1, $2) ON CONFLICT (name) DO NOTHING"
	case "sqlite":
		q = "INSERT OR IGNORE INTO buckets(name, owner) VALUES(?, ?)"
	default:
		q = "INSERT IGNORE INTO buckets(name, owner) VALUES(?, ?)"
	}
	if _, err := db.Exec(q, bucket, bucketOwnerOf(db)); err != nil {
		return fmt.Errorf("store bucket: %w", err)
	}
	return nil
}

// deleteBucketRow removes a bucket row. A missing row is not an error.
func (r *RemoteStore) deleteBucketRow(bucket string) error {
	db, dialect := r.handle()
	if db == nil {
		return nil
	}
	if _, err := db.Exec(p2Rebind(dialect, "DELETE FROM buckets WHERE name=?"), bucket); err != nil {
		return fmt.Errorf("delete bucket: %w", err)
	}
	return nil
}

// saveBucketPolicy stores the policy document of a bucket; nil clears it.
func (r *RemoteStore) saveBucketPolicy(bucket string, policy []byte) error {
	db, dialect := r.handle()
	if db == nil {
		return nil
	}
	var v any
	if policy != nil {
		v = string(policy)
	}
	if _, err := db.Exec(p2Rebind(dialect, "UPDATE buckets SET policy=? WHERE name=?"), v, bucket); err != nil {
		return fmt.Errorf("store bucket policy: %w", err)
	}
	return nil
}

// LoadBuckets adds the buckets and policies stored in the database to the in-memory
// set. Entries already in memory are kept. Call it once at startup, after SetDB. It
// does nothing when no database is attached.
func (r *RemoteStore) LoadBuckets() error {
	db, _ := r.handle()
	if db == nil {
		return nil
	}
	rows, err := db.Query("SELECT name, policy FROM buckets")
	if err != nil {
		return fmt.Errorf("load buckets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type bucketRow struct {
		name   string
		policy []byte
	}
	var found []bucketRow
	for rows.Next() {
		var name string
		var pol sql.NullString
		if err = rows.Scan(&name, &pol); err != nil {
			return fmt.Errorf("load buckets: %w", err)
		}
		b := bucketRow{name: name}
		if pol.Valid && pol.String != "" {
			b.policy = []byte(pol.String)
		}
		found = append(found, b)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("load buckets: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range found {
		if _, ok := r.buckets[b.name]; !ok {
			r.buckets[b.name] = struct{}{}
		}
		if b.policy != nil {
			if _, ok := r.bucketPolicy[b.name]; !ok {
				r.bucketPolicy[b.name] = b.policy
			}
		}
	}
	return nil
}
