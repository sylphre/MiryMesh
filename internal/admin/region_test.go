// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/metadata"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

// regionTestHandler builds a handler over a SQLite-backed setup store and a live router.
func regionTestHandler(t *testing.T) (*Handler, *s3api.Router, *SetupStoreDB) {
	t.Helper()
	db, err := metadata.OpenDB("sqlite", "file:"+filepath.Join(t.TempDir(), "region.db")+"?mode=rwc")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewSetupStoreDB(db, "sqlite", "test-master-key-32chars-1234567890")
	h := placementHandler(t, s)
	rt := s3api.NewRouter(h.storage, "")
	h.SetRegionController(rt)
	return h, rt, s
}

func regionDo(h *Handler, method, body, token string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(method, "/api/admin/region", strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeRegion(t *testing.T, rr *httptest.ResponseRecorder) regionDoc {
	t.Helper()
	var doc regionDoc
	if err := json.NewDecoder(rr.Body).Decode(&doc); err != nil {
		t.Fatalf("decode region: %v", err)
	}
	return doc
}

func TestRegionGetPutPersists(t *testing.T) {
	h, rt, s := regionTestHandler(t)

	rr := regionDo(h, http.MethodGet, "", testAdminToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body %s", rr.Code, rr.Body.String())
	}
	if doc := decodeRegion(t, rr); doc.Region != "us-east-1" || doc.Default != "us-east-1" || !doc.Persisted {
		t.Fatalf("GET doc = %+v", doc)
	}

	rr = regionDo(h, http.MethodPut, `{"region":" ap-northeast-1 "}`, testAdminToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s", rr.Code, rr.Body.String())
	}
	if doc := decodeRegion(t, rr); doc.Region != "ap-northeast-1" {
		t.Fatalf("PUT doc = %+v", doc)
	}
	if got := rt.Region(); got != "ap-northeast-1" {
		t.Fatalf("router region = %q, want ap-northeast-1", got)
	}
	if v, ok := s.GetSetting(settingRegion); !ok || v != "ap-northeast-1" {
		t.Fatalf("saved setting = %q, %v", v, ok)
	}

	// A restart: a fresh router picks the saved region up.
	rt2 := s3api.NewRouter(h.storage, "us-east-1")
	h.SetRegionController(rt2)
	h.LoadRegionSetting()
	if got := rt2.Region(); got != "ap-northeast-1" {
		t.Fatalf("restored region = %q, want ap-northeast-1", got)
	}
}

func TestRegionPutRejectsInvalid(t *testing.T) {
	h, rt, s := regionTestHandler(t)
	for _, body := range []string{`{"region":""}`, `{"region":"US_EAST_1"}`, `{"region":"a b"}`, `not json`} {
		rr := regionDo(h, http.MethodPut, body, testAdminToken)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("PUT %s status = %d, want 400", body, rr.Code)
		}
	}
	if got := rt.Region(); got != "us-east-1" {
		t.Fatalf("router region = %q, want unchanged us-east-1", got)
	}
	if _, ok := s.GetSetting(settingRegion); ok {
		t.Fatal("nothing should have been saved")
	}
}

func TestRegionRequiresAuthAndMethod(t *testing.T) {
	h, rt, _ := regionTestHandler(t)
	if rr := regionDo(h, http.MethodPut, `{"region":"eu-west-1"}`, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("PUT without token status = %d, want 401", rr.Code)
	}
	if got := rt.Region(); got != "us-east-1" {
		t.Fatalf("router region = %q, want unchanged", got)
	}
	if rr := regionDo(h, http.MethodDelete, "", testAdminToken); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rr.Code)
	}
}

func TestRegionWithoutController(t *testing.T) {
	h := placementHandler(t, nil)
	if rr := regionDo(h, http.MethodGet, "", testAdminToken); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status = %d, want 503", rr.Code)
	}
	h.LoadRegionSetting() // must not panic
}
