// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"context"
	"net/http"
)

type bucketFilterKey struct{}

// WithBucketFilter returns a request whose ListBuckets answer only contains the buckets
// allow reports true for. It is used to hide buckets a user has no permission on.
func WithBucketFilter(r *http.Request, allow func(bucket string) bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), bucketFilterKey{}, allow))
}

// filterBuckets applies the filter set by WithBucketFilter, if any.
func filterBuckets(r *http.Request, in []string) []string {
	allow, _ := r.Context().Value(bucketFilterKey{}).(func(string) bool)
	if allow == nil {
		return in
	}
	out := make([]string, 0, len(in))
	for _, b := range in {
		if allow(b) {
			out = append(out, b)
		}
	}
	return out
}
