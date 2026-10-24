// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sylphre/MiryMesh/internal/s3api"
)

// settingRegion is the app_settings key for the region the S3 API reports.
const settingRegion = "s3.region"

// RegionController reads and changes the region the S3 API reports
// (X-Amz-Bucket-Region and GetBucketLocation). *s3api.Router implements it.
type RegionController interface {
	Region() string
	SetRegion(region string) error
}

// SetRegionController wires the live S3 router so the console can change its region.
func (h *Handler) SetRegionController(rc RegionController) { h.region = rc }

// LoadRegionSetting applies the region saved from the console, if any. Without a
// saved value the region from the configuration file stays in effect. Call it once
// at startup, after SetRegionController.
func (h *Handler) LoadRegionSetting() {
	if h.region == nil {
		return
	}
	ss, ok := h.setupStore.(settingsStore)
	if !ok {
		return
	}
	v, found := ss.GetSetting(settingRegion)
	if !found || v == "" {
		return
	}
	if err := h.region.SetRegion(v); err != nil {
		slog.Warn("region: ignoring unusable saved region", "value", v, "err", err)
		return
	}
	slog.Info("region: restored saved region", "region", v)
}

type regionDoc struct {
	Region    string `json:"region"`
	Default   string `json:"default"`
	Persisted bool   `json:"persisted"`
}

func (h *Handler) regionDoc() regionDoc {
	_, persisted := h.setupStore.(settingsStore)
	return regionDoc{Region: h.region.Region(), Default: s3api.DefaultRegion, Persisted: persisted}
}

// HandleRegion serves /api/admin/region.
//
//	GET -> {"region": "...", "default": "us-east-1", "persisted": true}
//	PUT {"region": "ap-northeast-1"} -> same document after the change
//
// The region is what the S3 API reports in X-Amz-Bucket-Region and GetBucketLocation.
// SigV4 verification accepts the region the client signed with, so changing it does
// not break existing clients.
func (h *Handler) HandleRegion(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if h.region == nil {
		http.Error(w, "リージョンは変更できません", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.regionDoc())
	case http.MethodPut:
		var req struct {
			Region string `json:"region"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		region := strings.TrimSpace(req.Region)
		if !s3api.ValidRegion(region) {
			http.Error(w, "リージョンは英小文字・数字・ハイフンのみ、64 文字以内で指定してください (例: us-east-1, ap-northeast-1)", http.StatusBadRequest)
			return
		}
		prev := h.region.Region()
		if err := h.region.SetRegion(region); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if ss, ok := h.setupStore.(settingsStore); ok {
			if err := ss.SetSetting(settingRegion, region); err != nil {
				// Keep memory and database in agreement: undo the change.
				_ = h.region.SetRegion(prev)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		slog.Info("region: changed from console", "from", prev, "to", region)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(h.regionDoc())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
