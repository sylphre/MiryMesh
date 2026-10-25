// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

func newBucketsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// A new RemoteStore on the same database stands for the Master after a restart.
func TestRemoteStoreBucketsSurviveRestart(t *testing.T) {
	db := newBucketsTestDB(t)
	c := newMNCluster(t, []string{"n1"}, 1)

	rs1, _ := c.remote("single", c.master)
	rs1.SetDB(db, "sqlite")
	for _, b := range []string{"keep", "gone", "other"} {
		if err := rs1.CreateBucket(b); err != nil {
			t.Fatalf("CreateBucket(%s): %v", b, err)
		}
	}
	policy := []byte(`{"Version":"2012-10-17","Statement":[]}`)
	if err := rs1.PutBucketPolicy("keep", policy); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	if err := rs1.DeleteBucket("gone"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}

	rs2, _ := c.remote("single", c.master)
	rs2.SetDB(db, "sqlite")
	if got := rs2.ListBuckets(); len(got) != 0 {
		t.Fatalf("a fresh store lists %v before LoadBuckets", got)
	}
	if err := rs2.LoadBuckets(); err != nil {
		t.Fatalf("LoadBuckets: %v", err)
	}
	if got, want := rs2.ListBuckets(), []string{"keep", "other"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListBuckets after restart = %v, want %v", got, want)
	}
	got, err := rs2.GetBucketPolicy("keep")
	if err != nil || string(got) != string(policy) {
		t.Fatalf("GetBucketPolicy after restart = %q, %v", got, err)
	}
	if _, err = rs2.GetBucketPolicy("other"); !errors.Is(err, errNoSuchBucketPolicy) {
		t.Fatalf("policy of a bucket without one: err = %v", err)
	}
	if err = rs2.CreateBucket("keep"); !errors.Is(err, errBucketAlreadyOwned) {
		t.Fatalf("CreateBucket of a restored bucket: err = %v", err)
	}

	if err = rs2.DeleteBucketPolicy("keep"); err != nil {
		t.Fatalf("DeleteBucketPolicy: %v", err)
	}
	rs3, _ := c.remote("single", c.master)
	rs3.SetDB(db, "sqlite")
	if err = rs3.LoadBuckets(); err != nil {
		t.Fatalf("LoadBuckets: %v", err)
	}
	if _, err = rs3.GetBucketPolicy("keep"); !errors.Is(err, errNoSuchBucketPolicy) {
		t.Fatalf("deleted policy came back: err = %v", err)
	}
}

// Without a database nothing is stored and LoadBuckets is a no-op.
func TestRemoteStoreBucketsWithoutDB(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.LoadBuckets(); err != nil {
		t.Fatalf("LoadBuckets without a database: %v", err)
	}
	if err := rs.CreateBucket("mem"); err != nil {
		t.Fatal(err)
	}
	if !rs.BucketExists("mem") {
		t.Fatal("bucket missing")
	}
}
