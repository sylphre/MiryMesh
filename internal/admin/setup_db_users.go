// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import "github.com/sylphre/MiryMesh/internal/metadata"

// Driver returns the lower-cased DB driver name of the store.
func (s *SetupStoreDB) Driver() string { return s.driver }

// Keyring returns the master key keyring (current key + retired keys).
func (s *SetupStoreDB) Keyring() (*metadata.Keyring, error) { return s.keyring() }
