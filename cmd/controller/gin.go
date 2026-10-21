// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sylphre/MiryMesh/internal/admin"
	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

var frontendFS embed.FS

func newAdminEngine(h *admin.Handler, setupStore admin.SetupStoreIface, staticDir string, cfg config.Config) *gin.Engine {
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
	adminG.GET("/jobs", ginWrapAuth(h, h.HandleJobs))
	// Setup/auth
	adminG.GET("/setup/status", ginWrap(h.HandleSetupStatus))
	adminG.POST("/setup", ginWrap(h.HandleSetup))
	adminG.POST("/login", ginWrap(h.HandleLogin))
	adminG.POST("/generate-credentials", ginWrapAuth(h, h.HandleGenerateCredentials))
	adminG.POST("/pair-token", ginWrapAuth(h, h.HandlePairToken))
	adminG.GET("/config", ginWrap(h.HandleConfig))
	adminG.PUT("/config/policy", ginWrapAuth(h, h.HandleConfigPolicy))

	// Internal datanode (on admin port as well for simplicity; S3 port also serves /internal)
	r.POST("/internal/register", ginWrap(h.HandleInternalRegister))
	r.POST("/internal/heartbeat", ginWrap(h.HandleInternalHeartbeat))
	r.POST("/internal/disks", ginWrap(h.HandleInternalDisks))
	r.GET("/internal/nodes", ginWrap(h.HandleInternalNodes))
	r.POST("/internal/evacuate", ginWrap(h.HandleInternalEvacuate))

	// Static: serve frontend/dist if exists, else fallback to staticDir (web/static or filesystem)
	distDir := "frontend/dist"
	if fi, err := os.Stat(distDir); err == nil && fi.IsDir() {
		r.Static("/static", distDir)
		r.Static("/assets", filepath.Join(distDir, "assets"))
		r.GET("/", func(c *gin.Context) {
			if !setupStore.IsInitialized() && isHTML(c) {
				c.Redirect(302, "/setup")
				return
			}
			if setupStore.IsInitialized() && !hasTokenGin(c) && isHTML(c) {
				c.Redirect(302, "/login")
				return
			}
			c.File(filepath.Join(distDir, "index.html"))
		})
		// SPA fallback: Vue Router history mode
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/internal/") {
				c.JSON(404, gin.H{"error": "not found"})
				return
			}
			if strings.HasPrefix(c.Request.URL.Path, "/static/") || strings.HasPrefix(c.Request.URL.Path, "/assets/") {
				c.Status(404)
				return
			}
			// Setup/login gating for SPA routes
			if !setupStore.IsInitialized() && c.Request.URL.Path != "/setup" {
				if isHTML(c) {
					c.Redirect(302, "/setup")
					return
				}
			} else if setupStore.IsInitialized() && !hasTokenGin(c) && isHTML(c) {
				if c.Request.URL.Path != "/login" && c.Request.URL.Path != "/setup" {
					c.Redirect(302, "/login")
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
		slog.Warn("frontend/dist not found, serving from filesystem", "dir", staticDir)
		_ = frontendFS
	}
	return r
}

func newS3Engine(router *s3api.Router, authStore *auth.Store, cfg config.Config, reg *datanode.Registry, internalToken string) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(redactedLogger())
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	// Internal on S3 port too
	dnHandler := datanode.NewHandler(reg, internalToken)
	r.POST("/internal/register", ginWrap(dnHandler.HandleRegister))
	r.POST("/internal/heartbeat", ginWrap(dnHandler.HandleHeartbeat))
	r.Any("/internal/*path", ginWrap(dnHandler.HandleAny))
	// Admin hint on S3 port
	r.Any("/api/admin/*path", func(c *gin.Context) {
		c.String(404, "admin API lives on admin port — use that port with X-Admin-Token")
	})
	// S3 catch-all with SigV4 middleware
	r.Use(s3AuthMiddleware(authStore))
	r.Any("/*path", ginWrap(router.HandleHTTP))
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

func s3AuthMiddleware(store *auth.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/healthz" || c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		if store.Count() == 0 {
			if c.GetHeader("Authorization") != "" || c.Query("X-Amz-Algorithm") != "" {
				c.AbortWithStatusJSON(403, gin.H{"error": "AccessDenied"})
				return
			}
			c.Next()
			return
		}
		// Verify via sigv4
		lookup := func(k string) (string, bool) { return store.Secret(k) }
		// Use net/http verify helper via s3api
		if !s3api.VerifyRequest(c.Request, lookup) {
			c.AbortWithStatusJSON(403, gin.H{"error": "SignatureDoesNotMatch"})
			return
		}
		c.Next()
	}
}

func redactedLogger() gin.HandlerFunc {
	return gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		// Redact sensitive headers
		return ""
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

var _ = frontendFS
