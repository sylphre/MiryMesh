// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedCompleteStore holds CompleteMultipartUpload until gate is closed, like
// a DataNode that is still assembling a large object.
type gatedCompleteStore struct {
	*Storage
	gate <-chan struct{}
	fail error
}

func (g *gatedCompleteStore) CompleteMultipartUpload(id string, parts []CompletePart) (ObjectMeta, error) {
	<-g.gate
	if g.fail != nil {
		return ObjectMeta{}, g.fail
	}
	return g.Storage.CompleteMultipartUpload(id, parts)
}

// flushSignalWriter closes flushed on the first Flush, i.e. once keep-alive
// whitespace has been sent.
type flushSignalWriter struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (f *flushSignalWriter) Flush() {
	f.ResponseRecorder.Flush()
	f.once.Do(func() { close(f.flushed) })
}

func kaServe(rt *Router, method, target string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://example.com"+target, bytes.NewReader(body))
	rr := httptest.NewRecorder()
	rt.ServeHTTP(rr, req)
	return rr
}

// kaStart creates an upload, uploads parts and returns the upload ID and the
// CompleteMultipartUpload document for them.
func kaStart(t *testing.T, rt *Router, key string, parts [][]byte) (string, string) {
	t.Helper()
	rr := kaServe(rt, http.MethodPost, "/bkt/"+key+"?uploads", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("CreateMultipartUpload status=%d body=%s", rr.Code, rr.Body.String())
	}
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(rr.Body.Bytes(), &init); err != nil || init.UploadID == "" {
		t.Fatalf("no UploadId: %v body=%s", err, rr.Body.String())
	}
	var sb strings.Builder
	sb.WriteString("<CompleteMultipartUpload>")
	for i, p := range parts {
		pr := kaServe(rt, http.MethodPut, fmt.Sprintf("/bkt/%s?partNumber=%d&uploadId=%s", key, i+1, init.UploadID), p)
		if pr.Code != http.StatusOK {
			t.Fatalf("UploadPart %d status=%d body=%s", i+1, pr.Code, pr.Body.String())
		}
		fmt.Fprintf(&sb, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", i+1, pr.Header().Get("ETag"))
	}
	sb.WriteString("</CompleteMultipartUpload>")
	return init.UploadID, sb.String()
}

// multipartETag is the S3 multipart ETag: hex(md5(md5(p1)||...||md5(pN)))-N.
func multipartETag(parts [][]byte) string {
	var cat []byte
	for _, p := range parts {
		s := md5.Sum(p)
		cat = append(cat, s[:]...)
	}
	s := md5.Sum(cat)
	return fmt.Sprintf("%s-%d", hex.EncodeToString(s[:]), len(parts))
}

func newGatedRouter(t *testing.T, gate <-chan struct{}, fail error, keepAlive time.Duration) (*Router, *Storage) {
	t.Helper()
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	rt := NewRouter(&gatedCompleteStore{Storage: st, gate: gate, fail: fail}, "us-east-1")
	rt.completeKeepAlive = keepAlive
	return rt, st
}

// Regression: a slow Complete (large object) must answer 200 early and keep
// the connection busy with whitespace, then send the result document.
func TestCompleteMultipartKeepAliveSuccess(t *testing.T) {
	gate := make(chan struct{})
	rt, _ := newGatedRouter(t, gate, nil, time.Millisecond)
	parts := [][]byte{bytes.Repeat([]byte("a"), 1000), []byte("tail")}
	id, doc := kaStart(t, rt, "big.bin", parts)

	w := &flushSignalWriter{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	go func() {
		<-w.flushed // keep-alive was sent: let the assembly finish
		close(gate)
	}()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/bkt/big.bin?uploadId="+id, strings.NewReader(doc))
	rt.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, " ") {
		t.Fatalf("body must start with keep-alive whitespace: %q", body)
	}
	var res struct {
		XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
		ETag    string   `xml:"ETag"`
	}
	if err := xml.Unmarshal([]byte(strings.TrimSpace(body)), &res); err != nil {
		t.Fatalf("result XML: %v body=%q", err, body)
	}
	if want := `"` + multipartETag(parts) + `"`; res.ETag != want {
		t.Fatalf("ETag=%s want %s", res.ETag, want)
	}
	got := kaServe(rt, http.MethodGet, "/bkt/big.bin", nil)
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), bytes.Join(parts, nil)) {
		t.Fatalf("GET after complete status=%d len=%d", got.Code, got.Body.Len())
	}
}

