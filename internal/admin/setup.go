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
	"strconv"
	"strings"

	"github.com/sylphre/MiryMesh/internal/placement"
	"golang.org/x/crypto/argon2"
)

// SetupState holds the persisted setup state (DB: setup table).
type SetupState struct {
	Initialized       bool   `json:"initialized"`
	AdminUsername     string `json:"admin_username"`
	AdminPasswordHash string `json:"admin_password_hash"`
	AccessKey         string `json:"access_key"`
	SecretKey         string `json:"secret_key"`
	AdminToken        string `json:"admin_token"`
	DefaultPolicy     string `json:"default_policy"`
	DBDriver          string `json:"db_driver,omitempty"`
	DBDSN             string `json:"db_dsn,omitempty"`
	Region            string `json:"region,omitempty"`
	Gateway           bool   `json:"gateway,omitempty"`
	DatanodePaths     string `json:"datanode_paths,omitempty"`
}

// SetupStoreIfaceUpdater is an optional extension for post-init update.
type SetupStoreIfaceUpdater interface {
	SetupStoreIface
	Update(*SetupState) error
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

func generateRandomCredential() (string, string) {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = letters[int(b[i])%len(letters)]
	}
	sb := make([]byte, 32)
	_, _ = rand.Read(sb)
	secret := hex.EncodeToString(sb)[:32]
	return string(b), secret
}

var _ = strconv.Itoa

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
	mux.HandleFunc("/api/admin/pair-token", h.handlePairToken)
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

func (h *Handler) handlePairToken(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sdb, ok := h.setupStore.(*SetupStoreDB)
	if !ok || sdb == nil {
		http.Error(w, "pair token requires DB setup", http.StatusInternalServerError)
		return
	}
	token, err := sdb.CreatePairToken(15 * 60 * 1e9) // 15m
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"pair_token": token, "expires_in": 900})
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
			if !h.checkAuth(w, r) {
				return
			}
			if updater, ok := h.setupStore.(SetupStoreIfaceUpdater); ok {
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
				cur := h.setupStore.Get()
				if cur != nil {
					// Gateway with 0 nodes: allow empty S3 keys and skip policy validation
					isGatewayUpdate := req2.Gateway || cur.Gateway
					if !isGatewayUpdate && (strings.TrimSpace(req2.AccessKey) != "" || strings.TrimSpace(req2.SecretKey) != "") {
						if err := validateSetupInput(req2.AccessKey, req2.SecretKey, req2.DefaultPolicy); err != nil {
							http.Error(w, err.Error(), asStatus(err))
							return
						}
					}
					if !isGatewayUpdate && req2.DefaultPolicy != "" {
						if pol, err := placement.ParsePolicy(req2.DefaultPolicy); err == nil {
							if err := pol.ValidateForCluster(h.registry.OnlineNodeCount()); err != nil {
								http.Error(w, err.Error(), http.StatusBadRequest)
								return
							}
						}
					}
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
				if h.authStore != nil && cur != nil && cur.AccessKey != "" {
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
		if strings.TrimSpace(req.AccessKey) != "" || strings.TrimSpace(req.SecretKey) != "" {
			if err := validateSetupInput(req.AccessKey, req.SecretKey, req.DefaultPolicy); err != nil {
				http.Error(w, err.Error(), asStatus(err))
				return
			}
		}
		// Gateway + no datanode: S3 keys may be empty — allow empty, but if gateway and no nodes, S3 creation will be 503.
		// Non-gateway with no datanode and no S3 keys: allow empty as well (keys created later via generate-credentials).
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
		if h.authStore != nil && st.AccessKey != "" {
			_ = h.authStore.Add(st.AccessKey, st.SecretKey)
		}
		// Create session for the new admin token
		if sdb, ok := h.setupStore.(*SetupStoreDB); ok {
			_ = sdb.CreateSession(token, st.AdminUsername, 24*60*60*1e9)
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
	if !allowLogin(clientIP(r)) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
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
	// Issue session if DB-backed
	if sdb, ok := h.setupStore.(*SetupStoreDB); ok {
		_ = sdb.CreateSession(st.AdminToken, st.AdminUsername, 24*60*60*1e9)
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
