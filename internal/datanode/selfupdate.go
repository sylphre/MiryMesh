// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sylphre/MiryMesh/internal/version"
)

// DefaultReleaseBase is where self-update downloads release assets from.
// MIRYMESH_RELEASE_BASE overrides it (same variable install.sh uses).
const DefaultReleaseBase = "https://github.com/sylphre/MiryMesh/releases"

// ExitCodeUpdated is the exit status after a new binary has been staged.
// It is non-zero so that Restart=on-failure brings the service back up.
const ExitCodeUpdated = 75

const (
	stagedBinary  = "mirymesh-datanode"
	maxBinarySize = 256 << 20

	stateIdle        = "idle"
	stateDownloading = "downloading"
	stateStaged      = "staged"
	stateError       = "error"
)

var (
	ErrUpdateDisabled  = errors.New("self-update is not enabled on this node (run install.sh --update on it once)")
	ErrUpdateBusy      = errors.New("an update is already in progress")
	ErrSameVersion     = errors.New("this node already runs that version")
	ErrBadVersion      = errors.New("version must be a release tag such as v1.0.0 or v1.0.0-rc.1")
	ErrUnsupportedArch = errors.New("no prebuilt DataNode for this OS/CPU")
)

// Updater downloads a release DataNode binary, verifies it against the release's
// SHA256SUMS and stages it in StageDir. The service itself cannot write to its
// own binary (ProtectSystem=strict), so a privileged ExecStartPre step set up by
// install.sh moves the staged file into place when the service restarts.
type Updater struct {
	Current     string        // running version
	StageDir    string        // where the verified binary is staged
	ReleaseBase string        // GitHub releases URL, no trailing slash
	Enabled     bool          // true when the unit has the ExecStartPre step (MIRYMESH_SELF_UPDATE=1)
	Client      *http.Client  // download client
	Exit        func(int)     // called after staging; defaults to os.Exit
	ExitDelay   time.Duration // grace period so the HTTP response can be delivered

	mu      sync.Mutex
	state   string
	lastErr string
}

// NewUpdater builds an Updater for the running binary.
func NewUpdater(stageDir string) *Updater {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MIRYMESH_RELEASE_BASE")), "/")
	if base == "" {
		base = DefaultReleaseBase
	}
	u := &Updater{
		Current:     version.Version,
		StageDir:    stageDir,
		ReleaseBase: base,
		Enabled:     os.Getenv("MIRYMESH_SELF_UPDATE") == "1",
		Exit:        os.Exit,
		ExitDelay:   time.Second,
		state:       stateIdle,
	}
	u.Client = &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if strings.HasPrefix(base, "https://") && req.URL.Scheme != "https" {
				return errors.New("refusing redirect to a non-https URL")
			}
			return nil
		},
	}
	return u
}

// CheckStaged logs a warning when a staged binary was left behind, which means the
// unit has no privileged step to install it, and removes partial downloads.
func (u *Updater) CheckStaged() {
	_ = os.Remove(filepath.Join(u.StageDir, stagedBinary+".part"))
	if _, err := os.Stat(filepath.Join(u.StageDir, stagedBinary)); err == nil {
		slog.Warn("datanode: a staged update was not applied; run install.sh --update on this node to enable self-update", "dir", u.StageDir)
	}
}

// Info describes the updater for GET /internal/update.
func (u *Updater) Info() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return map[string]any{"version": u.Current, "self_update": u.Enabled, "state": u.state, "error": u.lastErr}
}

func assetName() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return "mirymesh-datanode-linux-" + runtime.GOARCH
	}
	return ""
}

