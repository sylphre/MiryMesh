// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package config provides centralized configuration loading and validation
// for all MiryMesh components. Validation is aggregated here so that
// business logic does not scatter ad-hoc checks.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level MiryMesh configuration.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	DB       DBConfig       `yaml:"db"`
	Datanode DatanodeConfig `yaml:"datanode"`
	Auth     AuthConfig     `yaml:"auth"`
	Storage  StorageConfig  `yaml:"storage"`
	Log      LogConfig      `yaml:"log"`
}

// ServerConfig holds controller / S3 endpoint settings.
type ServerConfig struct {
	Listen  string `yaml:"listen"`
	Region  string `yaml:"region"`
	DataDir string `yaml:"data_dir"`
}

// DBConfig holds metadata database settings.
// Driver must be "postgres" (primary) or "mysql"/"mariadb".
type DBConfig struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

// DatanodeConfig holds data-node agent settings.
type DatanodeConfig struct {
	ControllerURL string   `yaml:"controller_url"`
	Paths         []string `yaml:"paths"`
}

// StorageConfig holds replication / placement settings for controller.
// DefaultPolicy empty means "single" (M3+ default).
type StorageConfig struct {
	DefaultPolicy string            `yaml:"default_policy"`
	BucketPolicy  map[string]string `yaml:"bucket_policy"`
}

// AuthConfig holds S3 credential settings for standalone / single-node mode.
// In M1 the controller stores credentials in config file. Future milestones
// will persist them in the metadata DB.
type AuthConfig struct {
	Credentials []Credential `yaml:"credentials"`
}

// Credential is a single S3 access key / secret pair.
type Credential struct {
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level string `yaml:"level"`
}

var (
	regionRe = regexp.MustCompile(`^[a-z0-9-]+$`)
	// Access keys are 3-128 printable characters without whitespace or colon.
	accessKeyRe = regexp.MustCompile(`^[^\s:]{3,128}$`)

	validLogLevels = map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	validDBDrivers = map[string]bool{
		"postgres":   true,
		"postgresql": true,
		"mysql":      true,
		"mariadb":    true,
	}
)

// Default returns a Config populated with production defaults.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Listen:  ":9000",
			Region:  "us-east-1",
			DataDir: "/var/lib/mirymesh",
		},
		DB: DBConfig{},
		Log: LogConfig{
			Level: "info",
		},
	}
}

// Load reads YAML configuration from path and returns the parsed Config.
// If the file does not exist, an error is returned so callers can decide
// whether to fall back to Default().
func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) == "" {
		return cfg, errors.New("config path must not be empty")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// LoadOrDefault tries to load path; if the file is missing it returns Default().
func LoadOrDefault(path string) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		// os.ReadFile wraps ErrNotExist; check string as fallback.
		if strings.Contains(err.Error(), "no such file") {
			return Default(), nil
		}
		return Config{}, err
	}
	return cfg, nil
}

// Validate performs centralized validation of all fields.
func (c *Config) Validate() error {
	if err := c.Server.validate(); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if err := c.DB.validate(); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if err := c.Datanode.validate(); err != nil {
		return fmt.Errorf("datanode: %w", err)
	}
	if err := c.Auth.validate(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := c.Storage.validate(); err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	if err := c.Log.validate(); err != nil {
		return fmt.Errorf("log: %w", err)
	}
	return nil
}

func (s ServerConfig) validate() error {
	if strings.TrimSpace(s.Listen) == "" {
		return errors.New("listen must not be empty (e.g. \":9000\")")
	}
	if _, _, err := net.SplitHostPort(s.Listen); err != nil {
		return fmt.Errorf("listen %q is not a valid host:port: %w", s.Listen, err)
	}
	if s.Region != "" && !regionRe.MatchString(s.Region) {
		return fmt.Errorf("region %q must match %s", s.Region, regionRe.String())
	}
	if s.DataDir != "" && !filepath.IsAbs(s.DataDir) {
		return fmt.Errorf("data_dir %q must be an absolute path", s.DataDir)
	}
	return nil
}

func (d DBConfig) validate() error {
	if d.Driver == "" && d.DSN == "" {
		return nil
	}
	if d.Driver != "" {
		normalized := strings.ToLower(strings.TrimSpace(d.Driver))
		if !validDBDrivers[normalized] {
			return fmt.Errorf("driver %q must be one of postgres/postgresql/mysql/mariadb", d.Driver)
		}
	}
	if d.Driver != "" && strings.TrimSpace(d.DSN) == "" {
		return errors.New("dsn must not be empty when driver is set")
	}
	if d.Driver == "" && strings.TrimSpace(d.DSN) != "" {
		return errors.New("driver must be set when dsn is set")
	}
	return nil
}

func (d DatanodeConfig) validate() error {
	for i, p := range d.Paths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("paths[%d] must not be empty", i)
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("paths[%d] %q must be an absolute path", i, p)
		}
		cleaned := filepath.Clean(p)
		if cleaned == "/" {
			return fmt.Errorf("paths[%d] %q must not be root \"/\"", i, p)
		}
	}
	if d.ControllerURL != "" {
		if !strings.HasPrefix(d.ControllerURL, "http://") && !strings.HasPrefix(d.ControllerURL, "https://") {
			return fmt.Errorf("controller_url %q must start with http:// or https://", d.ControllerURL)
		}
	}
	return nil
}

func (a AuthConfig) validate() error {
	seen := make(map[string]bool, len(a.Credentials))
	for i, c := range a.Credentials {
		if strings.TrimSpace(c.AccessKey) == "" {
			return fmt.Errorf("credentials[%d].access_key must not be empty", i)
		}
		if !accessKeyRe.MatchString(c.AccessKey) {
			return fmt.Errorf("credentials[%d].access_key %q is invalid (3-128 chars, no whitespace/colon)", i, c.AccessKey)
		}
		if strings.TrimSpace(c.SecretKey) == "" {
			return fmt.Errorf("credentials[%d].secret_key must not be empty", i)
		}
		if len(c.SecretKey) < 8 {
			return fmt.Errorf("credentials[%d].secret_key is too short (min 8)", i)
		}
		if seen[c.AccessKey] {
			return fmt.Errorf("credentials[%d].access_key %q is duplicated", i, c.AccessKey)
		}
		seen[c.AccessKey] = true
	}
	return nil
}

func (s StorageConfig) validate() error {
	if s.DefaultPolicy != "" {
		if _, err := parsePlacementPolicy(s.DefaultPolicy); err != nil {
			return fmt.Errorf("default_policy: %w", err)
		}
	}
	for bucket, pol := range s.BucketPolicy {
		if strings.TrimSpace(bucket) == "" {
			return fmt.Errorf("bucket_policy: bucket name must not be empty")
		}
		if _, err := parsePlacementPolicy(pol); err != nil {
			return fmt.Errorf("bucket_policy[%q]: %w", bucket, err)
		}
	}
	return nil
}

// parsePlacementPolicy validates a single policy string.
// Canonical forms: single, dual, triple, ALL (case-insensitive).
func parsePlacementPolicy(s string) (string, error) {
	norm := strings.TrimSpace(strings.ToLower(s))
	switch norm {
	case "single", "dual", "triple", "all":
		return norm, nil
	default:
		return "", fmt.Errorf("policy %q must be one of single/dual/triple/ALL", s)
	}
}

func (l LogConfig) validate() error {
	if l.Level == "" {
		return nil
	}
	if !validLogLevels[strings.ToLower(l.Level)] {
		return fmt.Errorf("level %q must be one of debug/info/warn/error", l.Level)
	}
	return nil
}
