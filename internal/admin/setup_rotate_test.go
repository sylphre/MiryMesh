// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

const (
	rotOldKey = "0123456789abcdef0123456789abcdef"
	rotNewKey = "fedcba9876543210fedcba9876543210"
)

func openRotateDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func seedRotateSetup(t *testing.T, s *SetupStoreDB) {
	t.Helper()
	if err := s.Save(&SetupState{
		AdminUsername: "admin", AdminPasswordHash: "hash", AdminToken: "tok",
		DefaultPolicy: "single", Region: "us-east-1",
	}); err != nil {
		t.Fatalf("Save step 1: %v", err)
	}
	if err := s.Save(&SetupState{
		AdminUsername: "admin", AccessKey: "AKIATEST", SecretKey: "s3cr3t-value",
		DefaultPolicy: "single", Region: "us-east-1", DBDriver: "mysql",
		DBDSN: "u:p@tcp(db:3306)/mirymesh",
	}); err != nil {
		t.Fatalf("Save step 2: %v", err)
	}
}

func rawSecrets(t *testing.T, db *sql.DB) (sk, dsn string) {
	t.Helper()
	if err := db.QueryRow(`SELECT secret_key_enc, db_dsn_enc FROM setup WHERE id=1`).Scan(&sk, &dsn); err != nil {
		t.Fatal(err)
	}
	return sk, dsn
}

func keyIDOf(t *testing.T, key string) string {
	t.Helper()
	kr, err := metadata.NewKeyring(key)
	if err != nil {
		t.Fatal(err)
	}
	return kr.CurrentKeyID()
}

func TestRotateMasterKey(t *testing.T) {
	db := openRotateDB(t)
	oldStore := NewSetupStoreDB(db, "sqlite", rotOldKey)
	seedRotateSetup(t, oldStore)

	oldPrefix := "mk1:" + keyIDOf(t, rotOldKey) + ":"
	newPrefix := "mk1:" + keyIDOf(t, rotNewKey) + ":"
	sk, dsn := rawSecrets(t, db)
	if !strings.HasPrefix(sk, oldPrefix) || !strings.HasPrefix(dsn, oldPrefix) {
		t.Fatalf("secrets must be sealed with the old key: %q %q", sk, dsn)
	}

	newStore := NewSetupStoreDB(db, "sqlite", rotNewKey, rotOldKey)
	if got := newStore.Get(); got == nil || got.SecretKey != "s3cr3t-value" || got.DBDSN != "u:p@tcp(db:3306)/mirymesh" {
		t.Fatalf("old values must stay readable before rotation: %+v", got)
	}

	rep, err := newStore.RotateMasterKey(true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if want := []string{"secret_key_enc", "db_dsn_enc"}; !reflect.DeepEqual(rep.Rotated, want) {
		t.Fatalf("dry run Rotated = %v, want %v", rep.Rotated, want)
	}
	if sk2, dsn2 := rawSecrets(t, db); sk2 != sk || dsn2 != dsn {
		t.Fatal("dry run must not write")
	}

	rep, err = newStore.RotateMasterKey(false)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rep.KeyID != keyIDOf(t, rotNewKey) || len(rep.Rotated) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	sk, dsn = rawSecrets(t, db)
	if !strings.HasPrefix(sk, newPrefix) || !strings.HasPrefix(dsn, newPrefix) {
		t.Fatalf("secrets must be sealed with the new key: %q %q", sk, dsn)
	}

	if got := NewSetupStoreDB(db, "sqlite", rotNewKey).Get(); got == nil || got.SecretKey != "s3cr3t-value" || got.DBDSN != "u:p@tcp(db:3306)/mirymesh" {
		t.Fatalf("new key alone must read rotated values: %+v", got)
	}
	if got := oldStore.Get(); got == nil || got.SecretKey != "" {
		t.Fatalf("old key alone must no longer decrypt: %+v", got)
	}

	rep, err = newStore.RotateMasterKey(false)
	if err != nil {
		t.Fatalf("second rotate: %v", err)
	}
	if len(rep.Rotated) != 0 || len(rep.Current) != 2 {
		t.Fatalf("rotation must be idempotent: %+v", rep)
	}
}

func TestRotateMasterKeyLegacyCiphertext(t *testing.T) {
	db := openRotateDB(t)
	s := NewSetupStoreDB(db, "sqlite", rotOldKey)
	seedRotateSetup(t, s)

	raw, err := metadata.MasterKeyBytes(rotOldKey)
	if err != nil {
		t.Fatal(err)
	}
	legacySK, err := metadata.EncryptGCM(raw, "s3cr3t-value")
	if err != nil {
		t.Fatal(err)
	}
	legacyDSN, err := metadata.EncryptGCM(raw, "u:p@tcp(db:3306)/mirymesh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE setup SET secret_key_enc=?, db_dsn_enc=? WHERE id=1`, legacySK, legacyDSN); err != nil {
		t.Fatal(err)
	}

	newStore := NewSetupStoreDB(db, "sqlite", rotNewKey, rotOldKey)
	if got := newStore.Get(); got == nil || got.SecretKey != "s3cr3t-value" {
		t.Fatalf("legacy value must be readable with the old key: %+v", got)
	}
	if _, err = newStore.RotateMasterKey(false); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	sk, dsn := rawSecrets(t, db)
	newPrefix := "mk1:" + keyIDOf(t, rotNewKey) + ":"
	if !strings.HasPrefix(sk, newPrefix) || !strings.HasPrefix(dsn, newPrefix) {
		t.Fatalf("legacy values must be re-sealed: %q %q", sk, dsn)
	}
}

func TestRotateMasterKeyUnknownKeyLeavesDataUntouched(t *testing.T) {
	db := openRotateDB(t)
	seedRotateSetup(t, NewSetupStoreDB(db, "sqlite", rotOldKey))
	sk, dsn := rawSecrets(t, db)

	// The old key was not configured, so the rotation cannot read the stored values.
	_, err := NewSetupStoreDB(db, "sqlite", rotNewKey).RotateMasterKey(false)
	if !errors.Is(err, metadata.ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
	if sk2, dsn2 := rawSecrets(t, db); sk2 != sk || dsn2 != dsn {
		t.Fatal("a failed rotation must not modify the data")
	}
}

func TestRotateMasterKeyWithoutSetupRow(t *testing.T) {
	db := openRotateDB(t)
	rep, err := NewSetupStoreDB(db, "sqlite", rotNewKey).RotateMasterKey(false)
	if err != nil {
		t.Fatalf("rotate on empty db: %v", err)
	}
	if len(rep.Rotated) != 0 || len(rep.Current) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}
