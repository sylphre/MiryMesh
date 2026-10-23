// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type domainsResp struct {
	Bucket  string   `json:"bucket"`
	Domains []string `json:"domains"`
	Max     int      `json:"max"`
}

func domainsDo(mux http.Handler, method, bucket, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/admin/buckets/"+bucket+"/domains", strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeDomains(t *testing.T, rr *httptest.ResponseRecorder) domainsResp {
	t.Helper()
	var d domainsResp
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode domains response %q: %v", rr.Body.String(), err)
	}
	return d
}

func TestAdminDomainsGetPutRoundTrip(t *testing.T) {
	_, mux := quotaMux(t, "dom-rt")

	// Default: no restriction, and the JSON carries an empty array (not null).
	rr := domainsDo(mux, http.MethodGet, "dom-rt", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	if !strings.Contains(rr.Body.String(), `"domains":[]`) {
		t.Fatalf("default body should contain an empty array: %s", rr.Body.String())
	}
	d := decodeDomains(t, rr)
	if d.Bucket != "dom-rt" || len(d.Domains) != 0 || d.Max != 10 {
		t.Fatalf("unexpected default: %+v", d)
	}

	// PUT normalizes (case, "*." prefix, duplicates) and echoes the stored list.
	rr = domainsDo(mux, http.MethodPut, "dom-rt", `{"domains": ["Files.Example.com", "*.cdn.example.org", "files.example.com"]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	want := []string{"files.example.com", "cdn.example.org"}
	if d = decodeDomains(t, rr); !reflect.DeepEqual(d.Domains, want) {
		t.Fatalf("PUT response domains=%v; want %v", d.Domains, want)
	}
	rr = domainsDo(mux, http.MethodGet, "dom-rt", "")
	if d = decodeDomains(t, rr); rr.Code != http.StatusOK || !reflect.DeepEqual(d.Domains, want) {
		t.Fatalf("GET after PUT: status=%d domains=%v", rr.Code, d.Domains)
	}

	// An empty list removes the restriction.
	rr = domainsDo(mux, http.MethodPut, "dom-rt", `{"domains": []}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT [] status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = domainsDo(mux, http.MethodGet, "dom-rt", "")
	if d = decodeDomains(t, rr); len(d.Domains) != 0 {
		t.Fatalf("domains after clear = %v; want empty", d.Domains)
	}
}

func TestAdminDomainsValidation(t *testing.T) {
	_, mux := quotaMux(t, "dom-val")

	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("%q", fmt.Sprintf("d%d.example.com", i))
	}
	tooMany := `{"domains": [` + strings.Join(eleven, ",") + `]}`

	cases := []struct {
		name string
		body string
	}{
		{"invalid json", `{`},
		{"domains missing", `{}`},
		{"domains null", `{"domains": null}`},
		{"wrong type", `{"domains": "example.com"}`},
		{"scheme in domain", `{"domains": ["https://example.com"]}`},
		{"port in domain", `{"domains": ["example.com:8080"]}`},
		{"empty entry", `{"domains": [""]}`},
		{"eleven domains", tooMany},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := domainsDo(mux, http.MethodPut, "dom-val", c.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s; want 400", rr.Code, rr.Body.String())
			}
		})
	}

	// Rejected requests stored nothing.
	rr := domainsDo(mux, http.MethodGet, "dom-val", "")
	if d := decodeDomains(t, rr); len(d.Domains) != 0 {
		t.Fatalf("domains = %v; want empty", d.Domains)
	}

	// Exactly ten is accepted.
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = fmt.Sprintf("%q", fmt.Sprintf("d%d.example.com", i))
	}
	rr = domainsDo(mux, http.MethodPut, "dom-val", `{"domains": [`+strings.Join(ten, ",")+`]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("ten domains: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminDomainsUnknownBucketAndMethod(t *testing.T) {
	_, mux := quotaMux(t, "dom-m")

	if rr := domainsDo(mux, http.MethodGet, "no-such-bucket", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("GET unknown bucket status=%d; want 404", rr.Code)
	}
	if rr := domainsDo(mux, http.MethodPut, "no-such-bucket", `{"domains": []}`); rr.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown bucket status=%d; want 404", rr.Code)
	}
	if rr := domainsDo(mux, http.MethodDelete, "dom-m", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status=%d; want 405", rr.Code)
	}
}
