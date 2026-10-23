// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sylphre/MiryMesh/frontend"
	"github.com/sylphre/MiryMesh/internal/admin"
	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/s3api"
	"github.com/sylphre/MiryMesh/internal/users"
)

func newAdminEngine(h *admin.Handler, setupStore admin.SetupStoreIface, staticDir string, cfg config.Config, reg *datanode.Registry, internalToken string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(redactedLogger())
	// Health
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })

	// Admin API group (Gin handlers via admin)
	adminG := r.Group("/api/admin")
	adminG.GET("/health", ginWrap(h.HandleHealth))
	adminG.GET("/nodes", ginWrapAuth(h, h.HandleNodes))
	adminG.GET("/disks", ginWrapAuth(h, h.HandleDisks))
	adminG.GET("/cluster", ginWrapAuth(h, h.HandleCluster))
	adminG.GET("/metrics", ginWrapAuth(h, h.HandleMetrics))
	adminG.GET("/buckets", ginWrapAuth(h, h.HandleBuckets))
	adminG.POST("/buckets", ginWrapAuth(h, h.HandleBuckets))
	adminG.DELETE("/buckets/:bucket", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/buckets/:bucket/policy", ginWrapAuth(h, h.HandleBucketSub))
	adminG.PUT("/buckets/:bucket/policy", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/buckets/:bucket/s3-policy", ginWrapAuth(h, h.HandleBucketSub))
	adminG.PUT("/buckets/:bucket/s3-policy", ginWrapAuth(h, h.HandleBucketSub))
	adminG.DELETE("/buckets/:bucket/s3-policy", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/buckets/:bucket/quota", ginWrapAuth(h, h.HandleBucketSub))
	adminG.PUT("/buckets/:bucket/quota", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/buckets/:bucket/domains", ginWrapAuth(h, h.HandleBucketSub))
	adminG.PUT("/buckets/:bucket/domains", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/buckets/:bucket/objects", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/buckets/:bucket/object", ginWrapAuth(h, h.HandleBucketSub))
	adminG.PUT("/buckets/:bucket/object", ginWrapAuth(h, h.HandleBucketSub))
	adminG.DELETE("/buckets/:bucket/object", ginWrapAuth(h, h.HandleBucketSub))
	adminG.GET("/jobs", ginWrapAuth(h, h.HandleJobs))
	// S3 users (access keys + per-bucket permissions)
	adminG.GET("/users", ginWrapAuth(h, h.HandleUsers))
	adminG.POST("/users", ginWrapAuth(h, h.HandleUsers))
	adminG.GET("/users/:name", ginWrapAuth(h, h.HandleUsers))
	adminG.PATCH("/users/:name", ginWrapAuth(h, h.HandleUsers))
	adminG.DELETE("/users/:name", ginWrapAuth(h, h.HandleUsers))
	adminG.PUT("/users/:name/permissions", ginWrapAuth(h, h.HandleUsers))
	adminG.POST("/users/:name/rotate-key", ginWrapAuth(h, h.HandleUsers))
	// Setup/auth
	adminG.GET("/setup/status", ginWrap(h.HandleSetupStatus))
	adminG.POST("/setup", ginWrap(h.HandleSetup))
	adminG.POST("/login", ginWrap(h.HandleLogin))
	adminG.POST("/generate-credentials", ginWrapAuth(h, h.HandleGenerateCredentials))
	adminG.POST("/pair-token", ginWrapAuth(h, h.HandlePairToken))
	adminG.POST("/nodes/adopt", ginWrapAuth(h, h.HandleAdoptNode))
	adminG.GET("/config", ginWrap(h.HandleConfig))
	adminG.PUT("/config/policy", ginWrapAuth(h, h.HandleConfigPolicy))

	// Internal datanode (on admin port as well for simplicity; S3 port also serves /internal)
	dnHandler := datanode.NewHandler(reg, internalToken)
	// UI-issued one-time pair tokens are accepted by /internal/register.
	if sdb, ok := setupStore.(*admin.SetupStoreDB); ok && sdb != nil {
		dnHandler.SetPairConsumer(sdb.ConsumePairToken)
	}
	r.POST("/internal/register", ginWrap(dnHandler.HandleRegister))
	r.POST("/internal/heartbeat", ginWrap(dnHandler.HandleHeartbeat))
	r.POST("/internal/disks", ginWrap(dnHandler.HandleDisks))
	r.GET("/internal/disks", ginWrap(dnHandler.HandleDisks))
	r.GET("/internal/nodes", ginWrap(dnHandler.HandleNodes))
	r.POST("/internal/evacuate", ginWrap(dnHandler.HandleEvacuate))
	r.POST("/internal/restore", ginWrap(dnHandler.HandleRestore))
	r.Any("/internal/objects/*path", ginWrap(dnHandler.HandleObjects))

	// Static: prefer embed frontend/dist, fallback to filesystem distDir or staticDir
	distFS, distErr := fs.Sub(frontend.Dist, "dist")
	hasEmbedIndex := false
	if distErr == nil {
		if _, err := fs.Stat(distFS, "index.html"); err == nil {
			hasEmbedIndex = true
		}
	}
	if hasEmbedIndex {
		if assetsFS, err := fs.Sub(distFS, "assets"); err == nil {
			r.StaticFS("/assets", http.FS(assetsFS))
		} else {
			r.StaticFS("/assets", http.FS(distFS))
		}
		// Also serve index.html via embed for SPA
		r.GET("/", func(c *gin.Context) {
			if !setupStore.IsInitialized() && isHTML(c) {
				c.Redirect(302, "/setup")
				return
			}
			// Auth is enforced by the SPA router guard and the API: the token lives in
			// localStorage, so a browser navigation (F5) never carries it.
			c.FileFromFS("/", http.FS(distFS))
		})
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/internal/") {
				c.JSON(404, gin.H{"error": "not found"})
				return
			}
			if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
				c.Status(404)
				return
			}
			if !setupStore.IsInitialized() && c.Request.URL.Path != "/setup" {
				if isHTML(c) {
					c.Redirect(302, "/setup")
					return
				}
			}
			c.FileFromFS("/", http.FS(distFS))
		})
	} else if hasFilesystemDist() {
		distDir := "frontend/dist"
		r.Static("/static", distDir)
		r.Static("/assets", filepath.Join(distDir, "assets"))
		r.GET("/", func(c *gin.Context) {
			if !setupStore.IsInitialized() && isHTML(c) {
				c.Redirect(302, "/setup")
				return
			}
			c.File(filepath.Join(distDir, "index.html"))
		})
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/internal/") {
				c.JSON(404, gin.H{"error": "not found"})
				return
			}
			if strings.HasPrefix(c.Request.URL.Path, "/static/") || strings.HasPrefix(c.Request.URL.Path, "/assets/") {
				c.Status(404)
				return
			}
			if !setupStore.IsInitialized() && c.Request.URL.Path != "/setup" {
				if isHTML(c) {
					c.Redirect(302, "/setup")
					return
				}
			}
			c.File(filepath.Join(distDir, "index.html"))
		})
	} else {
		r.Static("/static", staticDir)
		r.GET("/", func(c *gin.Context) {
			if !setupStore.IsInitialized() && isHTML(c) {
				c.Redirect(302, "/static/setup.html")
				return
			}
			if setupStore.IsInitialized() && !hasTokenGin(c) && isHTML(c) {
				c.Redirect(302, "/static/login.html")
				return
			}
			c.File(filepath.Join(staticDir, "index.html"))
		})
		slog.Warn("frontend/dist not found or empty, serving from filesystem", "dir", staticDir)
	}
	return r
}

