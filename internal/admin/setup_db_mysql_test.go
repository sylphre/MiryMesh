// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/metadata"
	"github.com/sylphre/MiryMesh/internal/testdb"
)

// Runs against MySQL or MariaDB depending on MIRYMESH_TEST_MYSQL_DSN; skipped when unset.
func newMySQLSetupStore(t *testing.T) *SetupStoreDB {
	t.Helper()
	db := testdb.MySQL(t)
	if err := metadata.Migrate(db, "mysql"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return NewSetupStoreDB(db, "mysql", "0123456789abcdef0123456789abcdef")
}

func TestSetupStoreDBMySQLSaveGetUpdate(t *testing.T) {
	s := newMySQLSetupStore(t)
	if s.IsInitialized() {
		t.Fatal("fresh database must not be initialized")
	}

	// Step 1: admin only (no access key, no db driver / dsn) -> NULL columns must be accepted.
	step1 := &SetupState{
		AdminUsername:     "admin",
		AdminPasswordHash: "hash",
		AdminToken:        "tok",
		DefaultPolicy:     "single",
		Region:            "us-east-1",
	}
	if err := s.Save(step1); err != nil {
		t.Fatalf("Save step 1: %v", err)
	}
	if !s.IsInitialized() {
		t.Fatal("setup must be initialized after Save")
	}
	if err := s.Save(step1); err != errAlreadyInitialized {
		t.Fatalf("second Save without access key: err=%v want errAlreadyInitialized", err)
	}

	// Step 2: the second Save fills in S3 / DB / storage settings via Update.
	step2 := &SetupState{
		AdminUsername: "admin",
		AccessKey:     "AKIATEST",
		SecretKey:     "s3cr3t-value",
		DefaultPolicy: "dual",
		Region:        "ap-northeast-1",
		Gateway:       true,
		DBDriver:      "mysql",
		DBDSN:         "u:p@tcp(db:3306)/mirymesh",
		DatanodePaths: "/mnt/disk1,/mnt/disk2",
	}
	if err := s.Save(step2); err != nil {
		t.Fatalf("Save step 2 (Update): %v", err)
	}

	got := s.Get()
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.AdminUsername != "admin" || got.AccessKey != "AKIATEST" || got.SecretKey != "s3cr3t-value" {
		t.Errorf("credentials not round-tripped: %+v", got)
	}
	if got.DefaultPolicy != "dual" || got.Region != "ap-northeast-1" || !got.Gateway || !got.Initialized {
		t.Errorf("settings not round-tripped: %+v", got)
	}
	if got.DBDriver != "mysql" || got.DBDSN != "u:p@tcp(db:3306)/mirymesh" || got.DatanodePaths != "/mnt/disk1,/mnt/disk2" {
		t.Errorf("db / storage settings not round-tripped: %+v", got)
	}

	if err := s.UpdatePolicy("triple"); err != nil {
		t.Fatalf("UpdatePolicy: %v", err)
	}
	if p := s.Get().DefaultPolicy; p != "triple" {
		t.Errorf("DefaultPolicy=%q want triple", p)
	}
	if err := s.UpdatePolicy("bogus"); err == nil {
		t.Error("CHECK constraint on default_policy is not enforced")
	}
}

func TestSetupStoreDBMySQLSessionsAndPairTokens(t *testing.T) {
	s := newMySQLSetupStore(t)
	if err := s.Save(&SetupState{
		AdminUsername: "admin", AdminPasswordHash: "hash", AdminToken: "tok",
		DefaultPolicy: "single", Region: "us-east-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Valid session -> revoked session.
	if err := s.CreateSession("sess-1", "admin", time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !s.ValidateSession("sess-1") {
		t.Error("fresh session must be valid")
	}
	if s.ValidateSession("unknown") {
		t.Error("unknown session must be invalid")
	}
	if err := s.RevokeSession("sess-1"); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if s.ValidateSession("sess-1") {
		t.Error("revoked session must be invalid")
	}

	// Expired session (TTL in the past) must be rejected; this also proves that
	// expires_at is scanned as time.Time (parseTime) and compared in UTC.
	if err := s.CreateSession("sess-old", "admin", -time.Minute); err != nil {
		t.Fatalf("CreateSession expired: %v", err)
	}
	if s.ValidateSession("sess-old") {
		t.Error("expired session must be invalid")
	}

	// A session of a non-existing admin violates the real foreign key.
	if err := s.CreateSession("sess-x", "nobody", time.Hour); err == nil {
		t.Error("sessions.admin_username foreign key is not enforced")
	}

	// Pair token: one-time use.
	tok, err := s.CreatePairToken(15 * time.Minute)
	if err != nil {
		t.Fatalf("CreatePairToken: %v", err)
	}
	if err := s.ConsumePairToken(tok); err != nil {
		t.Fatalf("ConsumePairToken: %v", err)
	}
	if err := s.ConsumePairToken(tok); err == nil || err.Error() != "pair token already used" {
		t.Errorf("second consume: err=%v want 'pair token already used'", err)
	}
	if err := s.ConsumePairToken("does-not-exist"); err == nil || err.Error() != "invalid pair token" {
		t.Errorf("unknown token: err=%v want 'invalid pair token'", err)
	}

	// Expired pair token.
	old, err := s.CreatePairToken(-time.Minute)
	if err != nil {
		t.Fatalf("CreatePairToken expired: %v", err)
	}
	if err := s.ConsumePairToken(old); err == nil || err.Error() != "pair token expired" {
		t.Errorf("expired token: err=%v want 'pair token expired'", err)
	}
}
