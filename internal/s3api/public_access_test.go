// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const testPublicPolicy = `{"Version":"2012-10-17","Statement":[{"Sid":"PublicRead","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::pub/*"]}]}`

func newPublicTestStore(t *testing.T) ObjectStore {
	t.Helper()
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	for _, b := range []string{"pub", "priv"} {
		if err := st.CreateBucket(b); err != nil {
			t.Fatalf("CreateBucket(%s): %v", b, err)
		}
	}
	if err := st.PutBucketPolicy("pub", []byte(testPublicPolicy)); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	return st
}

func TestNormalizeDomain(t *testing.T) {
	good := map[string]string{
		"Example.COM":          "example.com",
		" files.example.com. ": "files.example.com",
		"*.example.com":        "example.com",
		"localhost":            "localhost",
		"a-b.example.co.jp":    "a-b.example.co.jp",
	}
	for in, want := range good {
		got, err := NormalizeDomain(in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "  ", "*.", "http://example.com", "example.com:8080", "example.com/path",
		"-a.example.com", "a-.example.com", "a..example.com", "exa mple.com",
		strings.Repeat("a", 64) + ".example.com",
		strings.Repeat("a.", 130) + "com",
	}
	for _, in := range bad {
		if _, err := NormalizeDomain(in); !errors.Is(err, ErrInvalidDomain) {
			t.Errorf("NormalizeDomain(%q) error = %v; want ErrInvalidDomain", in, err)
		}
	}
}

func TestNormalizeDomainsDedupeAndLimit(t *testing.T) {
	got, err := NormalizeDomains([]string{"B.example.com", "a.example.com", "*.b.example.com", "b.example.com"})
	if err != nil {
		t.Fatalf("NormalizeDomains: %v", err)
	}
	// "*.b.example.com" normalizes to "b.example.com" and is a duplicate of the first entry.
	if !reflect.DeepEqual(got, []string{"b.example.com", "a.example.com"}) {
		t.Fatalf("got %v; want %v", got, []string{"b.example.com", "a.example.com"})
	}

	mk := func(n int) []string {
		l := make([]string, n)
		for i := range l {
			l[i] = fmt.Sprintf("d%d.example.com", i)
		}
		return l
	}
	if got, err := NormalizeDomains(mk(MaxBucketDomains)); err != nil || len(got) != MaxBucketDomains {
		t.Fatalf("%d domains: len=%d err=%v; want ok", MaxBucketDomains, len(got), err)
	}
	if _, err := NormalizeDomains(mk(MaxBucketDomains + 1)); !errors.Is(err, ErrTooManyDomains) {
		t.Fatalf("%d domains: err=%v; want ErrTooManyDomains", MaxBucketDomains+1, err)
	}
	// Duplicates do not count against the limit.
	dup := append(mk(MaxBucketDomains), "D0.example.com")
	if got, err := NormalizeDomains(dup); err != nil || len(got) != MaxBucketDomains {
		t.Fatalf("with duplicate: len=%d err=%v; want ok", len(got), err)
	}
}

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		host    string
		domains []string
		want    bool
	}{
		{"anything.test", nil, true},
		{"", nil, true},
		{"example.com", []string{"example.com"}, true},
		{"EXAMPLE.com:9000", []string{"example.com"}, true},
		{"example.com.", []string{"example.com"}, true},
		{"cdn.example.com", []string{"example.com"}, true},
		{"a.b.example.com:443", []string{"example.com"}, true},
		{"notexample.com", []string{"example.com"}, false},
		{"example.com.evil.net", []string{"example.com"}, false},
		{"example.org", []string{"example.com"}, false},
		{"", []string{"example.com"}, false},
		{"[::1]:9000", []string{"example.com"}, false},
		{"files.example.org", []string{"example.com", "example.org"}, true},
	}
	for _, c := range cases {
		if got := HostAllowed(c.host, c.domains); got != c.want {
			t.Errorf("HostAllowed(%q, %v) = %v; want %v", c.host, c.domains, got, c.want)
		}
	}
}