func newS3Engine(router *s3api.Router, authStore *auth.Store, userMgr *users.Manager, cfg config.Config, reg *datanode.Registry, internalToken string, setupStore admin.SetupStoreIface) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(redactedLogger())
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	// Internal on S3 port too
	dnHandler := datanode.NewHandler(reg, internalToken)
	// UI-issued one-time pair tokens are accepted on this port as well.
	if sdb, ok := setupStore.(*admin.SetupStoreDB); ok && sdb != nil {
		dnHandler.SetPairConsumer(sdb.ConsumePairToken)
	}
	r.POST("/internal/register", ginWrap(dnHandler.HandleRegister))
	r.POST("/internal/heartbeat", ginWrap(dnHandler.HandleHeartbeat))
	r.POST("/internal/disks", ginWrap(dnHandler.HandleDisks))
	r.GET("/internal/disks", ginWrap(dnHandler.HandleDisks))
	r.GET("/internal/nodes", ginWrap(dnHandler.HandleNodes))
	r.POST("/internal/evacuate", ginWrap(dnHandler.HandleEvacuate))
	r.POST("/internal/restore", ginWrap(dnHandler.HandleRestore))
	r.Any("/internal/objects/*path", ginWrap(dnHandler.HandleObjects))
	// Admin hint on S3 port
	r.Any("/api/admin/*path", func(c *gin.Context) {
		c.String(404, "admin API lives on admin port — use that port with X-Admin-Token")
	})
	// S3 catch-all with SigV4 middleware — must not conflict with /internal/* static routes
	r.Use(s3HostGuard(router.BackingStore()))
	r.Use(s3AnonymousRead(router.BackingStore()))
	r.Use(s3AuthMiddleware(authStore, userMgr))
	r.NoRoute(func(c *gin.Context) {
		// Gin's /*path conflicts with /internal/* static routes, so S3 catch-all is via NoRoute
		router.HandleHTTP(c.Writer, c.Request)
	})
	return r
}

