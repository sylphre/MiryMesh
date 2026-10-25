// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"github.com/sylphre/MiryMesh/internal/config"
	"github.com/sylphre/MiryMesh/internal/metadata"
)

// adoptionSnapshot loads the newest evacuation snapshot for master takeover.
// A gateway Master keeps no files, and the restore step would create bucket
// directories under data_dir. Its buckets, nodes, placement and keys live in the
// database, so in gateway mode there is nothing to adopt and it returns nil.
func adoptionSnapshot(cfg config.Config, localDir string, remoteDirs []string) (*metadata.EvacuationSnapshot, error) {
	if cfg.Server.Mode == "gateway" {
		return nil, nil
	}
	return metadata.LoadAny(localDir, remoteDirs)
}
