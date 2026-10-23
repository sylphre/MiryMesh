// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// ObjectStore is the local storage interface for DataNode objects (subset of s3api.ObjectStore, defined here to avoid import cycle).
type ObjectStore interface {
	PutObject(bucket, key string, contentType string, meta map[string]string, r io.Reader) (interface{ GetETag() string }, error)
	GetObject(bucket, key string) (interface{ GetContentType() string }, io.ReadCloser, error)
	HeadObject(bucket, key string) (interface{}, error)
	DeleteObject(bucket, key string) error
	ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (interface{}, error)
	BucketExists(bucket string) bool
}

// Handler serves controller-side internal API for datanode registration,
// heartbeat and disk management. It does NOT handle S3 requests.
type Handler struct {
	registry *Registry
	// token is the shared registration token for M2 (single static token).
	// TODO: replace with GUI-issued one-time tokens (M4+). See M2-review #2.
	token string
	// evacDir is the controller-side evacuation dir for backup/restore (M5).
	evacDir string
	// store is the local object store on the DataNode (DataNode only).
	store interface{}
	// pairConsume validates and burns a one-time pair token (controller side).
	pairConsume func(token string) error
	// adopter handles controller-initiated pairing (DataNode side, opt-in).
	adopter func(controllerURL, pairToken string) error
	// nodeTok is the per-node token learned after registration (DataNode side).
	nodeTok tokenBox
}

// NewHandler creates a Handler.
func NewHandler(reg *Registry, token string) *Handler {
	return &Handler{registry: reg, token: token}
}

// NewHandlerWithEvacDir creates a Handler with evacuation dir.
func NewHandlerWithEvacDir(reg *Registry, token, evacDir string) *Handler {
	return &Handler{registry: reg, token: token, evacDir: evacDir}
}

// NewHandlerWithObjects creates a DataNode-side Handler with local store bound.
func NewHandlerWithObjects(reg *Registry, token string, store interface{}) *Handler {
	return &Handler{registry: reg, token: token, store: store}
}

// RegisterRoutes mounts /internal/* endpoints on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/internal/register", h.handleRegister)
	mux.HandleFunc("/internal/heartbeat", h.handleHeartbeat)
	mux.HandleFunc("/internal/disks", h.handleDisks)
	mux.HandleFunc("/internal/nodes", h.handleListNodes)
	mux.HandleFunc("/internal/evacuate", h.handleEvacuate)
	mux.HandleFunc("/internal/restore", h.handleRestore)
	// ObjectStore passthrough for gateway mode
	mux.HandleFunc("/internal/objects/", h.handleObjects)
	// Controller-initiated pairing (404 unless an adopter is installed)
	mux.HandleFunc("/internal/adopt", h.handleAdopt)
}

