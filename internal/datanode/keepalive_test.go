// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/config"
)

// A paired DataNode must never lose its node token just because the controller rejects it.
func TestRejectedNodeTokenIsNeverDeleted(t *testing.T) {
	// The controller now runs with another internal token, so the stored node token is rejected.
	h := NewHandler(NewRegistry(0, 0), "new-admin")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	old := NodeToken("old-admin", "n1")
	file := filepath.Join(t.TempDir(), "datanode.token")
	if err := os.WriteFile(file, []byte(old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	cfg.Datanode.ControllerURL = srv.URL
	cfg.Auth.Credentials = []config.Credential{{AccessKey: "pair", SecretKey: "burned"}}
	a := NewAgent(cfg)
	a.nodeID = "n1"
	a.Bind(file, nil)

	for i := 0; i < 3; i++ {
		if err := a.Register(); err == nil {
			t.Fatal("Register succeeded with a rejected token")
		}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("token file was removed: %v", err)
	}
	if strings.TrimSpace(string(b)) != old {
		t.Fatalf("token file changed: %q", b)
	}
	if a.currentToken() != old || !a.hasNodeToken() {
		t.Fatalf("agent forgot its node token: token=%q have=%v", a.currentToken(), a.hasNodeToken())
	}
}

// A fresh pair token still re-pairs a node whose stored token is rejected; the old token is kept as .prev.
func TestRePairReplacesRejectedToken(t *testing.T) {
	h := NewHandler(NewRegistry(0, 0), "new-admin")
	h.SetPairConsumer(func(tok string) error {
		if tok != "once2" {
			return os.ErrPermission
		}
		return nil
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	old := NodeToken("old-admin", "n1")
	file := filepath.Join(t.TempDir(), "datanode.token")
	if err := os.WriteFile(file, []byte(old+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	cfg.Datanode.ControllerURL = srv.URL
	cfg.Auth.Credentials = []config.Credential{{AccessKey: "pair", SecretKey: "once2"}}
	a := NewAgent(cfg)
	a.nodeID = "n1"
	a.Bind(file, nil)

	if err := a.Register(); err != nil {
		t.Fatalf("re-pair failed: %v", err)
	}
	want := NodeToken("new-admin", "n1")
	b, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(b)) != want {
		t.Fatalf("token file=%q err=%v want %q", b, err, want)
	}
	prev, err := os.ReadFile(file + ".prev")
	if err != nil || strings.TrimSpace(string(prev)) != old {
		t.Fatalf("previous token not kept: %q err=%v", prev, err)
	}
}

// The registry marks silent nodes offline but never forgets them, and they come back on a heartbeat.
func TestOfflineNodeIsKeptAndComesBack(t *testing.T) {
	reg := NewRegistry(time.Second, 2*time.Second)
	if err := reg.RegisterNode("n1", "n1:9002"); err != nil {
		t.Fatal(err)
	}
	if err := reg.UpsertDisk("n1", "/data", 100, 10, 0); err != nil {
		t.Fatal(err)
	}
	reg.MarkOfflineIfStale(time.Now().Add(24 * time.Hour))

	nodes := reg.ListNodes()
	if len(nodes) != 1 || nodes[0].Status != StatusOffline {
		t.Fatalf("nodes after outage: %+v", nodes)
	}
	if len(reg.ListDisksForNode("n1")) != 1 {
		t.Fatal("disks were dropped when the node went offline")
	}
	if err := reg.Heartbeat("n1"); err != nil {
		t.Fatalf("heartbeat after outage: %v", err)
	}
	if got := reg.ListNodes()[0].Status; got != StatusOnline {
		t.Fatalf("status after heartbeat=%s", got)
	}
}
