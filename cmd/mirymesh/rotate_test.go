// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/admin"
	"github.com/sylphre/MiryMesh/internal/metadata"
)

const (
	cliOldKey = "0123456789abcdef0123456789abcdef"
	cliNewKey = "fedcba9876543210fedcba9876543210"
)

// seedCLIDB creates a SQLite database whose secrets are sealed with cliOldKey.
func seedCLIDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := metadata.OpenDB("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s := admin.NewSetupStoreDB(db, "sqlite", cliOldKey)
	if err = s.Save(&admin.SetupState{
		AdminUsername: "admin", AdminPasswordHash: "hash", AdminToken: "tok",
		DefaultPolicy: "single", Region: "us-east-1",
	}); err != nil {
		t.Fatalf("Save step 1: %v", err)
	}
	if err = s.Save(&admin.SetupState{
		AdminUsername: "admin", AccessKey: "AKIATEST", SecretKey: "s3cr3t-value",
		DefaultPolicy: "single", Region: "us-east-1", DBDriver: "mysql",
		DBDSN: "u:p@tcp(db:3306)/mirymesh",
	}); err != nil {
		t.Fatalf("Save step 2: %v", err)
	}
}

func writeCLIConfig(t *testing.T, dir, dbPath, extraAuth string) string {
	t.Helper()
	cfg := fmt.Sprintf("db:\n  driver: sqlite\n  dsn: %q\nauth:\n  master_key: %q\n%s", dbPath, cliNewKey, extraAuth)
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func cliSecret(t *testing.T, dbPath, key string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	got := admin.NewSetupStoreDB(db, "sqlite", key).Get()
	if got == nil {
		t.Fatal("setup state is nil")
	}
	return got.SecretKey
}

func clearMasterKeyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MIRYMESH_MASTER_KEY", "MIRYMESH_MASTER_KEY_OLD", "MIRYMESH_DB_DRIVER", "MIRYMESH_DB_DSN"} {
		t.Setenv(k, "")
	}
}

func TestRunRotateMasterKey(t *testing.T) {
	clearMasterKeyEnv(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mirymesh.db")
	seedCLIDB(t, dbPath)
	cfgPath := writeCLIConfig(t, dir, dbPath, fmt.Sprintf("  old_master_keys:\n    - %q\n", cliOldKey))

	var out, errOut bytes.Buffer
	if code := runRotateMasterKey([]string{"--config", cfgPath, "--dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "would re-encrypt: secret_key_enc, db_dsn_enc") {
		t.Fatalf("dry run output = %q", out.String())
	}
	if got := cliSecret(t, dbPath, cliNewKey); got != "" {
		t.Fatalf("dry run must not write; new key alone already reads %q", got)
	}

	out.Reset()
	errOut.Reset()
	if code := runRotateMasterKey([]string{"--config", cfgPath}, &out, &errOut); code != 0 {
		t.Fatalf("rotate exit=%d stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "re-encrypted: secret_key_enc, db_dsn_enc") {
		t.Fatalf("rotate output = %q", out.String())
	}
	if got := cliSecret(t, dbPath, cliNewKey); got != "s3cr3t-value" {
		t.Fatalf("new key alone must read the rotated secret, got %q", got)
	}
	if got := cliSecret(t, dbPath, cliOldKey); got != "" {
		t.Fatalf("old key alone must no longer decrypt, got %q", got)
	}
}

func TestRunRotateMasterKeyFailsWithoutOldKey(t *testing.T) {
	clearMasterKeyEnv(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mirymesh.db")
	seedCLIDB(t, dbPath)
	cfgPath := writeCLIConfig(t, dir, dbPath, "")

	var out, errOut bytes.Buffer
	if code := runRotateMasterKey([]string{"--config", cfgPath}, &out, &errOut); code == 0 {
		t.Fatal("rotation without the old key must fail")
	}
	if !strings.Contains(errOut.String(), "not configured") {
		t.Fatalf("stderr = %q", errOut.String())
	}
	if got := cliSecret(t, dbPath, cliOldKey); got != "s3cr3t-value" {
		t.Fatalf("a failed rotation must leave data readable with the old key, got %q", got)
	}
}

func TestRunRotateMasterKeyRequiresDBAndKey(t *testing.T) {
	clearMasterKeyEnv(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runRotateMasterKey([]string{"--config", cfgPath}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "db.driver") {
		t.Fatalf("missing db: code=%d stderr=%q", code, errOut.String())
	}

	dbPath := filepath.Join(dir, "mirymesh.db")
	noKey := fmt.Sprintf("db:\n  driver: sqlite\n  dsn: %q\n", dbPath)
	if err := os.WriteFile(cfgPath, []byte(noKey), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut.Reset()
	if code := runRotateMasterKey([]string{"--config", cfgPath}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "new master key is required") {
		t.Fatalf("missing key: code=%d stderr=%q", code, errOut.String())
	}
}
