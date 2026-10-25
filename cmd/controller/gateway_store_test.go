// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

func newTestResolver(t *testing.T) (*datanode.Registry, *placement.Resolver) {
	t.Helper()
	reg := datanode.NewRegistry(30*time.Second, 90*time.Second)
	res, err := placement.NewResolver(reg, placement.New(0), "", nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return reg, res
}

// In gateway mode the bucket-only placeholder must be replaced by a RemoteStore,
// otherwise every PUT fails with "gateway: put requires DataNode".
func TestGatewayStoreReplacesPlaceholder(t *testing.T) {
	reg, res := newTestResolver(t)
	got := gatewayStore(&gatewayPlaceholder{dataDir: t.TempDir()}, reg, res, "tok")
	if _, ok := got.(*s3api.RemoteStore); !ok {
		t.Fatalf("gatewayStore returned %T, want *s3api.RemoteStore", got)
	}
}

// Standalone mode keeps its local Storage untouched.
func TestGatewayStoreKeepsStandaloneStorage(t *testing.T) {
	reg, res := newTestResolver(t)
	ls, err := s3api.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	got := gatewayStore(ls, reg, res, "tok")
	if got != s3api.ObjectStore(ls) {
		t.Fatalf("gatewayStore changed the standalone store: %T", got)
	}
}
