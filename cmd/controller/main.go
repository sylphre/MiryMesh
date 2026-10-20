// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package main is the entry point for the MiryMesh controller (master) node.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sylphre/MiryMesh/internal/admin"
	"github.com/sylphre/MiryMesh/internal/metadata"
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
			slog.Error("controller: load config failed — check ReadWritePaths includes /etc/mirymesh and file owner is mirymesh:mirymesh", "err", err)
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
	var store s3api.ObjectStore
	if cfg.Server.Mode == "gateway" {
		// Gateway: S3 data lives only on DataNodes; controller holds bucket meta only.
		// DataDir is still used for setup.json/.evac.
		if cfg.Server.DataDir != "" {
			_ = os.MkdirAll(cfg.Server.DataDir, 0o750)
		} else {
			cfg.Server.DataDir = "/var/lib/mirymesh"
			_ = os.MkdirAll(cfg.Server.DataDir, 0o750)
		}
		// Temporary: RemoteStore will be created after registry is ready; use local bucket set for now
		store = &gatewayPlaceholder{dataDir: cfg.Server.DataDir}
	} else {
		ls, err2 := s3api.NewStorage(cfg.Server.DataDir)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "controller: init storage: %v\n", err2)
			os.Exit(1)
		}
		store = ls
	}
	// Adoption mode: load snapshot early to restore storage, then restore placement after resolver
	var adoptedSnapshot *metadata.EvacuationSnapshot
	// Adoption mode: try to load latest evacuation snapshot from local + datanode remote dirs
	// (docs/07). If found, restore buckets and log result. This enables master takeover.
	// Local evacuation dir defaults to <DataDir>/.evac or override via MIRYMESH_EVAC_DIR.
	evacDir := os.Getenv("MIRYMESH_EVAC_DIR")
	localEvac := evacDir
	if localEvac == "" {
		localEvac = filepath.Join(cfg.Server.DataDir, ".evac")
	}
	// Remote dirs are datanode evac paths via MIRYMESH_REMOTE_EVAC_DIRS (comma-separated).
	var remoteDirs []string
	if v := os.Getenv("MIRYMESH_REMOTE_EVAC_DIRS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if s := strings.TrimSpace(p); s != "" {
				remoteDirs = append(remoteDirs, s)
			}
		}
	}
	if snap, loadErr := metadata.LoadAny(localEvac, remoteDirs); loadErr == nil && snap != nil {
		slog.Info("controller: evacuation snapshot found, attempting restore", "version", snap.Version, "buckets", len(snap.Buckets))
		restorer, restorerErr := metadata.NewRestorer(cfg.Server.DataDir)
		if restorerErr == nil {
			if res, restoreErr := restorer.Restore(snap); restoreErr == nil {
				slog.Info("controller: restore completed", "created", res.BucketsCreated, "restored", res.BucketsRestored, "missing", res.ObjectsMissing)
			} else {
				slog.Warn("controller: restore failed", "err", restoreErr)
			}
		}
		adoptedSnapshot = snap
	}
	authStore := auth.NewStore()
	for _, c := range cfg.Auth.Credentials {
		_ = authStore.Add(c.AccessKey, c.SecretKey)
	}
	// Treat db.dsn without driver as misconfigured (M14: no longer optional)
	if cfg.DB.DSN != "" && cfg.DB.Driver == "" {
		fmt.Fprintln(os.Stderr, "controller: db.dsn without driver — set db.driver (postgres/mysql) and auth.master_key / MIRYMESH_MASTER_KEY")
		os.Exit(1)
	}
	if cfg.DB.Driver != "" {
		masterKey := cfg.Auth.MasterKey
		if masterKey == "" {
			masterKey = os.Getenv("MIRYMESH_MASTER_KEY")
		}
		if masterKey == "" {
			fmt.Fprintln(os.Stderr, "controller: auth.master_key (or MIRYMESH_MASTER_KEY env) is required when db is configured — needed for secret_key_enc/dsn_enc")
			os.Exit(1)
		}
		_ = masterKey
		// Open DB, migrate, and seed setup row if empty — full DB wiring lands in next patch (SetupStoreDB)
		slog.Info("controller: db configured", "driver", cfg.DB.Driver, "master_key", "set")
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
	// Restore placement policies from adopted snapshot (docs/07)
	if adoptedSnapshot != nil {
		for b, pol := range adoptedSnapshot.PlacementPolicies {
			if p, err := placement.ParsePolicy(pol); err == nil {
				_ = resolver.SetBucketPolicy(b, p)
			}
		}
		// TODO(M10+): restore AuthIdentities — snapshot carries AccessKey presence list, new controller must re-seed
		// secrets from config.Auth.Credentials or GUI key management. When GUI issues keys, wire authStore.Add here
		// from adoptedSnapshot.AuthIdentities (presence check only; secrets remain out-of-band).
	}
	// TODO: GUI will manage bucket_policy at runtime; resolver.SetBucketPolicy is the wire point.
	jobsStore := replication.NewStore()
	// Setup store: DB-backed when db configured (M14: replaces setup.json), file otherwise.
	var setupStore admin.SetupStoreIface = admin.NewSetupStore(cfg.Server.DataDir)
	if cfg.DB.Driver != "" {
		masterKey := cfg.Auth.MasterKey
		if masterKey == "" {
			masterKey = os.Getenv("MIRYMESH_MASTER_KEY")
		}
		if dbConn, openErr := metadata.OpenDB(cfg.DB.Driver, cfg.DB.DSN); openErr == nil {
			if migErr := metadata.Migrate(dbConn, cfg.DB.Driver); migErr == nil {
				dbStore := admin.NewSetupStoreDB(dbConn, cfg.DB.Driver, masterKey)
				// One-time migration: copy file setup.json -> DB if DB empty and file exists
				if !dbStore.IsInitialized() {
					if fs := admin.NewSetupStore(cfg.Server.DataDir); fs.IsInitialized() {
						if st := fs.Get(); st != nil {
							_ = dbStore.Save(st)
							slog.Info("controller: migrated setup.json -> DB (miry_setup)")
						}
					}
				}
				setupStore = dbStore
				slog.Info("controller: setup store: DB (miry_setup)")
			} else {
				slog.Warn("controller: db migrate failed, falling back to file setup", "err", migErr)
			}
		} else {
			slog.Warn("controller: db open failed, falling back to file setup", "err", openErr)
		}
	}
	if st := setupStore.Get(); st != nil && st.Initialized {
		_ = authStore.Add(st.AccessKey, st.SecretKey)
		internalToken = st.AdminToken
	}
	s3Router := s3api.NewRouterWithPlacement(store, cfg.Server.Region, resolver)
	dnHandler := datanode.NewHandler(reg, internalToken)
	internalMux := http.NewServeMux()
	dnHandler.RegisterRoutes(internalMux)

	adminHandler := admin.NewWithSetup(reg, store, resolver, jobsStore, internalToken, setupStore, authStore)
	adminMux := http.NewServeMux()
	adminHandler.RegisterRoutes(adminMux)

	staticDir := resolveStaticDir()
	staticFS := http.FileServer(http.Dir(staticDir))

	// S3 mux: S3 + /internal/ + /healthz only (no admin). Admin requests here get 404 + hint.
	s3Mux := http.NewServeMux()
	s3Mux.Handle("/internal/", internalMux)
	s3Mux.HandleFunc("/api/admin/", func(w http.ResponseWriter, r *http.Request) {
		slog.Warn("admin API accessed on S3 port — use admin listen", "addr", cfg.Server.AdminListen, "path", r.URL.Path)
		http.Error(w, "admin API lives on admin port ("+cfg.Server.AdminListen+") — use that port with X-Admin-Token", http.StatusNotFound)
	})
	s3Mux.Handle("/healthz", healthHandler())
	s3Mux.Handle("/health", healthHandler())
	s3Served := s3api.AuthMiddleware(authStore, s3Router)
	// S3 catch-all: "/" is pure S3 ListBuckets (no GUI fallback)
	s3Mux.Handle("/", s3Served)

	// Admin mux: /api/admin/* + /static/* + / + setup redirect. No S3 here.
	adminMuxWithStatic := http.NewServeMux()
	// Mount admin API
	adminMuxWithStatic.Handle("/api/admin/", adminMux)
	// Setup wizard redirect: if not initialized, browser hits to / should go to setup.html
	isSetupNeeded := func() bool { return !setupStore.IsInitialized() }
	hasToken := func(r *http.Request) bool {
		if r.Header.Get("X-Admin-Token") != "" || r.URL.Query().Get("token") != "" {
			return true
		}
		// Cookie fallback? Not used; localStorage is client-side, so server checks header only.
		return false
	}
	isHTML := func(r *http.Request) bool { return strings.Contains(r.Header.Get("Accept"), "text/html") }
	// Uninitialized: redirect direct /static/* hits (except setup itself) to setup wizard — wrap static handler
	staticWithSetup := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSetupNeeded() {
			if r.URL.Path == "/static/setup.html" || strings.HasPrefix(r.URL.Path, "/static/css/") || strings.HasPrefix(r.URL.Path, "/static/js/") || strings.HasSuffix(r.URL.Path, "/setup.html") || strings.HasPrefix(r.URL.Path, "/api/admin/setup") {
				// Allow wizard assets and its API
			} else if strings.HasPrefix(r.URL.Path, "/static/") {
				http.Redirect(w, r, "/static/setup.html", http.StatusFound)
				return
			}
		} else if !hasToken(r) && isHTML(r) && strings.HasPrefix(r.URL.Path, "/static/") && r.URL.Path != "/static/login.html" && !strings.HasPrefix(r.URL.Path, "/static/css/") && !strings.HasPrefix(r.URL.Path, "/static/js/") {
			// Auth gate: initialized but no Token -> redirect to login (HTML only, API stays 401)
			http.Redirect(w, r, "/static/login.html", http.StatusFound)
			return
		}
		http.StripPrefix("/static/", staticFS).ServeHTTP(w, r)
	})
	// Re-register with setup gate
	adminMuxWithStatic.Handle("/static/", staticWithSetup)
	// Catch-all for admin: setup redirect + index fallback
	adminMuxWithStatic.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if isSetupNeeded() {
			if r.URL.Path == "/static/setup.html" || strings.HasPrefix(r.URL.Path, "/api/admin/setup") || strings.HasPrefix(r.URL.Path, "/static/css/") || strings.HasPrefix(r.URL.Path, "/static/js/") {
				// Allow wizard
			} else {
				http.Redirect(w, r, "/static/setup.html", http.StatusFound)
				return
			}
		} else if !hasToken(r) && isHTML(r) {
			if r.URL.Path != "/static/login.html" && !strings.HasPrefix(r.URL.Path, "/api/admin/") {
				http.Redirect(w, r, "/static/login.html", http.StatusFound)
				return
			}
		}
		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
			return
		}
		if r.URL.Path == "/static/" || r.URL.Path == "/static" {
			http.Redirect(w, r, "/static/index.html", http.StatusFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/static/") {
			http.ServeFile(w, r, filepath.Join(staticDir, strings.TrimPrefix(r.URL.Path, "/static/")))
			return
		}
		http.NotFound(w, r)
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

	adminListen := cfg.Server.AdminListen
	if adminListen == "" {
		adminListen = "0.0.0.0:9001"
	}
	tailscaleIP := detectTailscaleIP()
	slog.Info("mirymesh-controller: admin listen", "addr", adminListen, "hint", tailscaleIP+":9001; do not expose to public, keep behind firewall/Tailscale ACL")
	slog.Info("mirymesh-controller: s3 listen", "addr", cfg.Server.Listen, "hint", tailscaleIP+":9000 or reverse-proxy; do not expose to public")
	slog.Info("mirymesh-controller starting", "listen", cfg.Server.Listen, "dataDir", cfg.Server.DataDir, "region", cfg.Server.Region, "adminListen", adminListen)
	adminSrv := &http.Server{Addr: adminListen, Handler: adminMuxWithStatic, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("admin listen failed", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      s3Mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "controller: listen: %v\n", err)
		os.Exit(1)
	}
}

