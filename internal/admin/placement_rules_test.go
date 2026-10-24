// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/metadata"
	"github.com/sylphre/MiryMesh/internal/placement"
	"github.com/sylphre/MiryMesh/internal/replication"
	"github.com/sylphre/MiryMesh/internal/s3api"
)

// testAdminToken authenticates the test requests once a database-backed setup store is attached.
const testAdminToken = "test-admin-token"

// placementHandler builds a handler over two nodes: n1 with two disks, n2 with one.
func placementHandler(t *testing.T, setup SetupStoreIface) *Handler {
	t.Helper()
	st, err := s3api.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	reg := datanode.NewRegistry(0, 0)
	for _, id := range []string{"n1", "n2"} {
		if err = reg.RegisterNode(id, id+":9001"); err != nil {
			t.Fatalf("RegisterNode %s: %v", id, err)
		}
	}
	for _, d := range []struct{ node, path string }{{"n1", "/mnt/a"}, {"n1", "/mnt/b"}, {"n2", "/mnt/c"}} {
		if err = reg.UpsertDisk(d.node, d.path, 1000, 100, 0); err != nil {
			t.Fatalf("UpsertDisk %s:%s: %v", d.node, d.path, err)
		}
	}
	resolver, err := placement.NewResolver(reg, placement.New(1), "single", nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return NewWithSetup(reg, st, resolver, replication.NewStore(), testAdminToken, setup, nil)
}

func placementDo(h *Handler, method, bucket, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/api/admin/buckets/"+bucket+"/placement", rdr)
	req.Header.Set("X-Admin-Token", testAdminToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodePlacement(t *testing.T, rr *httptest.ResponseRecorder) placementDoc {
	t.Helper()
	var doc placementDoc
	if err := json.NewDecoder(rr.Body).Decode(&doc); err != nil {
		t.Fatalf("decode placement: %v", err)
	}
	return doc
}

func mustBucket(t *testing.T, h *Handler, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := h.storage.CreateBucket(n); err != nil {
			t.Fatalf("CreateBucket %s: %v", n, err)
		}
	}
}

func TestPlacementDefaultAndSameNode(t *testing.T) {
	h := placementHandler(t, nil)
	mustBucket(t, h, "docs")

	rr := placementDo(h, http.MethodGet, "docs", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", rr.Code, rr.Body.String())
	}
	if doc := decodePlacement(t, rr); doc.Mode != placement.ModeSpread || len(doc.Disks) != 0 || len(doc.ReservedDisks) != 0 {
		t.Fatalf("default doc = %+v", doc)
	}

	dual, err := placement.ParsePolicy("dual")
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if err = h.resolver.SetBucketPolicy("docs", dual); err != nil {
		t.Fatalf("SetBucketPolicy: %v", err)
	}
	body := `{"mode":"same_node","disks":[{"node_id":"n1","path":"/mnt/a/"},{"node_id":"n1","path":"/mnt/b"}]}`
	rr = placementDo(h, http.MethodPut, "docs", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}
	doc := decodePlacement(t, rr)
	if doc.Mode != placement.ModeSameNode || len(doc.Disks) != 2 || doc.Disks[0].Path != "/mnt/a" {
		t.Fatalf("saved doc = %+v", doc)
	}

	tgts, err := h.resolver.Resolve("docs", 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(tgts) != 2 || tgts[0].NodeID != "n1" || tgts[1].NodeID != "n1" || tgts[0].Path == tgts[1].Path {
		t.Fatalf("same_node targets = %+v", tgts)
	}
}

func TestPlacementReservedDiskIsExclusive(t *testing.T) {
	h := placementHandler(t, nil)
	mustBucket(t, h, "bkt-a", "bkt-b")

	rr := placementDo(h, http.MethodPut, "bkt-a", `{"mode":"spread","disks":[{"node_id":"n2","path":"/mnt/c"}],"reserved_disks":[{"node_id":"n2","path":"/mnt/c"}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT a status=%d body=%s", rr.Code, rr.Body.String())
	}
	if tg, err := h.resolver.Resolve("bkt-a", 1); err != nil || len(tg) != 1 || tg[0].Path != "/mnt/c" {
		t.Fatalf("bucket a targets = %+v, err=%v", tg, err)
	}

	// Another bucket can neither list nor reserve that disk.
	for _, body := range []string{
		`{"mode":"any","disks":[{"node_id":"n2","path":"/mnt/c"}]}`,
		`{"mode":"spread","reserved_disks":[{"node_id":"n2","path":"/mnt/c"}]}`,
	} {
		if rr := placementDo(h, http.MethodPut, "bkt-b", body); rr.Code != http.StatusConflict {
			t.Fatalf("PUT b %s: status=%d want 409 body=%s", body, rr.Code, rr.Body.String())
		}
	}
	// A rejected change leaves bucket b untouched.
	if doc := decodePlacement(t, placementDo(h, http.MethodGet, "bkt-b", "")); doc.Mode != placement.ModeSpread || len(doc.Disks) != 0 || len(doc.ReservedDisks) != 0 {
		t.Fatalf("bucket b changed after a rejected PUT: %+v", doc)
	}
	// Bucket b never writes to the reserved disk, and sees who holds it.
	for i := 0; i < 20; i++ {
		tg, err := h.resolver.Resolve("bkt-b", 1)
		if err != nil || len(tg) != 1 {
			t.Fatalf("Resolve b: %+v, %v", tg, err)
		}
		if tg[0].NodeID == "n2" && tg[0].Path == "/mnt/c" {
			t.Fatal("bucket b wrote to a disk reserved for bucket a")
		}
	}
	doc := decodePlacement(t, placementDo(h, http.MethodGet, "bkt-b", ""))
	if len(doc.ReservedByOthers) != 1 || doc.ReservedByOthers[0].Bucket != "bkt-a" {
		t.Fatalf("reserved_by_others = %+v", doc.ReservedByOthers)
	}

	// Clearing the reservation frees the disk.
	if rr := placementDo(h, http.MethodPut, "bkt-a", `{"mode":"spread"}`); rr.Code != http.StatusOK {
		t.Fatalf("clear a: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := placementDo(h, http.MethodPut, "bkt-b", `{"mode":"any","disks":[{"node_id":"n2","path":"/mnt/c"}]}`); rr.Code != http.StatusOK {
		t.Fatalf("PUT b after release: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPlacementValidation(t *testing.T) {
	h := placementHandler(t, nil)
	mustBucket(t, h, "docs")
	cases := []struct {
		name, bucket, body string
		want               int
	}{
		{"unknown mode", "docs", `{"mode":"zigzag"}`, http.StatusBadRequest},
		{"unknown disk", "docs", `{"mode":"any","disks":[{"node_id":"n9","path":"/x"}]}`, http.StatusBadRequest},
		{"disk without node", "docs", `{"mode":"any","disks":[{"path":"/mnt/a"}]}`, http.StatusBadRequest},
		{"reserved outside allow-list", "docs", `{"mode":"any","disks":[{"node_id":"n1","path":"/mnt/a"}],"reserved_disks":[{"node_id":"n1","path":"/mnt/b"}]}`, http.StatusBadRequest},
		{"bad json", "docs", `{`, http.StatusBadRequest},
		{"no such bucket", "ghost", `{"mode":"any"}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		if rr := placementDo(h, http.MethodPut, tc.bucket, tc.body); rr.Code != tc.want {
			t.Errorf("%s: status=%d want %d body=%s", tc.name, rr.Code, tc.want, rr.Body.String())
		}
	}
	if rr := placementDo(h, http.MethodDelete, "docs", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("DELETE status=%d want 405", rr.Code)
	}
}

func TestPlacementInfeasibleRuleIsRolledBack(t *testing.T) {
	h := placementHandler(t, nil)
	mustBucket(t, h, "docs")
	dual, _ := placement.ParsePolicy("dual")
	if err := h.resolver.SetBucketPolicy("docs", dual); err != nil {
		t.Fatalf("SetBucketPolicy: %v", err)
	}
	// Two replicas cannot sit on one disk.
	rr := placementDo(h, http.MethodPut, "docs", `{"mode":"same_node","disks":[{"node_id":"n1","path":"/mnt/a"}]}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rr.Code, rr.Body.String())
	}
	if doc := decodePlacement(t, placementDo(h, http.MethodGet, "docs", "")); doc.Mode != placement.ModeSpread || len(doc.Disks) != 0 {
		t.Fatalf("rule kept after a rejected PUT: %+v", doc)
	}
}

func TestPlacementPersistsAcrossRestart(t *testing.T) {
	db, err := metadata.OpenDB("sqlite", "file:"+filepath.Join(t.TempDir(), "placement.db")+"?mode=rwc")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	setup := NewSetupStoreDB(db, "sqlite", "test-master-key-32chars-1234567890")

	h1 := placementHandler(t, setup)
	mustBucket(t, h1, "docs")
	body := `{"mode":"any","disks":[{"node_id":"n1","path":"/mnt/a"},{"node_id":"n2","path":"/mnt/c"}],"reserved_disks":[{"node_id":"n2","path":"/mnt/c"}]}`
	if rr := placementDo(h1, http.MethodPut, "docs", body); rr.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", rr.Code, rr.Body.String())
	}

	// A new controller process: fresh resolver, same database.
	h2 := placementHandler(t, setup)
	mustBucket(t, h2, "docs")
	h2.LoadPlacementState()
	doc := decodePlacement(t, placementDo(h2, http.MethodGet, "docs", ""))
	if doc.Mode != placement.ModeAny || len(doc.Disks) != 2 || len(doc.ReservedDisks) != 1 || doc.ReservedDisks[0].Path != "/mnt/c" {
		t.Fatalf("restored doc = %+v", doc)
	}

	// Deleting the bucket clears the saved state too.
	h2.forgetPlacement("docs")
	h3 := placementHandler(t, setup)
	h3.LoadPlacementState()
	if len(h3.resolver.Reservations()) != 0 || !h3.resolver.RuleFor("docs").IsDefault() {
		t.Fatal("placement state survived the bucket delete")
	}
}
