// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// GetNodeToken returns the random token issued to nodeID. It reports false when the
// node has none yet or when the stored value cannot be decrypted (wrong master key);
// the second case is logged, and the node can recover by registering again.
func (s *SetupStoreDB) GetNodeToken(nodeID string) (string, bool) {
	if nodeID == "" {
		return "", false
	}
	var enc string
	err := s.db.QueryRow(`SELECT token_enc FROM node_tokens WHERE node_id=`+s.ph(1), nodeID).Scan(&enc)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("node token: read failed", "node", nodeID, "err", err)
		}
		return "", false
	}
	kr, err := s.keyring()
	if err != nil {
		slog.Warn("node token: master key is invalid", "err", err)
		return "", false
	}
	tok, err := kr.Decrypt(enc)
	if err != nil || tok == "" {
		slog.Warn("node token: cannot decrypt, check MIRYMESH_MASTER_KEY / old keys", "node", nodeID, "err", err)
		return "", false
	}
	return tok, true
}

// PutNodeToken stores token for nodeID, sealed with the current master key. An
// existing token of the node is replaced.
func (s *SetupStoreDB) PutNodeToken(nodeID, token string) error {
	if nodeID == "" || token == "" {
		return errors.New("node id and token are required")
	}
	kr, err := s.keyring()
	if err != nil {
		return err
	}
	enc, err := kr.Encrypt(token)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`DELETE FROM node_tokens WHERE node_id=`+s.ph(1), nodeID); err != nil {
		return fmt.Errorf("replace node token: %w", err)
	}
	if _, err = tx.Exec(fmt.Sprintf(`INSERT INTO node_tokens(node_id, token_enc) VALUES (%s, %s)`, s.ph(1), s.ph(2)), nodeID, enc); err != nil {
		return fmt.Errorf("store node token: %w", err)
	}
	return tx.Commit()
}

type nodeTokenUpdate struct {
	nodeID, oldEnc, newEnc string
}

// planNodeTokenRotation lists the node tokens that are not sealed with the current
// master key yet. total is the number of stored node tokens.
func (s *SetupStoreDB) planNodeTokenRotation(tx *sql.Tx, kr *metadata.Keyring) (updates []nodeTokenUpdate, total int, err error) {
	rows, err := tx.Query(`SELECT node_id, token_enc FROM node_tokens`)
	if err != nil {
		return nil, 0, err
	}
	type row struct{ id, enc string }
	var all []row
	for rows.Next() {
		var r row
		if serr := rows.Scan(&r.id, &r.enc); serr != nil {
			_ = rows.Close()
			return nil, 0, serr
		}
		all = append(all, r)
	}
	rerr := rows.Err()
	_ = rows.Close()
	if rerr != nil {
		return nil, 0, rerr
	}
	for _, r := range all {
		nv, rotated, verr := rotateValue(kr, r.enc)
		if verr != nil {
			return nil, 0, fmt.Errorf("node %q: %w", r.id, verr)
		}
		if rotated {
			updates = append(updates, nodeTokenUpdate{nodeID: r.id, oldEnc: r.enc, newEnc: nv})
		}
	}
	return updates, len(all), nil
}

// applyNodeTokenRotation writes the re-sealed node tokens, refusing to overwrite a
// row that changed in the meantime.
func (s *SetupStoreDB) applyNodeTokenRotation(tx *sql.Tx, updates []nodeTokenUpdate) error {
	for _, u := range updates {
		res, err := tx.Exec(fmt.Sprintf(`UPDATE node_tokens SET token_enc=%s WHERE node_id=%s AND token_enc=%s`,
			s.ph(1), s.ph(2), s.ph(3)), u.newEnc, u.nodeID, u.oldEnc)
		if err != nil {
			return fmt.Errorf("update node_tokens: %w", err)
		}
		if n, nerr := res.RowsAffected(); nerr == nil && n != 1 {
			return errors.New("node_tokens row changed concurrently; nothing was rotated, run again")
		}
	}
	return nil
}
