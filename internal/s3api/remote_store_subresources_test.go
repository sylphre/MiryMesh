// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/sylphre/MiryMesh/internal/auth"
	"github.com/sylphre/MiryMesh/internal/metadata"
	"github.com/sylphre/MiryMesh/internal/sigv4"
)

// newRemoteIntegrityEnv builds the same signed-request environment as
// newIntegrityEnv, but the Router sits on a RemoteStore that talks to real
// DataNode HTTP servers. This is the gateway-mode path.
func newRemoteIntegrityEnv(t *testing.T) (*integrityEnv, *RemoteStore) {
	t.Helper()
	c := newMNCluster(t, []string{"n1", "n2"}, 1)
	rs, res := c.remote("single", c.master)
	rt := NewRouterWithPlacement(rs, "us-east-1", res)
	as := auth.NewStoreWith(map[string]string{"TESTKEY": "testsecret12345"})
	sg := &sigv4.Signer{AccessKey: "TESTKEY", SecretKey: "testsecret12345", Region: "us-east-1", Service: "s3"}
	e := &integrityEnv{t: t, rt: rt, as: as, sg: sg, bucket: "intbkt"}
	if rr := e.do("PUT", "/"+e.bucket, nil, nil); rr.Code != http.StatusOK {
		t.Fatalf("CreateBucket status=%d body=%s", rr.Code, rr.Body.String())
	}
	return e, rs
}

// SSE-C plus a FULL_OBJECT CRC32 checksum needs a stored document at create time
// and for every part. Before RemoteStore implemented SubresourceStore, the Router
// fell back to noSubresources and this upload failed.
func TestRemoteStoreRouterMultipartSSECChecksum(t *testing.T) {
	e, _ := newRemoteIntegrityEnv(t)
	k := newTestKey(11)
	parts := multipartTestParts()
	whole := append(append([]byte{}, parts[0]...), parts[1]...)
	crc := ckAlgoByName("CRC32")

	createHdr := merge(k.headers(), map[string]string{"X-Amz-Checksum-Algorithm": "CRC32", "X-Amz-Checksum-Type": ckTypeFullObject})
	id, prs := e.multipartFlow("mp-full", createHdr, k.headers(), parts)
	for i, pr := range prs {
		if got, want := pr.Header().Get(crc.header), ckSumBase64(crc, parts[i]); got != want {
			t.Fatalf("part %d CRC32=%q want %q", i+1, got, want)
		}
	}
	rr := e.complete("mp-full", id, prs, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Complete status=%d body=%s", rr.Code, rr.Body.String())
	}
	wantSum := ckSumBase64(crc, whole)
	if !strings.Contains(rr.Body.String(), "<ChecksumCRC32>"+wantSum+"</ChecksumCRC32>") {
		t.Fatalf("Complete response lacks the FULL_OBJECT CRC32 %s: %s", wantSum, rr.Body.String())
	}

	get := e.do("GET", e.objPath("mp-full"), nil, k.headers())
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%.200s", get.Code, get.Body.String())
	}
	if !bytes.Equal(get.Body.Bytes(), whole) {
		t.Fatalf("decrypted object differs: got %d bytes, want %d", get.Body.Len(), len(whole))
	}
}

func TestRemoteStoreRouterMultipartCompositeSHA256(t *testing.T) {
	e, _ := newRemoteIntegrityEnv(t)
	parts := multipartTestParts()
	whole := append(append([]byte{}, parts[0]...), parts[1]...)

	id, prs := e.multipartFlow("mp-comp", map[string]string{"X-Amz-Checksum-Algorithm": "SHA256"}, nil, parts)
	rr := e.complete("mp-comp", id, prs, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("Complete status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<ChecksumSHA256>") {
		t.Fatalf("Complete response lacks ChecksumSHA256: %s", rr.Body.String())
	}
	get := e.do("GET", e.objPath("mp-comp"), nil, nil)
	if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), whole) {
		t.Fatalf("GET status=%d, %d bytes, want %d bytes", get.Code, get.Body.Len(), len(whole))
	}
}

// Documents written through one RemoteStore must be readable by another one that
// uses the same database (for example after a Master restart).
func TestRemoteStoreSubresourcesPersistInSQLite(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err = metadata.Migrate(db, "sqlite"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := newMNCluster(t, []string{"n1"}, 1)
	rs1, _ := c.remote("single", c.master)
	rs1.SetDB(db, "sqlite")

	if err = rs1.PutSubresource("b", "k", subTagging, "<Tagging/>"); err != nil {
		t.Fatalf("PutSubresource object: %v", err)
	}
	if err = rs1.PutSubresource("b", "", subCORS, "<CORS/>"); err != nil {
		t.Fatalf("PutSubresource bucket: %v", err)
	}
	if err = rs1.PutSubresource("b", "k", subTagging, "<Tagging v=\"2\"/>"); err != nil {
		t.Fatalf("PutSubresource replace: %v", err)
	}

	rs2, _ := c.remote("single", c.master)
	rs2.SetDB(db, "sqlite")
	if v, ok, gerr := rs2.GetSubresource("b", "k", subTagging); gerr != nil || !ok || v != "<Tagging v=\"2\"/>" {
		t.Fatalf("second store: got %q ok=%v err=%v", v, ok, gerr)
	}

	if err = rs2.PurgeObjectSubresources("b", "k"); err != nil {
		t.Fatalf("PurgeObjectSubresources: %v", err)
	}
	if _, ok, _ := rs1.GetSubresource("b", "k", subTagging); ok {
		t.Fatal("object document survived PurgeObjectSubresources")
	}
	if _, ok, _ := rs1.GetSubresource("b", "", subCORS); !ok {
		t.Fatal("bucket document was removed by PurgeObjectSubresources")
	}

	if err = rs1.PurgeBucketSubresources("b"); err != nil {
		t.Fatalf("PurgeBucketSubresources: %v", err)
	}
	if _, ok, _ := rs2.GetSubresource("b", "", subCORS); ok {
		t.Fatal("bucket document survived PurgeBucketSubresources")
	}
	if err = rs1.DeleteSubresource("b", "missing", subACL); err != nil {
		t.Fatalf("DeleteSubresource of a missing document: %v", err)
	}
}

// Without a database the documents live in memory, as Storage does.
func TestRemoteStoreSubresourcesInMemory(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if _, ok, err := rs.GetSubresource("b", "k", subACL); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	if err := rs.PutSubresource("b", "k", subACL, "<ACL/>"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := rs.GetSubresource("b", "k", subACL); err != nil || !ok || v != "<ACL/>" {
		t.Fatalf("got %q ok=%v err=%v", v, ok, err)
	}
	if err := rs.PurgeBucketSubresources("b"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := rs.GetSubresource("b", "k", subACL); ok {
		t.Fatal("document survived PurgeBucketSubresources")
	}
}
