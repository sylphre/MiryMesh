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
// Sessions: token_hash SHA256, pair_tokens: one-time 15m.
//
// setup.admin_token は移行期間の legacy fallback。
// 将来は sessions(token_hash SHA256, expires_at, revoked) のみに正規化し、
// setup.admin_token カラムは削除する。現状は両方に書き、ValidateSession を正とする。
type SetupStoreDB struct {
	db        *sql.DB
	driver    string
	masterKey string
}

func NewSetupStoreDB(db *sql.DB, driver, masterKey string) *SetupStoreDB {
	return &SetupStoreDB{db: db, driver: strings.ToLower(driver), masterKey: masterKey}
}

func (s *SetupStoreDB) isPG() bool { return s.driver == "postgres" || s.driver == "postgresql" }
func (s *SetupStoreDB) ph(n int) string {
	if s.isPG() {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// IsInitialized reports whether setup row id=1 exists and initialized.
func (s *SetupStoreDB) IsInitialized() bool {
	var ok bool
	ph := s.ph(1)
	if err := s.db.QueryRow(fmt.Sprintf(`SELECT initialized FROM setup WHERE id=%s`, ph), 1).Scan(&ok); err == nil {
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
	q := `SELECT admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized FROM setup WHERE id=` + s.ph(1)
	if err := s.db.QueryRow(q, 1).Scan(
		&st.AdminUsername, &st.AdminPasswordHash, &st.AccessKey, &skEnc, &adminToken, &st.DefaultPolicy, &st.Region, &st.Gateway, &st.DBDriver, &dsnEnc, &st.DatanodePaths, &initBool,
	); err != nil {
		return nil
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
	ph1, ph2, ph3, ph4, ph5, ph6, ph7, ph8, ph9, ph10, ph11 := s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8), s.ph(9), s.ph(10), s.ph(11)
	_, err = s.db.Exec(fmt.Sprintf(`INSERT INTO setup(id, admin_username, admin_password_hash, access_key, secret_key_enc, admin_token, default_policy, region, gateway, db_driver, db_dsn_enc, datanode_paths, initialized) VALUES (1, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, true)`, ph1, ph2, ph3, ph4, ph5, ph6, ph7, ph8, ph9, ph10, ph11),
		st.AdminUsername, st.AdminPasswordHash, st.AccessKey, skEnc, st.AdminToken, st.DefaultPolicy, st.Region, st.Gateway, st.DBDriver, dsnEnc, st.DatanodePaths)
	return err
}

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
	// updated_at: postgres now(), sqlite CURRENT_TIMESTAMP, mysql CURRENT_TIMESTAMP all work via CURRENT_TIMESTAMP fallback
	_, err = s.db.Exec(fmt.Sprintf(`UPDATE setup SET access_key=%s, secret_key_enc=%s, default_policy=%s, region=%s, gateway=%s, datanode_paths=%s, db_driver=%s, db_dsn_enc=%s, updated_at=CURRENT_TIMESTAMP WHERE id=%s`, s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6), s.ph(7), s.ph(8), s.ph(9)),
		st.AccessKey, skEnc, st.DefaultPolicy, st.Region, st.Gateway, st.DatanodePaths, st.DBDriver, dsnEnc, 1)
	return err
}

func (s *SetupStoreDB) UpdatePolicy(policy string) error {
	_, err := s.db.Exec(fmt.Sprintf(`UPDATE setup SET default_policy=%s, updated_at=CURRENT_TIMESTAMP WHERE id=%s`, s.ph(1), s.ph(2)), policy, 1)
	return err
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *SetupStoreDB) CreateSession(token, username string, ttl time.Duration) error {
	th := hashToken(token)
	exp := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(fmt.Sprintf(`INSERT INTO sessions(token_hash, admin_username, expires_at) VALUES (%s, %s, %s)`, s.ph(1), s.ph(2), s.ph(3)), th, username, exp)
	return err
}

func (s *SetupStoreDB) ValidateSession(token string) bool {
	th := hashToken(token)
	var revoked bool
	var exp time.Time
	if err := s.db.QueryRow(fmt.Sprintf(`SELECT revoked, expires_at FROM sessions WHERE token_hash=%s`, s.ph(1)), th).Scan(&revoked, &exp); err != nil {
		return false
	}
	if revoked || time.Now().After(exp) {
		return false
	}
	return true
}

func (s *SetupStoreDB) RevokeSession(token string) error {
	th := hashToken(token)
	_, err := s.db.Exec(fmt.Sprintf(`UPDATE sessions SET revoked=true WHERE token_hash=%s`, s.ph(1)), th)
	return err
}

func (s *SetupStoreDB) CreatePairToken(ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	th := hashToken(token)
	exp := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(fmt.Sprintf(`INSERT INTO pair_tokens(token_hash, expires_at) VALUES (%s, %s)`, s.ph(1), s.ph(2)), th, exp)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *SetupStoreDB) ConsumePairToken(token string) error {
	th := hashToken(token)
	var used bool
	var exp time.Time
	if err := s.db.QueryRow(fmt.Sprintf(`SELECT used, expires_at FROM pair_tokens WHERE token_hash=%s`, s.ph(1)), th).Scan(&used, &exp); err != nil {
		return fmt.Errorf("invalid pair token")
	}
	if used {
		return fmt.Errorf("pair token already used")
	}
	if time.Now().After(exp) {
		return fmt.Errorf("pair token expired")
	}
	_, err := s.db.Exec(fmt.Sprintf(`UPDATE pair_tokens SET used=true WHERE token_hash=%s`, s.ph(1)), th)
	return err
}

var _ SetupStoreIface = (*SetupStoreDB)(nil)
