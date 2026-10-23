// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// subDomains is the s3_subresources.kind of the bucket-level domain allow-list.
const subDomains = "domains"

// MaxBucketDomains is the largest number of domains one bucket may be bound to.
const MaxBucketDomains = 10

// domainsCacheTTL bounds how long a bucket's domain list is reused between requests.
const domainsCacheTTL = 2 * time.Second

// domainsCacheMax caps the cache so requests for random bucket names cannot grow it without bound.
const domainsCacheMax = 4096

// ErrDomainsUnsupported is returned when the storage backend cannot persist a domain list.
var ErrDomainsUnsupported = errSubresourceUnsupported

// ErrTooManyDomains is returned when more than MaxBucketDomains distinct domains are given.
var ErrTooManyDomains = fmt.Errorf("at most %d domains are allowed per bucket", MaxBucketDomains)

// ErrInvalidDomain is returned (wrapped) for a malformed domain name.
var ErrInvalidDomain = errors.New("invalid domain")

var domainLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type domainsDoc struct {
	Domains []string `json:"domains"`
}

type domainsEntry struct {
	domains []string
	at      time.Time
}

type domainsTracker struct {
	mu sync.Mutex
	m  map[string]domainsEntry
}

var domainsCache = &domainsTracker{m: make(map[string]domainsEntry)}

// BackingStore returns the object store the router serves from.
func (rt *Router) BackingStore() ObjectStore { return rt.store }

// NormalizeDomain lower-cases a domain and strips a leading "*." and a trailing dot.
// Schemes, ports, paths and anything that is not a valid host name are rejected.
func NormalizeDomain(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidDomain)
	}
	if len(d) > 253 {
		return "", fmt.Errorf("%w: %q is too long", ErrInvalidDomain, s)
	}
	for _, label := range strings.Split(d, ".") {
		if !domainLabelRe.MatchString(label) {
			return "", fmt.Errorf("%w: %q", ErrInvalidDomain, s)
		}
	}
	return d, nil
}

