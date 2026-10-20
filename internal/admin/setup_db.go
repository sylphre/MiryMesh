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
	if err := s.db.QueryRow(`SELECT initialized FROM miry_setup WHERE id=1`).Scan(&ok); err == nil {
		return ok
	}
	if err := s.db.QueryRow(`SELECT initialized FROM miry_setup WHERE id=$1`, 1).Scan(&ok); err == nil {
		return ok
	}
	return false
}

// Get returns decrypted SetupState.
func (s *SetupStoreDB) Get() *SetupState {
	var st SetupState
	var skEnc, dsnEnc string
	var initBool sql.NullBool
	// Try placeholder ?
	if err := s.db.QueryRow(`SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM miry_setup WHERE id=1`).Scan(
		&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &st.AdminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
	); err != nil {
		if err := s.db.QueryRow(`SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM miry_setup WHERE id=$1`, 1).Scan(
			&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &st.AdminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
		); err != nil {
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
// If that was initialized via file (setup.json) with empty credentials, Update merges S3/DB fields.
func (s *SetupStoreDB) Save(st *SetupState) error {
	if s.IsInitialized() {
		// If existing row has empty access_key (Step1-only file), allow Update to fill S3 fields.
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
	_, err = s.db.Exec(`INSERT INTO miry_setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true)`,
		st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	if err != nil {
		_, err = s.db.Exec(`INSERT INTO miry_setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, true)`,
			st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	}
	return err
}

// Update merges S3/DB fields when POST /api/admin/setup is called after initialization (Step2).
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
	_, err = s.db.Exec(`UPDATE miry_setup SET access_key=?, secret_key_enc=?, default_policy=?, region=?, gateway=?, datanode_paths=?, db_driver=?, db_dsn_enc=?, updated_at=now() WHERE id=1`, st.AccessKey, skEnc, st.DefaultPolicy, st.Region, st.Gateway, st.DatanodePaths, st.DBDriver, dsnEnc)
	if err != nil {
		_, err = s.db.Exec(`UPDATE miry_setup SET access_key=$1, secret_key_enc=$2, default_policy=$3, region=$4, gateway=$5, datanode_paths=$6, db_driver=$7, db_dsn_enc=$8, updated_at=now() WHERE id=1`, st.AccessKey, skEnc, st.DefaultPolicy, st.Region, st.Gateway, st.DatanodePaths, st.DBDriver, dsnEnc)
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
