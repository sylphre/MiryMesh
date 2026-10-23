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

// AuthenticateRequest checks SigV4 for Gin middleware and, for aws-chunked
// uploads, replaces r.Body with the decoded payload. It returns nil when the
// request may proceed.
func AuthenticateRequest(r *http.Request, lookup func(string) (string, bool)) *APIError {
	_, apiErr := AuthenticateRequestAK(r, lookup)
	return apiErr
}

// AuthenticateRequestAK is AuthenticateRequest that also returns the access key the
// request was signed with, so the caller can authorize it.
func AuthenticateRequestAK(r *http.Request, lookup func(string) (string, bool)) (string, *APIError) {
	res, err := sigv4.Verify(r, lookup)
	if err != nil {
		e := errAccessDenied
		if isSignatureError(err) {
			e = errSignatureMismatch
		}
		return "", &e
	}
	return res.AccessKey, prepareStreamingBody(r, res)
}

// VerifyRequest checks SigV4 for Gin middleware.
func VerifyRequest(r *http.Request, lookup func(string) (string, bool)) bool {
	return AuthenticateRequest(r, lookup) == nil
}
