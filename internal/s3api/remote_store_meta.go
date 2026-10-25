// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"strings"
)

// objectMetaFromResponse builds the ObjectMeta of an object from a DataNode
// GET/HEAD response. handleGetObject and handleHeadObject use Size for
// Content-Length and Range, and ETag / LastMod for conditional requests, so
// all of them must be filled in.
func objectMetaFromResponse(key string, resp *http.Response) ObjectMeta {
	m := ObjectMeta{
		Key:         key,
		ContentType: resp.Header.Get("Content-Type"),
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
	}
	if resp.ContentLength >= 0 {
		m.Size = resp.ContentLength
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		m.LastMod = t.UTC()
	}
	const metaPrefix = "x-amz-meta-"
	for k, vs := range resp.Header {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, metaPrefix) || len(vs) == 0 {
			continue
		}
		if m.Meta == nil {
			m.Meta = make(map[string]string)
		}
		m.Meta[lk[len(metaPrefix):]] = strings.Join(vs, ",")
	}
	return m
}
