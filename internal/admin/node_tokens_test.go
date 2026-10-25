// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

func newNodeTokenDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // every connection to :memory: is its own database
	t.Cleanup(func() { _ = db.Close() })
	if err := metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestNodeTokenStoreSealsAndRotates(t *testing.T) {
	keyA := strings.Repeat("a", 64)
	keyB := strings.Repeat("b", 64)
	db := newNodeTokenDB(t)

	a := NewSetupStoreDB(db, "sqlite", keyA)
	if _, ok := a.GetNodeToken("n1"); ok {
		t.Fatal("unknown node must have no token")
	}
	if err := a.PutNodeToken("n1", "token-one"); err != nil {
		t.Fatal(err)
	}
	if tok, ok := a.GetNodeToken("n1"); !ok || tok != "token-one" {
		t.Fatalf("get: %q %v", tok, ok)
	}
	var raw string
	if err := db.QueryRow(`SELECT token_enc FROM node_tokens WHERE node_id=?`, "n1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == "" || strings.Contains(raw, "token-one") {
		t.Fatalf("token must be stored encrypted, got %q", raw)
	}
	if err := a.PutNodeToken("n1", "token-two"); err != nil {
		t.Fatal(err)
	}
	if tok, _ := a.GetNodeToken("n1"); tok != "token-two" {
		t.Fatalf("replace: %q", tok)
	}

	// A new master key with the old one kept for decryption can still read it.
	b := NewSetupStoreDB(db, "sqlite", keyB, keyA)
	if tok, ok := b.GetNodeToken("n1"); !ok || tok != "token-two" {
		t.Fatalf("read with old key: %q %v", tok, ok)
	}
	kr, err := b.keyring()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	upd, total, err := b.planNodeTokenRotation(tx, kr)
	if err != nil || total != 1 || len(upd) != 1 {
		_ = tx.Rollback()
		t.Fatalf("plan: updates=%d total=%d err=%v", len(upd), total, err)
	}
	if err := b.applyNodeTokenRotation(tx, upd); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// After rotation the old key is no longer needed.
	only := NewSetupStoreDB(db, "sqlite", keyB)
	if tok, ok := only.GetNodeToken("n1"); !ok || tok != "token-two" {
		t.Fatalf("read after rotation: %q %v", tok, ok)
	}
}
