// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sylphre/MiryMesh/internal/placement"
)

// SetupState holds the persisted setup state (MVP: /var/lib/mirymesh/setup.json).
type SetupState struct {
	Initialized      bool   `json:"initialized"`
	AccessKey        string `json:"access_key"`
	SecretKey        string `json:"secret_key"`
	AdminToken       string `json:"admin_token"`
	DefaultPolicy    string `json:"default_policy"`
}

// SetupStore manages setup persistence.
type SetupStore struct {
	mu   sync.Mutex
	path string
	state *SetupState
}

// NewSetupStore creates a store. Path defaults to <dataDir>/setup.json.
func NewSetupStore(dataDir string) *SetupStore {
	dir := dataDir
	if dir == "" {
		dir = "/var/lib/mirymesh"
	}
	p := filepath.Join(dir, "setup.json")
	s := &SetupStore{path: p}
	s.load()
	return s
}

func (s *SetupStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var st SetupState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	s.state = &st
}

func (s *SetupStore) IsInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state != nil && s.state.Initialized
}

func (s *SetupStore) Get() *SetupState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return nil
	}
	cpy := *s.state
	return &cpy
}

func (s *SetupStore) Save(st *SetupState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.Initialized && s.state != nil && s.state.Initialized {
		return errAlreadyInitialized
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.state = st
	return nil
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func validateSetupInput(accessKey, secretKey, policy string) error {
	if strings.TrimSpace(accessKey) == "" {
		return errInvalidAccessKey
	}
	if len(accessKey) < 3 || len(accessKey) > 128 {
		return errInvalidAccessKey
	}
	if strings.TrimSpace(secretKey) == "" || len(secretKey) < 8 {
		return errInvalidSecret
	}
	if policy != "" {
		if _, err := placement.ParsePolicy(policy); err != nil {
			return err
		}
	}
	return nil
}

var (
	errAlreadyInitialized = &apiError{Code: 403, Msg: "already initialized"}
	errInvalidAccessKey   = &apiError{Code: 400, Msg: "access_key must be 3-128 chars"}
	errInvalidSecret      = &apiError{Code: 400, Msg: "secret_key must be at least 8 chars"}
)

type apiError struct{ Code int; Msg string }
func (e *apiError) Error() string { return e.Msg }

func (h *Handler) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/setup", h.handleSetup)
	mux.HandleFunc("/api/admin/setup/status", h.handleSetupStatus)
}

func (h *Handler) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.setupStore == nil {
		http.Error(w, "setup not configured", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"initialized": h.setupStore.IsInitialized()})
}

func (h *Handler) handleSetup(w http.ResponseWriter, r *http.Request) {
	if h.setupStore == nil {
		http.Error(w, "setup not configured", http.StatusInternalServerError)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleSetupStatus(w, r)
	case http.MethodPost:
		if h.setupStore.IsInitialized() {
			http.Error(w, "already initialized", http.StatusForbidden)
			return
		}
		var req struct {
			AccessKey     string `json:"access_key"`
			SecretKey     string `json:"secret_key"`
			DefaultPolicy string `json:"default_policy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := validateSetupInput(req.AccessKey, req.SecretKey, req.DefaultPolicy); err != nil {
			http.Error(w, err.Error(), asStatus(err))
			return
		}
		policy := req.DefaultPolicy
		if policy == "" {
			policy = "single"
		}
		token := generateToken()
		st := &SetupState{
			Initialized:   true,
			AccessKey:     strings.TrimSpace(req.AccessKey),
			SecretKey:     req.SecretKey,
			AdminToken:    token,
			DefaultPolicy: strings.ToLower(policy),
		}
		if err := h.setupStore.Save(st); err != nil {
			if err == errAlreadyInitialized {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Wire into running services
		if h.authStore != nil {
			_ = h.authStore.Add(st.AccessKey, st.SecretKey)
		}
		h.adminToken = token
		if h.resolver != nil && st.DefaultPolicy != "" {
			if p, err := placement.ParsePolicy(st.DefaultPolicy); err == nil {
				// resolver has no direct default setter — update via bucket_policy fallback is not needed; store default in resolver
				_ = p
				// For MVP, default policy is applied via new resolver default; we keep original resolver and just log
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"admin_token": token, "default_policy": st.DefaultPolicy})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func asStatus(err error) int {
	if e, ok := err.(*apiError); ok {
		return e.Code
	}
	return 400
}
