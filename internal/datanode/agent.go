// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/sylphre/MiryMesh/internal/config"
)

// Agent runs on a datanode: registers itself, reports disks and heartbeats.
type Agent struct {
	cfg    config.Config
	client *http.Client
	nodeID string
	token  string
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

// Register performs a single registration RPC.
func (a *Agent) Register() error {
	if a.cfg.Datanode.ControllerURL == "" {
		return fmt.Errorf("controller_url is not configured")
	}
	addr := a.listenAddr()
	body, _ := json.Marshal(map[string]string{
		"node_id": a.nodeID,
		"addr":    addr,
		"token":   a.token,
	})
	url := a.cfg.Datanode.ControllerURL + "/internal/register"
	resp, err := a.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register failed: %s", resp.Status)
	}
	slog.Info("datanode: registered", "node_id", a.nodeID, "addr", addr)
	return nil
}

// ReportDisks reports all configured paths to the controller.
func (a *Agent) ReportDisks() error {
	if a.cfg.Datanode.ControllerURL == "" {
		return nil
	}
	for _, p := range a.cfg.Datanode.Paths {
		capacity, used := diskUsage(p)
		body, _ := json.Marshal(map[string]any{
			"node_id":  a.nodeID,
			"path":     p,
			"capacity": capacity,
			"used":     used,
			"token":    a.token,
		})
		url := a.cfg.Datanode.ControllerURL + "/internal/disks"
		resp, err := a.client.Post(url, "application/json", bytes.NewReader(body))
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
	if a.cfg.Datanode.ControllerURL == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]string{
		"node_id": a.nodeID,
		"token":   a.token,
	})
	url := a.cfg.Datanode.ControllerURL + "/internal/heartbeat"
	resp, err := a.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed: %s", resp.Status)
	}
	return nil
}

// RegisterAndHeartbeatLoop registers, reports disks and then heartbeats periodically.
func (a *Agent) RegisterAndHeartbeatLoop(ticker *time.Ticker) error {
	defer ticker.Stop()
	// Initial register with retry.
	for i := 0; i < 10; i++ {
		if err := a.Register(); err == nil {
			break
		} else {
			slog.Warn("datanode: register retry", "err", err, "attempt", i+1)
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}
	if err := a.ReportDisks(); err != nil {
		slog.Warn("datanode: report disks failed", "err", err)
	}
	for range ticker.C {
		if err := a.Heartbeat(); err != nil {
			slog.Warn("datanode: heartbeat failed", "err", err)
		}
	}
	return nil
}

func (a *Agent) listenAddr() string {
	return a.nodeID + ":9001"
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
