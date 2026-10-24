// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"path/filepath"
	"testing"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

func TestSettingsRoundTripSQLite(t *testing.T) {
	db, err := metadata.OpenDB("sqlite", "file:"+filepath.Join(t.TempDir(), "settings.db")+"?mode=rwc")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewSetupStoreDB(db, "sqlite", "test-master-key-32chars-1234567890")

	if _, ok := s.GetSetting(settingNotifyChannel); ok {
		t.Fatal("setting should not exist yet")
	}
	for _, want := range []string{"prerelease", "release", "auto"} {
		if err := s.SetSetting(settingNotifyChannel, want); err != nil {
			t.Fatalf("SetSetting(%q): %v", want, err)
		}
		if got, ok := s.GetSetting(settingNotifyChannel); !ok || got != want {
			t.Fatalf("GetSetting = %q, %v; want %q", got, ok, want)
		}
	}
}
