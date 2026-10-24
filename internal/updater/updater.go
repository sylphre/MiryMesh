// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package updater checks GitHub Releases for a newer MiryMesh version so the
// admin console can tell the operator about it. It only reads release metadata;
// it never downloads or installs anything.
package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sylphre/MiryMesh/internal/version"
)

const (
	// DefaultAPIURL lists releases (pre-releases included, newest first).
	DefaultAPIURL = "https://api.github.com/repos/sylphre/MiryMesh/releases?per_page=30"
	// DefaultInterval is how often the background check runs.
	DefaultInterval = 6 * time.Hour

	startDelay = 10 * time.Second
	minRecheck = 30 * time.Second
	maxNotes   = 4000
)

// Status is what the admin API returns to the console.
type Status struct {
	Enabled         bool      `json:"enabled"`
	Current         string    `json:"current"`
	Latest          string    `json:"latest,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	Prerelease      bool      `json:"prerelease,omitempty"`
	ReleaseURL      string    `json:"release_url,omitempty"`
	Notes           string    `json:"notes,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitzero"`
	CheckedAt       time.Time `json:"checked_at,omitzero"`
	Error           string    `json:"error,omitempty"`
}

type release struct {
	Tag         string    `json:"tag_name"`
	URL         string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// Checker polls GitHub Releases and caches the result.
type Checker struct {
	current    string
	enabled    bool
	prerelease bool
	apiURL     string
	interval   time.Duration
	client     *http.Client

	fetchMu sync.Mutex // serialises lookups
	mu      sync.RWMutex
	status  Status
	last    time.Time // last attempt, successful or not
}

// New creates a Checker. When enabled is false it never contacts GitHub.
// Pre-releases are offered when prerelease is true, or when current is itself
// a pre-release or not a release version (a "dev" build).
func New(current string, enabled, prerelease bool) *Checker {
	return &Checker{
		current:    current,
		enabled:    enabled,
		prerelease: prerelease,
		apiURL:     DefaultAPIURL,
		interval:   DefaultInterval,
		client:     &http.Client{Timeout: 15 * time.Second},
		status:     Status{Enabled: enabled, Current: current},
	}
}

// Status returns the last known result.
func (c *Checker) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// Start runs the background check: once shortly after boot, then every interval.
func (c *Checker) Start() {
	if !c.enabled {
		return
	}
	go func() {
		time.Sleep(startDelay)
		for {
			c.Check(context.Background())
			time.Sleep(c.interval)
		}
	}()
}

// Check looks up the newest release. Calls within 30 seconds of the previous
// attempt return the cached result, so a button in the console cannot hammer the API.
func (c *Checker) Check(ctx context.Context) Status {
	if !c.enabled {
		return c.Status()
	}
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	c.mu.RLock()
	recent := !c.last.IsZero() && time.Since(c.last) < minRecheck
	c.mu.RUnlock()
	if recent {
		return c.Status()
	}

	rel, err := c.latest(ctx)
	now := time.Now().UTC()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = now
	st := c.status
	st.CheckedAt = now
	if err != nil {
		st.Error = err.Error()
		c.status = st
		return st
	}
	st.Error = ""
	st.Latest, st.ReleaseURL, st.Notes, st.PublishedAt = "", "", "", time.Time{}
	st.Prerelease, st.UpdateAvailable = false, false
	if rel != nil {
		st.Latest = rel.Tag
		st.ReleaseURL = rel.URL
		st.Notes = clip(rel.Body, maxNotes)
		st.PublishedAt = rel.PublishedAt
		st.Prerelease = rel.Prerelease || version.IsPrerelease(rel.Tag)
		if cmp, ok := version.Compare(c.current, rel.Tag); ok && cmp < 0 {
			st.UpdateAvailable = true
		}
	}
	c.status = st
	return st
}

func (c *Checker) latest(ctx context.Context) (*release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mirymesh-controller/"+c.current)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release lookup failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release lookup failed: %s", resp.Status)
	}
	var list []release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("release lookup returned invalid JSON: %w", err)
	}
	includePre := c.prerelease || version.IsPrerelease(c.current) || !version.Valid(c.current)
	var best *release
	for i := range list {
		r := &list[i]
		if r.Draft || !version.ValidTag(r.Tag) {
			continue
		}
		if (r.Prerelease || version.IsPrerelease(r.Tag)) && !includePre {
			continue
		}
		if best == nil {
			best = r
			continue
		}
		if cmp, ok := version.Compare(r.Tag, best.Tag); ok && cmp > 0 {
			best = r
		}
	}
	return best, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
