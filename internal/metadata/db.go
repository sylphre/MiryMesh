// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies pending migrations. Driver is "postgres" or "mysql"/"mariadb".
// Uses a simple ad-hoc runner (no goose/golang-migrate): tracks miry_schema_migrations.
func Migrate(db *sql.DB, driver string) error {
	driver = strings.ToLower(driver)
	isPG := driver == "postgres" || driver == "postgresql"
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	// Ensure migrations table (dialect-agnostic: keep TEXT/int for compat)
	ddl := `CREATE TABLE IF NOT EXISTS miry_schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`
	if !isPG {
		ddl = strings.ReplaceAll(ddl, "TIMESTAMPTZ", "TIMESTAMP")
	}
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	for _, name := range entries {
		var cnt int
		if err := db.QueryRow(`SELECT COUNT(*) FROM miry_schema_migrations WHERE filename=?`, name).Scan(&cnt); err != nil {
			// Try $1 placeholder for Postgres
			if err2 := db.QueryRow(`SELECT COUNT(*) FROM miry_schema_migrations WHERE filename=$1`, name).Scan(&cnt); err2 != nil {
				return fmt.Errorf("check migration %s: %w / %w", name, err, err2)
			}
		}
		if cnt > 0 {
			continue
		}
		raw, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return err
		}
		sqlText := string(raw)
		if !isPG {
			sqlText = toMySQLDialect(sqlText)
		}
		if _, err := db.Exec(sqlText); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := db.Exec(`INSERT INTO miry_schema_migrations(filename) VALUES (?)`, name); err != nil {
			_, _ = db.Exec(`INSERT INTO miry_schema_migrations(filename) VALUES ($1)`, name)
		}
	}
	return nil
}

func toMySQLDialect(s string) string {
	s = strings.ReplaceAll(s, "JSONB", "JSON")
	s = strings.ReplaceAll(s, "TIMESTAMPTZ", "TIMESTAMP")
	return s
}

// --- Master key / AES-GCM at-rest encryption ---

// MasterKeyBytes derives 32-byte AES key from MIRYMESH_MASTER_KEY / auth.master_key.
func MasterKeyBytes(masterKey string) ([]byte, error) {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" {
		return nil, errors.New("MIRYMESH_MASTER_KEY (or auth.master_key) must be set — required for secret_key_enc/dsn_enc")
	}
	// Accept 32/64 hex or raw 32 bytes base64 or plain 32+ chars (argon2 derive to 32)
	if b, err := hex.DecodeString(masterKey); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(masterKey); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(masterKey) >= 32 {
		// Derive 32 via SHA256? But need stable: use argon2 with fixed salt? Simpler: SHA256(masterKey) as 32
		h := sha256.Sum256([]byte(masterKey))
		return h[:], nil
	}
	return nil, errors.New("master key must be 32-byte hex/base64 or >=32 chars")
}

// EncryptGCM encrypts with AES-GCM + random nonce.
func EncryptGCM(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptGCM decrypts AES-GCM.
func DecryptGCM(key []byte, ciphertextB64 string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := raw[:nonceSize], raw[nonceSize:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// OpenDB opens the DB from config and returns *sql.DB. Caller must Close.
// driver is "postgres" or "mysql".
func OpenDB(driver, dsn string) (*sql.DB, error) {
	switch strings.ToLower(driver) {
	case "postgres", "postgresql":
		// import side-effect via blank import? Caller should import driver.
		return sql.Open("postgres", dsn)
	case "mysql", "mariadb":
		return sql.Open("mysql", dsn)
	default:
		return nil, fmt.Errorf("unsupported driver %q", driver)
	}
}
