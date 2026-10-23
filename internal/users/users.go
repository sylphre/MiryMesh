// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

// Package users manages S3 users. Every user owns a generated access key /
// secret key pair (like MinIO service accounts) and a set of per-bucket
// permissions: read, write or readwrite. The special bucket "*" applies to
// every bucket.
//
// Secret keys are sealed with the master key keyring before they reach the DB.
// Lookups on the request path are served from memory.
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

// Permission is what a user may do on a bucket.
type Permission string

const (
	// Read allows GET / HEAD / List and SelectObjectContent.
	Read Permission = "read"
	// Write allows PUT / DELETE / multipart uploads on objects.
	Write Permission = "write"
	// ReadWrite allows both.
	ReadWrite Permission = "readwrite"
)

// AllBuckets is the wildcard bucket name.
const AllBuckets = "*"

// Valid reports whether p is a known permission.
func (p Permission) Valid() bool {
	switch p {
	case Read, Write, ReadWrite:
		return true
	}
	return false
}

// allows reports whether p covers need (Read or Write).
func (p Permission) allows(need Permission) bool {
	return p == ReadWrite || p == need
}

var (
	ErrNotFound          = errors.New("user not found")
	ErrExists            = errors.New("user already exists")
	ErrInvalidName       = errors.New("invalid user name: use 1-64 characters from A-Z a-z 0-9 . _ -")
	ErrInvalidBucket     = errors.New("invalid bucket name")
	ErrInvalidPermission = errors.New("invalid permission: use read, write or readwrite")
)

// IsInvalid reports whether err is a validation error caused by the caller's input.
func IsInvalid(err error) bool {
	return errors.Is(err, ErrInvalidName) || errors.Is(err, ErrInvalidBucket) || errors.Is(err, ErrInvalidPermission)
}

// Grant is one bucket permission of a user.
type Grant struct {
	Bucket     string     `json:"bucket"`
	Permission Permission `json:"permission"`
}

