// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"net/url"
	"strings"
)

func parseCopySource(v string) (bucket, key string, err error) {
	// x-amz-copy-source is url-encoded source like /bucket/key or bucket/key (with ?versionId ignored)
	decoded, decErr := url.PathUnescape(v)
	if decErr != nil {
		decoded = v
	}
	// Strip query (?versionId=)
	if idx := strings.Index(decoded, "?"); idx >= 0 {
		decoded = decoded[:idx]
	}
	decoded = strings.TrimPrefix(decoded, "/")
	idx := strings.Index(decoded, "/")
	if idx < 0 {
		return "", "", errInvalidRequest
	}
	return decoded[:idx], decoded[idx+1:], nil
}

// copyCondition holds x-amz-copy-source-if-* preconditions.
type copyCondition struct {
	matchETag       string
	noneMatch       string
	modifiedSince   string
	unmodifiedSince string
}

func parseCopyConditions(r *http.Request) copyCondition {
	return copyCondition{
		matchETag:       r.Header.Get("x-amz-copy-source-if-match"),
		noneMatch:       r.Header.Get("x-amz-copy-source-if-none-match"),
		modifiedSince:   r.Header.Get("x-amz-copy-source-if-modified-since"),
		unmodifiedSince: r.Header.Get("x-amz-copy-source-if-unmodified-since"),
	}
}

func hasAnyCopyCondition(c copyCondition) bool {
	return c.matchETag != "" || c.noneMatch != "" || c.modifiedSince != "" || c.unmodifiedSince != ""
}
