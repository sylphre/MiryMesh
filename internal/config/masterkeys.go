// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package config

import (
	"os"
	"strings"
)

// MasterKeys returns the current master key and the retired keys that may still be
// used to decrypt stored secrets (see metadata.Keyring). Environment variables fill
// in what the file leaves out: MIRYMESH_MASTER_KEY for the current key and
// MIRYMESH_MASTER_KEY_OLD for retired keys (comma-separated, so keys given there
// must not contain a comma).
func (a AuthConfig) MasterKeys() (current string, old []string) {
	current = strings.TrimSpace(a.MasterKey)
	if current == "" {
		current = strings.TrimSpace(os.Getenv("MIRYMESH_MASTER_KEY"))
	}
	for _, k := range a.OldMasterKeys {
		if k = strings.TrimSpace(k); k != "" {
			old = append(old, k)
		}
	}
	for _, k := range strings.Split(os.Getenv("MIRYMESH_MASTER_KEY_OLD"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			old = append(old, k)
		}
	}
	return current, old
}
