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
)

func TestEvacuateWritesToConfiguredDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".evac")
	h := NewHandler(NewRegistry(0, 0), "")
	h.SetEvacDir(dir)

	req := httptest.NewRequest(http.MethodPost, "/internal/evacuate", strings.NewReader(`{"snapshot":{}}`))
	rec := httptest.NewRecorder()
	h.handleEvacuate(rec, req)
	if rec.Code >= 300 {
		t.Fatalf("evacuate status = %d, body = %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("evac dir not created: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("evacuate wrote nothing to the configured dir")
	}
}
