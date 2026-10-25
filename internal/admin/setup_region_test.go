// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/s3api"
)

// newRegionRouter builds a fresh router over the handler's storage, as a restart would.
func newRegionRouter(h *Handler) *s3api.Router {
	return s3api.NewRouter(h.storage, "us-east-1")
}

// setupDo posts a body to /api/admin/setup, the endpoint the Setup page saves to.
func setupDo(h *Handler, body, token string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// firstSetup runs the first setup (admin account only) and returns the new admin token.
func firstSetup(t *testing.T, h *Handler, body string) string {
	t.Helper()
	rr := setupDo(h, body, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("first setup status = %d, body %s", rr.Code, rr.Body.String())
	}
	var out struct {
		AdminToken string `json:"admin_token"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	if out.AdminToken == "" {
		t.Fatal("setup response has no admin_token")
	}
	return out.AdminToken
}

// The region chosen in the first setup must reach the router and app_settings,
// not only the setup.region column.
func TestSetupFirstRunAppliesRegion(t *testing.T) {
	h, rt, s := regionTestHandler(t)
	firstSetup(t, h, `{"username":"admin","password":"password123","region":" ap-northeast-3 "}`)

	if got := rt.Region(); got != "ap-northeast-3" {
		t.Fatalf("router region = %q, want ap-northeast-3", got)
	}
	if v, ok := s.GetSetting(settingRegion); !ok || v != "ap-northeast-3" {
		t.Fatalf("saved setting = %q, %v", v, ok)
	}
	if st := s.Get(); st == nil || st.Region != "ap-northeast-3" {
		t.Fatalf("setup.region = %+v", st)
	}
}

// The Setup page saves the admin account in step 1 and the rest in step 3. The
// region sent in step 3 goes through the update path and must be applied too.
func TestSetupUpdateAppliesRegion(t *testing.T) {
	h, rt, s := regionTestHandler(t)
	token := firstSetup(t, h, `{"username":"admin","password":"password123"}`)
	if got := rt.Region(); got != "us-east-1" {
		t.Fatalf("router region after step 1 = %q, want us-east-1", got)
	}

	rr := setupDo(h, `{"region":"ap-northeast-3"}`, token)
	if rr.Code >= 300 {
		t.Fatalf("update status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := rt.Region(); got != "ap-northeast-3" {
		t.Fatalf("router region = %q, want ap-northeast-3", got)
	}
	if v, ok := s.GetSetting(settingRegion); !ok || v != "ap-northeast-3" {
		t.Fatalf("saved setting = %q, %v", v, ok)
	}
	if st := s.Get(); st == nil || st.Region != "ap-northeast-3" {
		t.Fatalf("setup.region = %+v", st)
	}

	// An update without a region leaves the region alone.
	if rr = setupDo(h, `{}`, token); rr.Code >= 300 {
		t.Fatalf("update without region status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := rt.Region(); got != "ap-northeast-3" {
		t.Fatalf("router region = %q, want unchanged ap-northeast-3", got)
	}

	// A restart: a fresh router picks the saved region up.
	rt2 := newRegionRouter(h)
	h.SetRegionController(rt2)
	h.LoadRegionSetting()
	if got := rt2.Region(); got != "ap-northeast-3" {
		t.Fatalf("restored region = %q, want ap-northeast-3", got)
	}
}

func TestSetupRejectsInvalidRegion(t *testing.T) {
	h, rt, s := regionTestHandler(t)

	rr := setupDo(h, `{"username":"admin","password":"password123","region":"US_EAST_1"}`, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("first setup status = %d, want 400", rr.Code)
	}
	if s.IsInitialized() {
		t.Fatal("setup must not be saved when the region is invalid")
	}

	token := firstSetup(t, h, `{"username":"admin","password":"password123"}`)
	rr = setupDo(h, `{"region":"a b"}`, token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("update status = %d, want 400", rr.Code)
	}
	if got := rt.Region(); got != "us-east-1" {
		t.Fatalf("router region = %q, want unchanged us-east-1", got)
	}
	if _, ok := s.GetSetting(settingRegion); ok {
		t.Fatal("nothing should have been saved")
	}
}
