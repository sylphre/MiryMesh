// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathRejected is returned (wrapped) by a path adder when the requested data
// path cannot be used. The handler answers 400 for it.
var ErrPathRejected = errors.New("data path rejected")

const maxDiskPathLen = 4096

// CleanDiskPath validates a data path sent by the Master and returns its
// cleaned form. Only absolute paths other than "/" are accepted.
func CleanDiskPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	switch {
	case p == "":
		return "", fmt.Errorf("%w: path is empty", ErrPathRejected)
	case len(p) > maxDiskPathLen:
		return "", fmt.Errorf("%w: path is too long", ErrPathRejected)
	case strings.ContainsRune(p, 0):
		return "", fmt.Errorf("%w: path contains a NUL byte", ErrPathRejected)
	case !filepath.IsAbs(p):
		return "", fmt.Errorf("%w: path must be absolute", ErrPathRejected)
	}
	p = filepath.Clean(p)
	if p == string(filepath.Separator) {
		return "", fmt.Errorf("%w: the root directory cannot be a data path", ErrPathRejected)
	}
	return p, nil
}

func within(child, parent string) bool {
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, strings.TrimSuffix(parent, string(filepath.Separator))+string(filepath.Separator))
}

// PathsOverlap reports whether a and b are the same directory or one is inside
// the other. Two disks must not overlap, or their objects would mix.
func PathsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return within(a, b) || within(b, a)
}

// MergePaths returns base followed by the paths of extra that are not already
// in base. base entries are kept exactly as given (the Master knows the disks by
// those strings); duplicates are detected on the cleaned form.
func MergePaths(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]bool, len(base)+len(extra))
	for _, p := range base {
		if strings.TrimSpace(p) == "" {
			continue
		}
		c := filepath.Clean(p)
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, p)
	}
	for _, p := range extra {
		if strings.TrimSpace(p) == "" {
			continue
		}
		c := filepath.Clean(p)
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

type extraPathsFile struct {
	Paths []string `json:"paths"`
}

// LoadExtraPaths reads the data paths that were added from the Master. A
// missing file is not an error.
func LoadExtraPaths(file string) ([]string, error) {
	b, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var f extraPathsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", file, err)
	}
	out := make([]string, 0, len(f.Paths))
	seen := make(map[string]bool, len(f.Paths))
	for _, p := range f.Paths {
		c, err := CleanDiskPath(p)
		if err != nil || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

// SaveExtraPaths writes the added data paths through a temporary file and a
// rename, so a crash never leaves a half-written file.
func SaveExtraPaths(file string, paths []string) error {
	file = filepath.Clean(file)
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(extraPathsFile{Paths: paths}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".datanode.paths.json.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(name, 0o640); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, file); err != nil {
		cleanup()
		return err
	}
	return nil
}

// SetPathAdder enables POST /internal/paths on this DataNode. fn adds one data
// path and returns its cleaned form and whether it was new. Without it the
// endpoint answers 404.
func (h *Handler) SetPathAdder(fn func(path string) (string, bool, error)) { h.pathAdder = fn }

// handlePaths lets the Master add a data path (POST). Paths can only be added,
// never removed. It needs the node token; an open handler refuses it.
func (h *Handler) handlePaths(w http.ResponseWriter, r *http.Request) {
	want := h.objectToken()
	got := r.Header.Get("X-Internal-Token")
	if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	add := h.pathAdder
	if add == nil {
		http.Error(w, "adding disks is not available on this node", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	p, added, err := add(req.Path)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrPathRejected) {
			code = http.StatusBadRequest
		}
		http.Error(w, err.Error(), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "path": p, "added": added})
}
