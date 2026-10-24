// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sync"

	"github.com/sylphre/MiryMesh/internal/placement"
)

// settingPlacementState is the app_settings key that holds the per-bucket placement
// rules and disk reservations as JSON (placement.State).
const settingPlacementState = "placement.state"

// placementMu serializes changes to rules and reservations, so that the
// "apply, check, save, roll back on failure" sequence is not interleaved.
var placementMu sync.Mutex

type reservedByOther struct {
	NodeID string `json:"node_id"`
	Path   string `json:"path"`
	Bucket string `json:"bucket"`
}

// placementDoc is the body of GET/PUT /api/admin/buckets/{bucket}/placement.
//
//   - Mode: spread (every replica on another node, the default), same_node
//     (every replica on one node, on different disks) or any (different disks,
//     different nodes preferred).
//   - Disks: allow-list. When it is not empty the bucket only writes to these disks.
//   - ReservedDisks: disks dedicated to this bucket; no other bucket writes there.
type placementDoc struct {
	Bucket           string              `json:"bucket"`
	Mode             placement.Mode      `json:"mode"`
	Disks            []placement.DiskRef `json:"disks"`
	ReservedDisks    []placement.DiskRef `json:"reserved_disks"`
	ReservedByOthers []reservedByOther   `json:"reserved_by_others"`
}

// LoadPlacementState restores the saved rules and reservations into the resolver.
// Call it once at startup, after the setup store is wired and before serving.
func (h *Handler) LoadPlacementState() {
	if h.resolver == nil {
		return
	}
	ss, ok := h.setupStore.(settingsStore)
	if !ok {
		return
	}
	v, found := ss.GetSetting(settingPlacementState)
	if !found || v == "" {
		return
	}
	var st placement.State
	if err := json.Unmarshal([]byte(v), &st); err != nil {
		slog.Warn("placement: ignoring unreadable saved state", "err", err)
		return
	}
	if skipped := h.resolver.ImportState(st); skipped > 0 {
		slog.Warn("placement: skipped invalid saved entries", "count", skipped)
	}
	slog.Info("placement: restored saved rules", "rules", len(st.Rules), "reservations", len(st.Reservations))
}

// savePlacement writes the resolver state to app_settings. Without a database-backed
// store the rules live in memory only.
func (h *Handler) savePlacement() error {
	ss, ok := h.setupStore.(settingsStore)
	if !ok {
		return nil
	}
	b, err := json.Marshal(h.resolver.ExportState())
	if err != nil {
		return err
	}
	return ss.SetSetting(settingPlacementState, string(b))
}

// forgetPlacement drops the rule and the reservations of a deleted bucket.
func (h *Handler) forgetPlacement(bucket string) {
	if h.resolver == nil {
		return
	}
	placementMu.Lock()
	defer placementMu.Unlock()
	h.resolver.ForgetBucket(bucket)
	if err := h.savePlacement(); err != nil {
		slog.Warn("admin: save placement after bucket delete", "bucket", bucket, "err", err)
	}
}

