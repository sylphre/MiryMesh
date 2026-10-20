// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/sylphre/MiryMesh/internal/placement"
	"golang.org/x/crypto/argon2"
)

// SetupState holds the persisted setup state (MVP: /var/lib/mirymesh/setup.json).
type SetupState struct {
	Initialized       bool   `json:"initialized"`
	AdminUsername     string `json:"admin_username"`
	AdminPasswordHash string `json:"admin_password_hash"` // argon2id$<salt-hex>$<hash-hex>$time=1,memory=65536,threads=4
	AccessKey         string `json:"access_key"`
	SecretKey         string `json:"secret_key"`
	AdminToken        string `json:"admin_token"`
	DefaultPolicy     string `json:"default_policy"`
	DBDriver          string `json:"db_driver,omitempty"`
	DBDSN             string `json:"db_dsn,omitempty"`
	Region            string `json:"region,omitempty"`
	Gateway           bool   `json:"gateway,omitempty"`
	DatanodePaths     string `json:"datanode_paths,omitempty"` // comma-separated
}

// SetupStoreIfaceUpdater is an optional extension for post-init S3 update.
type SetupStoreIfaceUpdater interface {
	SetupStoreIface
	Update(*SetupState) error
}

// SetupStore manages setup persistence.
type SetupStore struct {
	mu    sync.Mutex
	path  string
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

// UpdatePolicy updates default policy without the already-initialized guard.
func (s *SetupStore) UpdatePolicy(policy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = &SetupState{Initialized: true, DefaultPolicy: policy}
	} else {
		s.state.DefaultPolicy = policy
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
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
	return nil
}

// Update merges S3/DB fields for the file store (Step2 after init).
func (s *SetupStore) Update(st *SetupState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state = st
		s.state.Initialized = true
	} else {
		// Merge non-empty fields
		if st.AccessKey != "" {
			s.state.AccessKey = st.AccessKey
		}
		if st.SecretKey != "" {
			s.state.SecretKey = st.SecretKey
		}
		if st.DefaultPolicy != "" {
			s.state.DefaultPolicy = st.DefaultPolicy
		}
		if st.Region != "" {
			s.state.Region = st.Region
		}
		if st.DatanodePaths != "" {
			s.state.DatanodePaths = st.DatanodePaths
		}
		if st.DBDriver != "" {
			s.state.DBDriver = st.DBDriver
		}
		if st.DBDSN != "" {
			s.state.DBDSN = st.DBDSN
		}
		if st.Gateway {
			s.state.Gateway = st.Gateway
		}
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
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

func hashPasswordArgon2id(password string) (string, error) {
	if password == "" {
		return "", errInvalidSecret
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$%s$%s$t=1,m=65536,p=4", hex.EncodeToString(salt), hex.EncodeToString(hash)), nil
}

func verifyPasswordArgon2id(password, stored string) bool {
	// stored format: argon2id$<salt-hex>$<hash-hex>$t=1,m=65536,p=4  or legacy argon2id$<salt>$<hash>
	parts := strings.Split(stored, "$")
	if len(parts) < 3 {
		return false
	}
	saltHex := parts[1]
	hashHex := parts[2]
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(got, expected) == 1
}

// generateRandomCredential creates a 20-char alphanumeric AccessKey and 32-char SecretKey via crypto/rand.
func generateRandomCredential() (string, string) {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	sb := make([]byte, 32)
	_, _ = rand.Read(sb)
	// base64-like but hex for simplicity
	secret := hex.EncodeToString(sb)[:32]
	return string(b), secret
}

var _ = strconv.Itoa // keep import

var (
	errAlreadyInitialized = &apiError{Code: 403, Msg: "already initialized"}
	errInvalidAccessKey   = &apiError{Code: 400, Msg: "access_key must be 3-128 chars"}
	errInvalidSecret      = &apiError{Code: 400, Msg: "secret_key must be at least 8 chars"}
)

type apiError struct {
	Code int
	Msg  string
}

func (e *apiError) Error() string { return e.Msg }

func (h *Handler) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/admin/setup", h.handleSetup)
	mux.HandleFunc("/api/admin/setup/status", h.handleSetupStatus)
	mux.HandleFunc("/api/admin/login", h.handleLogin)
	mux.HandleFunc("/api/admin/generate-credentials", h.handleGenerateCredentials)
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
			// Step2: S3/DB config via X-Admin-Token (already initialized)
			if !h.checkAuth(w, r) {
				return
			}
			// Try Update path (DB) or file Save fallback
			if updater, ok := h.setupStore.(SetupStoreIfaceUpdater); ok {
				// Parse same payload and update
				var req2 struct {
					AccessKey     string `json:"access_key"`
					SecretKey     string `json:"secret_key"`
					DefaultPolicy string `json:"default_policy"`
					Region        string `json:"region"`
					Gateway       bool   `json:"gateway"`
					DatanodePaths string `json:"datanode_paths"`
					DBDriver      string `json:"db_driver"`
					DBDSN         string `json:"db_dsn"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req2)
				// Build from existing + new values
				cur := h.setupStore.Get()
				if cur != nil {
					if strings.TrimSpace(req2.AccessKey) != "" {
						cur.AccessKey = strings.TrimSpace(req2.AccessKey)
					}
					if strings.TrimSpace(req2.SecretKey) != "" {
						cur.SecretKey = req2.SecretKey
					}
					if req2.DefaultPolicy != "" {
						cur.DefaultPolicy = strings.ToLower(req2.DefaultPolicy)
					}
					if req2.Region != "" {
						cur.Region = req2.Region
					}
					cur.Gateway = req2.Gateway
					if req2.DatanodePaths != "" {
						cur.DatanodePaths = req2.DatanodePaths
					}
					if req2.DBDriver != "" {
						cur.DBDriver = req2.DBDriver
					}
					if req2.DBDSN != "" {
						cur.DBDSN = req2.DBDSN
					}
					if err := updater.Update(cur); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				if h.authStore != nil && cur != nil {
					// If AccessKey rotated, remove old key so it doesn't linger.
					if old := h.setupStore.Get(); old != nil && old.AccessKey != "" && old.AccessKey != cur.AccessKey {
						h.authStore.Remove(old.AccessKey)
					}
					_ = h.authStore.Add(cur.AccessKey, cur.SecretKey)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"admin_token": cur.AdminToken, "default_policy": cur.DefaultPolicy})
				return
			}
			http.Error(w, "already initialized", http.StatusForbidden)
			return
		}
		var req struct {
			Username      string `json:"username"`
			Password      string `json:"password"`
			AccessKey     string `json:"access_key"`
			SecretKey     string `json:"secret_key"`
			DefaultPolicy string `json:"default_policy"`
			Region        string `json:"region"`
			Gateway       bool   `json:"gateway"`
			DatanodePaths string `json:"datanode_paths"`
			DBDriver      string `json:"db_driver"`
			DBDSN         string `json:"db_dsn"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Username) == "" || len(req.Username) < 3 || len(req.Username) > 64 {
			http.Error(w, "username must be 3-64 chars", http.StatusBadRequest)
			return
		}
		if len(req.Password) < 8 {
			http.Error(w, "password must be at least 8 chars", http.StatusBadRequest)
			return
		}
		// Step1 (Admin creation) may omit AccessKey/SecretKey — they come in Step2 via login + S3 config.
		// Only validate AK/SK if at least one is non-empty.
		if strings.TrimSpace(req.AccessKey) != "" || strings.TrimSpace(req.SecretKey) != "" {
			if err := validateSetupInput(req.AccessKey, req.SecretKey, req.DefaultPolicy); err != nil {
				http.Error(w, err.Error(), asStatus(err))
				return
			}
		}
		policy := req.DefaultPolicy
		if policy == "" {
			policy = "single"
		}
		hash, err := hashPasswordArgon2id(req.Password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		token := generateToken()
		st := &SetupState{
			Initialized:       true,
			AdminUsername:     strings.TrimSpace(req.Username),
			AdminPasswordHash: hash,
			AccessKey:         strings.TrimSpace(req.AccessKey),
			SecretKey:         req.SecretKey,
			AdminToken:        token,
			DefaultPolicy:     strings.ToLower(policy),
			Region:            req.Region,
			Gateway:           req.Gateway,
			DatanodePaths:     req.DatanodePaths,
			DBDriver:          req.DBDriver,
			DBDSN:             req.DBDSN,
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
				_ = h.resolver.SetDefaultPolicy(p)
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

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h.setupStore == nil {
		http.Error(w, "setup not configured", http.StatusInternalServerError)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.setupStore.IsInitialized() {
		http.Error(w, "not initialized", http.StatusBadRequest)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	st := h.setupStore.Get()
	if st == nil || st.AdminUsername != strings.TrimSpace(req.Username) || !verifyPasswordArgon2id(req.Password, st.AdminPasswordHash) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"admin_token": st.AdminToken})
}

func (h *Handler) handleGenerateCredentials(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ak, sk := generateRandomCredential()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_key": ak, "secret_key": sk})
}
