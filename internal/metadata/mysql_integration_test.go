// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/metadata"
	"github.com/sylphre/MiryMesh/internal/testdb"
)

// Runs against MySQL or MariaDB depending on MIRYMESH_TEST_MYSQL_DSN; skipped when unset.
func TestMigrateMySQL(t *testing.T) {
	db := testdb.MySQL(t)

	for i := 0; i < 2; i++ { // the second run must be a no-op
		if err := metadata.Migrate(db, "mysql"); err != nil {
			t.Fatalf("Migrate run %d: %v", i+1, err)
		}
	}

	var applied int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	// Expected count = number of MySQL migration files, so adding a migration needs no test edit.
	entries, rerr := os.ReadDir("migrations/mysql")
	if rerr != nil {
		t.Fatal(rerr)
	}
	want := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			want++
		}
	}
	if applied != want {
		t.Fatalf("schema_migrations rows=%d want %d", applied, want)
	}

	for _, table := range []string{"setup", "sessions", "pair_tokens", "buckets", "objects", "placement", "s3_subresources", "users", "user_bucket_perms"} {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, table).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("table %s: n=%d err=%v", table, n, err)
		}
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'buckets' AND column_name = 'policy'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("buckets.policy: n=%d err=%v", n, err)
	}

	var tz string
	if err := db.QueryRow(`SELECT @@session.time_zone`).Scan(&tz); err != nil || tz != "+00:00" {
		t.Errorf("session time_zone=%q err=%v want +00:00", tz, err)
	}

	// Foreign keys must really be enforced (inline REFERENCES would be ignored by InnoDB).
	if _, err := db.Exec(`INSERT INTO sessions(token_hash, admin_username, expires_at) VALUES ('h', 'nobody', ?)`, time.Now().UTC()); err == nil {
		t.Error("sessions.admin_username foreign key is not enforced")
	}
	if _, err := db.Exec(`INSERT INTO buckets(name, owner) VALUES ('b', 'nobody')`); err == nil {
		t.Error("buckets.owner foreign key is not enforced")
	}
}
