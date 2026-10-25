// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh data node agent.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	tokenDir := cfg.Server.DataDir
	if tokenDir == "" {
		tokenDir = "/var/lib/mirymesh"
	}
	// Disks added from the Master are kept here, apart from config.yaml, because
	// install.sh --path rewrites the paths line of config.yaml.
	pathsFile := filepath.Join(tokenDir, "datanode.paths.json")
	persisted, lErr := datanode.LoadExtraPaths(pathsFile)
	if lErr != nil {
		slog.Warn("datanode: cannot read added disk paths", "file", pathsFile, "err", lErr)
	}
	if len(persisted) > 0 {
		base := cfg.Datanode.Paths
		if len(base) == 0 {
			base = []string{filepath.Join("/var/lib/mirymesh", "data")}
		}
		cfg.Datanode.Paths = datanode.MergePaths(base, persisted)
		slog.Info("datanode: disks added from the Master", "paths", persisted)
	}

	// DataNode local storage for gateway objects: one store per configured disk.
	// The first usable disk is the default for requests without ?disk=.
	var store s3api.ObjectStore
	disks := make(map[string]s3api.ObjectStore)
	var current []string // cleaned paths of the disks that are in use
	storePaths := cfg.Datanode.Paths
	if len(storePaths) == 0 {
		storePaths = []string{filepath.Join("/var/lib/mirymesh", "data")}
	}
	for _, p := range storePaths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := os.MkdirAll(p, 0o750); err != nil {
			slog.Warn("datanode: local store path unusable", "path", p, "err", err)
			continue
		}
		s, sErr := s3api.NewStorage(p)
		if sErr != nil {
			slog.Warn("datanode: local store init failed", "path", p, "err", sErr)
			continue
		}
		disks[filepath.Clean(p)] = s
		current = append(current, filepath.Clean(p))
		if store == nil {
			store = s
		}
		slog.Info("datanode: local store", "path", p)
	}
	agent := datanode.NewAgent(cfg)
	// Serve DataNode internal API including /internal/objects/* for gateway
	mux := http.NewServeMux()
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	token := ""
	if len(cfg.Auth.Credentials) > 0 {
		token = cfg.Auth.Credentials[0].SecretKey
	}
	// Wrap only a non-nil store: a typed adapter around nil would defeat the
	// handler's "storage not configured" check.
	var objects interface{}
	var dnStore *s3api.DataNodeStore
	if store != nil {
		dnStore = s3api.NewDataNodeStoreWithDisks(store, disks)
		objects = dnStore
	}
	dnHandler := datanode.NewHandlerWithObjects(reg, token, objects)
	dnHandler.RegisterRoutes(mux)

	// Master-triggered disk addition (POST /internal/paths). Disks can only be added.
	var pathMu sync.Mutex
	addPath := func(raw string) (string, bool, error) {
		p, cErr := datanode.CleanDiskPath(raw)
		if cErr != nil {
			return "", false, cErr
		}
		pathMu.Lock()
		defer pathMu.Unlock()
		for _, c := range current {
			if c == p {
				return p, false, nil
			}
			if datanode.PathsOverlap(c, p) {
				return "", false, fmt.Errorf("%w: %s overlaps the existing disk %s", datanode.ErrPathRejected, p, c)
			}
		}
		if dnStore == nil {
			return "", false, errors.New("this node has no usable local store; fix its data path and restart the DataNode")
		}
		s, oErr := openDisk(p)
		if oErr != nil {
			return "", false, fmt.Errorf("%w: cannot use %s: %v", datanode.ErrPathRejected, p, oErr)
		}
		next := append(append([]string(nil), persisted...), p)
		if sErr := datanode.SaveExtraPaths(pathsFile, next); sErr != nil {
			return "", false, fmt.Errorf("save %s: %w", pathsFile, sErr)
		}
		if aErr := dnStore.AddDisk(p, s); aErr != nil {
			return "", false, aErr
		}
		persisted = next
		current = append(current, p)
		agent.AddPath(p)
		go func() {
			if rErr := agent.ReportDisks(); rErr != nil {
				slog.Warn("datanode: report disks after adding a path failed", "err", rErr)
			}
		}()
		slog.Info("datanode: disk added from the Master", "path", p)
		return p, true, nil
	}
	dnHandler.SetPathAdder(addPath)

	// Snapshots pushed by the Master are kept under the data dir, which survives
	// updates (and container rebuilds when it is a volume). The handler refuses
	// them when no directory is set, so nothing ever goes to /tmp.
	if tokenDir != "" {
		dnHandler.SetEvacDir(filepath.Join(tokenDir, ".evac"))
	}

	// Persist the controller-issued node token so restarts don't need a new pair token.
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

// openDisk prepares a new data directory: it must exist (or be creatable) and
// be writable. Under ProtectSystem=strict a path outside ReadWritePaths fails here.
func openDisk(p string) (s3api.ObjectStore, error) {
	if err := os.MkdirAll(p, 0o750); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(p, ".mirymesh-write-test-*")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	s, err := s3api.NewStorage(p)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
