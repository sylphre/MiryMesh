// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// nodeIDFile is the name of the file, next to datanode.token, that keeps the node ID.
const nodeIDFile = "datanode.id"

// validNodeID reports whether id is usable as a node ID read from disk.
func validNodeID(id string) bool {
	if id == "" || len(id) > 253 {
		return false
	}
	return !strings.ContainsAny(id, " \t\r\n/\\")
}

// loadNodeID keeps the node ID stable. The ID defaults to the host name, which
// changes with every new container, and the Master ties the node token to the ID:
// a recreated container used to come back with a new ID and be answered with 403.
// If file holds a valid ID it replaces the current one; otherwise the current ID
// is written to file. Call it before the agent registers.
func (a *Agent) loadNodeID(file string) {
	if b, err := os.ReadFile(filepath.Clean(file)); err == nil {
		if id := strings.TrimSpace(string(b)); validNodeID(id) {
			a.mu.Lock()
			if id != a.nodeID {
				// The stored ID is not a name that resolves now; keep dialing the old one.
				a.dialAs = a.nodeID
			}
			a.nodeID = id
			a.mu.Unlock()
			slog.Info("datanode: node id", "node_id", id, "file", file)
			return
		}
	}
	a.mu.RLock()
	id := a.nodeID
	a.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		slog.Warn("datanode: could not save the node id", "file", file, "err", err)
		return
	}
	// Write then rename, so a crash never leaves a half-written ID behind.
	tmp := file + ".tmp"
	err := os.WriteFile(tmp, []byte(id+"\n"), 0o600)
	if err == nil {
		err = os.Rename(tmp, file)
	}
	if err != nil {
		slog.Warn("datanode: could not save the node id", "file", file, "err", err)
		return
	}
	slog.Info("datanode: node id saved", "node_id", id, "file", file)
}

// dialHost is the host name the Master uses to reach this node. It is the node ID,
// unless the ID was loaded from datanode.id and differs from the host name this
// process started with: that ID may come from an earlier container whose name no
// longer resolves, so the starting host name is used.
func (a *Agent) dialHost() string {
	if a.dialAs != "" {
		return a.dialAs
	}
	return a.nodeID
}
