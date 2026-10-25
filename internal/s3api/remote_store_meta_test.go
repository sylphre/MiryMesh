// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"io"
	"testing"
)

// GET and HEAD through RemoteStore must report the size, ETag and modification
// time of the stored object. handleGetObject uses Size for Content-Length, so an
// empty value sends "Content-Length: 0" and the client sees a broken file.
func TestRemoteStoreGetHeadReportObjectMeta(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	payload := bytes.Repeat([]byte("0123456789abcdef"), 64*1024) // 1 MiB
	put, err := rs.PutObject("bkt", "photo.jpg", "image/jpeg", nil, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	gm, rc, err := rs.GetObject("bkt", "photo.jpg")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body differs: got %d bytes, want %d", len(body), len(payload))
	}
	if gm.Size != int64(len(payload)) {
		t.Errorf("GetObject Size = %d, want %d", gm.Size, len(payload))
	}
	if gm.ETag == "" {
		t.Errorf("GetObject ETag is empty")
	}
	if put.ETag != "" && gm.ETag != put.ETag {
		t.Errorf("GetObject ETag = %q, PutObject ETag = %q", gm.ETag, put.ETag)
	}
	if gm.LastMod.IsZero() {
		t.Errorf("GetObject LastMod is zero")
	}
	if gm.ContentType != "image/jpeg" {
		t.Errorf("GetObject ContentType = %q, want image/jpeg", gm.ContentType)
	}

	hm, err := rs.HeadObject("bkt", "photo.jpg")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if hm.Size != int64(len(payload)) {
		t.Errorf("HeadObject Size = %d, want %d", hm.Size, len(payload))
	}
	if hm.ETag != gm.ETag {
		t.Errorf("HeadObject ETag = %q, GetObject ETag = %q", hm.ETag, gm.ETag)
	}
	if hm.LastMod.IsZero() {
		t.Errorf("HeadObject LastMod is zero")
	}
}
