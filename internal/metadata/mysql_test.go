// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitSQLStatements(t *testing.T) {
	src := "-- header; with semicolon\nCREATE TABLE a (x TEXT DEFAULT 'a;b');\n\n-- mid\nALTER TABLE a ADD COLUMN y INT;\n"
	got := splitSQLStatements(src)
	want := []string{
		"CREATE TABLE a (x TEXT DEFAULT 'a;b')",
		"ALTER TABLE a ADD COLUMN y INT",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMySQLConfigPinsTimeHandling(t *testing.T) {
	cfg, err := mysqlConfig("u:p@tcp(127.0.0.1:3306)/db")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ParseTime || cfg.Loc != time.UTC || cfg.Params["time_zone"] != "'+00:00'" {
		t.Fatalf("parseTime=%v loc=%v time_zone=%q", cfg.ParseTime, cfg.Loc, cfg.Params["time_zone"])
	}

	cfg, err = mysqlConfig("u:p@tcp(h)/db?parseTime=false&loc=Asia%2FTokyo&time_zone=%27%2B09%3A00%27")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ParseTime || cfg.Loc != time.UTC {
		t.Fatalf("parseTime/loc must always be pinned: parseTime=%v loc=%v", cfg.ParseTime, cfg.Loc)
	}
	if cfg.Params["time_zone"] != "'+09:00'" {
		t.Fatalf("explicit time_zone must be kept, got %q", cfg.Params["time_zone"])
	}

	if _, err := mysqlConfig("not a dsn"); err == nil {
		t.Fatal("invalid DSN must be rejected")
	}
}

func TestMySQLMigrationsOverrideEveryGenericMigration(t *testing.T) {
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil || len(entries) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(entries))
	}
	for _, name := range entries {
		over := path.Join(path.Dir(name), "mysql", path.Base(name))
		b, err := fs.ReadFile(migrationsFS, over)
		if err != nil {
			t.Errorf("%s has no hand-written MySQL version %s: %v", name, over, err)
			continue
		}
		// InnoDB silently ignores inline "col ... REFERENCES t(c)"; only FOREIGN KEY is enforced.
		for _, stmt := range splitSQLStatements(string(b)) {
			if r, f := strings.Count(stmt, "REFERENCES"), strings.Count(stmt, "FOREIGN KEY"); r != f {
				t.Errorf("%s: %d REFERENCES but %d FOREIGN KEY in %.60q", over, r, f, stmt)
			}
		}
	}
}
