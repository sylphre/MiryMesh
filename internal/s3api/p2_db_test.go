// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// newP2DBRouter builds a router whose storage is backed by a real SQLite file,
// with the schema created by the shipped migrations (0001-0003) via metadata.Migrate.
func newP2DBRouter(t *testing.T, dbPath, root string) (*Router, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st, err := NewStorageWithDB(root, db, "sqlite")
	if err != nil {
		t.Fatalf("NewStorageWithDB: %v", err)
	}
	return NewRouter(st, "us-east-1"), db
}

func TestP2SubresourcesPersistInSQLite(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "p2.db")
	rt, db := newP2DBRouter(t, dbPath, root)
	if err := rt.store.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := rt.store.PutObject("bkt", "obj.txt", "text/plain", nil, strings.NewReader("hello")); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	tags := `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag></TagSet></Tagging>`
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", tags, nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?cors", p2TestCORS, nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?acl", "", map[string]string{"X-Amz-Acl": "public-read"}), http.StatusOK, "")

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ?`, "bkt").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("rows=%d want 3", n)
	}

	// A second PUT replaces the document (upsert) instead of adding a row.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging",
		`<Tagging><TagSet><Tag><Key>env</Key><Value>dev</Value></Tag></TagSet></Tagging>`, nil), http.StatusOK, "")
	if err := db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ? AND kind = ?`, "bkt", subTagging).Scan(&n); err != nil {
		t.Fatalf("count tagging: %v", err)
	}
	if n != 1 {
		t.Fatalf("tagging rows=%d want 1", n)
	}

	// A new Storage over the same DB (a "restart") still sees everything.
	rt2, _ := newP2DBRouter(t, dbPath, root)
	rr := p2Do(rt2, http.MethodGet, "/bkt/obj.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "<Key>env</Key><Value>dev</Value>")
	p2Expect(t, p2Do(rt2, http.MethodGet, "/bkt?cors", "", nil), http.StatusOK, "<AllowedOrigin>https://app.example.com</AllowedOrigin>")
	rr = p2Do(rt2, http.MethodGet, "/bkt?acl", "", nil)
	p2Expect(t, rr, http.StatusOK, "AllUsers")

	// Cleanup paths hit the DB as well.
	p2Expect(t, p2Do(rt2, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusNoContent, "")
	if err := db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ? AND object_key = ?`, "bkt", "obj.txt").Scan(&n); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if n != 0 {
		t.Fatalf("object rows after delete=%d want 0", n)
	}
	p2Expect(t, p2Do(rt2, http.MethodDelete, "/bkt", "", nil), http.StatusNoContent, "")
	if err := db.QueryRow(`SELECT COUNT(*) FROM s3_subresources WHERE bucket = ?`, "bkt").Scan(&n); err != nil {
		t.Fatalf("count after bucket delete: %v", err)
	}
	if n != 0 {
		t.Fatalf("bucket rows after delete=%d want 0", n)
	}
}
