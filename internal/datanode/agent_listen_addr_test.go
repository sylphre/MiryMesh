// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"testing"

	"github.com/sylphre/MiryMesh/internal/config"
)

func TestAgentListenAddr(t *testing.T) {
	tests := []struct {
		name   string
		listen string
		want   string
	}{
		{"default when empty", "", "n1:9002"},
		{"port from listen", "0.0.0.0:9102", "n1:9102"},
		{"tailscale style", "100.64.0.1:9002", "n1:9002"},
		{"invalid falls back to default", "not-an-addr", "n1:9002"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg config.Config
			cfg.Datanode.Listen = tt.listen
			a := &Agent{cfg: cfg, nodeID: "n1"}
			if got := a.listenAddr(); got != tt.want {
				t.Fatalf("listenAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
