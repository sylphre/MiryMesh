// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// AuthMiddleware verifies SigV4 for all S3 requests. Healthz is bypassed.
func AuthMiddleware(store *auth.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		// If no credentials configured, allow anonymous (dev mode) — but still try to verify if Authorization present.
		if store.Count() == 0 {
			if r.Header.Get("Authorization") != "" || r.URL.Query().Get("X-Amz-Algorithm") != "" {
				// No store to verify against — deny.
				writeError(w, r, errAccessDenied)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		lookup := func(k string) (string, bool) { return store.Secret(k) }
		res, err := sigv4.Verify(r, lookup)
		if err != nil {
			// Map to S3 errors.
			if isSignatureError(err) {
				writeError(w, r, errSignatureMismatch)
				return
			}
			writeError(w, r, errAccessDenied)
			return
		}
		_ = res
		next.ServeHTTP(w, r)
	})
}

func isSignatureError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return msg == "signature mismatch" || msg == "missing SigV4 Authorization or presigned X-Amz-Algorithm"
}
