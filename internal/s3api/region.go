// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"fmt"
	"regexp"
)

// DefaultRegion is used when no region is configured.
const DefaultRegion = "us-east-1"

// maxRegionLen matches the width of the setup.region column (VARCHAR(64)).
const maxRegionLen = 64

var regionPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidRegion reports whether s is usable as a region name (lowercase letters,
// digits and hyphens, at most 64 characters), the same rule as server.region.
func ValidRegion(s string) bool {
	return s != "" && len(s) <= maxRegionLen && regionPattern.MatchString(s)
}

// Region returns the region reported in X-Amz-Bucket-Region and GetBucketLocation.
func (rt *Router) Region() string {
	rt.regionMu.RLock()
	defer rt.regionMu.RUnlock()
	return rt.region
}

// SetRegion changes the reported region at runtime. An empty value restores
// DefaultRegion; an invalid name is rejected and leaves the region unchanged.
// SigV4 verification does not depend on it: the server accepts the region the
// client signed with.
func (rt *Router) SetRegion(region string) error {
	if region == "" {
		region = DefaultRegion
	}
	if !ValidRegion(region) {
		return fmt.Errorf("region %q must match %s and be at most %d characters", region, regionPattern.String(), maxRegionLen)
	}
	rt.regionMu.Lock()
	rt.region = region
	rt.regionMu.Unlock()
	return nil
}
