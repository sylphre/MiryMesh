// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import "path/filepath"

// AddPath makes the agent report p as a disk from the next ReportDisks on.
// Paths already known (configured or added before) are ignored.
func (a *Agent) AddPath(p string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := filepath.Clean(p)
	for _, e := range a.cfg.Datanode.Paths {
		if filepath.Clean(e) == c {
			return
		}
	}
	for _, e := range a.extraPaths {
		if filepath.Clean(e) == c {
			return
		}
	}
	a.extraPaths = append(a.extraPaths, p)
}

// diskPaths returns the configured paths followed by the ones added at runtime.
func (a *Agent) diskPaths() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.cfg.Datanode.Paths)+len(a.extraPaths))
	out = append(out, a.cfg.Datanode.Paths...)
	out = append(out, a.extraPaths...)
	return out
}
