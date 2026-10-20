// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics holds S3 traffic counters. Updated via RecordGet/RecordPut.
type Metrics struct {
	gets     atomic.Int64
	puts     atomic.Int64
	getBytes atomic.Int64
	putBytes atomic.Int64
	start    time.Time
	// For GiB/s: snapshot window
	mu           sync.Mutex
	prevGetBytes int64
	prevPutBytes int64
	prevAt       time.Time
}

// NewMetrics creates a Metrics.
func NewMetrics() *Metrics {
	now := time.Now()
	return &Metrics{start: now, prevAt: now}
}

// RecordGet records a GET (bytes may be 0 if unknown).
func (m *Metrics) RecordGet(bytes int64) {
	m.gets.Add(1)
	if bytes > 0 {
		m.getBytes.Add(bytes)
	}
}

// RecordPut records a PUT.
func (m *Metrics) RecordPut(bytes int64) {
	m.puts.Add(1)
	if bytes > 0 {
		m.putBytes.Add(bytes)
	}
}

// Snapshot returns rates and totals. GiB/s is bytes delta / elapsed since last snapshot.
func (m *Metrics) Snapshot() (gets, puts int64, getGiBs, putGiBs float64, uptime time.Duration) {
	now := time.Now()
	gets = m.gets.Load()
	puts = m.puts.Load()
	getBytes := m.getBytes.Load()
	putBytes := m.putBytes.Load()
	uptime = now.Sub(m.start)
	m.mu.Lock()
	defer m.mu.Unlock()
	elapsed := now.Sub(m.prevAt).Seconds()
	if elapsed > 0.1 {
		getGiBs = float64(getBytes-m.prevGetBytes) / elapsed / 1024 / 1024 / 1024
		putGiBs = float64(putBytes-m.prevPutBytes) / elapsed / 1024 / 1024 / 1024
		m.prevGetBytes = getBytes
		m.prevPutBytes = putBytes
		m.prevAt = now
	}
	return gets, puts, getGiBs, putGiBs, uptime
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	gets, puts, getGiBs, putGiBs, uptime := h.metrics.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"gets":      gets,
		"puts":      puts,
		"get_gib_s": getGiBs,
		"put_gib_s": putGiBs,
		"uptime":    uptime.String(),
	})
}
