// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh data node agent.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/datanode"
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
			fmt.Fprintf(os.Stderr, "datanode: load config: %v\n", err)
			os.Exit(1)
		}
	} else {
		cfg = config.Default()
		slog.Info("datanode: config not found, using defaults", "path", cfgPath)
	}
	if vErr := cfg.Validate(); vErr != nil {
		fmt.Fprintf(os.Stderr, "datanode: invalid config: %v\n", vErr)
		os.Exit(1)
	}
	// DataNode local storage for gateway objects: use first Datanode path or fallback
	var store s3api.ObjectStore
	storePath := ""
	if len(cfg.Datanode.Paths) > 0 {
		storePath = cfg.Datanode.Paths[0]
	}
	if storePath == "" {
		storePath = filepath.Join("/var/lib/mirymesh", "data")
	}
	if err := os.MkdirAll(storePath, 0o750); err == nil {
		if s, sErr := s3api.NewStorage(storePath); sErr == nil {
			store = s
			slog.Info("datanode: local store", "path", storePath)
		} else {
			slog.Warn("datanode: local store init failed", "err", sErr)
		}
	}
	agent := datanode.NewAgent(cfg)
	// Serve DataNode internal API including /internal/objects/* for gateway
	mux := http.NewServeMux()
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	token := ""
	if len(cfg.Auth.Credentials) > 0 {
		token = cfg.Auth.Credentials[0].SecretKey
	}
	dnHandler := datanode.NewHandlerWithObjects(reg, token, store)
	dnHandler.RegisterRoutes(mux)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Listen on :9002 by default (DataNode)
	listen := ":9002"
	go func() {
		slog.Info("datanode: listening", "addr", listen)
		if err := http.ListenAndServe(listen, mux); err != nil {
			slog.Error("datanode: listen failed", "err", err)
		}
	}()
	go func() {
		if err := agent.RegisterAndHeartbeatLoop(time.NewTicker(30 * time.Second)); err != nil {
			slog.Error("datanode: register loop failed", "err", err)
		}
	}()
	slog.Info("mirymesh-datanode started", "controller", cfg.Datanode.ControllerURL, "paths", cfg.Datanode.Paths)
	select {}
}
