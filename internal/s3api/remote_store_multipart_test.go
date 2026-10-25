// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"
	"time"
)

// These tests run RemoteStore multipart calls against real DataNode HTTP servers
// (see newMNCluster): create, stream parts, list, complete, abort, and the cases
// where the Master has to find the node that holds an upload.

func mpTestBytes(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// failAfterReader yields data and then returns err instead of io.EOF.
type failAfterReader struct {
	data []byte
	err  error
	off  int
}

func (f *failAfterReader) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, f.err
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func mpExpectCode(t *testing.T, err error, code string) {
	t.Helper()
	ae, ok := asAPIError(err)
	if !ok {
		t.Fatalf("want S3 error %s, got %v", code, err)
	}
	if ae.Code != code {
		t.Fatalf("want S3 error %s, got %s (%s)", code, ae.Code, ae.Message)
	}
}

func TestRemoteStoreMultipartRoundTrip(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("mpbucket"); err != nil {
		t.Fatal(err)
	}

	st, err := rs.CreateMultipartUpload("mpbucket", "dir/file.bin", "application/x-test", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	if st.UploadID == "" || st.Bucket != "mpbucket" || st.Key != "dir/file.bin" {
		t.Fatalf("unexpected upload state: %+v", st)
	}

	sizes := []int{5 << 20, 5 << 20, 1234}
	var all []byte
	var done []CompletePart
	for i, size := range sizes {
		data := mpTestBytes(int64(i+1), size)
		all = append(all, data...)
		rec, err := rs.UploadPart(st.UploadID, i+1, bytes.NewReader(data))
		if err != nil {
			t.Fatalf("UploadPart %d: %v", i+1, err)
		}
		if rec.PartNumber != i+1 || rec.Size != int64(size) || rec.ETag == "" {
			t.Fatalf("part %d record: %+v", i+1, rec)
		}
		done = append(done, CompletePart{PartNumber: i + 1, ETag: rec.ETag})
	}

	if up := rs.GetUpload(st.UploadID); up == nil || up.Bucket != "mpbucket" || up.Key != "dir/file.bin" {
		t.Fatalf("GetUpload: %+v", up)
	}
	parts, err := rs.ListParts(st.UploadID)
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(parts) != len(sizes) {
		t.Fatalf("ListParts returned %d parts, want %d", len(parts), len(sizes))
	}
	for i, p := range parts {
		if p.PartNumber != i+1 {
			t.Fatalf("parts not ordered: %+v", parts)
		}
	}
	ups, err := rs.ListMultipartUploads("mpbucket")
	if err != nil {
		t.Fatalf("ListMultipartUploads: %v", err)
	}
	if len(ups) != 1 || ups[0].UploadID != st.UploadID {
		t.Fatalf("ListMultipartUploads: %+v", ups)
	}

	meta, err := rs.CompleteMultipartUpload(st.UploadID, done)
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	if meta.Size != int64(len(all)) {
		t.Fatalf("completed size %d, want %d", meta.Size, len(all))
	}

	head, err := rs.HeadObject("mpbucket", "dir/file.bin")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if head.Size != int64(len(all)) {
		t.Fatalf("HeadObject size %d, want %d", head.Size, len(all))
	}
	got, rc, err := rs.GetObject("mpbucket", "dir/file.bin")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != int64(len(all)) || !bytes.Equal(body, all) {
		t.Fatalf("object differs from the uploaded parts (size %d, want %d)", len(body), len(all))
	}

	if up := rs.GetUpload(st.UploadID); up != nil {
		t.Fatalf("upload still exists after complete: %+v", up)
	}
	if ups, _ := rs.ListMultipartUploads("mpbucket"); len(ups) != 0 {
		t.Fatalf("uploads left after complete: %+v", ups)
	}
}

func TestRemoteStoreMultipartFoundByAnotherMaster(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2"}, 1)
	first, _ := c.remote("single", c.master)
	if err := first.CreateBucket("mpbucket"); err != nil {
		t.Fatal(err)
	}
	st, err := first.CreateMultipartUpload("mpbucket", "k", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.UploadPart(st.UploadID, 1, bytes.NewReader(mpTestBytes(1, 2048))); err != nil {
		t.Fatal(err)
	}

	// A second RemoteStore has no memory of the upload, like a restarted Master.
	second, _ := c.remote("single", c.master)
	if err := second.CreateBucket("mpbucket"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.UploadPart(st.UploadID, 2, bytes.NewReader(mpTestBytes(2, 512))); err != nil {
		t.Fatalf("UploadPart through a store that does not know the upload: %v", err)
	}
	parts, err := second.ListParts(st.UploadID)
	if err != nil || len(parts) != 2 {
		t.Fatalf("ListParts = %+v, %v; want 2 parts", parts, err)
	}

	if err := second.AbortMultipartUpload(st.UploadID); err != nil {
		t.Fatalf("AbortMultipartUpload: %v", err)
	}
	if up := first.GetUpload(st.UploadID); up != nil {
		t.Fatalf("upload still exists after abort: %+v", up)
	}
	_, err = second.ListParts(st.UploadID)
	mpExpectCode(t, err, "NoSuchUpload")
}

func TestRemoteStoreMultipartUnknownUpload(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("mpbucket"); err != nil {
		t.Fatal(err)
	}
	if up := rs.GetUpload("does-not-exist"); up != nil {
		t.Fatalf("GetUpload of an unknown upload = %+v", up)
	}
	_, err := rs.UploadPart("does-not-exist", 1, bytes.NewReader([]byte("x")))
	mpExpectCode(t, err, "NoSuchUpload")
	_, err = rs.CompleteMultipartUpload("does-not-exist", []CompletePart{{PartNumber: 1, ETag: "x"}})
	mpExpectCode(t, err, "NoSuchUpload")
	if err := rs.AbortMultipartUpload("does-not-exist"); err == nil {
		t.Fatal("abort of an unknown upload succeeded")
	}
}

func TestRemoteStoreMultipartCompleteRejectsMissingPart(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("mpbucket"); err != nil {
		t.Fatal(err)
	}
	st, err := rs.CreateMultipartUpload("mpbucket", "k", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := rs.UploadPart(st.UploadID, 1, bytes.NewReader(mpTestBytes(1, 100)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = rs.CompleteMultipartUpload(st.UploadID, []CompletePart{
		{PartNumber: 1, ETag: rec.ETag},
		{PartNumber: 9, ETag: "missing"},
	})
	mpExpectCode(t, err, "InvalidPart")
}

// A source that fails (for example a checksum mismatch found at the end of the
// body) must return its own error and must not leave a part behind.
func TestRemoteStoreMultipartSourceErrorLeavesNoPart(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("mpbucket"); err != nil {
		t.Fatal(err)
	}
	st, err := rs.CreateMultipartUpload("mpbucket", "k", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom: checksum mismatch")
	_, err = rs.UploadPart(st.UploadID, 1, &failAfterReader{data: mpTestBytes(1, 1<<20), err: boom})
	if !errors.Is(err, boom) {
		t.Fatalf("want the source error, got %v", err)
	}

	// The DataNode may still be cleaning up when the call returns.
	deadline := time.Now().Add(3 * time.Second)
	for {
		parts, lerr := rs.ListParts(st.UploadID)
		if lerr != nil {
			t.Fatalf("ListParts: %v", lerr)
		}
		if len(parts) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a part was stored although the source failed: %+v", parts)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
