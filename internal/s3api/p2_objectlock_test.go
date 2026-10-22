// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"testing"
	"time"
)

const p2BypassHdr = "x-amz-bypass-governance-retention"

func p2RetentionBody(mode string, d time.Duration) string {
	return `<Retention><Mode>` + mode + `</Mode><RetainUntilDate>` +
		time.Now().UTC().Add(d).Format(time.RFC3339) + `</RetainUntilDate></Retention>`
}

func p2LegalHoldBody(status string) string {
	return `<LegalHold><Status>` + status + `</Status></LegalHold>`
}

func p2PutObj(t *testing.T, rt *Router, key string) {
	t.Helper()
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/"+key, "data", nil), http.StatusOK, "")
}

func TestP2LegalHoldLifecycle(t *testing.T) {
	rt := newP2Router(t)
	const target = "/bkt/obj.txt?legal-hold"

	p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusNotFound, "<Code>NoSuchObjectLockConfiguration</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, `<?xml version="1.0" encoding="UTF-8"?>`+p2LegalHoldBody("ON"), nil), http.StatusOK, "")

	got := p2Do(rt, http.MethodGet, target, "", nil)
	p2Expect(t, got, http.StatusOK, "<Status>ON</Status>")
	p2Expect(t, got, http.StatusOK, `<LegalHold xmlns="`+p2XMLNS+`">`)

	// Protected from delete and overwrite, even with the governance bypass header.
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", map[string]string{p2BypassHdr: "true"}), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt", "overwrite", nil), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt", "", nil), http.StatusOK, "hello")

	p2Expect(t, p2Do(rt, http.MethodPut, target, p2LegalHoldBody("OFF"), nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusOK, "<Status>OFF</Status>")
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusNoContent, "")

	// The stored hold goes away with the object.
	p2PutObj(t, rt, "obj.txt")
	p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusNotFound, "<Code>NoSuchObjectLockConfiguration</Code>")
}

func TestP2LegalHoldValidation(t *testing.T) {
	rt := newP2Router(t)
	cases := []struct {
		name   string
		method string
		target string
		body   string
		status int
		want   string
	}{
		{"bad status", http.MethodPut, "/bkt/obj.txt?legal-hold", p2LegalHoldBody("MAYBE"), http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"empty body", http.MethodPut, "/bkt/obj.txt?legal-hold", "", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"not xml", http.MethodPut, "/bkt/obj.txt?legal-hold", "ON", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"wrong root", http.MethodPut, "/bkt/obj.txt?legal-hold", `<Retention><Status>ON</Status></Retention>`, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"trailing element", http.MethodPut, "/bkt/obj.txt?legal-hold", p2LegalHoldBody("ON") + "<Extra/>", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"missing object", http.MethodGet, "/bkt/missing.txt?legal-hold", "", http.StatusNotFound, ""},
		{"missing bucket", http.MethodGet, "/nobucket/obj.txt?legal-hold", "", http.StatusNotFound, "<Code>NoSuchBucket</Code>"},
		{"bad method", http.MethodDelete, "/bkt/obj.txt?legal-hold", "", http.StatusMethodNotAllowed, "<Code>MethodNotAllowed</Code>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, c.method, c.target, c.body, nil), c.status, c.want)
		})
	}
	// Nothing invalid may have been stored.
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt?legal-hold", "", nil), http.StatusNotFound, "<Code>NoSuchObjectLockConfiguration</Code>")
}

func TestP2RetentionGovernance(t *testing.T) {
	rt := newP2Router(t)
	const target = "/bkt/obj.txt?retention"
	bypass := map[string]string{p2BypassHdr: "true"}

	p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusNotFound, "<Code>NoSuchObjectLockConfiguration</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, `<?xml version="1.0" encoding="UTF-8"?>`+p2RetentionBody("GOVERNANCE", time.Hour), nil), http.StatusOK, "")

	got := p2Do(rt, http.MethodGet, target, "", nil)
	p2Expect(t, got, http.StatusOK, "<Mode>GOVERNANCE</Mode>")
	p2Expect(t, got, http.StatusOK, "<RetainUntilDate>")

	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt", "overwrite", nil), http.StatusForbidden, "<Code>AccessDenied</Code>")

	// Shortening needs the bypass header; extending and upgrading do not.
	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("GOVERNANCE", 30*time.Minute), nil), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("GOVERNANCE", 30*time.Minute), bypass), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("GOVERNANCE", 2*time.Hour), nil), http.StatusOK, "")

	// Governance retention is bypassable on delete.
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", bypass), http.StatusNoContent, "")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt", "", nil), http.StatusNotFound, "")
}

