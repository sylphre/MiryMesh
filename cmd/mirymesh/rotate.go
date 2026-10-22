// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sylphre/MiryMesh/internal/admin"
	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/metadata"
)

const defaultConfigPath = "/etc/mirymesh/config.yaml"

// runRotateMasterKey re-encrypts the secrets stored in the setup table with the current
// master key. The previous key must be listed in auth.old_master_keys (or
// MIRYMESH_MASTER_KEY_OLD) so the existing values can be read. It returns the process exit code.
func runRotateMasterKey(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rotate-master-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", defaultConfigPath, "path to the MiryMesh config file")
	dryRun := fs.Bool("dry-run", false, "report what would be re-encrypted without writing anything")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var cfg config.Config
	if _, statErr := os.Stat(*cfgPath); statErr == nil {
		loaded, err := config.Load(*cfgPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "rotate-master-key: load config: %v\n", err)
			return 1
		}
		cfg = loaded
	} else {
		cfg = config.Default()
	}
	// Same environment overrides as the controller.
	if v := os.Getenv("MIRYMESH_DB_DRIVER"); v != "" && cfg.DB.Driver == "" {
		cfg.DB.Driver = v
	}
	if v := os.Getenv("MIRYMESH_DB_DSN"); v != "" && cfg.DB.DSN == "" {
		cfg.DB.DSN = v
	}
	if cfg.DB.Driver == "" || cfg.DB.DSN == "" {
		_, _ = fmt.Fprintln(stderr, "rotate-master-key: db.driver and db.dsn must be configured (config file or MIRYMESH_DB_DRIVER / MIRYMESH_DB_DSN)")
		return 1
	}

	masterKey, oldKeys := cfg.Auth.MasterKeys()
	if masterKey == "" {
		_, _ = fmt.Fprintln(stderr, "rotate-master-key: the new master key is required (auth.master_key or MIRYMESH_MASTER_KEY)")
		return 1
	}
	kr, err := metadata.NewKeyring(masterKey, oldKeys...)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "rotate-master-key: master key: %v\n", err)
		return 1
	}

	db, err := metadata.OpenDB(cfg.DB.Driver, cfg.DB.DSN)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "rotate-master-key: db open: %v\n", err)
		return 1
	}
	defer func() { _ = db.Close() }()

	store := admin.NewSetupStoreDB(db, cfg.DB.Driver, masterKey, oldKeys...)
	rep, err := store.RotateMasterKey(*dryRun)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "rotate-master-key: %v\n", err)
		return 1
	}

	verb := "re-encrypted"
	if *dryRun {
		verb = "would re-encrypt"
	}
	_, _ = fmt.Fprintf(stdout, "rotate-master-key: current key id %s (%d old key(s) configured)\n", kr.CurrentKeyID(), len(oldKeys))
	_, _ = fmt.Fprintf(stdout, "rotate-master-key: %s: %s\n", verb, listOrNone(rep.Rotated))
	_, _ = fmt.Fprintf(stdout, "rotate-master-key: already on the current key: %s\n", listOrNone(rep.Current))
	if !*dryRun && len(rep.Rotated) > 0 {
		_, _ = fmt.Fprintln(stdout, "rotate-master-key: done. Restart controllers with the new key, then remove the old key from auth.old_master_keys / MIRYMESH_MASTER_KEY_OLD.")
	}
	return 0
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