// gatewayPlaceholder holds bucket meta only when in gateway mode until RemoteStore is wired.
// It will be replaced with RemoteStore once registry is available.
type gatewayPlaceholder struct {
	dataDir string
	buckets map[string]struct{}
}

func (g *gatewayPlaceholder) CreateBucket(b string) error {
	if g.buckets == nil {
		g.buckets = make(map[string]struct{})
	}
	g.buckets[b] = struct{}{}
	return nil
}
func (g *gatewayPlaceholder) BucketExists(b string) bool  { _, ok := g.buckets[b]; return ok }
func (g *gatewayPlaceholder) DeleteBucket(b string) error { delete(g.buckets, b); return nil }
func (g *gatewayPlaceholder) ListBuckets() []string {
	var o []string
	for k := range g.buckets {
		o = append(o, k)
	}
	return o
}
func (g *gatewayPlaceholder) PutObject(b, k, ct string, meta map[string]string, r io.Reader) (s3api.ObjectMeta, error) {
	return s3api.ObjectMeta{}, fmt.Errorf("gateway: put requires DataNode — register at least one")
}
func (g *gatewayPlaceholder) GetObject(b, k string) (s3api.ObjectMeta, io.ReadCloser, error) {
	return s3api.ObjectMeta{}, nil, fmt.Errorf("gateway: no objects")
}
func (g *gatewayPlaceholder) HeadObject(b, k string) (s3api.ObjectMeta, error) {
	return s3api.ObjectMeta{}, fmt.Errorf("not found")
}
func (g *gatewayPlaceholder) DeleteObject(b, k string) error { return nil }
func (g *gatewayPlaceholder) ListObjectsV2(b, prefix, delim, cont string, max int) (s3api.ListObjectsV2Result, error) {
	return s3api.ListObjectsV2Result{}, nil
}
func (g *gatewayPlaceholder) CreateMultipartUpload(b, k, ct string, meta map[string]string) (*s3api.MultipartUploadState, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *gatewayPlaceholder) UploadPart(id string, n int, r io.Reader) (s3api.PartRecord, error) {
	return s3api.PartRecord{}, fmt.Errorf("not implemented")
}
func (g *gatewayPlaceholder) ListParts(id string) ([]s3api.PartRecord, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *gatewayPlaceholder) ListMultipartUploads(b string) ([]*s3api.MultipartUploadState, error) {
	return nil, fmt.Errorf("not implemented")
}
func (g *gatewayPlaceholder) AbortMultipartUpload(id string) error {
	return fmt.Errorf("not implemented")
}
func (g *gatewayPlaceholder) CompleteMultipartUpload(id string, parts []s3api.CompletePart) (s3api.ObjectMeta, error) {
	return s3api.ObjectMeta{}, fmt.Errorf("not implemented")
}
func (g *gatewayPlaceholder) GetUpload(id string) *s3api.MultipartUploadState { return nil }
func (g *gatewayPlaceholder) MultipartDir(id string) string                   { return "" }

func (g *gatewayPlaceholder) Root() string              { return g.dataDir }
func (g *gatewayPlaceholder) FilesystemCapacity() int64 { return 0 }
func (g *gatewayPlaceholder) FilesystemAvail() int64    { return 0 }
func (g *gatewayPlaceholder) FilesystemUsed() int64     { return 0 }
func (g *gatewayPlaceholder) IsOverQuota(int64) bool    { return false }

func healthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})
}

func detectTailscaleIP() string {
	out, err := os.ReadFile("/proc/net/route")
	_ = out
	_ = err
	// Try tailscale ip -4
	if b, err := os.ReadFile("/tmp/tailscale-ip"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	// Try running tailscale binary if available
	return "<tailscale-ip>"
}

func resolveStaticDir() string {
	for _, c := range []string{"web/static", "./web/static", "/usr/share/mirymesh/static"} {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			return c
		}
	}
	return "web/static"
}
