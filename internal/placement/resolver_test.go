// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package placement

import (
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

func TestResolverPolicyFor(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	resolver, err := NewResolver(reg, New(1), "single", map[string]string{"mybucket": "dual"})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if got := resolver.PolicyFor("mybucket"); got != PolicyDual {
		t.Fatalf("PolicyFor mybucket=%q want dual", got)
	}
	if got := resolver.PolicyFor("other"); got != PolicySingle {
		t.Fatalf("PolicyFor other=%q want single", got)
	}
}

func TestResolverInvalidPolicy(t *testing.T) {
	reg := datanode.NewRegistry(0, 0)
	if _, err := NewResolver(reg, New(1), "quorum", nil); err == nil {
		t.Fatal("invalid default policy should fail")
	}
	if _, err := NewResolver(reg, New(1), "single", map[string]string{"b": "BAD"}); err == nil {
		t.Fatal("invalid bucket policy should fail")
	}
}