// After an early 200, a failure must arrive as an Error document in the body,
// as Amazon S3 does.
func TestCompleteMultipartKeepAliveEmbeddedError(t *testing.T) {
	gate := make(chan struct{})
	fail := APIError{Code: "InvalidPart", Message: "injected", Status: http.StatusBadRequest}
	rt, _ := newGatedRouter(t, gate, fail, time.Millisecond)
	id, doc := kaStart(t, rt, "k", [][]byte{[]byte("x")})

	w := &flushSignalWriter{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	go func() {
		<-w.flushed
		close(gate)
	}()
	rt.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://example.com/bkt/k?uploadId="+id, strings.NewReader(doc)))

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want the early 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<Code>InvalidPart</Code>") {
		t.Fatalf("body has no embedded error: %q", w.Body.String())
	}
}

// A Complete that fails before the first interval keeps its error status.
func TestCompleteMultipartFastErrorKeepsStatus(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	fail := APIError{Code: "InvalidPart", Message: "injected", Status: http.StatusBadRequest}
	rt, _ := newGatedRouter(t, gate, fail, time.Hour)
	id, doc := kaStart(t, rt, "k", [][]byte{[]byte("x")})
	rr := kaServe(rt, http.MethodPost, "/bkt/k?uploadId="+id, []byte(doc))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "InvalidPart") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// Regression: a client retrying Complete while the first one still copies
// parts must not assemble the object twice, and Abort must not delete the
// parts under the running copy.
func TestCompleteMultipartRejectsConcurrentComplete(t *testing.T) {
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	up, err := st.CreateMultipartUpload("bkt", "k", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.UploadPart(up.UploadID, 1, strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	parts := []CompletePart{{PartNumber: 1, ETag: rec.ETag}}

	if _, err = st.beginComplete(up.UploadID); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"complete": func() error { _, e := st.CompleteMultipartUpload(up.UploadID, parts); return e },
		"abort":    func() error { return st.AbortMultipartUpload(up.UploadID) },
	} {
		ae, ok := asAPIError(call())
		if !ok || ae.Code != errCompleteInProgress.Code {
			t.Fatalf("%s during a running complete: %v", name, ae)
		}
	}
	st.endComplete(up.UploadID)

	meta, err := st.CompleteMultipartUpload(up.UploadID, parts)
	if err != nil {
		t.Fatalf("complete after the first finished: %v", err)
	}
	if meta.Size != 4 {
		t.Fatalf("size=%d", meta.Size)
	}
}

// Regression: GetUpload shared the Parts map with UploadPart, so reading the
// parts (planComplete) raced with a part upload. Run with -race.
func TestGetUploadPartsIsolatedFromUploadPart(t *testing.T) {
	st, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateBucket("bkt"); err != nil {
		t.Fatal(err)
	}
	up, err := st.CreateMultipartUpload("bkt", "k", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 1; n <= 50; n++ {
			if _, uerr := st.UploadPart(up.UploadID, n, strings.NewReader("x")); uerr != nil {
				t.Error(uerr)
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		if u := st.GetUpload(up.UploadID); u != nil {
			for range u.Parts {
			}
		}
	}
	wg.Wait()

	snap := st.GetUpload(up.UploadID)
	snap.Parts[9999] = PartRecord{}
	if _, leaked := st.GetUpload(up.UploadID).Parts[9999]; leaked {
		t.Fatal("a change to the returned Parts map reached the store")
	}
}