type registerReq struct {
	NodeID string `json:"node_id"`
	Addr   string `json:"addr"`
	Token  string `json:"token"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	authed := h.tokenOK(req.NodeID, req.Token)
	viaPair := !authed && h.pairConsume != nil && req.Token != ""
	if !authed && !viaPair {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if strings.TrimSpace(req.NodeID) == "" || strings.TrimSpace(req.Addr) == "" {
		http.Error(w, "node_id and addr required", http.StatusBadRequest)
		return
	}
	// A one-time pair token is only burned once the request is known to be well-formed.
	if viaPair {
		if err := h.pairConsume(req.Token); err != nil {
			http.Error(w, "invalid token", http.StatusForbidden)
			return
		}
	}
	if err := h.registry.RegisterNode(req.NodeID, req.Addr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := map[string]string{"status": "ok"}
	if h.token != "" {
		// Per-node credential for later heartbeat/disk calls and re-registration.
		resp["node_token"] = NodeToken(h.token, req.NodeID)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type heartbeatReq struct {
	NodeID string `json:"node_id"`
	Token  string `json:"token"` // optional: same token check if configured
}

func (h *Handler) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req heartbeatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if h.token != "" && req.Token != "" && !h.tokenOK(req.NodeID, req.Token) {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if err := h.registry.Heartbeat(req.NodeID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// diskReq represents upsert/delete payload for a disk path.
type diskReq struct {
	NodeID   string `json:"node_id"`
	Path     string `json:"path"`
	Capacity int64  `json:"capacity"`
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
	Token    string `json:"token"`
}

func (h *Handler) handleDisks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListDisks(w, r)
	case http.MethodPost:
		var req diskReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if h.token != "" && req.Token != "" && !h.tokenOK(req.NodeID, req.Token) {
			http.Error(w, "invalid token", http.StatusForbidden)
			return
		}
		if err := h.registry.UpsertDisk(req.NodeID, req.Path, req.Capacity, req.Used, req.Limit); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	case http.MethodDelete:
		nodeID := r.URL.Query().Get("node_id")
		path := r.URL.Query().Get("path")
		if nodeID == "" || path == "" {
			http.Error(w, "node_id and path required", http.StatusBadRequest)
			return
		}
		if err := h.registry.RemoveDisk(nodeID, path); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleListDisks(w http.ResponseWriter, _ *http.Request) {
	disks := h.registry.ListDisks()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"disks": disks})
}

func (h *Handler) handleListNodes(w http.ResponseWriter, _ *http.Request) {
	nodes := h.registry.ListNodes()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes})
}

func (h *Handler) handleEvacuate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Token string `json:"token"`
		// Snapshot payload: if empty, evacuates current registry snapshot.
		Snapshot *metadata.EvacuationSnapshot `json:"snapshot,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if h.token != "" && req.Token != h.token {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	dir := h.evacDir
	if dir == "" {
		dir = os.TempDir()
	}
	ev, err := metadata.NewEvacuator(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	snap := req.Snapshot
	if snap == nil {
		// TODO(M5/Phase4): fan-out and authoritative snapshot shape.
		// - Fan-out: controller's Evacuator.Save should POST the snapshot to each
		//   datanode's own evacuate endpoint (registry-discovered) so every node
		//   holds a copy for master takeover (docs/07). M5 only writes locally.
		// - Shape: this fallback currently mirrors registry nodes into Buckets; the
		//   authoritative snapshot should be built from s3api.Storage (buckets/objects)
		//   plus PlacementPolicies and Auth credentials (docs/05/07). Wire bucket/object
		//   enumeration + policy/auth evacuation in Phase4.
		snap = &metadata.EvacuationSnapshot{Buckets: []string{}}
		for _, n := range h.registry.ListNodes() {
			snap.Buckets = append(snap.Buckets, n.ID)
		}
	}
	if err := ev.Save(snap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": snap.Version})
}

func (h *Handler) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// TODO(M5/Phase4): adoption mode — new master on startup should auto-call
	// metadata.LoadAny(localDir, remoteDirs...) then Restorer.Restore to adopt
	// evacuated state (docs/07). Wire this into cmd/controller startup sequence.
	// TODO(M5/Phase4): PlacementPolicies/Auth evacuation — extend EvacuationSnapshot
	// to include placement policies and access keys so GUI restore is complete.
	dir := h.evacDir
	if dir == "" {
		dir = os.TempDir()
	}
	snap, err := metadata.LoadAny(dir, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("no snapshot: %v", err), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

// objectStore is the minimal DataNode storage interface (avoids import cycle with s3api).
type objectStore interface {
	PutObject(bucket, key string, contentType string, meta map[string]string, r io.Reader) (objectMeta, error)
	GetObject(bucket, key string) (objectMeta, io.ReadCloser, error)
	HeadObject(bucket, key string) (objectMeta, error)
	DeleteObject(bucket, key string) error
	ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (objectListResult, error)
}
type objectMeta struct {
	ETag, ContentType string
	Meta              map[string]string
}
type objectListResult struct {
	Objects        []objectMeta
	CommonPrefixes []string
}

func (h *Handler) tryStore() (objectStore, bool) {
	s, ok := h.store.(objectStore)
	return s, ok
}

// handleObjects proxies S3 object operations to local storage on the DataNode itself.
// Controller's RemoteStore calls this via http.Client (streaming).
// Path: /internal/objects/{bucket}/{key...} (+ ?prefix/delimiter/continuation-token/max-keys for list, ?token for auth)
func (h *Handler) handleObjects(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "DataNode storage not configured", http.StatusInternalServerError)
		return
	}
	if want := h.objectToken(); want != "" && r.Header.Get("X-Internal-Token") != want && r.URL.Query().Get("token") != want {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	s, ok := h.tryStore()
	if !ok {
		http.Error(w, "DataNode storage type mismatch", http.StatusInternalServerError)
		return
	}
	// Unescape path
	trim := strings.TrimPrefix(r.URL.Path, "/internal/objects/")
	if dec, err := url.PathUnescape(trim); err == nil {
		trim = dec
	}
	parts := strings.SplitN(trim, "/", 2)
	bucket := ""
	key := ""
	if len(parts) >= 1 {
		bucket, _ = url.PathUnescape(parts[0])
	}
	if len(parts) == 2 {
		key, _ = url.PathUnescape(parts[1])
	}
	// Bucket-level list: GET /internal/objects/{bucket}?prefix=&delimiter=&continuation-token=&max-keys=
	if r.Method == http.MethodGet && key == "" {
		q := r.URL.Query()
		prefix := q.Get("prefix")
		delimiter := q.Get("delimiter")
		cont := q.Get("continuation-token")
		maxKeysStr := q.Get("max-keys")
		maxKeys := 1000
		if maxKeysStr != "" {
			_, _ = fmt.Sscanf(maxKeysStr, "%d", &maxKeys)
		}
		res, err := s.ListObjectsV2(bucket, prefix, delimiter, cont, maxKeys)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"objects": res.Objects, "prefixes": res.CommonPrefixes})
		return
	}
	switch r.Method {
	case http.MethodPut:
		ct := r.Header.Get("Content-Type")
		if ct == "" {
			ct = r.Header.Get("content-type")
		}
		meta := map[string]string{}
		for k, vs := range r.Header {
			lk := strings.ToLower(k)
			if strings.HasPrefix(lk, "x-amz-meta-") {
				meta[lk[len("x-amz-meta-"):]] = strings.Join(vs, ",")
			}
		}
		m, err := s.PutObject(bucket, key, ct, meta, r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("ETag", m.ETag) // objectMeta
		w.WriteHeader(200)
	case http.MethodGet:
		meta, rc, err := s.GetObject(bucket, key)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		defer rc.Close()
		w.Header().Set("ETag", meta.ETag)
		if meta.ContentType != "" {
			w.Header().Set("Content-Type", meta.ContentType)
		}
		for k, v := range meta.Meta {
			w.Header().Set("X-Amz-Meta-"+k, v)
		}
		w.WriteHeader(200)
		_, _ = io.Copy(w, rc)
	case http.MethodHead:
		meta, err := s.HeadObject(bucket, key)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		w.Header().Set("ETag", meta.ETag)
		if meta.ContentType != "" {
			w.Header().Set("Content-Type", meta.ContentType)
		}
		for k, v := range meta.Meta {
			w.Header().Set("X-Amz-Meta-"+k, v)
		}
		w.WriteHeader(200)
	case http.MethodDelete:
		if err := s.DeleteObject(bucket, key); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
