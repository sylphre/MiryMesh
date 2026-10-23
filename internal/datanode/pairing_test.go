// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/config"
)

func pairPost(t *testing.T, h http.Handler, path string, v any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	return rr
}

func TestRegisterWithPairToken(t *testing.T) {
	h := NewHandler(NewRegistry(0, 0), "admin")
	burned := false
	h.SetPairConsumer(func(tok string) error {
		if tok != "once" || burned {
			return errors.New("invalid pair token")
		}
		burned = true
		return nil
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// A malformed request must not burn the one-time token.
	rr := pairPost(t, mux, "/internal/register", map[string]string{"node_id": "n1", "addr": "", "token": "once"})
	if rr.Code != http.StatusBadRequest || burned {
		t.Fatalf("malformed: status=%d burned=%v", rr.Code, burned)
	}

	rr = pairPost(t, mux, "/internal/register", map[string]string{"node_id": "n1", "addr": "10.0.0.1:9002", "token": "once"})
	if rr.Code != http.StatusOK {
		t.Fatalf("pair register status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		NodeToken string `json:"node_token"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if want := NodeToken("admin", "n1"); out.NodeToken != want {
		t.Fatalf("node_token=%q want %q", out.NodeToken, want)
	}

	// The pair token is single-use.
	rr = pairPost(t, mux, "/internal/register", map[string]string{"node_id": "n1", "addr": "10.0.0.1:9002", "token": "once"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("reused pair token status=%d", rr.Code)
	}

	// The node token re-registers and heartbeats; another node's token is rejected.
	rr = pairPost(t, mux, "/internal/register", map[string]string{"node_id": "n1", "addr": "10.0.0.1:9002", "token": out.NodeToken})
	if rr.Code != http.StatusOK {
		t.Fatalf("node token register status=%d", rr.Code)
	}
	rr = pairPost(t, mux, "/internal/heartbeat", map[string]string{"node_id": "n1", "token": out.NodeToken})
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d", rr.Code)
	}
	rr = pairPost(t, mux, "/internal/heartbeat", map[string]string{"node_id": "n1", "token": NodeToken("admin", "n2")})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign node token status=%d", rr.Code)
	}
}

func TestAdoptEndpoint(t *testing.T) {
	h := NewHandler(NewRegistry(0, 0), "")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := map[string]string{"controller_url": "http://m:9001", "pair_token": "x"}

	if rr := pairPost(t, mux, "/internal/adopt", req); rr.Code != http.StatusNotFound {
		t.Fatalf("disabled status=%d", rr.Code)
	}
	h.SetAdopter(func(_, tok string) error {
		switch tok {
		case "paired":
			return ErrAlreadyPaired
		case "":
			return errors.New("pair_token is required")
		}
		return nil
	})
	if rr := pairPost(t, mux, "/internal/adopt", req); rr.Code != http.StatusOK {
		t.Fatalf("ok status=%d", rr.Code)
	}
	if rr := pairPost(t, mux, "/internal/adopt", map[string]string{"controller_url": "http://m:9001", "pair_token": "paired"}); rr.Code != http.StatusConflict {
		t.Fatalf("paired status=%d", rr.Code)
	}
	if rr := pairPost(t, mux, "/internal/adopt", map[string]string{"controller_url": "http://m:9001"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("missing token status=%d", rr.Code)
	}
}

func TestObjectTokenPrefersNodeToken(t *testing.T) {
	h := NewHandler(NewRegistry(0, 0), "boot")
	if got := h.objectToken(); got != "boot" {
		t.Fatalf("objectToken=%q", got)
	}
	h.SetNodeToken("node")
	if got := h.objectToken(); got != "node" {
		t.Fatalf("objectToken=%q", got)
	}
	h.SetNodeToken("")
	if got := h.objectToken(); got != "boot" {
		t.Fatalf("objectToken after reset=%q", got)
	}
}

func TestAgentRegisterSwitchesToNodeToken(t *testing.T) {
	h := NewHandler(NewRegistry(0, 0), "admin")
	h.SetPairConsumer(func(tok string) error {
		if tok != "once" {
			return errors.New("invalid pair token")
		}
		return nil
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var cfg config.Config
	cfg.Datanode.ControllerURL = srv.URL
	cfg.Auth.Credentials = []config.Credential{{AccessKey: "pair", SecretKey: "once"}}
	a := NewAgent(cfg)
	a.nodeID = "n1"

	file := filepath.Join(t.TempDir(), "sub", "datanode.token")
	var hooked string
	a.Bind(file, func(s string) { hooked = s })

	if err := a.Register(); err != nil {
		t.Fatalf("Register: %v", err)
	}
	want := NodeToken("admin", "n1")
	if hooked != want || a.currentToken() != want {
		t.Fatalf("hook=%q token=%q want %q", hooked, a.currentToken(), want)
	}
	b, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(b)) != want {
		t.Fatalf("persisted token=%q err=%v", b, err)
	}
	// Re-registration now goes through with the node token alone.
	if err := a.Register(); err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if err := a.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	// A paired node refuses to be adopted again.
	if err := a.Adopt("http://other:9001", "tok"); !errors.Is(err, ErrAlreadyPaired) {
		t.Fatalf("Adopt err=%v", err)
	}
}

func TestAgentAdoptValidation(t *testing.T) {
	a := &Agent{nodeID: "n1"}
	if err := a.Adopt("ftp://x", "tok"); err == nil {
		t.Fatal("non-http url accepted")
	}
	if err := a.Adopt("http://m:9001", " "); err == nil {
		t.Fatal("empty pair token accepted")
	}
}
