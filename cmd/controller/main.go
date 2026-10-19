// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh controller (master) node.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sylphre/MiryMesh/internal/admin"
	"github.com/sylphre/MiryMesh/internal/replication"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
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
	// Internal datanode registry (M2) and placement resolver (M3).
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	internalToken := ""
	if len(cfg.Auth.Credentials) > 0 {
		internalToken = cfg.Auth.Credentials[0].SecretKey
	}
	resolver, err := placement.NewResolver(reg, placement.New(0), cfg.Storage.DefaultPolicy, cfg.Storage.BucketPolicy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller: invalid storage policy: %v\n", err)
		os.Exit(1)
	}
	// TODO: GUI will manage bucket_policy at runtime; resolver.SetBucketPolicy is the wire point.
	s3Router := s3api.NewRouterWithPlacement(store, cfg.Server.Region, resolver)
	dnHandler := datanode.NewHandler(reg, internalToken)
	internalMux := http.NewServeMux()
	dnHandler.RegisterRoutes(internalMux)

	jobsStore := replication.NewStore()
	adminHandler := admin.New(reg, store, resolver, jobsStore, internalToken)
	adminMux := http.NewServeMux()
	adminHandler.RegisterRoutes(adminMux)

	staticDir := resolveStaticDir()
	staticFS := http.FileServer(http.Dir(staticDir))

	mux := http.NewServeMux()
	mux.Handle("/api/admin/", adminMux)
	mux.Handle("/internal/", internalMux)
	mux.Handle("/static/", http.StripPrefix("/static/", staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// GUI entry lives at /static/index.html and /admin; root "/" is S3 ListBuckets.
		// Only redirect browser hits that explicitly accept text/html to the GUI.
		if r.URL.Path == "/" && r.Header.Get("Authorization") == "" && r.URL.Query().Get("X-Amz-Algorithm") == "" {
			if contains(r.Header.Get("Accept"), "text/html") {
				http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
				return
			}
		}
		s3api.AuthMiddleware(authStore, s3Router).ServeHTTP(w, r)
	})

	go func() {
		ticker := time.NewTicker(reg.HeartbeatInterval())
		defer ticker.Stop()
		for range ticker.C {
			offline := reg.MarkOfflineIfStale(time.Now())
			for _, id := range offline {
				slog.Info("node marked offline", "node_id", id)
			}
		}
	}()

	srv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
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

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func resolveStaticDir() string {
	for _, c := range []string{"web/static", "./web/static", "/usr/share/mirymesh/static"} {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return "web/static"
}
