// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package users

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sylphre/MiryMesh/internal/metadata"
)

func TestRequired(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		hdr    map[string]string
		want   []Requirement
		ok     bool
	}{
		{"get object", "GET", "/bkt/key", nil, []Requirement{{"bkt", Read}}, true},
		{"head bucket", "HEAD", "/bkt", nil, []Requirement{{"bkt", Read}}, true},
		{"list objects", "GET", "/bkt?list-type=2", nil, []Requirement{{"bkt", Read}}, true},
		{"put object", "PUT", "/bkt/key", nil, []Requirement{{"bkt", Write}}, true},
		{"delete object", "DELETE", "/bkt/key", nil, []Requirement{{"bkt", Write}}, true},
		{"copy object", "PUT", "/dst/key", map[string]string{"X-Amz-Copy-Source": "/src/a%20b?versionId=1"},
			[]Requirement{{"dst", Write}, {"src", Read}}, true},
		{"delete objects", "POST", "/bkt?delete", nil, []Requirement{{"bkt", Write}}, true},
		{"multipart init", "POST", "/bkt/key?uploads", nil, []Requirement{{"bkt", Write}}, true},
		{"select", "POST", "/bkt/key?select&select-type=2", nil, []Requirement{{"bkt", Read}}, true},
		{"list buckets", "GET", "/", nil, nil, true},
		{"create bucket", "PUT", "/bkt", nil, nil, false},
		{"delete bucket", "DELETE", "/bkt", nil, nil, false},
		{"bucket policy", "PUT", "/bkt?policy", nil, nil, false},
		{"service put", "PUT", "/", nil, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.target, nil)
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			got, ok := Required(r)
			if ok != c.ok || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Required() = %v, %v; want %v, %v", got, ok, c.want, c.ok)
			}
		})
	}
	if _, ok := Required(httptest.NewRequest(http.MethodPatch, "/bkt/key", nil)); ok {
		t.Fatal("PATCH must not be allowed")
	}
}

func TestManagerLifecycle(t *testing.T) {
	m := NewManager()
	u, secret, err := m.Create("alice", map[string]Permission{"photos": ReadWrite, "docs": Read})
	if err != nil {
		t.Fatal(err)
	}
	if len(u.AccessKey) != 20 || len(secret) != 40 {
		t.Fatalf("unexpected key sizes: %d / %d", len(u.AccessKey), len(secret))
	}
	if got, ok := m.Secret(u.AccessKey); !ok || got != secret {
		t.Fatal("secret lookup failed")
	}
	if _, _, err = m.Create("alice", nil); err != ErrExists {
		t.Fatalf("duplicate create: %v", err)
	}

	read := func(b string) []Requirement { return []Requirement{{b, Read}} }
	write := func(b string) []Requirement { return []Requirement{{b, Write}} }
	if !m.Authorize(u.AccessKey, read("docs")) || m.Authorize(u.AccessKey, write("docs")) {
		t.Fatal("docs must be read only")
	}
	if !m.Authorize(u.AccessKey, write("photos")) || !m.Authorize(u.AccessKey, read("photos")) {
		t.Fatal("photos must be readwrite")
	}
	if m.Authorize(u.AccessKey, read("other")) || m.CanSee(u.AccessKey, "other") {
		t.Fatal("other bucket must be denied")
	}
	if !m.CanSee(u.AccessKey, "docs") {
		t.Fatal("docs must be visible")
	}

	if _, err = m.SetPermissions("alice", map[string]Permission{AllBuckets: Write}); err != nil {
		t.Fatal(err)
	}
	if !m.Authorize(u.AccessKey, write("anything")) || m.Authorize(u.AccessKey, read("anything")) {
		t.Fatal("wildcard must grant write only")
	}

	if _, err = m.SetEnabled("alice", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Secret(u.AccessKey); ok || m.Authorize(u.AccessKey, write("x")) {
		t.Fatal("disabled user must be rejected")
	}
	if _, err = m.SetEnabled("alice", true); err != nil {
		t.Fatal(err)
	}

	u2, secret2, err := m.RotateKey("alice")
	if err != nil {
		t.Fatal(err)
	}
	if u2.AccessKey == u.AccessKey || secret2 == secret {
		t.Fatal("rotation must change the key pair")
	}
	if _, ok := m.Secret(u.AccessKey); ok {
		t.Fatal("old key must stop working")
	}

	if _, err := m.SetPermissions("alice", map[string]Permission{"photos": Read}); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveBucket("photos"); err != nil {
		t.Fatal(err)
	}
	if m.CanSee(u2.AccessKey, "photos") {
		t.Fatal("grants must be removed together with the bucket")
	}

	if err := m.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Secret(u2.AccessKey); ok || m.Count() != 0 {
		t.Fatal("deleted user must be gone")
	}
	if err := m.Delete("alice"); err != ErrNotFound {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestManagerValidation(t *testing.T) {
	m := NewManager()
	for _, name := range []string{"", "a b", "名前", "a/b", string(make([]byte, 65))} {
		if _, _, err := m.Create(name, nil); !IsInvalid(err) {
			t.Errorf("name %q: want validation error, got %v", name, err)
		}
	}
	if _, _, err := m.Create("bob", map[string]Permission{"b": "admin"}); !IsInvalid(err) {
		t.Errorf("bad permission: %v", err)
	}
	if _, _, err := m.Create("bob", map[string]Permission{"a/b": Read}); !IsInvalid(err) {
		t.Errorf("bad bucket: %v", err)
	}
}

func TestManagerPersistsToDB(t *testing.T) {
	db, err := metadata.OpenDB("sqlite", "file:"+filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	kr, err := metadata.NewKeyring("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}

	m := NewDBManager(db, "sqlite", kr)
	if err = m.Load(); err != nil {
		t.Fatal(err)
	}
	u, secret, err := m.Create("alice", map[string]Permission{"photos": ReadWrite})
	if err != nil {
		t.Fatal(err)
	}

	var enc string
	if err := db.QueryRow(`SELECT secret_key_enc FROM users WHERE name = 'alice'`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || enc == secret {
		t.Fatal("secret key must be stored sealed")
	}

	m2 := NewDBManager(db, "sqlite", kr)
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	if got, ok := m2.Secret(u.AccessKey); !ok || got != secret {
		t.Fatal("secret must survive a reload")
	}
	got, ok := m2.Get("alice")
	if !ok || !reflect.DeepEqual(got.Permissions, []Grant{{Bucket: "photos", Permission: ReadWrite}}) {
		t.Fatalf("permissions after reload: %+v", got.Permissions)
	}

	if err := m2.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	m3 := NewDBManager(db, "sqlite", kr)
	if err := m3.Load(); err != nil {
		t.Fatal(err)
	}
	if m3.Count() != 0 {
		t.Fatal("delete must persist")
	}
}
