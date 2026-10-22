// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"strings"
	"testing"
)

func TestToSqliteDialectRewritesAddColumnIfNotExists(t *testing.T) {
	in := "ALTER TABLE buckets ADD COLUMN IF NOT EXISTS policy JSONB;"
	got := toSqliteDialect(in)
	if strings.Contains(got, "IF NOT EXISTS") {
		t.Fatalf("SQLite does not support ADD COLUMN IF NOT EXISTS, got %q", got)
	}
	want := "ALTER TABLE buckets ADD COLUMN policy JSON;"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestToSqliteDialectKeepsCreateTableIfNotExists(t *testing.T) {
	in := "CREATE TABLE IF NOT EXISTS s3_subresources (updated_at TIMESTAMPTZ NOT NULL DEFAULT now());"
	got := toSqliteDialect(in)
	if !strings.Contains(got, "CREATE TABLE IF NOT EXISTS") {
		t.Fatalf("CREATE TABLE IF NOT EXISTS must be preserved, got %q", got)
	}
	if !strings.Contains(got, "DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP") {
		t.Fatalf("timestamp columns must be rewritten, got %q", got)
	}
}
