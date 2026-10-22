// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newP2Router(t *testing.T) *Router {
	t.Helper()
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	if err := st.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if _, err := st.PutObject("bkt", "obj.txt", "text/plain", nil, strings.NewReader("hello")); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	return NewRouter(st, "us-east-1")
}

func p2Do(rt *Router, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	rt.ServeHTTP(rr, req)
	return rr
}

func p2Expect(t *testing.T, rr *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status=%d want %d body=%s", rr.Code, status, rr.Body.String())
	}
	if want != "" && !strings.Contains(rr.Body.String(), want) {
		t.Fatalf("body missing %q: %s", want, rr.Body.String())
	}
}

func p2ExpectAbsent(t *testing.T, rr *httptest.ResponseRecorder, unwanted string) {
	t.Helper()
	if strings.Contains(rr.Body.String(), unwanted) {
		t.Fatalf("body must not contain %q: %s", unwanted, rr.Body.String())
	}
}

// Request shapes whose answer does not depend on stored state.
func TestP2Subresources(t *testing.T) {
	rt := newP2Router(t)
	cases := []struct {
		name   string
		method string
		target string
		hdr    map[string]string
		body   string
		status int
		want   string // substring expected in the body
	}{
		{"versioning get", http.MethodGet, "/bkt?versioning", nil, "", http.StatusOK, "VersioningConfiguration"},
		{"versioning put", http.MethodPut, "/bkt?versioning", nil, "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"versioning missing bucket", http.MethodGet, "/nobucket?versioning", nil, "", http.StatusNotFound, "<Code>NoSuchBucket</Code>"},
		{"versioning bad method", http.MethodPost, "/bkt?versioning", nil, "", http.StatusMethodNotAllowed, "<Code>MethodNotAllowed</Code>"},
		{"versioning on object", http.MethodGet, "/bkt/obj.txt?versioning", nil, "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"bucket acl get default", http.MethodGet, "/bkt?acl", nil, "", http.StatusOK, "FULL_CONTROL"},
		{"object acl get default", http.MethodGet, "/bkt/obj.txt?acl", nil, "", http.StatusOK, "FULL_CONTROL"},
		{"object acl missing key", http.MethodGet, "/bkt/missing.txt?acl", nil, "", http.StatusNotFound, "<Code>NoSuchKey</Code>"},
		{"acl put without anything", http.MethodPut, "/bkt?acl", nil, "", http.StatusBadRequest, "<Code>MissingSecurityHeader</Code>"},
		{"acl put unknown canned", http.MethodPut, "/bkt?acl", map[string]string{"X-Amz-Acl": "bogus"}, "", http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"acl put canned and grant header", http.MethodPut, "/bkt?acl",
			map[string]string{"X-Amz-Acl": "private", "X-Amz-Grant-Read": `id="u1"`}, "", http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"acl put malformed body", http.MethodPut, "/bkt?acl", nil, "<nope", http.StatusBadRequest, "<Code>MalformedACLError</Code>"},
		{"acl put log-delivery-write on object", http.MethodPut, "/bkt/obj.txt?acl",
			map[string]string{"X-Amz-Acl": "log-delivery-write"}, "", http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"bucket tagging get", http.MethodGet, "/bkt?tagging", nil, "", http.StatusNotFound, "<Code>NoSuchTagSet</Code>"},
		{"object tagging get", http.MethodGet, "/bkt/obj.txt?tagging", nil, "", http.StatusOK, "TagSet"},
		{"object tagging missing key", http.MethodGet, "/bkt/missing.txt?tagging", nil, "", http.StatusNotFound, "<Code>NoSuchKey</Code>"},
		{"tagging delete", http.MethodDelete, "/bkt?tagging", nil, "", http.StatusNoContent, ""},
		{"tagging put empty body", http.MethodPut, "/bkt?tagging", nil, "", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"cors get", http.MethodGet, "/bkt?cors", nil, "", http.StatusNotFound, "<Code>NoSuchCORSConfiguration</Code>"},
		{"cors delete", http.MethodDelete, "/bkt?cors", nil, "", http.StatusNoContent, ""},
		{"cors put empty body", http.MethodPut, "/bkt?cors", nil, "", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"cors on object", http.MethodGet, "/bkt/obj.txt?cors", nil, "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"lifecycle get", http.MethodGet, "/bkt?lifecycle", nil, "", http.StatusNotFound, "<Code>NoSuchLifecycleConfiguration</Code>"},
		{"lifecycle delete", http.MethodDelete, "/bkt?lifecycle", nil, "", http.StatusNoContent, ""},
		{"lifecycle put empty body", http.MethodPut, "/bkt?lifecycle", nil, "", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"analytics stays 501", http.MethodGet, "/bkt?analytics", nil, "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"select stays 501", http.MethodPost, "/bkt/obj.txt?select&select-type=2", nil, "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"root subresource", http.MethodGet, "/?acl", nil, "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, tc.method, tc.target, tc.body, tc.hdr), tc.status, tc.want)
		})
	}
}

// Normal routing must be unaffected when no P2 subresource is present.
func TestP2DoesNotShadowObjectGet(t *testing.T) {
	rt := newP2Router(t)
	rr := p2Do(rt, http.MethodGet, "/bkt/obj.txt", "", nil)
	if rr.Code != http.StatusOK || rr.Body.String() != "hello" {
		t.Fatalf("GetObject status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestP2ACLCannedRoundTrip(t *testing.T) {
	rt := newP2Router(t)
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?acl", "", map[string]string{"X-Amz-Acl": "public-read"}), http.StatusOK, "")
	rr := p2Do(rt, http.MethodGet, "/bkt?acl", "", nil)
	p2Expect(t, rr, http.StatusOK, "http://acs.amazonaws.com/groups/global/AllUsers")
	p2Expect(t, rr, http.StatusOK, "<Permission>READ</Permission>")
	p2Expect(t, rr, http.StatusOK, "FULL_CONTROL")

	// Back to private: the AllUsers grant must be gone.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?acl", "", map[string]string{"X-Amz-Acl": "private"}), http.StatusOK, "")
	rr = p2Do(rt, http.MethodGet, "/bkt?acl", "", nil)
	p2Expect(t, rr, http.StatusOK, "FULL_CONTROL")
	p2ExpectAbsent(t, rr, "AllUsers")
}

func TestP2ACLGrantHeaderAndBody(t *testing.T) {
	rt := newP2Router(t)
	hdr := map[string]string{"X-Amz-Grant-Read": `uri="http://acs.amazonaws.com/groups/global/AuthenticatedUsers"`}
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?acl", "", hdr), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt?acl", "", nil), http.StatusOK, "AuthenticatedUsers")

	body := `<AccessControlPolicy><Owner><ID>mirymesh</ID></Owner><AccessControlList>` +
		`<Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group">` +
		`<URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee><Permission>READ</Permission></Grant>` +
		`</AccessControlList></AccessControlPolicy>`
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?acl", body, nil), http.StatusOK, "")
	rr := p2Do(rt, http.MethodGet, "/bkt/obj.txt?acl", "", nil)
	p2Expect(t, rr, http.StatusOK, "AllUsers")
	p2ExpectAbsent(t, rr, "AuthenticatedUsers")

	badGroup := strings.ReplaceAll(body, "global/AllUsers", "global/Nobody")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?acl", badGroup, nil), http.StatusBadRequest, "<Code>InvalidArgument</Code>")
}

func TestP2TaggingRoundTrip(t *testing.T) {
	rt := newP2Router(t)
	tags := `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag><Tag><Key>team</Key><Value>core</Value></Tag></TagSet></Tagging>`

	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", tags, nil), http.StatusOK, "")
	rr := p2Do(rt, http.MethodGet, "/bkt/obj.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "<Key>env</Key><Value>prod</Value>")
	p2Expect(t, rr, http.StatusOK, "<Key>team</Key><Value>core</Value>")

	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt?tagging", "", nil), http.StatusNoContent, "")
	rr = p2Do(rt, http.MethodGet, "/bkt/obj.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "TagSet")
	p2ExpectAbsent(t, rr, "<Tag>")

	// Bucket tagging: PUT answers 204, an unset bucket answers NoSuchTagSet.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?tagging", tags, nil), http.StatusNoContent, "")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt?tagging", "", nil), http.StatusOK, "<Key>env</Key>")
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt?tagging", "", nil), http.StatusNoContent, "")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt?tagging", "", nil), http.StatusNotFound, "<Code>NoSuchTagSet</Code>")
}

func TestP2TaggingValidation(t *testing.T) {
	rt := newP2Router(t)
	wrap := func(tags string) string { return "<Tagging><TagSet>" + tags + "</TagSet></Tagging>" }
	tag := func(k, v string) string { return "<Tag><Key>" + k + "</Key><Value>" + v + "</Value></Tag>" }

	var eleven strings.Builder
	for i := 0; i < 11; i++ {
		eleven.WriteString(tag(fmt.Sprintf("k%d", i), "v"))
	}
	cases := []struct {
		name, body, code string
	}{
		{"duplicate key", wrap(tag("a", "1") + tag("a", "2")), "InvalidTag"},
		{"reserved aws: prefix", wrap(tag("aws:x", "1")), "InvalidTag"},
		{"empty key", wrap(tag("", "1")), "InvalidTag"},
		{"too many for an object", wrap(eleven.String()), "InvalidTag"},
		{"illegal character", wrap(tag("a", "<b>")), "MalformedXML"},
		{"not xml", "nope", "MalformedXML"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", tc.body, nil)
			p2Expect(t, rr, http.StatusBadRequest, "<Code>"+tc.code+"</Code>")
		})
	}
	// 11 tags are fine on a bucket (limit 50).
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?tagging", wrap(eleven.String()), nil), http.StatusNoContent, "")
}

func TestP2PutObjectTaggingHeaderAndOverwrite(t *testing.T) {
	rt := newP2Router(t)
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/tagged.txt", "payload", map[string]string{"X-Amz-Tagging": "b=2&a=1"}), http.StatusOK, "")
	rr := p2Do(rt, http.MethodGet, "/bkt/tagged.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "<Key>a</Key><Value>1</Value>")
	p2Expect(t, rr, http.StatusOK, "<Key>b</Key><Value>2</Value>")

	// Give it a non-default ACL as well, then overwrite without any header.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/tagged.txt?acl", "", map[string]string{"X-Amz-Acl": "public-read"}), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/tagged.txt", "payload2", nil), http.StatusOK, "")

	rr = p2Do(rt, http.MethodGet, "/bkt/tagged.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "TagSet")
	p2ExpectAbsent(t, rr, "<Tag>")
	rr = p2Do(rt, http.MethodGet, "/bkt/tagged.txt?acl", "", nil)
	p2Expect(t, rr, http.StatusOK, "FULL_CONTROL")
	p2ExpectAbsent(t, rr, "AllUsers")

	// A malformed x-amz-tagging header must reject the upload.
	rr = p2Do(rt, http.MethodPut, "/bkt/bad.txt", "x", map[string]string{"X-Amz-Tagging": "aws:k=v"})
	p2Expect(t, rr, http.StatusBadRequest, "<Code>InvalidTag</Code>")
}

const p2TestCORS = `<CORSConfiguration><CORSRule>` +
	`<ID>app</ID>` +
	`<AllowedOrigin>https://app.example.com</AllowedOrigin>` +
	`<AllowedMethod>GET</AllowedMethod><AllowedMethod>PUT</AllowedMethod>` +
	`<AllowedHeader>*</AllowedHeader>` +
	`<ExposeHeader>ETag</ExposeHeader>` +
	`<MaxAgeSeconds>600</MaxAgeSeconds>` +
	`</CORSRule></CORSConfiguration>`

func TestP2CORSConfigRoundTrip(t *testing.T) {
	rt := newP2Router(t)
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?cors", p2TestCORS, nil), http.StatusOK, "")
	rr := p2Do(rt, http.MethodGet, "/bkt?cors", "", nil)
	p2Expect(t, rr, http.StatusOK, "<AllowedOrigin>https://app.example.com</AllowedOrigin>")
	p2Expect(t, rr, http.StatusOK, "<MaxAgeSeconds>600</MaxAgeSeconds>")

	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt?cors", "", nil), http.StatusNoContent, "")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt?cors", "", nil), http.StatusNotFound, "<Code>NoSuchCORSConfiguration</Code>")
}

func TestP2CORSValidation(t *testing.T) {
	rt := newP2Router(t)
	rule := func(inner string) string {
		return "<CORSConfiguration><CORSRule>" + inner + "</CORSRule></CORSConfiguration>"
	}
	cases := []struct {
		name, body, code string
	}{
		{"unsupported method", rule(`<AllowedOrigin>*</AllowedOrigin><AllowedMethod>PATCH</AllowedMethod>`), "InvalidRequest"},
		{"two wildcards", rule(`<AllowedOrigin>*.*.example.com</AllowedOrigin><AllowedMethod>GET</AllowedMethod>`), "InvalidRequest"},
		{"missing origin", rule(`<AllowedMethod>GET</AllowedMethod>`), "MalformedXML"},
		{"missing method", rule(`<AllowedOrigin>*</AllowedOrigin>`), "MalformedXML"},
		{"no rules", "<CORSConfiguration></CORSConfiguration>", "MalformedXML"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?cors", tc.body, nil), http.StatusBadRequest, "<Code>"+tc.code+"</Code>")
		})
	}
}

