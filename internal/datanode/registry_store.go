// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"log/slog"
	"time"
)

// NodeStore keeps the node and disk list of a Registry outside the process, so a
// Master restart does not forget the DataNodes. The Master backs it with its database.
type NodeStore interface {
	SaveNode(n Node) error
	DeleteNode(id string) error
	SaveDisk(d DiskPath) error
	DeleteDisk(nodeID, path string) error
	LoadNodes() ([]Node, []DiskPath, error)
}

// SetStore attaches s and loads the nodes and disks it holds. Restored nodes are
// offline: they turn online on their next heartbeat, which also works for DataNodes
// that never register again after the Master forgot them. LastSeen of a restored node
// is the time of the restore, not a real sighting. Call it once at startup, before the
// registry serves requests. When loading fails the store is not attached.
func (r *Registry) SetStore(s NodeStore) error {
	nodes, disks, err := s.LoadNodes()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store = s
	now := time.Now()
	for _, n := range nodes {
		if _, exists := r.nodes[n.ID]; exists {
			continue
		}
		nn := n
		nn.Status = StatusOffline
		nn.LastSeen = now
		r.nodes[nn.ID] = &nn
	}
	for _, d := range disks {
		if _, ok := r.nodes[d.NodeID]; !ok {
			continue
		}
		if r.disks[d.NodeID] == nil {
			r.disks[d.NodeID] = make(map[string]*DiskPath)
		}
		if _, exists := r.disks[d.NodeID][d.Path]; exists {
			continue
		}
		dd := d
		r.disks[d.NodeID][d.Path] = &dd
	}
	return nil
}

// nodeStore returns the attached store or nil. Never call it while holding r.mu.
func (r *Registry) nodeStore() NodeStore {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.store
}

func (r *Registry) persistNode(n Node) {
	if s := r.nodeStore(); s != nil {
		if err := s.SaveNode(n); err != nil {
			slog.Warn("registry: could not save node", "node", n.ID, "err", err)
		}
	}
}

func (r *Registry) persistDisk(d DiskPath) {
	if s := r.nodeStore(); s != nil {
		if err := s.SaveDisk(d); err != nil {
			slog.Warn("registry: could not save disk", "node", d.NodeID, "path", d.Path, "err", err)
		}
	}
}

func (r *Registry) forgetDisk(nodeID, path string) {
	if s := r.nodeStore(); s != nil {
		if err := s.DeleteDisk(nodeID, path); err != nil {
			slog.Warn("registry: could not delete disk", "node", nodeID, "path", path, "err", err)
		}
	}
}

func (r *Registry) forgetNode(id string) {
	if s := r.nodeStore(); s != nil {
		if err := s.DeleteNode(id); err != nil {
			slog.Warn("registry: could not delete node", "node", id, "err", err)
		}
	}
}