// User is the public view of a user. It never carries the secret key.
type User struct {
	Name        string    `json:"name"`
	AccessKey   string    `json:"access_key"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	Permissions []Grant   `json:"permissions"`
}

type record struct {
	name      string
	accessKey string
	secret    string
	enabled   bool
	createdAt time.Time
	perms     map[string]Permission
}

func (r *record) user() User {
	u := User{Name: r.name, AccessKey: r.accessKey, Enabled: r.enabled, CreatedAt: r.createdAt, Permissions: make([]Grant, 0, len(r.perms))}
	for b, p := range r.perms {
		u.Permissions = append(u.Permissions, Grant{Bucket: b, Permission: p})
	}
	sort.Slice(u.Permissions, func(i, j int) bool { return u.Permissions[i].Bucket < u.Permissions[j].Bucket })
	return u
}

func (r *record) allows(q Requirement) bool {
	if p, ok := r.perms[q.Bucket]; ok && p.allows(q.Need) {
		return true
	}
	if p, ok := r.perms[AllBuckets]; ok && p.allows(q.Need) {
		return true
	}
	return false
}

// Manager keeps the users in memory and, when a DB is attached, writes every
// change through to it.
type Manager struct {
	mu     sync.RWMutex
	db     *sql.DB
	pg     bool
	kr     *metadata.Keyring
	byName map[string]*record
	byKey  map[string]*record
}

// NewManager returns a memory-only manager (no DB configured, data is lost on restart).
func NewManager() *Manager {
	return &Manager{byName: map[string]*record{}, byKey: map[string]*record{}}
}

// NewDBManager returns a manager backed by db. Call Load before use.
func NewDBManager(db *sql.DB, driver string, kr *metadata.Keyring) *Manager {
	m := NewManager()
	m.db = db
	d := strings.ToLower(driver)
	m.pg = d == "postgres" || d == "postgresql"
	m.kr = kr
	return m
}

// rebind converts ? placeholders to $n for PostgreSQL.
func (m *Manager) rebind(q string) string {
	if !m.pg {
		return q
	}
	var b strings.Builder
	n := 0
	for _, c := range q {
		if c == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (m *Manager) seal(secret string) (string, error) {
	if m.db == nil {
		return secret, nil
	}
	if m.kr == nil {
		return "", errors.New("users: keyring is not configured")
	}
	return m.kr.Encrypt(secret)
}

// keyID returns the key id of an "mk1:<id>:<data>" ciphertext, or "" for the legacy format.
func keyID(enc string) string {
	if strings.HasPrefix(enc, "mk1:") {
		if parts := strings.SplitN(enc, ":", 3); len(parts) == 3 {
			return parts[1]
		}
	}
	return ""
}

func parseTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t.UTC()
	case []byte:
		return parseTimeString(string(t))
	case string:
		return parseTimeString(t)
	}
	return time.Time{}
}

func parseTimeString(s string) time.Time {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Load reads all users from the DB. Secrets sealed with a retired master key are
// re-sealed with the current one, so removing the old key after a rotation is safe.
func (m *Manager) Load() error {
	if m == nil || m.db == nil {
		return nil
	}
	if m.kr == nil {
		return errors.New("users: keyring is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	byName := map[string]*record{}
	byKey := map[string]*record{}
	type reseal struct{ name, enc string }
	var todo []reseal

	err := func() error {
		rows, err := m.db.Query(`SELECT name, access_key, secret_key_enc, enabled, created_at FROM users`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name, ak, enc string
			var enabled bool
			var created any
			if err := rows.Scan(&name, &ak, &enc, &enabled, &created); err != nil {
				return err
			}
			rec := &record{name: name, accessKey: ak, enabled: enabled, createdAt: parseTime(created), perms: map[string]Permission{}}
			if secret, derr := m.kr.Decrypt(enc); derr != nil {
				slog.Warn("users: cannot decrypt secret key, rotate the key of this user", "user", name, "err", derr)
			} else {
				rec.secret = secret
				if fresh, eerr := m.kr.Encrypt(secret); eerr == nil && keyID(fresh) != keyID(enc) {
					todo = append(todo, reseal{name: name, enc: fresh})
				}
			}
			byName[name] = rec
			byKey[ak] = rec
		}
		return rows.Err()
	}()
	if err != nil {
		return fmt.Errorf("load users: %w", err)
	}

	err = func() error {
		rows, qerr := m.db.Query(`SELECT user_name, bucket, permission FROM user_bucket_perms`)
		if qerr != nil {
			return qerr
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name, bucket, perm string
			if serr := rows.Scan(&name, &bucket, &perm); serr != nil {
				return serr
			}
			if rec, ok := byName[name]; ok && Permission(perm).Valid() {
				rec.perms[bucket] = Permission(perm)
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return fmt.Errorf("load user permissions: %w", err)
	}

	for _, t := range todo {
		if _, err := m.db.Exec(m.rebind(`UPDATE users SET secret_key_enc = ? WHERE name = ?`), t.enc, t.name); err != nil {
			slog.Warn("users: cannot re-seal secret key with the current master key", "user", t.name, "err", err)
		}
	}
	m.byName = byName
	m.byKey = byKey
	return nil
}

// Count returns the number of users.
func (m *Manager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byName)
}

// Secret returns the secret key for an enabled user's access key (SigV4 lookup).
func (m *Manager) Secret(accessKey string) (string, bool) {
	if m == nil {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.byKey[accessKey]
	if !ok || !r.enabled || r.secret == "" {
		return "", false
	}
	return r.secret, true
}

// Authorize reports whether the user behind accessKey satisfies every requirement.
func (m *Manager) Authorize(accessKey string, reqs []Requirement) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.byKey[accessKey]
	if !ok || !r.enabled {
		return false
	}
	for _, q := range reqs {
		if !r.allows(q) {
			return false
		}
	}
	return true
}

// CanSee reports whether the user has any permission on bucket (used to filter ListBuckets).
func (m *Manager) CanSee(accessKey, bucket string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.byKey[accessKey]
	if !ok || !r.enabled {
		return false
	}
	if _, has := r.perms[bucket]; has {
		return true
	}
	_, ok = r.perms[AllBuckets]
	return ok
}

// IsUserKey reports whether accessKey belongs to a user (as opposed to the root credentials).
func (m *Manager) IsUserKey(accessKey string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.byKey[accessKey]
	return ok
}

// List returns all users sorted by name.
func (m *Manager) List() []User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]User, 0, len(m.byName))
	for _, r := range m.byName {
		out = append(out, r.user())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one user.
func (m *Manager) Get(name string) (User, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.byName[name]
	if !ok {
		return User{}, false
	}
	return r.user(), true
}

// Create adds a user with a freshly generated key pair. The secret key is returned
// once and cannot be read back later.
func (m *Manager) Create(name string, perms map[string]Permission) (User, string, error) {
	if err := validName(name); err != nil {
		return User{}, "", err
	}
	if err := validatePerms(perms); err != nil {
		return User{}, "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byName[name]; ok {
		return User{}, "", ErrExists
	}
	ak, secret, err := m.newKeyPair()
	if err != nil {
		return User{}, "", err
	}
	enc, err := m.seal(secret)
	if err != nil {
		return User{}, "", err
	}
	if err := m.inTx(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.rebind(`INSERT INTO users (name, access_key, secret_key_enc, enabled) VALUES (?, ?, ?, ?)`), name, ak, enc, true); err != nil {
			return err
		}
		return m.insertPerms(ctx, tx, name, perms)
	}); err != nil {
		return User{}, "", fmt.Errorf("create user: %w", err)
	}
	rec := &record{name: name, accessKey: ak, secret: secret, enabled: true, createdAt: time.Now().UTC(), perms: clonePerms(perms)}
	m.byName[name] = rec
	m.byKey[ak] = rec
	return rec.user(), secret, nil
}

// Delete removes a user and its permissions. Its key stops working immediately.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byName[name]
	if !ok {
		return ErrNotFound
	}
	if err := m.inTx(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.rebind(`DELETE FROM user_bucket_perms WHERE user_name = ?`), name); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, m.rebind(`DELETE FROM users WHERE name = ?`), name)
		return err
	}); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	delete(m.byName, name)
	delete(m.byKey, rec.accessKey)
	return nil
}

// SetEnabled enables or disables a user without touching its key or permissions.
func (m *Manager) SetEnabled(name string, enabled bool) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byName[name]
	if !ok {
		return User{}, ErrNotFound
	}
	if err := m.exec(`UPDATE users SET enabled = ? WHERE name = ?`, enabled, name); err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	rec.enabled = enabled
	return rec.user(), nil
}

// SetPermissions replaces all permissions of a user.
func (m *Manager) SetPermissions(name string, perms map[string]Permission) (User, error) {
	if err := validatePerms(perms); err != nil {
		return User{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byName[name]
	if !ok {
		return User{}, ErrNotFound
	}
	if err := m.inTx(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.rebind(`DELETE FROM user_bucket_perms WHERE user_name = ?`), name); err != nil {
			return err
		}
		return m.insertPerms(ctx, tx, name, perms)
	}); err != nil {
		return User{}, fmt.Errorf("update permissions: %w", err)
	}
	rec.perms = clonePerms(perms)
	return rec.user(), nil
}

// RotateKey issues a new access key / secret key pair. The old pair stops working immediately.
func (m *Manager) RotateKey(name string) (User, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byName[name]
	if !ok {
		return User{}, "", ErrNotFound
	}
	ak, secret, err := m.newKeyPair()
	if err != nil {
		return User{}, "", err
	}
	enc, err := m.seal(secret)
	if err != nil {
		return User{}, "", err
	}
	if err := m.exec(`UPDATE users SET access_key = ?, secret_key_enc = ? WHERE name = ?`, ak, enc, name); err != nil {
		return User{}, "", fmt.Errorf("rotate key: %w", err)
	}
	delete(m.byKey, rec.accessKey)
	rec.accessKey = ak
	rec.secret = secret
	m.byKey[ak] = rec
	return rec.user(), secret, nil
}

// RemoveBucket drops every permission on bucket. It is called when a bucket is deleted so a
// bucket re-created later under the same name does not inherit old grants.
func (m *Manager) RemoveBucket(bucket string) error {
	if m == nil || bucket == "" || bucket == AllBuckets {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.byName {
		delete(r.perms, bucket)
	}
	if err := m.exec(`DELETE FROM user_bucket_perms WHERE bucket = ?`, bucket); err != nil {
		return fmt.Errorf("remove bucket permissions: %w", err)
	}
	return nil
}

// newKeyPair generates an unused access key (20 chars, "MM" prefix) and a 40 char secret.
// The caller holds m.mu.
func (m *Manager) newKeyPair() (string, string, error) {
	for i := 0; i < 8; i++ {
		body, err := randomString(accessAlphabet, 18)
		if err != nil {
			return "", "", err
		}
		ak := "MM" + body
		if _, taken := m.byKey[ak]; taken {
			continue
		}
		secret, err := randomString(secretAlphabet, 40)
		if err != nil {
			return "", "", err
		}
		return ak, secret, nil
	}
	return "", "", errors.New("users: cannot generate a unique access key")
}

func (m *Manager) inTx(fn func(ctx context.Context, tx *sql.Tx) error) error {
	if m.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (m *Manager) exec(query string, args ...any) error {
	if m.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := m.db.ExecContext(ctx, m.rebind(query), args...)
	return err
}

func (m *Manager) insertPerms(ctx context.Context, tx *sql.Tx, name string, perms map[string]Permission) error {
	buckets := make([]string, 0, len(perms))
	for b := range perms {
		buckets = append(buckets, b)
	}
	sort.Strings(buckets)
	for _, b := range buckets {
		if _, err := tx.ExecContext(ctx, m.rebind(`INSERT INTO user_bucket_perms (user_name, bucket, permission) VALUES (?, ?, ?)`), name, b, string(perms[b])); err != nil {
			return err
		}
	}
	return nil
}

func clonePerms(in map[string]Permission) map[string]Permission {
	out := make(map[string]Permission, len(in))
	for b, p := range in {
		out[b] = p
	}
	return out
}

func validName(name string) error {
	if len(name) < 1 || len(name) > 64 {
		return ErrInvalidName
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return ErrInvalidName
		}
	}
	return nil
}

func validBucket(b string) error {
	if b == AllBuckets {
		return nil
	}
	if len(b) == 0 || len(b) > 255 {
		return ErrInvalidBucket
	}
	for i := 0; i < len(b); i++ {
		if c := b[i]; c <= 0x20 || c >= 0x7f || c == '/' {
			return ErrInvalidBucket
		}
	}
	return nil
}

func validatePerms(perms map[string]Permission) error {
	for b, p := range perms {
		if err := validBucket(b); err != nil {
			return err
		}
		if !p.Valid() {
			return ErrInvalidPermission
		}
	}
	return nil
}

const (
	accessAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

func randomString(alphabet string, n int) (string, error) {
	limit := big.NewInt(int64(len(alphabet)))
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

// Requirement is one bucket permission a request needs.
type Requirement struct {
	Bucket string
	Need   Permission
}

// Required maps an S3 request to the permissions a non-root user needs.
//
// ok=false means the request is reserved for the root credentials: creating or
// deleting buckets and changing bucket level settings (?policy, ?acl, ?cors,
// ?lifecycle ...). ok=true with no requirements is ListBuckets, which the router
// filters per user.
func Required(r *http.Request) ([]Requirement, bool) {
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	q := r.URL.Query()

	if bucket == "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			return nil, true
		}
		return nil, false
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return []Requirement{{Bucket: bucket, Need: Read}}, true
	case http.MethodPost:
		switch {
		case key == "" && q.Has("delete"): // DeleteObjects
			return []Requirement{{Bucket: bucket, Need: Write}}, true
		case key == "":
			return nil, false
		case q.Has("select"): // SelectObjectContent
			return []Requirement{{Bucket: bucket, Need: Read}}, true
		default: // CreateMultipartUpload / CompleteMultipartUpload
			return []Requirement{{Bucket: bucket, Need: Write}}, true
		}
	case http.MethodPut, http.MethodDelete:
		if key == "" {
			return nil, false
		}
		reqs := []Requirement{{Bucket: bucket, Need: Write}}
		if r.Method == http.MethodPut {
			if src := r.Header.Get("X-Amz-Copy-Source"); src != "" {
				sb := copySourceBucket(src)
				if sb == "" {
					return nil, false
				}
				reqs = append(reqs, Requirement{Bucket: sb, Need: Read})
			}
		}
		return reqs, true
	}
	return nil, false
}

// copySourceBucket extracts the source bucket of an x-amz-copy-source header.
func copySourceBucket(h string) string {
	if i := strings.IndexByte(h, '?'); i >= 0 {
		h = h[:i]
	}
	if u, err := url.PathUnescape(h); err == nil {
		h = u
	}
	b, _, _ := strings.Cut(strings.TrimPrefix(h, "/"), "/")
	return b
}
