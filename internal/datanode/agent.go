// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sylphre/MiryMesh/internal/config"
)

// errUnknownNode is returned by Heartbeat when the controller does not know this node
// (e.g. the controller restarted), which means the node has to register again.
var errUnknownNode = errors.New("controller does not know this node")

// Agent runs on a datanode: registers itself, reports disks and heartbeats.
type Agent struct {
	cfg    config.Config
	client *http.Client
	nodeID string

	mu            sync.RWMutex
	token         string // credential sent to the controller: pair token, shared token or node token
	ctrlURL       string // overrides cfg.Datanode.ControllerURL after Adopt
	paired        bool   // true once a registration succeeded
	haveNodeToken bool   // token currently holds a controller-issued node token
	tokenFile     string
	onNodeToken   func(string)
}

// NewAgent creates an agent from config. NodeID defaults to hostname.
func NewAgent(cfg config.Config) *Agent {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "datanode-1"
	}
	token := ""
	if len(cfg.Auth.Credentials) > 0 {
		token = cfg.Auth.Credentials[0].SecretKey
	}
	return &Agent{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
		nodeID: hostname,
		token:  token,
	}
}

// Bind persists the node token to tokenFile and reports every new node token to
// onNodeToken (typically Handler.SetNodeToken). A token stored by a previous run is
// loaded immediately, so a restart does not need a fresh one-time pair token.
func (a *Agent) Bind(tokenFile string, onNodeToken func(string)) {
	a.mu.Lock()
	a.tokenFile = tokenFile
	a.onNodeToken = onNodeToken
	a.mu.Unlock()
	if tokenFile == "" {
		return
	}
	b, err := os.ReadFile(filepath.Clean(tokenFile))
	if err != nil {
		return
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return
	}
	a.mu.Lock()
	a.token = tok
	a.haveNodeToken = true
	a.mu.Unlock()
	if onNodeToken != nil {
		onNodeToken(tok)
	}
}

func (a *Agent) cfgToken() string {
	if len(a.cfg.Auth.Credentials) > 0 {
		return a.cfg.Auth.Credentials[0].SecretKey
	}
	return ""
}

func (a *Agent) currentToken() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

func (a *Agent) controllerURL() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.ctrlURL != "" {
		return a.ctrlURL
	}
	return a.cfg.Datanode.ControllerURL
}

func (a *Agent) isPaired() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paired
}

// acceptNodeToken records a successful registration and switches to the node token.
func (a *Agent) acceptNodeToken(tok string) {
	a.mu.Lock()
	a.paired = true
	if tok != "" {
		a.token = tok
		a.haveNodeToken = true
	}
	file, hook := a.tokenFile, a.onNodeToken
	a.mu.Unlock()
	if tok == "" {
		return
	}
	if hook != nil {
		hook(tok)
	}
	if file != "" {
		err := os.MkdirAll(filepath.Dir(file), 0o700)
		if err == nil {
			err = os.WriteFile(file, []byte(tok+"\n"), 0o600)
		}
		if err != nil {
			slog.Warn("datanode: could not persist node token", "path", file, "err", err)
		}
	}
}

// dropStoredToken forgets a node token the controller rejected and falls back to the
// configured credential. It reports whether there was a node token to drop.
func (a *Agent) dropStoredToken() bool {
	a.mu.Lock()
	if !a.haveNodeToken {
		a.mu.Unlock()
		return false
	}
	a.haveNodeToken = false
	a.paired = false
	a.token = a.cfgToken()
	file, hook := a.tokenFile, a.onNodeToken
	a.mu.Unlock()
	if file != "" {
		_ = os.Remove(file)
	}
	if hook != nil {
		hook("")
	}
	return true
}

