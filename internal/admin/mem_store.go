// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import "sync"

// MemSetupStore is an in-memory fallback when no DB is configured (dev only).
type MemSetupStore struct {
	mu    sync.Mutex
	state *SetupState
}

func NewMemSetupStore() *MemSetupStore { return &MemSetupStore{} }

func (m *MemSetupStore) IsInitialized() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state != nil && m.state.Initialized
}
func (m *MemSetupStore) Get() *SetupState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return nil
	}
	cpy := *m.state
	return &cpy
}
func (m *MemSetupStore) Save(st *SetupState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != nil && m.state.Initialized {
		return errAlreadyInitialized
	}
	cpy := *st
	m.state = &cpy
	return nil
}
func (m *MemSetupStore) UpdatePolicy(policy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		m.state = &SetupState{Initialized: true, DefaultPolicy: policy}
	} else {
		m.state.DefaultPolicy = policy
	}
	return nil
}
func (m *MemSetupStore) Update(st *SetupState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		m.state = st
		m.state.Initialized = true
		return nil
	}
	if st.AccessKey != "" {
		m.state.AccessKey = st.AccessKey
	}
	if st.SecretKey != "" {
		m.state.SecretKey = st.SecretKey
	}
	if st.DefaultPolicy != "" {
		m.state.DefaultPolicy = st.DefaultPolicy
	}
	if st.Region != "" {
		m.state.Region = st.Region
	}
	if st.DatanodePaths != "" {
		m.state.DatanodePaths = st.DatanodePaths
	}
	if st.DBDriver != "" {
		m.state.DBDriver = st.DBDriver
	}
	if st.DBDSN != "" {
		m.state.DBDSN = st.DBDSN
	}
	if st.Gateway {
		m.state.Gateway = st.Gateway
	}
	return nil
}
func (m *MemSetupStore) ValidateSession(token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return false
	}
	return token == m.state.AdminToken
}
func (m *MemSetupStore) CreateSession(token, username string, _ interface{}) error { return nil }

var _ SetupStoreIface = (*MemSetupStore)(nil)
