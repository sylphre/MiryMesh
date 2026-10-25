// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func rotateRequest(h *Handler, method, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/admin/rotate-token", nil)
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	rec := httptest.NewRecorder()
	h.HandleRotateToken(rec, req)
	return rec
}

func TestRotateAdminToken(t *testing.T) {
	db := openRotateDB(t)
	store := NewSetupStoreDB(db, "sqlite", rotOldKey)
	seedRotateSetup(t, store)
	if err := store.CreateSession("old-session", "admin", time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	h := NewWithSetup(nil, nil, nil, nil, "tok", store, nil)

	// No token: rejected, nothing changes.
	if rec := rotateRequest(h, http.MethodPost, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	if !h.validToken("tok") {
		t.Fatal("a rejected request must not change the admin token")
	}

	// Wrong method: rejected, nothing changes.
	if rec := rotateRequest(h, http.MethodGet, "tok"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: status = %d, want 405", rec.Code)
	}
	if !h.validToken("tok") {
		t.Fatal("a GET must not change the admin token")
	}

	rec := rotateRequest(h, http.MethodPost, "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	var out struct {
		AdminToken string `json:"admin_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.AdminToken) != 64 || out.AdminToken == "tok" {
		t.Fatalf("unexpected new token %q", out.AdminToken)
	}

	if h.validToken("tok") {
		t.Fatal("old admin token must stop working")
	}
	if h.validToken("old-session") {
		t.Fatal("existing sessions must be revoked")
	}
	if !h.validToken(out.AdminToken) {
		t.Fatal("new admin token must work")
	}
	if got := store.Get(); got == nil || got.AdminToken != out.AdminToken {
		t.Fatalf("stored admin token = %+v, want %s", got, out.AdminToken)
	}
	// DataNode credentials derived from the start-up secret stay valid.
	if got := h.nodeCredentialSecret(); got != "tok" {
		t.Fatalf("nodeCredentialSecret = %q, want the start-up secret", got)
	}

	// The new token can rotate again.
	if rec := rotateRequest(h, http.MethodPost, out.AdminToken); rec.Code != http.StatusOK {
		t.Fatalf("second rotate: status = %d", rec.Code)
	}
}

func TestRotateAdminTokenRejectsEmpty(t *testing.T) {
	db := openRotateDB(t)
	store := NewSetupStoreDB(db, "sqlite", rotOldKey)
	seedRotateSetup(t, store)
	if err := store.RotateAdminToken("  "); err == nil {
		t.Fatal("empty token must be rejected")
	}
	if err := NewMemSetupStore().RotateAdminToken(""); err == nil {
		t.Fatal("empty token must be rejected by the memory store")
	}
}
