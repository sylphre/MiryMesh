// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// nodeTokenTransport authenticates requests to a registered DataNode with that
// node's own token (derived from the controller's internal token), instead of the
// shared internal token. Requests to unknown hosts are passed through unchanged.
type nodeTokenTransport struct {
	reg   *datanode.Registry
	token string
	base  http.RoundTripper
}

func (t *nodeTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != "" && t.reg != nil {
		for _, n := range t.reg.ListNodes() {
			if n.Addr == req.URL.Host {
				// RoundTrippers must not mutate the caller's request.
				req = req.Clone(req.Context())
				req.Header.Set("X-Internal-Token", datanode.NodeToken(t.token, n.ID))
				break
			}
		}
	}
	return t.base.RoundTrip(req)
}
