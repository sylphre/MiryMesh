// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh controller (master) node.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

func main() {
	cfgPath := "/etc/mirymesh/config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}
	var cfg config.Config
	var err error
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "controller: load config: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg = config.Default()
		slog.Info("controller: config not found, using defaults", "path", cfgPath)
	}
	if vErr := cfg.Validate(); vErr != nil {
		fmt.Fprintf(os.Stderr, "controller: invalid config: %v\n", vErr)
		os.Exit(1)
	}
	store, err := s3api.NewStorage(cfg.Server.DataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller: init storage: %v\n", err)
		os.Exit(1)
	}
	authStore := auth.NewStore()
	for _, c := range cfg.Auth.Credentials {
		_ = authStore.Add(c.AccessKey, c.SecretKey)
	}
	router := s3api.NewRouter(store, cfg.Server.Region)
	handler := s3api.AuthMiddleware(authStore, router)

	srv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	slog.Info("mirymesh-controller starting", "listen", cfg.Server.Listen, "dataDir", cfg.Server.DataDir, "region", cfg.Server.Region)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "controller: listen: %v\n", err)
		os.Exit(1)
	}
}
