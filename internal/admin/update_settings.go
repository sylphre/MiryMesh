// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/sylphre/MiryMesh/internal/updater"
)

// settingNotifyChannel is the app_settings key for the update notice channel (auto, release, prerelease).
const settingNotifyChannel = "update.notify_channel"

// settingsStore is implemented by the DB setup store (*SetupStoreDB).
type settingsStore interface {
	GetSetting(key string) (string, bool)
	SetSetting(key, value string) error
}

// LoadUpdateSettings applies the notification channel saved in the database to the
// update checker. Call it after SetUpdateChecker and before the checker starts.
func (h *Handler) LoadUpdateSettings() {
	if h.updates == nil {
		return
	}
	ss, ok := h.setupStore.(settingsStore)
	if !ok {
		return
	}
	v, found := ss.GetSetting(settingNotifyChannel)
	if !found {
		return
	}
	ch, valid := updater.ParseChannel(v)
	if !valid {
		slog.Warn("update: ignoring unknown saved notify channel", "value", v)
		return
	}
	h.updates.SetChannel(ch)
}

// HandleUpdateSettings (PUT /api/admin/update/settings) chooses which releases the
// update notice offers. Body: {"notify_channel": "auto" | "release" | "prerelease"}.
// It answers with the refreshed update status.
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.updates == nil {
		http.Error(w, "更新の確認は無効です", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		NotifyChannel string `json:"notify_channel"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	ch, ok := updater.ParseChannel(req.NotifyChannel)
	if !ok {
		http.Error(w, "notify_channel は auto / release / prerelease のいずれかです", http.StatusBadRequest)
		return
	}
	if ss, ok := h.setupStore.(settingsStore); ok {
		if err := ss.SetSetting(settingNotifyChannel, updater.ChannelName(ch)); err != nil {
			slog.Error("update: save notify channel failed", "err", err)
			http.Error(w, "設定を保存できませんでした", http.StatusInternalServerError)
			return
		}
	}
	h.updates.SetChannel(ch)
	st := h.updates.Check(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}
