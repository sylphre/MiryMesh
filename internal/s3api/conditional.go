// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// precondResult is the outcome of evaluating conditional request headers.
type precondResult int

const (
	precondOK          precondResult = iota // serve the object
	precondNotModified                      // 304
	precondFailed                           // 412
)

// etagMatches reports whether an If-Match / If-None-Match header value (a
// comma separated list of entity tags, or "*") matches etag. Weak validators
// (W/"...") are compared by their opaque tag, which is what S3 does.
func etagMatches(header, etag string) bool {
	etag = strings.Trim(etag, `"`)
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		if p == "*" {
			return true
		}
		p = strings.Trim(strings.TrimPrefix(p, "W/"), `"`)
		if p != "" && p == etag {
			return true
		}
	}
	return false
}

// evalReadPreconditions evaluates the conditional headers of GET/HEAD Object
// in the order defined by RFC 7232 section 6, which is also S3's behavior:
//
//  1. If-Match (else If-Unmodified-Since) -> 412 when it does not hold.
//  2. If-None-Match (else If-Modified-Since) -> 304 when it does not hold.
//
// HTTP dates have one second resolution, so the object's modification time is
// truncated to whole seconds before comparing. Unparseable dates are ignored.
func evalReadPreconditions(h http.Header, meta ObjectMeta) precondResult {
	mod := meta.LastMod.UTC().Truncate(time.Second)

	if im := h.Get("If-Match"); im != "" {
		if !etagMatches(im, meta.ETag) {
			return precondFailed
		}
	} else if ius := h.Get("If-Unmodified-Since"); ius != "" {
		if t, err := http.ParseTime(ius); err == nil && mod.After(t) {
			return precondFailed
		}
	}

	if inm := h.Get("If-None-Match"); inm != "" {
		if etagMatches(inm, meta.ETag) {
			return precondNotModified
		}
	} else if ims := h.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil && !mod.After(t) {
			return precondNotModified
		}
	}
	return precondOK
}

// rangeResult classifies a Range request header against an object size.
type rangeResult int

const (
	rangeIgnore        rangeResult = iota // malformed / unsupported: serve the whole object (200)
	rangeOK                               // satisfiable single range (206)
	rangeUnsatisfiable                    // 416 InvalidRange
)

// parseByteRange parses a single "bytes=" range (START-END, START-, -SUFFIX).
// Like S3, a malformed header or a multi-range request is ignored (the whole
// object is returned), while a well-formed range that starts at or beyond the
// end of the object is unsatisfiable. start/end are inclusive and only
// meaningful when the result is rangeOK.
func parseByteRange(spec string, size int64) (int64, int64, rangeResult) {
	if !strings.HasPrefix(spec, "bytes=") {
		return 0, 0, rangeIgnore
	}
	rest := strings.TrimSpace(strings.TrimPrefix(spec, "bytes="))
	if strings.Contains(rest, ",") {
		return 0, 0, rangeIgnore
	}
	parts := strings.SplitN(rest, "-", 2)
	if len(parts) != 2 {
		return 0, 0, rangeIgnore
	}
	first, last := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	if first == "" { // suffix range: the last N bytes
		n, err := strconv.ParseInt(last, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, rangeIgnore
		}
		if n == 0 || size == 0 {
			return 0, 0, rangeUnsatisfiable
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, rangeOK
	}

	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, rangeIgnore
	}
	end := size - 1
	if last != "" {
		e, perr := strconv.ParseInt(last, 10, 64)
		if perr != nil || e < start {
			return 0, 0, rangeIgnore
		}
		if e < end {
			end = e
		}
	}
	if start >= size {
		return 0, 0, rangeUnsatisfiable
	}
	return start, end, rangeOK
}
