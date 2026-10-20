// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package auth manages S3 access key/secret credentials and bucket
// authorization.
package auth

import (
	"errors"
	"sync"
)

// Store holds S3 credentials in memory. In M1 it is populated from
// config; later milestones will back it with the metadata DB.
type Store struct {
	mu    sync.RWMutex
	creds map[string]string // accessKey -> secretKey
}

// NewStore returns an empty credential store.
func NewStore() *Store {
	return &Store{creds: make(map[string]string)}
}

// NewStoreWith creates a store pre-populated with the given map.
func NewStoreWith(m map[string]string) *Store {
	s := NewStore()
	for k, v := range m {
		s.creds[k] = v
	}
	return s
}

// Add inserts or replaces a credential.
func (s *Store) Add(accessKey, secretKey string) error {
	if accessKey == "" || secretKey == "" {
		return errors.New("accessKey and secretKey must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.creds[accessKey] = secretKey
	return nil
}

// Remove deletes a credential.
func (s *Store) Remove(accessKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.creds, accessKey)
}

// Secret returns the secret key for an access key, or false if unknown.
func (s *Store) Secret(accessKey string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.creds[accessKey]
	return v, ok
}

// Exists reports whether an access key is known.
func (s *Store) Exists(accessKey string) bool {
	_, ok := s.Secret(accessKey)
	return ok
}

// Count returns the number of stored credentials.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.creds)
}
