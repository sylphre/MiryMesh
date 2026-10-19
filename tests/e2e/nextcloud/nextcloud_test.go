//go:build !e2e_external

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package nextcloud_test

import "testing"

func TestNextcloud_Placeholder(t *testing.T) {
	t.Log("nextcloud e2e placeholder — run with -tags=e2e_external against docker compose fixture")
}
