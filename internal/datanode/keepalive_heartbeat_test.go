// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/config"
)

// A heartbeat the controller rejects is reported as errHeartbeatRejected (so the loop re-registers)
// and never makes the agent forget its node token.
func TestRejectedHeartbeatKeepsTokenAndSignalsReRegister(t *testing.T) {
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
	a := NewAgent(cfg)
	a.nodeID = "n1"
	a.Bind(file, nil)

	for i := 0; i < 3; i++ {
		err := a.Heartbeat()
		if !errors.Is(err, errHeartbeatRejected) {
			t.Fatalf("heartbeat error = %v, want errHeartbeatRejected", err)
		}
	}
	b, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(b)) != old {
		t.Fatalf("token file=%q err=%v", b, err)
	}
	if a.currentToken() != old || !a.hasNodeToken() {
		t.Fatalf("agent forgot its node token: token=%q have=%v", a.currentToken(), a.hasNodeToken())
	}
}