// NormalizeDomains normalizes and de-duplicates a domain list, keeping the first-seen order.
func NormalizeDomains(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		d, err := NormalizeDomain(raw)
		if err != nil {
			return nil, err
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	if len(out) > MaxBucketDomains {
		return nil, ErrTooManyDomains
	}
	return out, nil
}

// GetBucketDomains returns the domains bucket is bound to; an empty list means "any host".
func GetBucketDomains(store ObjectStore, bucket string) ([]string, error) {
	k := quotaCacheKey(store, bucket)
	domainsCache.mu.Lock()
	e, ok := domainsCache.m[k]
	domainsCache.mu.Unlock()
	if ok && time.Since(e.at) <= domainsCacheTTL {
		return append([]string(nil), e.domains...), nil
	}
	body, found, err := subStoreOf(store).GetSubresource(bucket, "", subDomains)
	if err != nil {
		return nil, err
	}
	var list []string
	if found {
		var d domainsDoc
		if uerr := json.Unmarshal([]byte(body), &d); uerr != nil {
			return nil, uerr
		}
		list = d.Domains
	}
	domainsCache.mu.Lock()
	if len(domainsCache.m) >= domainsCacheMax {
		domainsCache.m = make(map[string]domainsEntry)
	}
	domainsCache.m[k] = domainsEntry{domains: list, at: time.Now()}
	domainsCache.mu.Unlock()
	return append([]string(nil), list...), nil
}

// SetBucketDomains stores the domain list of bucket and returns the normalized list.
// An empty list removes the restriction.
func SetBucketDomains(store ObjectStore, bucket string, domains []string) ([]string, error) {
	norm, err := NormalizeDomains(domains)
	if err != nil {
		return nil, err
	}
	ss := subStoreOf(store)
	if len(norm) == 0 {
		err = ss.DeleteSubresource(bucket, "", subDomains)
	} else {
		var b []byte
		b, err = json.Marshal(domainsDoc{Domains: norm})
		if err == nil {
			err = ss.PutSubresource(bucket, "", subDomains, string(b))
		}
	}
	if err != nil {
		return nil, err
	}
	domainsCache.mu.Lock()
	delete(domainsCache.m, quotaCacheKey(store, bucket))
	domainsCache.mu.Unlock()
	return norm, nil
}

// HostAllowed reports whether host (a Host header value, port allowed) equals one of
// domains or is a subdomain of one. An empty domain list allows every host.
func HostAllowed(host string, domains []string) bool {
	if len(domains) == 0 {
		return true
	}
	h := strings.ToLower(strings.TrimSpace(host))
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(h, ".")
	for _, d := range domains {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// CheckBucketHost applies the bucket's domain allow-list to the request's Host header.
// Requests that do not address a bucket are always allowed.
func CheckBucketHost(store ObjectStore, r *http.Request) (bool, error) {
	bucket, _, _ := splitPath(r.URL.Path)
	if bucket == "" {
		return true, nil
	}
	domains, err := GetBucketDomains(store, bucket)
	if err != nil {
		return false, err
	}
	return HostAllowed(r.Host, domains), nil
}

// anonQueryAllowed lists the query parameters an anonymous GET may carry; anything
// else (?acl, ?tagging, ?uploadId, ...) needs a real permission and is refused.
var anonQueryAllowed = map[string]bool{
	"response-content-type":        true,
	"response-content-disposition": true,
	"response-cache-control":       true,
	"response-content-language":    true,
	"response-content-encoding":    true,
	"response-expires":             true,
}

// AnonymousReadAllowed reports whether r is an unsigned GET/HEAD of an object that the
// bucket policy makes publicly readable.
func AnonymousReadAllowed(store ObjectStore, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	q := r.URL.Query()
	if r.Header.Get("Authorization") != "" || q.Has("X-Amz-Algorithm") || q.Has("X-Amz-Signature") {
		return false
	}
	for k := range q {
		if !anonQueryAllowed[strings.ToLower(k)] {
			return false
		}
	}
	bucket, key, _ := splitPath(r.URL.Path)
	if bucket == "" || key == "" {
		return false
	}
	pol, err := store.GetBucketPolicy(bucket)
	if err != nil || len(pol) == 0 {
		return false
	}
	return PolicyAllowsPublicRead(pol, bucket, key)
}

type policyStatement struct {
	Effect    string          `json:"Effect"`
	Principal json.RawMessage `json:"Principal"`
	Action    json.RawMessage `json:"Action"`
	Resource  json.RawMessage `json:"Resource"`
	Condition json.RawMessage `json:"Condition"`
}

// PolicyAllowsPublicRead reports whether the policy document lets any principal run
// s3:GetObject on bucket/key. Allow statements with a Condition are ignored (they cannot
// be evaluated here); an explicit Deny for everyone always wins.
func PolicyAllowsPublicRead(policy []byte, bucket, key string) bool {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal(policy, &doc); err != nil || len(doc.Statement) == 0 {
		return false
	}
	var stmts []policyStatement
	if err := json.Unmarshal(doc.Statement, &stmts); err != nil {
		var one policyStatement
		if err2 := json.Unmarshal(doc.Statement, &one); err2 != nil {
			return false
		}
		stmts = []policyStatement{one}
	}
	target := "arn:aws:s3:::" + bucket + "/" + key
	allowed := false
	for _, s := range stmts {
		if !principalIsAny(s.Principal) || !actionMatchesGetObject(s.Action) || !resourceMatches(s.Resource, target) {
			continue
		}
		hasCond := len(s.Condition) > 0 && string(s.Condition) != "null" && string(s.Condition) != "{}"
		switch s.Effect {
		case "Deny":
			return false
		case "Allow":
			if !hasCond {
				allowed = true
			}
		}
	}
	return allowed
}

func rawStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var l []string
	if json.Unmarshal(raw, &l) == nil {
		return l
	}
	return nil
}

func principalIsAny(raw json.RawMessage) bool {
	for _, p := range rawStrings(raw) {
		if p == "*" {
			return true
		}
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		for _, p := range rawStrings(m["AWS"]) {
			if p == "*" {
				return true
			}
		}
	}
	return false
}

func actionMatchesGetObject(raw json.RawMessage) bool {
	for _, a := range rawStrings(raw) {
		if wildMatch(strings.ToLower(a), "s3:getobject") {
			return true
		}
	}
	return false
}

func resourceMatches(raw json.RawMessage, target string) bool {
	for _, res := range rawStrings(raw) {
		if wildMatch(res, target) {
			return true
		}
	}
	return false
}

// wildMatch matches s against a pattern where * matches any run and ? any single byte.
func wildMatch(p, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case pi < len(p) && (p[pi] == '?' || p[pi] == s[si]):
			pi++
			si++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