func TestP2CORSEnforcement(t *testing.T) {
	rt := newP2Router(t)
	if err := rt.store.CreateBucket("nocors"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?cors", p2TestCORS, nil), http.StatusOK, "")

	preflight := func(target, origin, method, headers string) *httptest.ResponseRecorder {
		h := map[string]string{"Origin": origin, "Access-Control-Request-Method": method}
		if headers != "" {
			h["Access-Control-Request-Headers"] = headers
		}
		return p2Do(rt, http.MethodOptions, target, "", h)
	}

	rr := preflight("/bkt/obj.txt", "https://app.example.com", "PUT", "content-type")
	p2Expect(t, rr, http.StatusOK, "")
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin=%q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "GET, PUT" {
		t.Fatalf("Allow-Methods=%q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "content-type" {
		t.Fatalf("Allow-Headers=%q", got)
	}
	if got := rr.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("Max-Age=%q", got)
	}

	p2Expect(t, preflight("/bkt/obj.txt", "https://evil.example", "PUT", ""), http.StatusForbidden, "<Code>AccessForbidden</Code>")
	p2Expect(t, preflight("/bkt/obj.txt", "https://app.example.com", "DELETE", ""), http.StatusForbidden, "<Code>AccessForbidden</Code>")
	p2Expect(t, preflight("/nocors/x", "https://app.example.com", "GET", ""), http.StatusForbidden, "<Code>AccessForbidden</Code>")

	// Simple cross-origin request: the object is served and annotated.
	rr = p2Do(rt, http.MethodGet, "/bkt/obj.txt", "", map[string]string{"Origin": "https://app.example.com"})
	if rr.Code != http.StatusOK || rr.Body.String() != "hello" {
		t.Fatalf("GET status=%d body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin=%q", got)
	}
	if got := rr.Header().Get("Access-Control-Expose-Headers"); got != "ETag" {
		t.Fatalf("Expose-Headers=%q", got)
	}

	// An origin that is not allowed still gets the object, but no CORS headers.
	rr = p2Do(rt, http.MethodGet, "/bkt/obj.txt", "", map[string]string{"Origin": "https://evil.example"})
	if rr.Code != http.StatusOK || rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("status=%d allow-origin=%q", rr.Code, rr.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestP2LifecycleRoundTripAndValidation(t *testing.T) {
	rt := newP2Router(t)
	valid := `<LifecycleConfiguration><Rule><ID>r1</ID><Status>Enabled</Status>` +
		`<Filter><Prefix>logs/</Prefix></Filter><Expiration><Days>30</Days></Expiration></Rule></LifecycleConfiguration>`
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?lifecycle", valid, nil), http.StatusOK, "")
	rr := p2Do(rt, http.MethodGet, "/bkt?lifecycle", "", nil)
	p2Expect(t, rr, http.StatusOK, "<ID>r1</ID>")
	p2Expect(t, rr, http.StatusOK, "<Days>30</Days>")

	cases := []struct {
		name, body, code string
	}{
		{"bad status", strings.ReplaceAll(valid, "Enabled", "Maybe"), "MalformedXML"},
		{"duplicate id", strings.Replace(valid, "</LifecycleConfiguration>", strings.TrimSuffix(strings.TrimPrefix(valid, "<LifecycleConfiguration>"), "</LifecycleConfiguration>")+"</LifecycleConfiguration>", 1), "InvalidArgument"},
		{"rule without action", `<LifecycleConfiguration><Rule><ID>x</ID><Status>Enabled</Status></Rule></LifecycleConfiguration>`, "MalformedXML"},
		{"zero days", strings.ReplaceAll(valid, "<Days>30</Days>", "<Days>0</Days>"), "InvalidArgument"},
		{"bad date", strings.ReplaceAll(valid, "<Days>30</Days>", "<Date>yesterday</Date>"), "InvalidArgument"},
		{"no rules", "<LifecycleConfiguration></LifecycleConfiguration>", "MalformedXML"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?lifecycle", tc.body, nil), http.StatusBadRequest, "<Code>"+tc.code+"</Code>")
		})
	}

	// A rejected PUT must not have replaced the stored configuration.
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt?lifecycle", "", nil), http.StatusOK, "<ID>r1</ID>")
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt?lifecycle", "", nil), http.StatusNoContent, "")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt?lifecycle", "", nil), http.StatusNotFound, "<Code>NoSuchLifecycleConfiguration</Code>")
}

