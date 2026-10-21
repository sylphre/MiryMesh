// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// SetupStoreDB is the DB-backed implementation (DB name mirymesh, tables: setup/sessions/pair_tokens).
type SetupStoreDB struct {
	db        *sql.DB
	driver    string
	masterKey string
}

// NewSetupStoreDB creates a DB-backed store. masterKey is required for at-rest encryption.
func NewSetupStoreDB(db *sql.DB, driver, masterKey string) *SetupStoreDB {
	return &SetupStoreDB{db: db, driver: strings.ToLower(driver), masterKey: masterKey}
}

// IsInitialized reports whether setup row id=1 exists and initialized.
func (s *SetupStoreDB) IsInitialized() bool {
	var ok bool
	if err := s.db.QueryRow(`SELECT initialized FROM setup WHERE id=1`).Scan(&ok); err == nil {
		return ok
	}
	if err := s.db.QueryRow(`SELECT initialized FROM setup WHERE id=$1`, 1).Scan(&ok); err == nil {
		return ok
	}
	return false
}

// Get returns decrypted SetupState.
func (s *SetupStoreDB) Get() *SetupState {
	var st SetupState
	var skEnc, dsnEnc string
	var initBool sql.NullBool
	var adminToken sql.NullString
	// Try ? placeholder (mysql/sqlite), fallback to $1 (postgres)
	if err := s.db.QueryRow(`SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM setup WHERE id=1`).Scan(
		&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &adminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
	); err != nil {
		if err := s.db.QueryRow(`SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM setup WHERE id=$1`, 1).Scan(
			&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &adminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
		); err != nil {
			return nil
		}
	}
	if adminToken.Valid {
		st.AdminToken = adminToken.String
	}
	if initBool.Valid {
		st.Initialized = initBool.Bool
	} else {
		st.Initialized = true
	}
	mk, err := metadata.MasterKeyBytes(s.masterKey)
	if err == nil {
		if pt, err := metadata.DecryptGCM(mk, skEnc); err == nil {
			st.SecretKey = pt
		}
		if dsnEnc != "" {
			if pt, err := metadata.DecryptGCM(mk, dsnEnc); err == nil {
				st.DBDSN = pt
			}
		}
	}
	return &st
}