func (h *Handler) handleBucketPlacement(w http.ResponseWriter, r *http.Request, bucket string) {
	if h.resolver == nil {
		http.Error(w, "placement is not available", http.StatusNotImplemented)
		return
	}
	if !h.storage.BucketExists(bucket) {
		http.Error(w, "no such bucket", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.writePlacement(w, bucket)
	case http.MethodPut:
		h.putPlacement(w, r, bucket)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) reservedOf(bucket string) []placement.DiskRef {
	out := []placement.DiskRef{}
	for _, rv := range h.resolver.Reservations() {
		if rv.Bucket == bucket {
			out = append(out, rv.Disk())
		}
	}
	return out
}

func (h *Handler) writePlacement(w http.ResponseWriter, bucket string) {
	rule := h.resolver.RuleFor(bucket)
	mode := rule.Mode
	if mode == "" {
		mode = placement.ModeSpread
	}
	disks := rule.Disks
	if disks == nil {
		disks = []placement.DiskRef{}
	}
	doc := placementDoc{
		Bucket:           bucket,
		Mode:             mode,
		Disks:            disks,
		ReservedDisks:    h.reservedOf(bucket),
		ReservedByOthers: []reservedByOther{},
	}
	for _, rv := range h.resolver.Reservations() {
		if rv.Bucket != bucket {
			doc.ReservedByOthers = append(doc.ReservedByOthers, reservedByOther{NodeID: rv.NodeID, Path: rv.Path, Bucket: rv.Bucket})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (h *Handler) putPlacement(w http.ResponseWriter, r *http.Request, bucket string) {
	var req struct {
		Mode          string              `json:"mode"`
		Disks         []placement.DiskRef `json:"disks"`
		ReservedDisks []placement.DiskRef `json:"reserved_disks"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	rule, err := placement.Rule{Mode: placement.Mode(req.Mode), Disks: req.Disks}.Normalize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Reuse Normalize to clean and de-duplicate the reserved list.
	resRule, err := placement.Rule{Disks: req.ReservedDisks}.Normalize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reserved := resRule.Disks

	known := make(map[placement.DiskRef]bool)
	for _, d := range h.registry.ListDisks() {
		known[placement.DiskRef{NodeID: d.NodeID, Path: path.Clean(d.Path)}] = true
	}
	for _, d := range rule.Disks {
		if !known[d] {
			http.Error(w, fmt.Sprintf("disk %s:%s is not registered", d.NodeID, d.Path), http.StatusBadRequest)
			return
		}
	}
	allowed := make(map[placement.DiskRef]bool, len(rule.Disks))
	for _, d := range rule.Disks {
		allowed[d] = true
	}
	for _, d := range reserved {
		if !known[d] {
			http.Error(w, fmt.Sprintf("disk %s:%s is not registered", d.NodeID, d.Path), http.StatusBadRequest)
			return
		}
		if len(rule.Disks) > 0 && !allowed[d] {
			http.Error(w, fmt.Sprintf("reserved disk %s:%s must also be in disks", d.NodeID, d.Path), http.StatusBadRequest)
			return
		}
	}

	placementMu.Lock()
	defer placementMu.Unlock()
	prevRule := h.resolver.RuleFor(bucket)
	prevReserved := h.reservedOf(bucket)

	if err := h.applyPlacement(bucket, rule, reserved); err != nil {
		h.restorePlacement(bucket, prevRule, prevReserved)
		status := http.StatusBadRequest
		if errors.Is(err, placement.ErrConflict) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	if err := h.savePlacement(); err != nil {
		h.restorePlacement(bucket, prevRule, prevReserved)
		slog.Error("admin: save placement failed", "bucket", bucket, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writePlacement(w, bucket)
}

// applyPlacement installs the reservations and the rule of one bucket, then checks
// that a write of the bucket's policy can still be placed. The caller holds placementMu
// and rolls back with restorePlacement when this fails.
func (h *Handler) applyPlacement(bucket string, rule placement.Rule, reserved []placement.DiskRef) error {
	want := make(map[placement.DiskRef]bool, len(reserved))
	for _, d := range reserved {
		want[d] = true
	}
	for _, d := range h.reservedOf(bucket) {
		if !want[d] {
			h.resolver.Release(d)
		}
	}
	for _, d := range reserved {
		if err := h.resolver.Reserve(bucket, d); err != nil {
			return err
		}
	}
	if _, err := h.resolver.SetBucketRule(bucket, rule); err != nil {
		return err
	}
	if !rule.IsDefault() || len(reserved) > 0 {
		if _, err := h.resolver.Resolve(bucket, 0); err != nil {
			return fmt.Errorf("this placement cannot be satisfied by the nodes and disks that are online now: %w", err)
		}
	}
	return nil
}

// restorePlacement puts back the rule and reservations a bucket had before a failed change.
func (h *Handler) restorePlacement(bucket string, rule placement.Rule, reserved []placement.DiskRef) {
	for _, d := range h.reservedOf(bucket) {
		h.resolver.Release(d)
	}
	for _, d := range reserved {
		if err := h.resolver.Reserve(bucket, d); err != nil {
			slog.Error("admin: restore reservation failed", "bucket", bucket, "err", err)
		}
	}
	if _, err := h.resolver.SetBucketRule(bucket, rule); err != nil {
		slog.Error("admin: restore placement rule failed", "bucket", bucket, "err", err)
	}
}
