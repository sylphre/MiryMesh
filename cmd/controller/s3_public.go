// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

// anonReadKey marks a request that the bucket policy lets through without a signature.
const anonReadKey = "mirymesh.anonymous_read"

// s3HostGuard refuses requests whose Host header is not one of the domains configured
// for the addressed bucket (the domain itself or any subdomain). Buckets without a
// domain list accept every host. Behind NGINX, pass the original host with
// "proxy_set_header Host $host;".
func s3HostGuard(store s3api.ObjectStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, err := s3api.CheckBucketHost(store, c.Request)
		if err != nil {
			slog.Error("s3: bucket domain lookup failed", "err", err)
			c.AbortWithStatusJSON(503, gin.H{"error": "ServiceUnavailable"})
			return
		}
		if !ok {
			c.AbortWithStatusJSON(403, gin.H{"error": "AccessDenied"})
			return
		}
		c.Next()
	}
}

// s3AnonymousRead lets unsigned GET/HEAD requests through to objects that the bucket
// policy makes public (Principal "*" with s3:GetObject). Everything else still needs SigV4.
func s3AnonymousRead(store s3api.ObjectStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if s3api.AnonymousReadAllowed(store, c.Request) {
			c.Set(anonReadKey, true)
		}
		c.Next()
	}
}
