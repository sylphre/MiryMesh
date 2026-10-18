// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh data node agent.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/datanode"
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
	agent := datanode.NewAgent(cfg)
	// Health endpoint for liveness probe.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Run health server if we have a listen addr? Datanode reuses controller listen for now if not set.
	// Otherwise start registration + heartbeat loop in background.
	go func() {
		if err := agent.RegisterAndHeartbeatLoop(time.NewTicker(30 * time.Second)); err != nil {
			slog.Error("datanode: register loop failed", "err", err)
		}
	}()
	// Keep process alive; health endpoint is optional future.
	_ = mux
	slog.Info("mirymesh-datanode started", "controller", cfg.Datanode.ControllerURL, "paths", cfg.Datanode.Paths)
	select {}
}