func TestP2RetentionClear(t *testing.T) {
	rt := newP2Router(t)
	const target = "/bkt/obj.txt?retention"
	bypass := map[string]string{p2BypassHdr: "true"}

	// Clearing when nothing is stored is a no-op.
	p2Expect(t, p2Do(rt, http.MethodPut, target, `<Retention></Retention>`, nil), http.StatusOK, "")

	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("GOVERNANCE", time.Hour), nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, target, `<Retention></Retention>`, nil), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, `<Retention></Retention>`, bypass), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusNotFound, "<Code>NoSuchObjectLockConfiguration</Code>")
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusNoContent, "")
}

func TestP2RetentionCompliance(t *testing.T) {
	rt := newP2Router(t)
	const target = "/bkt/obj.txt?retention"
	bypass := map[string]string{p2BypassHdr: "true"}

	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("COMPLIANCE", time.Hour), nil), http.StatusOK, "")

	// The bypass header never helps against COMPLIANCE.
	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", bypass), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt", "overwrite", bypass), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("COMPLIANCE", 30*time.Minute), bypass), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("GOVERNANCE", 2*time.Hour), bypass), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodPut, target, `<Retention></Retention>`, bypass), http.StatusForbidden, "<Code>AccessDenied</Code>")

	// Extending is the only change allowed.
	p2Expect(t, p2Do(rt, http.MethodPut, target, p2RetentionBody("COMPLIANCE", 2*time.Hour), nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusOK, "<Mode>COMPLIANCE</Mode>")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt", "", nil), http.StatusOK, "hello")
}

func TestP2RetentionValidation(t *testing.T) {
	rt := newP2Router(t)
	cases := []struct {
		name   string
		method string
		target string
		body   string
		status int
		want   string
	}{
		{"past date", http.MethodPut, "/bkt/obj.txt?retention", p2RetentionBody("GOVERNANCE", -time.Hour), http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"bad mode", http.MethodPut, "/bkt/obj.txt?retention", p2RetentionBody("FOREVER", time.Hour), http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"bad date", http.MethodPut, "/bkt/obj.txt?retention", `<Retention><Mode>GOVERNANCE</Mode><RetainUntilDate>tomorrow</RetainUntilDate></Retention>`, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"mode without date", http.MethodPut, "/bkt/obj.txt?retention", `<Retention><Mode>GOVERNANCE</Mode></Retention>`, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"empty body", http.MethodPut, "/bkt/obj.txt?retention", "", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"wrong root", http.MethodPut, "/bkt/obj.txt?retention", p2LegalHoldBody("ON"), http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"trailing element", http.MethodPut, "/bkt/obj.txt?retention", p2RetentionBody("GOVERNANCE", time.Hour) + "<Extra/>", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"missing object", http.MethodPut, "/bkt/missing.txt?retention", p2RetentionBody("GOVERNANCE", time.Hour), http.StatusNotFound, ""},
		{"bad method", http.MethodDelete, "/bkt/obj.txt?retention", "", http.StatusMethodNotAllowed, "<Code>MethodNotAllowed</Code>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, c.method, c.target, c.body, nil), c.status, c.want)
		})
	}
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt?retention", "", nil), http.StatusNotFound, "<Code>NoSuchObjectLockConfiguration</Code>")
}

func TestP2ObjectLockBlocksOverwritePaths(t *testing.T) {
	rt := newP2Router(t)
	p2PutObj(t, rt, "dst.txt")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/dst.txt?legal-hold", p2LegalHoldBody("ON"), nil), http.StatusOK, "")

	// CopyObject onto a protected destination.
	copyHdr := map[string]string{"x-amz-copy-source": "/bkt/obj.txt"}
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/dst.txt", "", copyHdr), http.StatusForbidden, "<Code>AccessDenied</Code>")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/dst.txt", "", nil), http.StatusOK, "data")

	// Copying FROM a protected object is fine.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/copy.txt", "", map[string]string{"x-amz-copy-source": "/bkt/dst.txt"}), http.StatusOK, "")

	// Multi-object delete reports the protected key as an error and deletes the rest.
	p2PutObj(t, rt, "free.txt")
	del := `<Delete><Object><Key>dst.txt</Key></Object><Object><Key>free.txt</Key></Object></Delete>`
	rr := p2Do(rt, http.MethodPost, "/bkt?delete", del, nil)
	p2Expect(t, rr, http.StatusOK, "<Code>AccessDenied</Code>")
	p2Expect(t, rr, http.StatusOK, "<Key>dst.txt</Key>")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/dst.txt", "", nil), http.StatusOK, "data")
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/free.txt", "", nil), http.StatusNotFound, "")
}