// Start validates the request and runs the update in the background. When the
// binary is staged the process exits with ExitCodeUpdated.
func (u *Updater) Start(tag string) error {
	if !u.Enabled {
		return ErrUpdateDisabled
	}
	if !version.ValidTag(tag) {
		return ErrBadVersion
	}
	if c, ok := version.Compare(u.Current, tag); ok && c == 0 {
		return ErrSameVersion
	}
	if assetName() == "" {
		return ErrUnsupportedArch
	}
	u.mu.Lock()
	if u.state == stateDownloading || u.state == stateStaged {
		u.mu.Unlock()
		return ErrUpdateBusy
	}
	u.state, u.lastErr = stateDownloading, ""
	u.mu.Unlock()
	go u.run(tag)
	return nil
}

func (u *Updater) run(tag string) {
	slog.Info("datanode: self-update started", "from", u.Current, "to", tag)
	if err := u.Apply(tag); err != nil {
		u.mu.Lock()
		u.state, u.lastErr = stateError, err.Error()
		u.mu.Unlock()
		slog.Error("datanode: self-update failed", "err", err)
		return
	}
	u.mu.Lock()
	u.state = stateStaged
	u.mu.Unlock()
	slog.Info("datanode: update staged, restarting", "to", tag)
	time.Sleep(u.ExitDelay)
	exit := u.Exit
	if exit == nil {
		exit = os.Exit
	}
	exit(ExitCodeUpdated)
}

// Apply downloads the release asset for this platform, verifies its sha256 and
// stages it as StageDir/mirymesh-datanode. It does not restart anything.
func (u *Updater) Apply(tag string) error {
	name := assetName()
	if name == "" {
		return ErrUnsupportedArch
	}
	base := strings.TrimRight(u.ReleaseBase, "/") + "/download/" + tag
	sums, err := u.fetch(base+"/SHA256SUMS", 1<<20)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want := checksumFor(string(sums), name)
	if want == "" {
		return fmt.Errorf("SHA256SUMS has no entry for %s", name)
	}
	if mkErr := os.MkdirAll(u.StageDir, 0o750); mkErr != nil {
		return fmt.Errorf("create %s: %w", u.StageDir, mkErr)
	}
	part := filepath.Join(u.StageDir, stagedBinary+".part")
	got, err := u.download(base+"/"+name, part)
	if err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("download %s: %w", name, err)
	}
	if got != want {
		_ = os.Remove(part)
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", name, want, got)
	}
	if err := os.Chmod(part, 0o755); err != nil { //nolint:gosec // the binary must be executable
		_ = os.Remove(part)
		return err
	}
	if err := os.Rename(part, filepath.Join(u.StageDir, stagedBinary)); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

func (u *Updater) get(url string) (*http.Response, error) {
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(url) //nolint:gosec // URL is built from the configured release base and a validated tag
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}

func (u *Updater) fetch(url string, limit int64) ([]byte, error) {
	resp, err := u.get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// download streams url into path and returns the sha256 (hex) of what was written.
func (u *Updater) download(url, path string) (string, error) {
	resp, err := u.get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBinarySize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n > maxBinarySize {
		return "", errors.New("download is larger than the allowed maximum")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checksumFor returns the hex digest listed for name in a sha256sum-style file.
func checksumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

// SetUpdater enables /internal/update on this DataNode.
func (h *Handler) SetUpdater(u *Updater) { h.updater = u }

// handleUpdate lets the controller trigger (POST) or inspect (GET) a self-update.
// It needs the node token; an open handler (no token configured) refuses it.
func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	want := h.objectToken()
	got := r.Header.Get("X-Internal-Token")
	if want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	u := h.updater
	if u == nil {
		http.Error(w, "self-update is not available on this node", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.Info())
	case http.MethodPost:
		var req struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := u.Start(strings.TrimSpace(req.Version)); err != nil {
			code := http.StatusConflict
			if errors.Is(err, ErrBadVersion) || errors.Is(err, ErrUnsupportedArch) {
				code = http.StatusBadRequest
			}
			http.Error(w, err.Error(), code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "updating", "from": u.Current, "to": strings.TrimSpace(req.Version)})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
