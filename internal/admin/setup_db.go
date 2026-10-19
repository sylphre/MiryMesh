// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"database/sql"
	"strings"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// SetupStoreDB is the DB-backed implementation. Use when db is configured.
// It replaces file-based SetupStore (setup.json) per M14.
type SetupStoreDB struct {
	db        *sql.DB
	driver    string
	masterKey string // raw MIRYMESH_MASTER_KEY / auth.master_key
}

// NewSetupStoreDB creates a DB-backed store. masterKey is required.
func NewSetupStoreDB(db *sql.DB, driver, masterKey string) *SetupStoreDB {
	return &SetupStoreDB{db: db, driver: strings.ToLower(driver), masterKey: masterKey}
}

// IsInitialized reports whether miry_setup row id=1 exists and initialized.
func (s *SetupStoreDB) IsInitialized() bool {
	var ok bool
	// Try ? then $1 for compat
	err := s.db.QueryRow(`SELECT initialized FROM miry_setup WHERE id=1`).Scan(&ok)
	if err != nil {
		_ = s.db.QueryRow(`SELECT initialized FROM miry_setup WHERE id=$1`, 1).Scan(&ok)
		if err != nil {
			return false
		}
	}
	return ok
}

// Get returns decrypted SetupState.
func (s *SetupStoreDB) Get() *SetupState {
	var st SetupState
	var skEnc, dsnEnc string
	var initBool sql.NullBool
	// Try placeholder ?
	err := s.db.QueryRow(`SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM miry_setup WHERE id=1`).Scan(
		&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &st.AdminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
	)
	if err != nil {
		_ = s.db.QueryRow(`SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM miry_setup WHERE id=$1`, 1).Scan(
			&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &st.AdminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
		)
		if err != nil {
			return nil
		}
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

// Save inserts miry_setup id=1. Fails if already initialized (409).
func (s *SetupStoreDB) Save(st *SetupState) error {
	if s.IsInitialized() {
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
	_, err = s.db.Exec(`INSERT INTO miry_setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true)`,
		st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	if err != nil {
		_, err = s.db.Exec(`INSERT INTO miry_setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, true)`,
			st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	}
	return err
}

// UpdatePolicy updates miry_setup.default_policy.
func (s *SetupStoreDB) UpdatePolicy(policy string) error {
	_, err := s.db.Exec(`UPDATE miry_setup SET default_policy=?, updated_at=now() WHERE id=1`, policy)
	if err != nil {
		_, err = s.db.Exec(`UPDATE miry_setup SET default_policy=$1, updated_at=now() WHERE id=1`, policy)
	}
	return err
}

var _ SetupStoreIface = (*SetupStoreDB)(nil)
// File store satisfies the same interface (asserted in handler.go).
