// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
)

type memNodeTokens struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemNodeTokens() *memNodeTokens { return &memNodeTokens{m: map[string]string{}} }

func (s *memNodeTokens) GetNodeToken(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.m[id]
	return tok, ok
}

func (s *memNodeTokens) PutNodeToken(id, tok string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = tok
	return nil
}

func newStoreMux(admin string, st NodeTokenStore) *http.ServeMux {
	h := NewHandler(NewRegistry(0, 0), admin)
	h.SetPairConsumer(func(tok string) error {
		if tok != "once" {
			return errors.New("invalid pair token")
		}
		return nil
	})
	h.SetNodeTokenStore(st)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux
}

func registerWith(t *testing.T, mux *http.ServeMux, nodeID, tok string) (int, string) {
	t.Helper()
	rr := pairPost(t, mux, "/internal/register", map[string]string{"node_id": nodeID, "addr": "10.0.0.1:9002", "token": tok})
	var out struct {
		NodeToken string `json:"node_token"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out.NodeToken
}

func heartbeatWith(t *testing.T, mux *http.ServeMux, nodeID, tok string) int {
	t.Helper()
	return pairPost(t, mux, "/internal/heartbeat", map[string]string{"node_id": nodeID, "token": tok}).Code
}

func TestPairingIssuesRandomStoredNodeToken(t *testing.T) {
	st := newMemNodeTokens()
	mux := newStoreMux("admin", st)

	code, tok := registerWith(t, mux, "n1", "once")
	if code != http.StatusOK || len(tok) != 64 {
		t.Fatalf("register: status=%d token=%q", code, tok)
	}
	if tok == NodeToken("admin", "n1") {
		t.Fatal("node token must be random, not derived from the admin token")
	}
	if got, _ := st.GetNodeToken("n1"); got != tok {
		t.Fatalf("stored token %q != issued %q", got, tok)
	}
	if c := heartbeatWith(t, mux, "n1", tok); c != http.StatusOK {
		t.Fatalf("heartbeat with node token: %d", c)
	}

	// Registering with the stored token again keeps the same token.
	if code, again := registerWith(t, mux, "n1", tok); code != http.StatusOK || again != tok {
		t.Fatalf("re-register: status=%d token=%q want %q", code, again, tok)
	}
}

func TestNodeTokenSurvivesAdminTokenChange(t *testing.T) {
	st := newMemNodeTokens()
	_, tok := registerWith(t, newStoreMux("old-admin", st), "n1", "once")

	// The Master now runs with a different admin token but the same database.
	mux := newStoreMux("new-admin", st)
	if code, _ := registerWith(t, mux, "n1", tok); code != http.StatusOK {
		t.Fatalf("register after admin token change: %d", code)
	}
	if c := heartbeatWith(t, mux, "n1", tok); c != http.StatusOK {
		t.Fatalf("heartbeat after admin token change: %d", c)
	}
	// The old derived token no longer works once the node has a stored token.
	if c := heartbeatWith(t, mux, "n1", NodeToken("old-admin", "n1")); c != http.StatusForbidden {
		t.Fatalf("legacy token on heartbeat: %d, want 403", c)
	}
}

func TestLegacyNodeMigratesToRandomToken(t *testing.T) {
	st := newMemNodeTokens()
	mux := newStoreMux("admin", st)
	legacy := NodeToken("admin", "n1")

	// Not migrated yet: the derived token still authenticates.
	if _, ok := st.GetNodeToken("n1"); ok {
		t.Fatal("no stored token expected before migration")
	}
	code, tok := registerWith(t, mux, "n1", legacy)
	if code != http.StatusOK || tok == "" || tok == legacy {
		t.Fatalf("migration register: status=%d token=%q", code, tok)
	}
	// If the response was lost, the node still holds the legacy token and can register again.
	if code, _ := registerWith(t, mux, "n1", legacy); code != http.StatusOK {
		t.Fatalf("register with legacy token after migration: %d", code)
	}
}

func TestRegistryNodeCredential(t *testing.T) {
	reg := NewRegistry(0, 0)
	if got := reg.NodeCredential("n1", ""); got != "" {
		t.Fatalf("no source, no master: %q", got)
	}
	if got := reg.NodeCredential("n1", "m"); got != NodeToken("m", "n1") {
		t.Fatalf("fallback: %q", got)
	}
	st := newMemNodeTokens()
	_ = st.PutNodeToken("n1", "stored")
	reg.SetTokenSource(st.GetNodeToken)
	if got := reg.NodeCredential("n1", "m"); got != "stored" {
		t.Fatalf("stored token: %q", got)
	}
	if got := reg.NodeCredential("n2", "m"); got != NodeToken("m", "n2") {
		t.Fatalf("node without stored token: %q", got)
	}
}
