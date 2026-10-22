// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import "strings"

// maxKeyBytes is the S3 limit for an object key (UTF-8 bytes).
const maxKeyBytes = 1024

// validateObjectKey rejects keys that cannot be stored safely on a filesystem
// backend: over-long keys, NUL bytes, and "." / ".." path segments (which would
// either escape the bucket directory or collide with another key after
// cleaning). The boolean is false when the key is invalid and the returned
// APIError should be sent to the client.
func validateObjectKey(key string) (APIError, bool) {
	if len(key) > maxKeyBytes {
		return errKeyTooLong, false
	}
	if strings.IndexByte(key, 0) >= 0 {
		return errInvalidRequest, false
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == ".." || seg == "." {
			return errInvalidRequest, false
		}
	}
	return APIError{}, true
}

// ValidateObjectKey is the exported form of validateObjectKey for callers
// outside this package (e.g. the admin API). It rejects empty keys as well as
// every key validateObjectKey rejects, returning the matching APIError.
func ValidateObjectKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errInvalidRequest
	}
	if apiErr, ok := validateObjectKey(key); !ok {
		return apiErr
	}
	return nil
}
