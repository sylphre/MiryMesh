// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// releaseServer serves SHA256SUMS and the binary for tag v1.0.0. sumOverride, when
// non-empty, replaces the real digest.
func releaseServer(t *testing.T, payload []byte, sumOverride string) *httptest.Server {
	t.Helper()
	name := assetName()
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	if sumOverride != "" {
		digest = sumOverride
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/download/v1.0.0/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", digest, name)
	})
	mux.HandleFunc("/download/v1.0.0/"+name, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdaterApplyStagesVerifiedBinary(t *testing.T) {
	if assetName() == "" {
		t.Skip("no prebuilt DataNode for this platform")
	}
	payload := []byte("\x7fELF-test-binary")
	srv := releaseServer(t, payload, "")
	u := &Updater{Current: "v0.9.0", StageDir: t.TempDir(), ReleaseBase: srv.URL, Enabled: true, Client: srv.Client()}
	if err := u.Apply("v1.0.0"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(u.StageDir, stagedBinary))
	if err != nil || string(got) != string(payload) {
		t.Fatalf("staged file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(u.StageDir, stagedBinary+".part")); err == nil {
		t.Fatal("partial download was left behind")
	}
}

func TestUpdaterApplyRejectsChecksumMismatch(t *testing.T) {
	if assetName() == "" {
		t.Skip("no prebuilt DataNode for this platform")
	}
	srv := releaseServer(t, []byte("payload"), strings.Repeat("0", 64))
	u := &Updater{Current: "v0.9.0", StageDir: t.TempDir(), ReleaseBase: srv.URL, Enabled: true, Client: srv.Client()}
	if err := u.Apply("v1.0.0"); err == nil {
		t.Fatal("Apply accepted a binary whose checksum does not match")
	}
	if _, err := os.Stat(filepath.Join(u.StageDir, stagedBinary)); err == nil {
		t.Fatal("a binary that failed verification was staged")
	}
}

func TestHandleUpdate(t *testing.T) {
	if assetName() == "" {
		t.Skip("no prebuilt DataNode for this platform")
	}
	srv := releaseServer(t, []byte("\x7fELF-test-binary"), "")
	exited := make(chan int, 1)
	u := &Updater{
		Current: "v0.9.0", StageDir: t.TempDir(), ReleaseBase: srv.URL, Enabled: true, Client: srv.Client(),
		Exit: func(code int) { exited <- code },
	}
	h := NewHandler(NewRegistry(0, 0), "secret")
	h.SetUpdater(u)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	post := func(token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/update", strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Internal-Token", token)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	if rr := post("", `{"version":"v1.0.0"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("no token: got %d, want 403", rr.Code)
	}
	if rr := post("wrong", `{"version":"v1.0.0"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("wrong token: got %d, want 403", rr.Code)
	}
	if rr := post("secret", `{"version":"../x"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad version: got %d, want 400", rr.Code)
	}
	if rr := post("secret", `{"version":"v0.9.0"}`); rr.Code != http.StatusConflict {
		t.Fatalf("same version: got %d, want 409", rr.Code)
	}
	if rr := post("secret", `{"version":"v1.0.0"}`); rr.Code != http.StatusAccepted {
		t.Fatalf("update: got %d (%s), want 202", rr.Code, rr.Body.String())
	}
	select {
	case code := <-exited:
		if code != ExitCodeUpdated {
			t.Fatalf("exit code = %d, want %d", code, ExitCodeUpdated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the node did not restart after staging")
	}
}

func TestHandleUpdateDisabled(t *testing.T) {
	u := &Updater{Current: "v0.9.0", StageDir: t.TempDir(), Enabled: false}
	h := NewHandler(NewRegistry(0, 0), "secret")
	h.SetUpdater(u)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/internal/update", strings.NewReader(`{"version":"v1.0.0"}`))
	req.Header.Set("X-Internal-Token", "secret")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", rr.Code)
	}
}
