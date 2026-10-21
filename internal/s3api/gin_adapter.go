// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"

	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// HandleHTTP is the Gin-compatible handler (also used for net/http).
func (rt *Router) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	rt.ServeHTTP(w, r)
}

// VerifyRequest checks SigV4 for Gin middleware.
func VerifyRequest(r *http.Request, lookup func(string) (string, bool)) bool {
	_, err := sigv4.Verify(r, lookup)
	return err == nil
}
