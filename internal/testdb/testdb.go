// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package testdb gives tests a throw-away MySQL / MariaDB database.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"

	mysqldrv "github.com/go-sql-driver/mysql"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// MySQLEnv names the environment variable holding a DSN for a server where the test
// user may CREATE / DROP databases, e.g. root:pw@tcp(127.0.0.1:3306)/ .
const MySQLEnv = "MIRYMESH_TEST_MYSQL_DSN"

// MySQL creates a fresh empty database on the server from MySQLEnv and returns a
// handle to it. The database is dropped when the test ends. The test is skipped
// when MySQLEnv is unset. The database name in the DSN, if any, is ignored.
func MySQL(t testing.TB) *sql.DB {
	t.Helper()
	dsn := os.Getenv(MySQLEnv)
	if dsn == "" {
		t.Skipf("%s not set; skipping MySQL/MariaDB integration test", MySQLEnv)
	}
	cfg, err := mysqldrv.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("%s: %v", MySQLEnv, err)
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random name: %v", err)
	}
	name := "mirymesh_test_" + hex.EncodeToString(buf) // hex only: safe to quote into DDL

	serverCfg := cfg.Clone()
	serverCfg.DBName = ""
	serverDB, err := metadata.OpenDB("mysql", serverCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open server: %v", err)
	}
	t.Cleanup(func() {
		_, _ = serverDB.Exec("DROP DATABASE IF EXISTS `" + name + "`")
		_ = serverDB.Close()
	})
	if _, err := serverDB.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4"); err != nil {
		t.Fatalf("create database: %v", err)
	}

	cfg.DBName = name
	db, err := metadata.OpenDB("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
