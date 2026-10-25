// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/config"
)

func TestNodeIDPersistsAcrossAgents(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "datanode.token")
	idFile := filepath.Join(dir, nodeIDFile)

	first := NewAgent(config.Config{})
	first.Bind(tokenFile, nil)
	b, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatalf("node id file not written: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != first.nodeID {
		t.Fatalf("stored id = %q, want %q", got, first.nodeID)
	}

	// A recreated container has another host name; the stored ID must win.
	if err := os.WriteFile(idFile, []byte("node-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := NewAgent(config.Config{})
	second.Bind(tokenFile, nil)
	if second.nodeID != "node-a" {
		t.Fatalf("nodeID = %q, want node-a", second.nodeID)
	}
	addr := second.listenAddr()
	if strings.Contains(addr, "node-a") {
		t.Errorf("listen address %q must use the current host name, not the stored ID", addr)
	}
	if !strings.HasSuffix(addr, ":9002") {
		t.Errorf("listen address %q should end with :9002", addr)
	}
}

func TestNodeIDInvalidFileIsReplaced(t *testing.T) {
	dir := t.TempDir()
	idFile := filepath.Join(dir, nodeIDFile)
	if err := os.WriteFile(idFile, []byte("not valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := NewAgent(config.Config{})
	want := a.nodeID
	a.Bind(filepath.Join(dir, "datanode.token"), nil)
	if a.nodeID != want {
		t.Fatalf("nodeID = %q, want %q", a.nodeID, want)
	}
	b, err := os.ReadFile(idFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != want {
		t.Fatalf("stored id = %q, want %q", got, want)
	}
}