func TestP2SQLHelpers(t *testing.T) {
	if got := p2Rebind("postgres", "a=? AND b=? AND c=?"); got != "a=$1 AND b=$2 AND c=$3" {
		t.Fatalf("postgres rebind=%q", got)
	}
	if got := p2Rebind("mysql", "a=? AND b=?"); got != "a=? AND b=?" {
		t.Fatalf("mysql rebind=%q", got)
	}
	for driver, want := range map[string]string{
		"postgres": "postgres", "postgresql": "postgres", "sqlite": "sqlite", "sqlite3": "sqlite", "mysql": "mysql", "mariadb": "mysql", "": "mysql",
	} {
		if got := p2Dialect(driver); got != want {
			t.Fatalf("p2Dialect(%q)=%q want %q", driver, got, want)
		}
	}
	for dialect, want := range map[string]string{
		"postgres": "ON CONFLICT (bucket, object_key, kind) DO UPDATE",
		"sqlite":   "ON CONFLICT (bucket, object_key, kind) DO UPDATE",
		"mysql":    "ON DUPLICATE KEY UPDATE",
	} {
		if got := p2UpsertSQL(dialect); !strings.Contains(got, want) {
			t.Fatalf("p2UpsertSQL(%q)=%q missing %q", dialect, got, want)
		}
	}
}
