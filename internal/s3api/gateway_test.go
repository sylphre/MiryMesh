// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
)

func TestGateway_PlaceholderRequiresDataNode(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	engine := placement.New(1)
	if _, err := engine.Select(reg, placement.PolicySingle, 10); err == nil {
		t.Fatalf("expected insufficient storage with no nodes")
	}
	_ = NewRouter(nil, "us-east-1")
}

func TestGateway_FailoverAndAllDown(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	_ = reg.RegisterNode("n1", "n1:9001")
	_ = reg.UpsertDisk("n1", "/mnt/a", 1000, 100, 0)
	engine := placement.New(1)
	if _, err := engine.Select(reg, placement.PolicySingle, 10); err != nil {
		t.Fatalf("single: %v", err)
	}
	_ = reg.RemoveNode("n1")
	if _, err := engine.Select(reg, placement.PolicySingle, 10); err == nil {
		t.Fatalf("expected 507 with no nodes")
	}
}