func ginWrap(h http.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

func ginWrapAuth(h *admin.Handler, fn http.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		// checkAuth uses ResponseWriter to write 401; we need to intercept
		rw := &ginResponseWriter{ResponseWriter: c.Writer, status: 200}
		if !h.CheckAuth(rw, c.Request) {
			if rw.status != 200 {
				c.AbortWithStatus(rw.status)
			} else {
				c.AbortWithStatus(401)
			}
			return
		}
		fn.ServeHTTP(c.Writer, c.Request)
	}
}

type ginResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *ginResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// s3AuthMiddleware verifies SigV4 against the root credentials and the S3 users. Root keys may do
// anything; a user key is limited to the buckets and permissions granted to it.
func s3AuthMiddleware(store *auth.Store, userMgr *users.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/healthz" || c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		if _, anon := c.Get(anonReadKey); anon {
			c.Next()
			return
		}
		if store.Count() == 0 && userMgr.Count() == 0 {
			if c.GetHeader("Authorization") != "" || c.Query("X-Amz-Algorithm") != "" {
				c.AbortWithStatusJSON(403, gin.H{"error": "AccessDenied"})
				return
			}
			c.Next()
			return
		}
		// Verify via sigv4
		lookup := func(k string) (string, bool) {
			if s, ok := store.Secret(k); ok {
				return s, true
			}
			return userMgr.Secret(k)
		}
		// Use net/http verify helper via s3api; it also decodes aws-chunked bodies.
		accessKey, apiErr := s3api.AuthenticateRequestAK(c.Request, lookup)
		if apiErr != nil {
			c.AbortWithStatusJSON(apiErr.Status, gin.H{"error": apiErr.Code})
			return
		}
		if !store.Exists(accessKey) {
			// A user key: only what its grants allow.
			reqs, ok := users.Required(c.Request)
			if !ok || !userMgr.Authorize(accessKey, reqs) {
				c.AbortWithStatusJSON(403, gin.H{"error": "AccessDenied"})
				return
			}
			if len(reqs) == 0 { // ListBuckets: show only the buckets the user can access
				c.Request = s3api.WithBucketFilter(c.Request, func(b string) bool { return userMgr.CanSee(accessKey, b) })
			}
		}
		c.Next()
	}
}

func redactedLogger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(p gin.LogFormatterParams) string {
		msg := fmt.Sprintf("%s %s %d %s %s\n", p.Method, p.Path, p.StatusCode, p.Latency, p.ClientIP)
		// Redact token values in log line
		if tok := p.Request.Header.Get("X-Admin-Token"); tok != "" {
			msg = strings.ReplaceAll(msg, tok, "***")
		}
		if tok := p.Request.URL.Query().Get("token"); tok != "" {
			msg = strings.ReplaceAll(msg, tok, "***")
		}
		return msg
	})
}

func isHTML(c *gin.Context) bool {
	return strings.Contains(c.GetHeader("Accept"), "text/html")
}

func hasTokenGin(c *gin.Context) bool {
	if c.GetHeader("X-Admin-Token") != "" || c.Query("token") != "" {
		return true
	}
	return false
}

func hasFilesystemDist() bool {
	fi, err := os.Stat("frontend/dist")
	return err == nil && fi.IsDir()
}

func migrateFileOnce(dataDir string, dbStore *admin.SetupStoreDB) {
	if dbStore.IsInitialized() {
		return
	}
	p := filepath.Join(dataDir, "setup.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var st admin.SetupState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	if !st.Initialized {
		return
	}
	if err := dbStore.Save(&st); err != nil {
		slog.Warn("migrate setup.json -> DB failed", "err", err)
		return
	}
	// Rename to avoid re-migration
	_ = os.Rename(p, p+".migrated-"+time.Now().Format("20060102-150405"))
	slog.Info("migrated setup.json -> DB")
}