// Register performs a single registration RPC.
func (a *Agent) Register() error {
	base := a.controllerURL()
	if base == "" {
		return fmt.Errorf("controller_url is not configured")
	}
	addr := a.listenAddr()
	body, _ := json.Marshal(map[string]string{
		"node_id": a.nodeID,
		"addr":    addr,
		"token":   a.currentToken(),
	})
	resp, err := a.client.Post(base+"/internal/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden && a.dropStoredToken() {
		return fmt.Errorf("register failed: %s (stored node token rejected; falling back to configured token)", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register failed: %s", resp.Status)
	}
	var out struct {
		NodeToken string `json:"node_token"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
	a.acceptNodeToken(out.NodeToken)
	slog.Info("datanode: registered", "node_id", a.nodeID, "addr", addr)
	return nil
}

// Adopt handles a controller-initiated pairing request: it points the agent at
// controllerURL, uses the one-time pairToken, and registers in the background.
// It only works while the node is not yet paired.
func (a *Agent) Adopt(controllerURL, pairToken string) error {
	u, err := neturl.Parse(strings.TrimSpace(controllerURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("controller_url must be http(s)://host:port")
	}
	pairToken = strings.TrimSpace(pairToken)
	if pairToken == "" {
		return fmt.Errorf("pair_token is required")
	}
	a.mu.Lock()
	if a.paired {
		a.mu.Unlock()
		return ErrAlreadyPaired
	}
	a.ctrlURL = u.Scheme + "://" + u.Host
	a.token = pairToken
	a.haveNodeToken = false
	a.mu.Unlock()
	slog.Info("datanode: adopt requested by controller", "controller", u.Scheme+"://"+u.Host)
	go func() {
		for i := 0; i < 5 && !a.isPaired(); i++ {
			err := a.Register()
			if err == nil {
				if rerr := a.ReportDisks(); rerr != nil {
					slog.Warn("datanode: report disks failed", "err", rerr)
				}
				return
			}
			slog.Warn("datanode: adopt register retry", "err", err, "attempt", i+1)
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}()
	return nil
}

// ReportDisks reports all configured paths to the controller.
func (a *Agent) ReportDisks() error {
	base := a.controllerURL()
	if base == "" {
		return nil
	}
	for _, p := range a.cfg.Datanode.Paths {
		capacity, used := diskUsage(p)
		body, _ := json.Marshal(map[string]any{
			"node_id":  a.nodeID,
			"path":     p,
			"capacity": capacity,
			"used":     used,
			"token":    a.currentToken(),
		})
		resp, err := a.client.Post(base+"/internal/disks", "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("report disk %s failed: %s", p, resp.Status)
		}
	}
	return nil
}

// Heartbeat sends a single heartbeat.
func (a *Agent) Heartbeat() error {
	base := a.controllerURL()
	if base == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{
		"node_id": a.nodeID,
		"token":   a.currentToken(),
	})
	resp, err := a.client.Post(base+"/internal/heartbeat", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errUnknownNode
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed: %s", resp.Status)
	}
	return nil
}

// registerAndReport registers once and, on success, reports the disks.
func (a *Agent) registerAndReport() {
	if err := a.Register(); err != nil {
		slog.Warn("datanode: register failed", "err", err)
		return
	}
	if err := a.ReportDisks(); err != nil {
		slog.Warn("datanode: report disks failed", "err", err)
	}
}

// RegisterAndHeartbeatLoop registers, reports disks and then heartbeats periodically.
// Unpaired nodes keep trying to register on every tick, so they can also be adopted later.
func (a *Agent) RegisterAndHeartbeatLoop(ticker *time.Ticker) error {
	defer ticker.Stop()
	// Initial register with retry.
	for i := 0; i < 10 && !a.isPaired(); i++ {
		err := a.Register()
		if err == nil {
			break
		}
		slog.Warn("datanode: register retry", "err", err, "attempt", i+1)
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if a.isPaired() {
		if err := a.ReportDisks(); err != nil {
			slog.Warn("datanode: report disks failed", "err", err)
		}
	}
	for range ticker.C {
		if !a.isPaired() {
			a.registerAndReport()
			continue
		}
		err := a.Heartbeat()
		if err == nil {
			continue
		}
		slog.Warn("datanode: heartbeat failed", "err", err)
		if errors.Is(err, errUnknownNode) {
			a.registerAndReport()
		}
	}
	return nil
}

// listenAddr returns the address the controller should use to reach this
// DataNode (/internal/objects/*). It uses the port from datanode.listen
// (default 9002) instead of the controller's admin port.
func (a *Agent) listenAddr() string {
	port := "9002"
	if _, p, err := net.SplitHostPort(strings.TrimSpace(a.cfg.Datanode.Listen)); err == nil && p != "" {
		port = p
	}
	return net.JoinHostPort(a.nodeID, port)
}

func diskUsage(path string) (capacity, used int64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		// If path does not exist, try to use net.Dial trick to avoid unused import.
		_ = net.ParseIP("127.0.0.1")
		return 0, 0
	}
	_ = net.ParseIP("127.0.0.1")
	capacity = int64(st.Blocks * uint64(st.Bsize))
	used = int64((st.Blocks - st.Bavail) * uint64(st.Bsize))
	return capacity, used
}
