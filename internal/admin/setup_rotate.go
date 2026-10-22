// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// RotateReport describes the outcome of RotateMasterKey.
type RotateReport struct {
	KeyID   string   // fingerprint of the current master key
	Rotated []string // columns re-encrypted (or that would be, on a dry run)
	Current []string // columns already sealed with the current key
}

// keyring builds the keyring from the current master key and the retired keys.
func (s *SetupStoreDB) keyring() (*metadata.Keyring, error) {
	return metadata.NewKeyring(s.masterKey, s.oldKeys...)
}

// RotateMasterKey re-encrypts setup.secret_key_enc and setup.db_dsn_enc with the
// current master key. Retired keys must be configured as old keys so that existing
// values can be read. It runs in one transaction and refuses to overwrite a row
// that changed concurrently. With dryRun nothing is written.
func (s *SetupStoreDB) RotateMasterKey(dryRun bool) (*RotateReport, error) {
	kr, err := s.keyring()
	if err != nil {
		return nil, err
	}
	rep := &RotateReport{KeyID: kr.CurrentKeyID()}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var skEnc string
	var dsnEnc sql.NullString
	err = tx.QueryRow(`SELECT secret_key_enc, db_dsn_enc FROM setup WHERE id=`+s.ph(1), 1).Scan(&skEnc, &dsnEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return rep, nil // nothing set up yet, nothing to rotate
	}
	if err != nil {
		return nil, fmt.Errorf("read setup: %w", err)
	}

	newSK, skRotated, err := rotateValue(kr, skEnc)
	if err != nil {
		return nil, fmt.Errorf("secret_key_enc: %w", err)
	}
	record(rep, "secret_key_enc", skRotated)

	newDSN, dsnRotated := dsnEnc.String, false
	if dsnEnc.Valid && dsnEnc.String != "" {
		newDSN, dsnRotated, err = rotateValue(kr, dsnEnc.String)
		if err != nil {
			return nil, fmt.Errorf("db_dsn_enc: %w", err)
		}
		record(rep, "db_dsn_enc", dsnRotated)
	}

	if dryRun || (!skRotated && !dsnRotated) {
		return rep, nil
	}

	var dsnArg interface{} // keep NULL as NULL
	if dsnEnc.Valid {
		dsnArg = newDSN
	}
	res, err := tx.Exec(fmt.Sprintf(
		`UPDATE setup SET secret_key_enc=%s, db_dsn_enc=%s, updated_at=CURRENT_TIMESTAMP WHERE id=%s AND secret_key_enc=%s`,
		s.ph(1), s.ph(2), s.ph(3), s.ph(4)), newSK, dsnArg, 1, skEnc)
	if err != nil {
		return nil, fmt.Errorf("update setup: %w", err)
	}
	if n, nerr := res.RowsAffected(); nerr == nil && n != 1 {
		return nil, errors.New("setup row changed concurrently; nothing was rotated, run again")
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return rep, nil
}

func record(rep *RotateReport, column string, rotated bool) {
	if rotated {
		rep.Rotated = append(rep.Rotated, column)
	} else {
		rep.Current = append(rep.Current, column)
	}
}

// rotateValue re-seals v with the current key when it is not already.
func rotateValue(kr *metadata.Keyring, v string) (string, bool, error) {
	if v == "" || !kr.NeedsRotation(v) {
		return v, false, nil
	}
	pt, err := kr.Decrypt(v)
	if err != nil {
		return "", false, err
	}
	nv, err := kr.Encrypt(pt)
	if err != nil {
		return "", false, err
	}
	return nv, true, nil
}
