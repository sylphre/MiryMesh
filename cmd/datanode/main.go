// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh data node agent.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/s3api"
	"github.com/sylphre/MiryMesh/internal/version"
)

func main() {
	var controllerURL, pairToken string
	var paths multiFlag
	var allowAdopt, showVersion bool
	flag.StringVar(&controllerURL, "controller-url", "", "controller URL (e.g. http://master:9001)")
	flag.StringVar(&pairToken, "pair-token", "", "one-time pairing token from POST /api/admin/pair-token")
	flag.Var(&paths, "path", "datanode path (repeatable, e.g. --path /mnt/disk1)")
	flag.BoolVar(&allowAdopt, "allow-adopt", false, "accept controller-initiated pairing via POST /internal/adopt (only while unpaired; enable on trusted networks only)")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()
	if showVersion {
		fmt.Println(version.Version)
		return
	}
	cfgPath := "/etc/mirymesh/config.yaml"
	if flag.NArg() > 0 {
		cfgPath = flag.Arg(0)
	} else if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
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
	// CLI overrides
	if controllerURL != "" {
		cfg.Datanode.ControllerURL = controllerURL
	}
	if len(paths) > 0 {
		cfg.Datanode.Paths = []string(paths)
	}
	if pairToken != "" {
		// Use pair token as internal token for registration
		cfg.Auth.Credentials = []config.Credential{{AccessKey: "pair", SecretKey: pairToken}}
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
	// Persist the controller-issued node token so restarts don't need a new pair token.
	tokenDir := cfg.Server.DataDir
	if tokenDir == "" {
		tokenDir = "/var/lib/mirymesh"
	}
	agent.Bind(filepath.Join(tokenDir, "datanode.token"), dnHandler.SetNodeToken)
	// Master-triggered self-update: stages a verified release binary under <data dir>/update.
	selfUpdater := datanode.NewUpdater(filepath.Join(tokenDir, "update"))
	selfUpdater.CheckStaged()
	dnHandler.SetUpdater(selfUpdater)
	slog.Info("datanode: version", "version", version.Version, "self_update", selfUpdater.Enabled)
	if allowAdopt {
		dnHandler.SetAdopter(agent.Adopt)
		slog.Warn("datanode: controller-initiated pairing enabled (/internal/adopt); keep the DataNode port on a trusted network")
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Listen on :9002 by default (DataNode)
	listen := cfg.Datanode.Listen
	if listen == "" {
		listen = "0.0.0.0:9002"
	}
	go func() {
		slog.Info("datanode: listening", "addr", listen, "hint", "0.0.0.0:9002 on Tailscale <tailscale-ip>:9002; do not expose to public, keep behind firewall/Tailscale ACL")
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

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