// Save inserts setup id=1. Fails if already initialized.
func (s *SetupStoreDB) Save(st *SetupState) error {
	if s.IsInitialized() {
		if cur := s.Get(); cur != nil && strings.TrimSpace(cur.AccessKey) == "" && strings.TrimSpace(st.AccessKey) != "" {
			return s.Update(st)
		}
		return errAlreadyInitialized
	}
	mk, err := metadata.MasterKeyBytes(s.masterKey)
	if err != nil {
		return err
	}
	skEnc, err := metadata.EncryptGCM(mk, st.SecretKey)
	if err != nil {
		return err
	}
	dsnEnc := ""
	if st.DBDSN != "" {
		dsnEnc, err = metadata.EncryptGCM(mk, st.DBDSN)
		if err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`INSERT INTO setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true)`,
		st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	if err != nil {
		_, err = s.db.Exec(`INSERT INTO setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, true)`,
			st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	}
	return err
}

// Update merges fields when POST /api/admin/setup is called after initialization (Step2).
func (s *SetupStoreDB) Update(st *SetupState) error {
	mk, err := metadata.MasterKeyBytes(s.masterKey)
	if err != nil {
		return err
	}
	skEnc, err := metadata.EncryptGCM(mk, st.SecretKey)
	if err != nil {
		return err
	}
	dsnEnc := ""
	if st.DBDSN != "" {
		dsnEnc, err = metadata.EncryptGCM(mk, st.DBDSN)
		if err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`UPDATE setup SET access_key=?, secret_key_enc=?, default_policy=?, region=?, gateway=?, datanode_paths=?, db_driver=?, db_dsn_enc=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`, st.AccessKey, skEnc, st.DefaultPolicy, st.Region, st.Gateway, st.DatanodePaths, st.DBDriver, dsnEnc)
	if err != nil {
		_, err = s.db.Exec(`UPDATE setup SET access_key=$1, secret_key_enc=$2, default_policy=$3, region=$4, gateway=$5, datanode_paths=$6, db_driver=$7, db_dsn_enc=$8, updated_at=now() WHERE id=1`, st.AccessKey, skEnc, st.DefaultPolicy, st.Region, st.Gateway, st.DatanodePaths, st.DBDriver, dsnEnc)
		// sqlite fallback with CURRENT_TIMESTAMP
		if err != nil {
			_, err = s.db.Exec(`UPDATE setup SET access_key=?, secret_key_enc=?, default_policy=?, region=?, gateway=?, datanode_paths=?, db_driver=?, db_dsn_enc=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`, st.AccessKey, skEnc, st.DefaultPolicy, st.Region, st.Gateway, st.DatanodePaths, st.DBDriver, dsnEnc)
		}
	}
	return err
}

// UpdatePolicy updates setup.default_policy.
func (s *SetupStoreDB) UpdatePolicy(policy string) error {
	_, err := s.db.Exec(`UPDATE setup SET default_policy=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`, policy)
	if err != nil {
		_, err = s.db.Exec(`UPDATE setup SET default_policy=$1, updated_at=now() WHERE id=1`, policy)
	}
	return err
}

// --- Sessions (token_hash SHA256) ---

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateSession stores a hashed token. ttl e.g. 24h.
func (s *SetupStoreDB) CreateSession(token, username string, ttl time.Duration) error {
	th := hashToken(token)
	exp := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash, admin_username, expires_at) VALUES (?, ?, ?)`, th, username, exp)
	if err != nil {
		_, err = s.db.Exec(`INSERT INTO sessions(token_hash, admin_username, expires_at) VALUES ($1,$2,$3)`, th, username, exp)
	}
	return err
}

// ValidateSession checks token_hash exists, not revoked, not expired.
func (s *SetupStoreDB) ValidateSession(token string) bool {
	th := hashToken(token)
	var revoked bool
	var exp time.Time
	if err := s.db.QueryRow(`SELECT revoked, expires_at FROM sessions WHERE token_hash=?`, th).Scan(&revoked, &exp); err != nil {
		if err := s.db.QueryRow(`SELECT revoked, expires_at FROM sessions WHERE token_hash=$1`, th).Scan(&revoked, &exp); err != nil {
			return false
		}
	}
	if revoked {
		return false
	}
	if time.Now().After(exp) {
		return false
	}
	return true
}

// RevokeSession marks token revoked.
func (s *SetupStoreDB) RevokeSession(token string) error {
	th := hashToken(token)
	_, err := s.db.Exec(`UPDATE sessions SET revoked=true WHERE token_hash=?`, th)
	if err != nil {
		_, err = s.db.Exec(`UPDATE sessions SET revoked=true WHERE token_hash=$1`, th)
	}
	return err
}

// --- Pair tokens (one-time datanode pairing, 15m) ---

func (s *SetupStoreDB) CreatePairToken(ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	th := hashToken(token)
	exp := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(`INSERT INTO pair_tokens(token_hash, expires_at) VALUES (?, ?)`, th, exp)
	if err != nil {
		_, err = s.db.Exec(`INSERT INTO pair_tokens(token_hash, expires_at) VALUES ($1,$2)`, th, exp)
	}
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *SetupStoreDB) ConsumePairToken(token string) error {
	th := hashToken(token)
	var used bool
	var exp time.Time
	if err := s.db.QueryRow(`SELECT used, expires_at FROM pair_tokens WHERE token_hash=?`, th).Scan(&used, &exp); err != nil {
		if err := s.db.QueryRow(`SELECT used, expires_at FROM pair_tokens WHERE token_hash=$1`, th).Scan(&used, &exp); err != nil {
			return fmt.Errorf("invalid pair token")
		}
	}
	if used {
		return fmt.Errorf("pair token already used")
	}
	if time.Now().After(exp) {
		return fmt.Errorf("pair token expired")
	}
	_, err := s.db.Exec(`UPDATE pair_tokens SET used=true WHERE token_hash=?`, th)
	if err != nil {
		_, err = s.db.Exec(`UPDATE pair_tokens SET used=true WHERE token_hash=$1`, th)
	}
	return err
}

var _ SetupStoreIface = (*SetupStoreDB)(nil)
