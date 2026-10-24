// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// newOwnerTestStorage opens a SQLite DB with foreign keys enforced and the
// shipped migrations applied.
func newOwnerTestStorage(t *testing.T) (*Storage, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "owner.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st, err := NewStorageWithDB(t.TempDir(), db, "sqlite")
	if err != nil {
		t.Fatalf("NewStorageWithDB: %v", err)
	}
	return st, db
}

// A bucket must be owned by the administrator chosen at Setup, whatever the
// name is; a hard-coded "admin" violated buckets_owner_fkey otherwise.
func TestCreateBucketUsesSetupAdminAsOwner(t *testing.T) {
	st, db := newOwnerTestStorage(t)
	if _, err := db.Exec(`INSERT INTO setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token) VALUES (1, 'miriel', 'h', 'ak', 'sk', 'tok')`); err != nil {
		t.Fatalf("seed setup: %v", err)
	}
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	var owner string
	if err := db.QueryRow(`SELECT owner FROM buckets WHERE name = 'bkt'`).Scan(&owner); err != nil {
		t.Fatalf("select owner: %v", err)
	}
	if owner != "miriel" {
		t.Fatalf("owner = %q, want %q", owner, "miriel")
	}
}

// Without a setup row the previous default is kept (no panic, no empty owner).
func TestBucketOwnerFallsBackWithoutSetupRow(t *testing.T) {
	st, _ := newOwnerTestStorage(t)
	if got := st.bucketOwner(); got != defaultBucketOwner {
		t.Fatalf("bucketOwner() = %q, want %q", got, defaultBucketOwner)
	}
}
