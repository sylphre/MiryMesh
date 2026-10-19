//go:build !e2e_external

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package minio_test

import "testing"
func TestMinio_Placeholder(t *testing.T) { t.Log("mc/rclone e2e — run with -tags=e2e_external") }