func TestPolicyAllowsPublicRead(t *testing.T) {
	stmt := func(s string) string {
		return `{"Version":"2012-10-17","Statement":` + s + `}`
	}
	cases := []struct {
		name   string
		policy string
		bucket string
		key    string
		want   bool
	}{
		{"public read", testPublicPolicy, "pub", "a.txt", true},
		{"nested key", testPublicPolicy, "pub", "dir/sub/b.txt", true},
		{"other bucket", testPublicPolicy, "other", "a.txt", false},
		{"single statement object", stmt(`{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/*"}`), "pub", "a.txt", true},
		{"principal AWS star", stmt(`[{"Effect":"Allow","Principal":{"AWS":"*"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/*"}]`), "pub", "a.txt", true},
		{"action wildcard", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::pub/*"}]`), "pub", "a.txt", true},
		{"prefix scoped allows inside", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/public/*"}]`), "pub", "public/x", true},
		{"prefix scoped refuses outside", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/public/*"}]`), "pub", "private/x", false},
		{"put only", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:PutObject","Resource":"arn:aws:s3:::pub/*"}]`), "pub", "a.txt", false},
		{"specific principal", stmt(`[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::123456789012:root"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/*"}]`), "pub", "a.txt", false},
		{"allow with condition is ignored", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/*","Condition":{"IpAddress":{"aws:SourceIp":"10.0.0.0/8"}}}]`), "pub", "a.txt", false},
		{"deny wins", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/*"},{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/secret/*"}]`), "pub", "secret/k", false},
		{"deny elsewhere keeps allow", stmt(`[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/*"},{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::pub/secret/*"}]`), "pub", "open/k", true},
		{"invalid json", `{`, "pub", "a.txt", false},
		{"no statement", `{"Version":"2012-10-17"}`, "pub", "a.txt", false},
		{"empty", ``, "pub", "a.txt", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PolicyAllowsPublicRead([]byte(c.policy), c.bucket, c.key); got != c.want {
				t.Fatalf("got %v; want %v", got, c.want)
			}
		})
	}
}

func TestAnonymousReadAllowed(t *testing.T) {
	st := newPublicTestStore(t)
	cases := []struct {
		name   string
		method string
		target string
		auth   bool
		want   bool
	}{
		{"GET public object", http.MethodGet, "/pub/a.txt", false, true},
		{"HEAD public object", http.MethodHead, "/pub/dir/a.txt", false, true},
		{"response override query", http.MethodGet, "/pub/a.txt?response-content-type=text/plain", false, true},
		{"PUT is refused", http.MethodPut, "/pub/a.txt", false, false},
		{"DELETE is refused", http.MethodDelete, "/pub/a.txt", false, false},
		{"POST is refused", http.MethodPost, "/pub/a.txt", false, false},
		{"private bucket", http.MethodGet, "/priv/a.txt", false, false},
		{"missing bucket", http.MethodGet, "/nope/a.txt", false, false},
		{"bucket listing", http.MethodGet, "/pub/", false, false},
		{"bucket without slash", http.MethodGet, "/pub", false, false},
		{"root", http.MethodGet, "/", false, false},
		{"?acl is refused", http.MethodGet, "/pub/a.txt?acl", false, false},
		{"?tagging is refused", http.MethodGet, "/pub/a.txt?tagging", false, false},
		{"?uploadId is refused", http.MethodGet, "/pub/a.txt?uploadId=1", false, false},
		{"presigned query", http.MethodGet, "/pub/a.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=x", false, false},
		{"signed request goes through SigV4", http.MethodGet, "/pub/a.txt", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.target, nil)
			if c.auth {
				r.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=x")
			}
			if got := AnonymousReadAllowed(st, r); got != c.want {
				t.Fatalf("got %v; want %v", got, c.want)
			}
		})
	}
}

func TestBucketDomainsRoundTrip(t *testing.T) {
	st := newPublicTestStore(t)

	got, err := GetBucketDomains(st, "pub")
	if err != nil || len(got) != 0 {
		t.Fatalf("default: %v, %v; want empty", got, err)
	}

	norm, err := SetBucketDomains(st, "pub", []string{"B.example.com", "a.example.com", "b.example.com"})
	if err != nil {
		t.Fatalf("SetBucketDomains: %v", err)
	}
	want := []string{"b.example.com", "a.example.com"}
	if !reflect.DeepEqual(norm, want) {
		t.Fatalf("Set returned %v; want %v", norm, want)
	}
	// Set invalidates the cache, so the new list is visible immediately.
	if got, err = GetBucketDomains(st, "pub"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Get after Set = %v, %v; want %v", got, err, want)
	}
	// The returned slice is a copy: mutating it does not change the cache.
	got[0] = "mutated.example.com"
	if again, _ := GetBucketDomains(st, "pub"); !reflect.DeepEqual(again, want) {
		t.Fatalf("cache was mutated through the returned slice: %v", again)
	}
	// Other buckets are unaffected.
	if other, _ := GetBucketDomains(st, "priv"); len(other) != 0 {
		t.Fatalf("priv domains = %v; want empty", other)
	}

	// A rejected update leaves the stored list as it was.
	if _, err = SetBucketDomains(st, "pub", []string{"bad host"}); !errors.Is(err, ErrInvalidDomain) {
		t.Fatalf("invalid: err=%v; want ErrInvalidDomain", err)
	}
	tooMany := make([]string, MaxBucketDomains+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("d%d.example.com", i)
	}
	if _, err = SetBucketDomains(st, "pub", tooMany); !errors.Is(err, ErrTooManyDomains) {
		t.Fatalf("too many: err=%v; want ErrTooManyDomains", err)
	}
	if got, _ = GetBucketDomains(st, "pub"); !reflect.DeepEqual(got, want) {
		t.Fatalf("list changed after rejected updates: %v", got)
	}

	// An empty list removes the restriction.
	if norm, err = SetBucketDomains(st, "pub", nil); err != nil || len(norm) != 0 {
		t.Fatalf("clear: %v, %v", norm, err)
	}
	if got, _ = GetBucketDomains(st, "pub"); len(got) != 0 {
		t.Fatalf("after clear: %v; want empty", got)
	}
}

func TestCheckBucketHost(t *testing.T) {
	st := newPublicTestStore(t)
	if _, err := SetBucketDomains(st, "pub", []string{"example.com"}); err != nil {
		t.Fatalf("SetBucketDomains: %v", err)
	}
	cases := []struct {
		name   string
		target string
		host   string
		want   bool
	}{
		{"exact domain", "/pub/a.txt", "example.com", true},
		{"subdomain with port", "/pub/a.txt", "cdn.example.com:9000", true},
		{"other domain", "/pub/a.txt", "evil.test", false},
		{"suffix lookalike", "/pub/a.txt", "badexample.com", false},
		{"bucket listing is checked too", "/pub/", "evil.test", false},
		{"bucket without a list accepts any host", "/priv/a.txt", "evil.test", true},
		{"no bucket in the path", "/", "evil.test", true},
		{"health probe", "/healthz", "evil.test", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, c.target, nil)
			r.Host = c.host
			got, err := CheckBucketHost(st, r)
			if err != nil || got != c.want {
				t.Fatalf("got %v, %v; want %v", got, err, c.want)
			}
		})
	}
}
