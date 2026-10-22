// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/metadata"
	"github.com/sylphre/MiryMesh/internal/testdb"
)

// newP2MySQLRouter builds a router backed by a throw-away MySQL / MariaDB database
// (MIRYMESH_TEST_MYSQL_DSN; skipped when unset) with the shipped migrations applied.
// buckets.owner has a real foreign key on setup(admin_username) there, so the "admin"
// owner used by CreateBucket must exist first.
func newP2MySQLRouter(t *testing.T) (*Router, *Storage, func() *Storage) {
	t.Helper()
	db := testdb.MySQL(t)
	if err := metadata.Migrate(db, "mysql"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token) VALUES (1, 'admin', 'h', 'ak', 'sk', 'tok')`); err != nil {
		t.Fatalf("seed setup: %v", err)
	}
	root := t.TempDir()
	open := func() *Storage {
		st, err := NewStorageWithDB(root, db, "mysql")
		if err != nil {
			t.Fatalf("NewStorageWithDB: %v", err)
		}
		return st
	}
	st := open()
	return NewRouter(st, "us-east-1"), st, open
}

func TestP2SubresourcesPersistInMySQL(t *testing.T) {
	rt, st, reopen := newP2MySQLRouter(t)
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// Keys that differ only by case must stay separate rows (VARBINARY comparison).
	for _, key := range []string{"obj.txt", "OBJ.txt"} {
		if _, err := st.PutObject("bkt", key, "text/plain", nil, strings.NewReader("hello")); err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
	}

	tag := func(v string) string {
		return `<Tagging><TagSet><Tag><Key>env</Key><Value>` + v + `</Value></Tag></TagSet></Tagging>`
	}
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", tag("prod"), nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/OBJ.txt?tagging", tag("dev"), nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?cors", p2TestCORS, nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?acl", "", map[string]string{"X-Amz-Acl": "public-read"}), http.StatusOK, "")

	// A second PUT replaces the document (ON DUPLICATE KEY UPDATE) instead of adding a row.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", tag("stage"), nil), http.StatusOK, "")

	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ?`, "bkt").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 4 {
		t.Fatalf("rows=%d want 4", n)
	}

	// A new Storage over the same DB and root (a "restart") still sees everything.
	rt2 := NewRouter(reopen(), "us-east-1")
	p2Expect(t, p2Do(rt2, http.MethodGet, "/bkt/obj.txt?tagging", "", nil), http.StatusOK, "<Value>stage</Value>")
	p2Expect(t, p2Do(rt2, http.MethodGet, "/bkt/OBJ.txt?tagging", "", nil), http.StatusOK, "<Value>dev</Value>")
	p2Expect(t, p2Do(rt2, http.MethodGet, "/bkt?cors", "", nil), http.StatusOK, "<AllowedOrigin>https://app.example.com</AllowedOrigin>")
	p2Expect(t, p2Do(rt2, http.MethodGet, "/bkt?acl", "", nil), http.StatusOK, "AllUsers")

	// Cleanup paths hit the DB as well.
	p2Expect(t, p2Do(rt2, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusNoContent, "")
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ? AND object_key = ?`, "bkt", "obj.txt").Scan(&n); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if n != 0 {
		t.Fatalf("object rows after delete=%d want 0", n)
	}
	p2Expect(t, p2Do(rt2, http.MethodDelete, "/bkt/OBJ.txt", "", nil), http.StatusNoContent, "")
	p2Expect(t, p2Do(rt2, http.MethodDelete, "/bkt", "", nil), http.StatusNoContent, "")
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ?`, "bkt").Scan(&n); err != nil {
		t.Fatalf("count after bucket delete: %v", err)
	}
	if n != 0 {
		t.Fatalf("bucket rows after delete=%d want 0", n)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM buckets WHERE name = ?`, "bkt").Scan(&n); err != nil || n != 0 {
		t.Fatalf("buckets row after delete: n=%d err=%v", n, err)
	}
}

func TestBucketPolicyPersistsInMySQL(t *testing.T) {
	_, st, reopen := newP2MySQLRouter(t)
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// Creating the same bucket again must not fail on the DB side (INSERT IGNORE path).
	if err := st.insertBucketRow("bkt", "admin"); err != nil {
		t.Fatalf("insertBucketRow twice: %v", err)
	}

	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::bkt/*"}]}`
	if err := st.PutBucketPolicy("bkt", []byte(policy)); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}

	// MySQL's JSON type normalises whitespace, so compare as JSON, not as bytes.
	got, err := reopen().GetBucketPolicy("bkt")
	if err != nil {
		t.Fatalf("GetBucketPolicy after restart: %v", err)
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("stored policy is not JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(policy), &b); err != nil {
		t.Fatal(err)
	}
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if string(ga) != string(gb) {
		t.Fatalf("policy changed:\n got %s\nwant %s", ga, gb)
	}

	if err := reopen().CreateBucket("other"); err != nil {
		t.Fatalf("CreateBucket other: %v", err)
	}
	if _, err := reopen().GetBucketPolicy("other"); err != errNoSuchBucketPolicy {
		t.Fatalf("policy of a bucket without one: err=%v want errNoSuchBucketPolicy", err)
	}
}
