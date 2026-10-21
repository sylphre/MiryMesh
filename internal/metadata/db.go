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

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// normalizeDriver returns canonical driver name: postgres/mysql/sqlite.
func normalizeDriver(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if d == "postgresql" {
		return "postgres"
	}
	if d == "mariadb" {
		return "mysql"
	}
	if d == "sqlite3" {
		return "sqlite"
	}
	return d
}

// Migrate applies pending migrations. Driver is postgres/mysql/sqlite.
func Migrate(db *sql.DB, driver string) error {
	driver = normalizeDriver(driver)
	isPG := driver == "postgres"
	isSQLite := driver == "sqlite"
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	ddl := `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`
	if isSQLite {
		ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (filename TEXT PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`
	} else if !isPG {
		ddl = strings.ReplaceAll(ddl, "TIMESTAMPTZ", "TIMESTAMP")
	}
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	ph := "?"
	if isPG {
		ph = "$1"
	}
	for _, name := range entries {
		var cnt int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename=`+ph, name).Scan(&cnt); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if cnt > 0 {
			continue
		}
		raw, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			return err
		}
		sqlText := string(raw)
		if isSQLite {
			sqlText = toSqliteDialect(sqlText)
		} else if !isPG {
			sqlText = toMySQLDialect(sqlText)
		}
		if _, err := db.Exec(sqlText); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(filename) VALUES (`+ph+`)`, name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

func toMySQLDialect(s string) string {
	s = strings.ReplaceAll(s, "JSONB", "JSON")
	s = strings.ReplaceAll(s, "TIMESTAMPTZ", "TIMESTAMP")
	return s
}

func toSqliteDialect(s string) string {
	s = strings.ReplaceAll(s, "JSONB", "JSON")
	s = strings.ReplaceAll(s, "TIMESTAMPTZ", "DATETIME")
	// now() -> CURRENT_TIMESTAMP, BOOLEAN -> INTEGER
	s = strings.ReplaceAll(s, "now()", "CURRENT_TIMESTAMP")
	s = strings.ReplaceAll(s, "BOOLEAN", "INTEGER")
	// SMALLINT PRIMARY KEY CHECK(id=1) -> INTEGER PRIMARY KEY CHECK(id=1) for sqlite
	s = strings.ReplaceAll(s, "SMALLINT PRIMARY KEY", "INTEGER PRIMARY KEY")
	return s
}

// --- Master key / AES-GCM at-rest encryption ---

// MasterKeyBytes derives 32-byte AES key from MIRYMESH_MASTER_KEY / auth.master_key.
// TODO: migrate to argon2.IDKey with stable salt when key rotation is designed.
func MasterKeyBytes(masterKey string) ([]byte, error) {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" {
		return nil, errors.New("MIRYMESH_MASTER_KEY (or auth.master_key) must be set — required for secret_key_enc/dsn_enc")
	}
	if b, err := hex.DecodeString(masterKey); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(masterKey); err == nil && len(b) == 32 {
		return b, nil
	}
	if len(masterKey) >= 32 {
		// TODO: use argon2.IDKey([]byte(masterKey), salt, 1, 64*1024, 4, 32) with stable salt
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
func OpenDB(driver, dsn string) (*sql.DB, error) {
	switch normalizeDriver(driver) {
	case "postgres":
		return sql.Open("postgres", dsn)
	case "mysql":
		return sql.Open("mysql", dsn)
	case "sqlite":
		return sql.Open("sqlite3", dsn)
	default:
		return nil, fmt.Errorf("unsupported driver %q", driver)
	}
}
