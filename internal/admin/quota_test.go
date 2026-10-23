// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/replication"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

type quotaResp struct {
	Bucket    string `json:"bucket"`
	MaxBytes  int64  `json:"max_bytes"`
	UsedBytes *int64 `json:"used_bytes"`
}

func quotaMux(t *testing.T, bucket string) (*Handler, *http.ServeMux) {
	t.Helper()
	h := newAdminHandler(t)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	if err := h.storage.CreateBucket(bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	return h, mux
}

func quotaDo(mux http.Handler, method, bucket string, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/api/admin/buckets/"+bucket+"/quota", rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeQuota(t *testing.T, rr *httptest.ResponseRecorder) quotaResp {
	t.Helper()
	var q quotaResp
	if err := json.Unmarshal(rr.Body.Bytes(), &q); err != nil {
		t.Fatalf("decode quota response %q: %v", rr.Body.String(), err)
	}
	return q
}

func TestAdminQuotaGetPutRoundTrip(t *testing.T) {
	_, mux := quotaMux(t, "quota-rt")

	// Default: unlimited, nothing stored.
	rr := quotaDo(mux, http.MethodGet, "quota-rt", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q", got)
	}
	q := decodeQuota(t, rr)
	if q.Bucket != "quota-rt" || q.MaxBytes != 0 || q.UsedBytes == nil || *q.UsedBytes != 0 {
		t.Fatalf("unexpected default quota: %+v", q)
	}

	// Set a limit: the response echoes it and GET returns the same value.
	rr = quotaDo(mux, http.MethodPut, "quota-rt", `{"max_bytes": 1048576}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	if q = decodeQuota(t, rr); q.MaxBytes != 1048576 {
		t.Fatalf("PUT response max_bytes=%d", q.MaxBytes)
	}
	rr = quotaDo(mux, http.MethodGet, "quota-rt", "")
	if q = decodeQuota(t, rr); rr.Code != http.StatusOK || q.MaxBytes != 1048576 {
		t.Fatalf("GET after PUT: status=%d quota=%+v", rr.Code, q)
	}

	// 0 removes the limit.
	rr = quotaDo(mux, http.MethodPut, "quota-rt", `{"max_bytes": 0}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT 0 status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = quotaDo(mux, http.MethodGet, "quota-rt", "")
	if q = decodeQuota(t, rr); q.MaxBytes != 0 {
		t.Fatalf("quota after removal=%d", q.MaxBytes)
	}
}

func TestAdminQuotaRejectsBadInput(t *testing.T) {
	_, mux := quotaMux(t, "quota-bad")

	cases := []struct {
		name   string
		method string
		bucket string
		body   string
		want   int
	}{
		{"unknown bucket GET", http.MethodGet, "no-such-bucket", "", http.StatusNotFound},
		{"unknown bucket PUT", http.MethodPut, "no-such-bucket", `{"max_bytes": 10}`, http.StatusNotFound},
		{"negative", http.MethodPut, "quota-bad", `{"max_bytes": -1}`, http.StatusBadRequest},
		{"missing field", http.MethodPut, "quota-bad", `{}`, http.StatusBadRequest},
		{"null field", http.MethodPut, "quota-bad", `{"max_bytes": null}`, http.StatusBadRequest},
		{"string value", http.MethodPut, "quota-bad", `{"max_bytes": "10"}`, http.StatusBadRequest},
		{"fractional value", http.MethodPut, "quota-bad", `{"max_bytes": 1.5}`, http.StatusBadRequest},
		{"malformed json", http.MethodPut, "quota-bad", `{"max_bytes":`, http.StatusBadRequest},
		{"empty body", http.MethodPut, "quota-bad", "", http.StatusBadRequest},
		{"POST not allowed", http.MethodPost, "quota-bad", `{"max_bytes": 10}`, http.StatusMethodNotAllowed},
		{"DELETE not allowed", http.MethodDelete, "quota-bad", "", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := quotaDo(mux, tc.method, tc.bucket, tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status=%d, want %d (body=%s)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}

	// Rejected requests must not have changed anything.
	rr := quotaDo(mux, http.MethodGet, "quota-bad", "")
	if q := decodeQuota(t, rr); q.MaxBytes != 0 {
		t.Fatalf("quota changed by rejected requests: %d", q.MaxBytes)
	}
}

func TestAdminQuotaRejectsOversizedBody(t *testing.T) {
	_, mux := quotaMux(t, "quota-big")
	body := `{"max_bytes": 10, "pad": "` + strings.Repeat("x", 8<<10) + `"}`
	rr := quotaDo(mux, http.MethodPut, "quota-big", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status=%d", rr.Code)
	}
}

func TestAdminQuotaReportsUsage(t *testing.T) {
	_, mux := quotaMux(t, "quota-use")
	upload := func(key string, n int) int {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/buckets/quota-use/object?key="+key, bytes.NewReader(bytes.Repeat([]byte("a"), n)))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := upload("a", 40); code != http.StatusCreated {
		t.Fatalf("upload status=%d", code)
	}
	rr := quotaDo(mux, http.MethodGet, "quota-use", "")
	if q := decodeQuota(t, rr); q.UsedBytes == nil || *q.UsedBytes != 40 {
		t.Fatalf("used_bytes=%v, want 40", q.UsedBytes)
	}
}

func TestAdminObjectUploadHonoursQuota(t *testing.T) {
	_, mux := quotaMux(t, "quota-up")
	if rr := quotaDo(mux, http.MethodPut, "quota-up", `{"max_bytes": 100}`); rr.Code != http.StatusOK {
		t.Fatalf("PUT quota status=%d body=%s", rr.Code, rr.Body.String())
	}
	upload := func(key string, n int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/admin/buckets/quota-up/object?key="+key, bytes.NewReader(bytes.Repeat([]byte("a"), n)))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	if rr := upload("a", 60); rr.Code != http.StatusCreated {
		t.Fatalf("upload within quota status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := upload("b", 60); rr.Code != http.StatusInsufficientStorage {
		t.Fatalf("upload over quota status=%d, want 507 (body=%s)", rr.Code, rr.Body.String())
	}
	// Overwriting only counts the size difference.
	if rr := upload("a", 100); rr.Code != http.StatusCreated {
		t.Fatalf("overwrite up to the limit status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := upload("a", 101); rr.Code != http.StatusInsufficientStorage {
		t.Fatalf("overwrite past the limit status=%d, want 507", rr.Code)
	}

	// Lifting the limit allows the upload again.
	if rr := quotaDo(mux, http.MethodPut, "quota-up", `{"max_bytes": 0}`); rr.Code != http.StatusOK {
		t.Fatalf("PUT 0 status=%d", rr.Code)
	}
	if rr := upload("b", 60); rr.Code != http.StatusCreated {
		t.Fatalf("upload after lifting the limit status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// quotaAuthStore is a minimal initialized setup store so that checkAuth is enforced.
type quotaAuthStore struct{}

func (quotaAuthStore) IsInitialized() bool       { return true }
func (quotaAuthStore) Get() *SetupState          { return &SetupState{} }
func (quotaAuthStore) Save(*SetupState) error    { return nil }
func (quotaAuthStore) UpdatePolicy(string) error { return nil }

func TestAdminQuotaRequiresAuth(t *testing.T) {
	st, err := s3api.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	reg := datanode.NewRegistry(0, 0)
	resolver, _ := placement.NewResolver(reg, placement.New(1), "single", nil)
	h := NewWithSetup(reg, st, resolver, replication.NewStore(), "s3cret-token", quotaAuthStore{}, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	if err := st.CreateBucket("quota-auth"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	do := func(method, token, body string) int {
		req := httptest.NewRequest(method, "/api/admin/buckets/quota-auth/quota", strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Admin-Token", token)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := do(http.MethodGet, "", ""); code != http.StatusUnauthorized {
		t.Fatalf("GET without token status=%d, want 401", code)
	}
	if code := do(http.MethodPut, "wrong", `{"max_bytes": 10}`); code != http.StatusUnauthorized {
		t.Fatalf("PUT with wrong token status=%d, want 401", code)
	}
	if limit, _ := s3api.GetBucketQuota(st, "quota-auth"); limit != 0 {
		t.Fatalf("unauthenticated PUT changed the quota: %d", limit)
	}
	if code := do(http.MethodPut, "s3cret-token", `{"max_bytes": 10}`); code != http.StatusOK {
		t.Fatalf("PUT with valid token status=%d, want 200", code)
	}
	if code := do(http.MethodGet, "s3cret-token", ""); code != http.StatusOK {
		t.Fatalf("GET with valid token status=%d, want 200", code)
	}
}
