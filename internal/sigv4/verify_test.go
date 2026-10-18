// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package sigv4

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestHeaderRoundTrip(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "AKIAEXAMPLE" {
			return "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", true
		}
		return "", false
	}
	signer := Signer{AccessKey: "AKIAEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "s3"}
	now := time.Now().UTC().Format(iso8601Layout)
	body := []byte("hello world")
	payloadHash := HashPayload(body)

	u, _ := url.Parse("http://example.com/my-bucket/my%2Fkey?list-type=2")
	req := &http.Request{Method: "PUT", URL: u, Header: http.Header{}, Host: "example.com"}
	signer.Sign(req, now, payloadHash)

	res, err := Verify(req, lookup)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if res.AccessKey != "AKIAEXAMPLE" {
		t.Fatalf("AccessKey = %q", res.AccessKey)
	}
}

func TestHeaderBadSignatureRejected(t *testing.T) {
	lookup := func(k string) (string, bool) { return "secret12345678", true }
	signer := Signer{AccessKey: "AKIA", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	now := time.Now().UTC().Format(iso8601Layout)
	u, _ := url.Parse("http://example.com/b/k")
	req := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "example.com"}
	signer.Sign(req, now, HashPayload(nil))
	// Tamper signature.
	req.Header.Set("Authorization", req.Header.Get("Authorization")+"0")
	if _, err := Verify(req, lookup); err == nil {
		t.Fatal("expected signature mismatch")
	}
}

func TestPresignedRoundTrip(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "AKIAEXAMPLE" {
			return "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", true
		}
		return "", false
	}
	signer := Signer{AccessKey: "AKIAEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "s3"}
	now := time.Now().UTC().Format(iso8601Layout)
	u, _ := url.Parse("http://example.com/my-bucket/my-key")
	req := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "example.com"}
	signer.Presign(req, now, 3600)
	res, err := Verify(req, lookup)
	if err != nil {
		t.Fatalf("Presigned Verify failed: %v", err)
	}
	if res.AccessKey != "AKIAEXAMPLE" {
		t.Fatalf("AccessKey = %q", res.AccessKey)
	}
}

func TestPresignedExpiredRejected(t *testing.T) {
	lookup := func(k string) (string, bool) { return "secret12345678", true }
	signer := Signer{AccessKey: "AKIA", SecretKey: "secret12345678", Region: "us-east-1", Service: "s3"}
	old := time.Now().UTC().Add(-2 * time.Hour).Format(iso8601Layout)
	u, _ := url.Parse("http://example.com/b/k")
	req := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "example.com"}
	signer.Presign(req, old, 10) // expires 10s after old date, so already expired
	if _, err := Verify(req, lookup); err == nil {
		t.Fatal("expected presigned expired error")
	}
}

func TestMissingAuthRejected(t *testing.T) {
	lookup := func(k string) (string, bool) { return "secret", true }
	u, _ := url.Parse("http://example.com/b/k")
	req := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "example.com"}
	if _, err := Verify(req, lookup); err == nil {
		t.Fatal("expected missing auth error")
	}
}

func TestUnsignedPayloadAllowed(t *testing.T) {
	lookup := func(k string) (string, bool) { return "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", true }
	signer := Signer{AccessKey: "AKIAEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "s3"}
	now := time.Now().UTC().Format(iso8601Layout)
	u, _ := url.Parse("http://example.com/b/k")
	req := &http.Request{Method: "PUT", URL: u, Header: http.Header{}, Host: "example.com"}
	signer.Sign(req, now, "UNSIGNED-PAYLOAD")
	if _, err := Verify(req, lookup); err != nil {
		t.Fatalf("UNSIGNED-PAYLOAD should be allowed: %v", err)
	}
}
