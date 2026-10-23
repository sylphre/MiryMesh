// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sylphre/MiryMesh/internal/users"
)

// SetUserManager attaches the S3 user manager used by /api/admin/users.
func (h *Handler) SetUserManager(m *users.Manager) { h.userMgr = m }

// HandleUsers is the exported entry point for Gin.
func (h *Handler) HandleUsers(w http.ResponseWriter, r *http.Request) { h.handleUsers(w, r) }

type userGrantReq struct {
	Bucket     string `json:"bucket"`
	Permission string `json:"permission"`
}

type userCreateReq struct {
	Name        string         `json:"name"`
	Permissions []userGrantReq `json:"permissions"`
}

type userPermsReq struct {
	Permissions []userGrantReq `json:"permissions"`
}

type userPatchReq struct {
	Enabled *bool `json:"enabled"`
}

type userKeyResp struct {
	User      users.User `json:"user"`
	SecretKey string     `json:"secret_key"`
}

func userJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func userDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return false
	}
	return true
}

func userError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, users.ErrExists):
		http.Error(w, err.Error(), http.StatusConflict)
	case users.IsInvalid(err):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		slog.Error("admin: users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// toPerms validates grants and turns them into a permission map. Unknown buckets are rejected
// so a typo does not silently create a grant that applies to a bucket created later.
func (h *Handler) toPerms(grants []userGrantReq) (map[string]users.Permission, error) {
	perms := make(map[string]users.Permission, len(grants))
	for _, g := range grants {
		b := strings.TrimSpace(g.Bucket)
		p := users.Permission(strings.TrimSpace(g.Permission))
		if !p.Valid() {
			return nil, users.ErrInvalidPermission
		}
		if b != users.AllBuckets {
			if b == "" {
				return nil, users.ErrInvalidBucket
			}
			if h.storage != nil && !h.storage.BucketExists(b) {
				return nil, fmt.Errorf("%w: %q does not exist", users.ErrInvalidBucket, b)
			}
		}
		perms[b] = p
	}
	return perms, nil
}

// handleUsers serves:
//
//	GET    /api/admin/users
//	POST   /api/admin/users                     {name, permissions[]}   -> access_key + secret_key (shown once)
//	GET    /api/admin/users/{name}
//	PATCH  /api/admin/users/{name}              {enabled}
//	DELETE /api/admin/users/{name}
//	PUT    /api/admin/users/{name}/permissions  {permissions[]}
//	POST   /api/admin/users/{name}/rotate-key                           -> new access_key + secret_key
func (h *Handler) handleUsers(w http.ResponseWriter, r *http.Request) {
	if !h.checkAuth(w, r) {
		return
	}
	if h.userMgr == nil {
		http.Error(w, "users are not available", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users"), "/")
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, "/")
	}

	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		userJSON(w, http.StatusOK, map[string]any{"users": h.userMgr.List()})

	case len(parts) == 0 && r.Method == http.MethodPost:
		var req userCreateReq
		if !userDecode(w, r, &req) {
			return
		}
		perms, err := h.toPerms(req.Permissions)
		if err != nil {
			userError(w, err)
			return
		}
		u, secret, err := h.userMgr.Create(strings.TrimSpace(req.Name), perms)
		if err != nil {
			userError(w, err)
			return
		}
		userJSON(w, http.StatusCreated, userKeyResp{User: u, SecretKey: secret})

	case len(parts) == 1 && r.Method == http.MethodGet:
		u, ok := h.userMgr.Get(parts[0])
		if !ok {
			userError(w, users.ErrNotFound)
			return
		}
		userJSON(w, http.StatusOK, u)

	case len(parts) == 1 && r.Method == http.MethodPatch:
		var req userPatchReq
		if !userDecode(w, r, &req) {
			return
		}
		if req.Enabled == nil {
			http.Error(w, "enabled required", http.StatusBadRequest)
			return
		}
		u, err := h.userMgr.SetEnabled(parts[0], *req.Enabled)
		if err != nil {
			userError(w, err)
			return
		}
		userJSON(w, http.StatusOK, u)

	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := h.userMgr.Delete(parts[0]); err != nil {
			userError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case len(parts) == 2 && parts[1] == "permissions" && r.Method == http.MethodPut:
		var req userPermsReq
		if !userDecode(w, r, &req) {
			return
		}
		perms, err := h.toPerms(req.Permissions)
		if err != nil {
			userError(w, err)
			return
		}
		u, err := h.userMgr.SetPermissions(parts[0], perms)
		if err != nil {
			userError(w, err)
			return
		}
		userJSON(w, http.StatusOK, u)

	case len(parts) == 2 && parts[1] == "rotate-key" && r.Method == http.MethodPost:
		u, secret, err := h.userMgr.RotateKey(parts[0])
		if err != nil {
			userError(w, err)
			return
		}
		userJSON(w, http.StatusOK, userKeyResp{User: u, SecretKey: secret})

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}
