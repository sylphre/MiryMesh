// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

// putTestObject creates the bucket (once) and stores key with body, returning
// the ETag header exactly as sent to the client.
func putTestObject(t *testing.T, bucket, key string, body []byte) (string, func(method, path string, hdr map[string]string, payload []byte) (int, string, http.Header)) {
	t.Helper()
	router, astore, signer := newTestServer(t)
	do := func(method, path string, hdr map[string]string, payload []byte) (int, string, http.Header) {
		req := signedReq(t, signer, method, "http://example.com/"+path, payload)
		if payload != nil {
			req.Body = io.NopCloser(bytes.NewReader(payload))
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rr := doReq(router, astore, req)
		return rr.Code, rr.Body.String(), rr.Header()
	}
	if code, _, _ := do("PUT", bucket, nil, nil); code != http.StatusOK {
		t.Fatalf("CreateBucket status=%d", code)
	}
	code, _, h := do("PUT", bucket+"/"+key, nil, body)
	if code != http.StatusOK {
		t.Fatalf("PutObject status=%d", code)
	}
	return h.Get("ETag"), do
}

func TestGetObjectConditionalAndRange(t *testing.T) {
	etag, do := putTestObject(t, "bkt", "obj.txt", []byte("0123456789"))
	if etag == "" {
		t.Fatal("PutObject returned no ETag")
	}

	cases := []struct {
		name string
		hdr  map[string]string
		code int
		body string
	}{
		{"plain", nil, http.StatusOK, "0123456789"},
		{"if-none-match hit", map[string]string{"If-None-Match": etag}, http.StatusNotModified, ""},
		{"if-none-match miss", map[string]string{"If-None-Match": `"nope"`}, http.StatusOK, "0123456789"},
		{"if-match miss", map[string]string{"If-Match": `"nope"`}, http.StatusPreconditionFailed, ""},
		{"if-match hit", map[string]string{"If-Match": etag}, http.StatusOK, "0123456789"},
		{"range", map[string]string{"Range": "bytes=2-4"}, http.StatusPartialContent, "234"},
		{"range suffix", map[string]string{"Range": "bytes=-3"}, http.StatusPartialContent, "789"},
		{"range open", map[string]string{"Range": "bytes=8-"}, http.StatusPartialContent, "89"},
		{"range unsatisfiable", map[string]string{"Range": "bytes=10-"}, http.StatusRequestedRangeNotSatisfiable, ""},
		{"range malformed is ignored", map[string]string{"Range": "bytes=5-2"}, http.StatusOK, "0123456789"},
	}
	for _, c := range cases {
		code, body, _ := do("GET", "bkt/obj.txt", c.hdr, nil)
		if code != c.code {
			t.Fatalf("%s: status=%d want %d (body %q)", c.name, code, c.code, body)
		}
		if c.body != "" && body != c.body {
			t.Fatalf("%s: body=%q want %q", c.name, body, c.body)
		}
	}

	code, _, h := do("GET", "bkt/obj.txt", map[string]string{"Range": "bytes=2-4"}, nil)
	if code != http.StatusPartialContent || h.Get("Content-Range") != "bytes 2-4/10" {
		t.Fatalf("Content-Range=%q status=%d", h.Get("Content-Range"), code)
	}
	if code, _, h = do("GET", "bkt/obj.txt", map[string]string{"Range": "bytes=10-"}, nil); code != http.StatusRequestedRangeNotSatisfiable ||
		h.Get("Content-Range") != "bytes */10" {
		t.Fatalf("416 Content-Range=%q status=%d", h.Get("Content-Range"), code)
	}
}

func TestHeadObjectConditional(t *testing.T) {
	etag, do := putTestObject(t, "bkt", "obj.txt", []byte("abc"))
	if code, _, _ := do("HEAD", "bkt/obj.txt", map[string]string{"If-None-Match": etag}, nil); code != http.StatusNotModified {
		t.Fatalf("HEAD if-none-match status=%d", code)
	}
	if code, _, _ := do("HEAD", "bkt/obj.txt", map[string]string{"If-Match": `"nope"`}, nil); code != http.StatusPreconditionFailed {
		t.Fatalf("HEAD if-match status=%d", code)
	}
}

func TestListObjectsV2Params(t *testing.T) {
	_, do := putTestObject(t, "bkt", "a.txt", []byte("a"))
	for _, k := range []string{"b.txt", "c d.txt"} {
		if code, _, _ := do("PUT", "bkt/"+strings.ReplaceAll(k, " ", "%20"), nil, []byte(k)); code != http.StatusOK {
			t.Fatalf("Put %s status=%d", k, code)
		}
	}

	// max-keys=0: empty and not truncated.
	code, body, _ := do("GET", "bkt?list-type=2&max-keys=0", nil, nil)
	if code != http.StatusOK || strings.Contains(body, "<Key>") || !strings.Contains(body, "<IsTruncated>false</IsTruncated>") {
		t.Fatalf("max-keys=0: status=%d body=%s", code, body)
	}

	// start-after skips keys up to and including the given key.
	code, body, _ = do("GET", "bkt?list-type=2&start-after=a.txt", nil, nil)
	if code != http.StatusOK || strings.Contains(body, "<Key>a.txt</Key>") || !strings.Contains(body, "<Key>b.txt</Key>") ||
		!strings.Contains(body, "<StartAfter>a.txt</StartAfter>") {
		t.Fatalf("start-after: status=%d body=%s", code, body)
	}

	// encoding-type=url is accepted; anything else is rejected.
	if code, body, _ = do("GET", "bkt?list-type=2&encoding-type=url", nil, nil); code != http.StatusOK || !strings.Contains(body, "<EncodingType>url</EncodingType>") {
		t.Fatalf("encoding-type=url: status=%d body=%s", code, body)
	}
	if code, _, _ = do("GET", "bkt?list-type=2&encoding-type=bogus", nil, nil); code != http.StatusBadRequest {
		t.Fatalf("encoding-type=bogus status=%d", code)
	}

	// max-keys above 1000 is clamped rather than rejected.
	if code, _, _ = do("GET", "bkt?list-type=2&max-keys=5000", nil, nil); code != http.StatusOK {
		t.Fatalf("max-keys=5000 status=%d", code)
	}
}

func TestDeleteObjectsReportsInvalidKeys(t *testing.T) {
	_, do := putTestObject(t, "bkt", "keep.txt", []byte("x"))
	if code, _, _ := do("PUT", "bkt/gone.txt", nil, []byte("y")); code != http.StatusOK {
		t.Fatalf("Put gone.txt status=%d", code)
	}
	payload := []byte(`<Delete><Object><Key>gone.txt</Key></Object><Object><Key>missing.txt</Key></Object><Object><Key>a/../b</Key></Object></Delete>`)
	code, body, _ := do("POST", "bkt?delete", nil, payload)
	if code != http.StatusOK {
		t.Fatalf("DeleteObjects status=%d body=%s", code, body)
	}
	if !strings.Contains(body, "<Deleted><Key>gone.txt</Key></Deleted>") || !strings.Contains(body, "<Deleted><Key>missing.txt</Key></Deleted>") {
		t.Fatalf("expected both valid keys deleted: %s", body)
	}
	if !strings.Contains(body, "<Error><Key>a/../b</Key>") {
		t.Fatalf("expected per-key error for traversal key: %s", body)
	}
	if code, _, _ = do("GET", "bkt/gone.txt", nil, nil); code != http.StatusNotFound {
		t.Fatalf("gone.txt still readable, status=%d", code)
	}
	if code, _, _ = do("GET", "bkt/keep.txt", nil, nil); code != http.StatusOK {
		t.Fatalf("keep.txt lost, status=%d", code)
	}
}
