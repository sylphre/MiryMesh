// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultValidate(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default() should be valid, got %v", err)
	}
}

func TestServerValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{"valid :9000", ServerConfig{Listen: ":9000", Region: "us-east-1", DataDir: "/var/lib/mirymesh"}, false},
		{"valid 127.0.0.1:9000", ServerConfig{Listen: "127.0.0.1:9000"}, false},
		{"empty listen", ServerConfig{Listen: ""}, true},
		{"bad listen", ServerConfig{Listen: "9000"}, true},
		{"bad region", ServerConfig{Listen: ":9000", Region: "US_EAST_1"}, true},
		{"relative datadir", ServerConfig{Listen: ":9000", DataDir: "var/lib"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDBValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     DBConfig
		wantErr bool
	}{
		{"empty ok", DBConfig{}, false},
		{"postgres with dsn", DBConfig{Driver: "postgres", DSN: "postgres://user:pass@localhost/db?sslmode=disable"}, false},
		{"mysql with dsn", DBConfig{Driver: "mysql", DSN: "user:pass@tcp(localhost:3306)/mirymesh"}, false},
		{"mariadb alias", DBConfig{Driver: "mariadb", DSN: "user:pass@tcp(localhost:3306)/mirymesh"}, false},
		{"unknown driver", DBConfig{Driver: "sqlite", DSN: "file::memory:"}, true},
		{"driver without dsn", DBConfig{Driver: "postgres", DSN: ""}, true},
		{"dsn without driver", DBConfig{Driver: "", DSN: "postgres://localhost/db"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDatanodeValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     DatanodeConfig
		wantErr bool
	}{
		{"empty ok", DatanodeConfig{}, false},
		{"valid paths", DatanodeConfig{Paths: []string{"/mnt/disk1", "/mnt/disk2"}, ControllerURL: "http://controller:9000"}, false},
		{"relative path", DatanodeConfig{Paths: []string{"mnt/disk1"}}, true},
		{"root path", DatanodeConfig{Paths: []string{"/"}}, true},
		{"empty path entry", DatanodeConfig{Paths: []string{"/mnt/disk1", ""}}, true},
		{"bad controller url", DatanodeConfig{ControllerURL: "controller:9000"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestLogValidate(t *testing.T) {
	if err := (LogConfig{Level: "info"}).validate(); err != nil {
		t.Fatalf("info should be valid: %v", err)
	}
	if err := (LogConfig{Level: "VERBOSE"}).validate(); err == nil {
		t.Fatalf("VERBOSE should be invalid")
	}
	if err := (LogConfig{Level: ""}).validate(); err != nil {
		t.Fatalf("empty level should be valid: %v", err)
	}
}

func TestLoadValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "server:\n  listen: \":9001\"\n  admin_listen: \":9002\"\n  region: \"ap-northeast-1\"\ndb:\n  driver: \"postgres\"\n  dsn: \"postgres://localhost/mirymesh?sslmode=disable\"\nlog:\n  level: \"debug\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != ":9001" {
		t.Fatalf("Listen = %q, want :9001", cfg.Server.Listen)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/mirymesh-config.yaml")
	if err == nil {
		t.Fatal("Load() should error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("::bad: [yaml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should error for invalid yaml")
	}
}
