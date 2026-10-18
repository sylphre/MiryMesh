// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh controller (master) node.
package main

import (
	"fmt"
	"os"

	"github.com/sylphre/MiryMesh/internal/config"
)

func main() {
	cfgPath := "/etc/mirymesh/config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller: load config: %v\n", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "controller: invalid config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("mirymesh-controller starting on %s\n", cfg.Server.Listen)
}
