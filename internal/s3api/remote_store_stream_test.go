// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

var errTestSource = errors.New("test source failed")

// failingReader yields data and then fails with errTestSource instead of io.EOF.
type failingReader struct {
	data []byte
	off  int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, errTestSource
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func readAllObject(t *testing.T, rs *RemoteStore, bucket, key string) []byte {
	t.Helper()
	_, rc, err := rs.GetObject(bucket, key)
	if err != nil {
		t.Fatalf("GetObject %s: %v", key, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return b
}

// With a size hint the body is streamed to the DataNode and must be stored intact.
func TestRemoteStorePutObjectStreamsBody(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	payload := bytes.Repeat([]byte("0123456789abcdef"), 512*1024) // 8 MiB

	hinted, err := rs.PutObject("bkt", "hinted.bin", "application/octet-stream", nil,
		withSizeHint(bytes.NewReader(payload), int64(len(payload))))
	if err != nil {
		t.Fatalf("PutObject with size hint: %v", err)
	}
	if hinted.Size != int64(len(payload)) {
		t.Errorf("hinted Size = %d, want %d", hinted.Size, len(payload))
	}
	if got := readAllObject(t, rs, "bkt", "hinted.bin"); !bytes.Equal(got, payload) {
		t.Errorf("hinted object differs: got %d bytes, want %d", len(got), len(payload))
	}

	// A body of unknown size is refused, not spooled:
	// see TestRemoteStorePutObjectRefusesUnknownSize.
}

func TestRemoteStorePutObjectEmptyBody(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	m, err := rs.PutObject("bkt", "empty", "text/plain", nil, withSizeHint(bytes.NewReader(nil), 0))
	if err != nil {
		t.Fatalf("PutObject empty: %v", err)
	}
	if m.Size != 0 {
		t.Errorf("Size = %d, want 0", m.Size)
	}
	if got := readAllObject(t, rs, "bkt", "empty"); len(got) != 0 {
		t.Errorf("empty object has %d bytes", len(got))
	}
}

// A source that fails (for example a checksum mismatch reported on the last
// read) must return its own error and must not leave an object behind, even
// when it fails only after delivering every announced byte.
func TestRemoteStorePutObjectSourceErrorStoresNothing(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	payload := bytes.Repeat([]byte("x"), 256*1024)

	cases := map[string]int64{
		"fails-after-all-bytes": int64(len(payload)),
		"fails-midway":          int64(len(payload)) * 2,
	}
	for key, hint := range cases {
		_, err := rs.PutObject("bkt", key, "application/octet-stream", nil,
			withSizeHint(&failingReader{data: payload}, hint))
		if !errors.Is(err, errTestSource) {
			t.Errorf("%s: err = %v, want errTestSource", key, err)
		}
		if _, _, getErr := rs.GetObject("bkt", key); getErr == nil {
			t.Errorf("%s: object was stored although the source failed", key)
		}
	}
}
