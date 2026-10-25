// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// SetupStoreDB keeps the DataNode registry in the datanodes and datanode_disks tables
// (migrations 0007 and 0008), so a Master restart does not empty the node list.
var _ datanode.NodeStore = (*SetupStoreDB)(nil)

// diskRowID is the primary key of a datanode_disks row.
func diskRowID(nodeID, path string) string {
	sum := sha256.Sum256([]byte(nodeID + "\x00" + path))
	return hex.EncodeToString(sum[:])
}

// SaveNode creates or replaces the row of a node.
func (s *SetupStoreDB) SaveNode(n datanode.Node) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`DELETE FROM datanodes WHERE node_id=`+s.ph(1), n.ID); err != nil {
		return fmt.Errorf("replace datanode: %w", err)
	}
	if _, err = tx.Exec(fmt.Sprintf(`INSERT INTO datanodes(node_id, addr, version) VALUES (%s, %s, %s)`,
		s.ph(1), s.ph(2), s.ph(3)), n.ID, n.Addr, n.Version); err != nil {
		return fmt.Errorf("store datanode: %w", err)
	}
	return tx.Commit()
}

// DeleteNode removes a node and its disks.
func (s *SetupStoreDB) DeleteNode(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`DELETE FROM datanode_disks WHERE node_id=`+s.ph(1), id); err != nil {
		return fmt.Errorf("delete datanode disks: %w", err)
	}
	if _, err = tx.Exec(`DELETE FROM datanodes WHERE node_id=`+s.ph(1), id); err != nil {
		return fmt.Errorf("delete datanode: %w", err)
	}
	return tx.Commit()
}

// SaveDisk creates or replaces the row of a disk.
func (s *SetupStoreDB) SaveDisk(d datanode.DiskPath) error {
	id := diskRowID(d.NodeID, d.Path)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`DELETE FROM datanode_disks WHERE disk_id=`+s.ph(1), id); err != nil {
		return fmt.Errorf("replace datanode disk: %w", err)
	}
	if _, err = tx.Exec(fmt.Sprintf(
		`INSERT INTO datanode_disks(disk_id, node_id, path, capacity, used, limit_bytes) VALUES (%s, %s, %s, %s, %s, %s)`,
		s.ph(1), s.ph(2), s.ph(3), s.ph(4), s.ph(5), s.ph(6)),
		id, d.NodeID, d.Path, d.Capacity, d.Used, d.Limit); err != nil {
		return fmt.Errorf("store datanode disk: %w", err)
	}
	return tx.Commit()
}

// DeleteDisk removes one disk row.
func (s *SetupStoreDB) DeleteDisk(nodeID, path string) error {
	_, err := s.db.Exec(`DELETE FROM datanode_disks WHERE disk_id=`+s.ph(1), diskRowID(nodeID, path))
	return err
}

// LoadNodes returns every stored node and disk.
func (s *SetupStoreDB) LoadNodes() ([]datanode.Node, []datanode.DiskPath, error) {
	rows, err := s.db.Query(`SELECT node_id, addr, version FROM datanodes`)
	if err != nil {
		return nil, nil, fmt.Errorf("load datanodes: %w", err)
	}
	var nodes []datanode.Node
	for rows.Next() {
		var n datanode.Node
		if serr := rows.Scan(&n.ID, &n.Addr, &n.Version); serr != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("load datanodes: %w", serr)
		}
		nodes = append(nodes, n)
	}
	rerr := rows.Err()
	_ = rows.Close()
	if rerr != nil {
		return nil, nil, fmt.Errorf("load datanodes: %w", rerr)
	}

	drows, err := s.db.Query(`SELECT node_id, path, capacity, used, limit_bytes FROM datanode_disks`)
	if err != nil {
		return nil, nil, fmt.Errorf("load datanode disks: %w", err)
	}
	var disks []datanode.DiskPath
	for drows.Next() {
		var d datanode.DiskPath
		if serr := drows.Scan(&d.NodeID, &d.Path, &d.Capacity, &d.Used, &d.Limit); serr != nil {
			_ = drows.Close()
			return nil, nil, fmt.Errorf("load datanode disks: %w", serr)
		}
		disks = append(disks, d)
	}
	rerr = drows.Err()
	_ = drows.Close()
	if rerr != nil {
		return nil, nil, fmt.Errorf("load datanode disks: %w", rerr)
	}
	return nodes, disks, nil
}
