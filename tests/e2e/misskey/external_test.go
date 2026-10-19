//go:build e2e_external

// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package misskey_test

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/sigv4"
)

func TestMisskey_Smoke(t *testing.T) {
	signer := sigv4.Signer{AccessKey: "TESTKEY", SecretKey: "testsecret12345", Region: "us-east-1", Service: "s3"}
	client := &http.Client{Timeout: 10 * time.Second}
	signed := func(method, path string, body []byte) *http.Request {
		u, _ := url.Parse("http://localhost:9000" + path)
		req, _ := http.NewRequest(method, u.String(), bytes.NewReader(body))
		req.Host = u.Host
		now := time.Now().UTC().Format("20060102T150405Z")
		signer.Sign(req, now, sigv4.HashPayload(body))
		if body != nil {
			req.ContentLength = int64(len(body))
		}
		return req
	}
	req := signed("PUT", "/e2e-misskey-smoke", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("controller not reachable: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 409 {
		t.Fatalf("CreateBucket %d", resp.StatusCode)
	}
	body := []byte("misskey-smoke")
	req = signed("PUT", "/e2e-misskey-smoke/file.dat", body)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Put %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Put %d", resp.StatusCode)
	}
	req = signed("GET", "/e2e-misskey-smoke/file.dat", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Get %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("Get %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if buf.String() != string(body) {
		t.Fatalf("Get body %q", buf.String())
	}
}
