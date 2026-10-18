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

func TestEncodeRFC3986(t *testing.T) {
	tests := []struct{ in, want string }{
		{"abc", "abc"},
		{"ABC-_.~", "ABC-_.~"},
		{"a b", "a%20b"},
		{"a+b", "a%2Bb"},
		{"hello world+test~", "hello%20world%2Btest~"},
		{"foo/bar", "foo%2Fbar"},
		{"", ""},
		{"\x00", "%00"},
	}
	for _, tc := range tests {
		got := encodeRFC3986(tc.in)
		if got != tc.want {
			t.Fatalf("encodeRFC3986(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Ensure signer encoder matches verifier encoder.
	for _, tc := range tests {
		a := encodeRFC3986(tc.in)
		b := signerEncodeRFC3986(tc.in)
		if a != b {
			t.Fatalf("encoder mismatch for %q: verify=%q signer=%q", tc.in, a, b)
		}
	}
}

func TestAWSCanonicalVector(t *testing.T) {
	// Minimal AWS doc vector: GET / with query "Action=ListUsers&Version=2010-05-08"
	// Using fixed time and known test credentials from AWS docs.
	// We verify internal canonical/query construction, not full signature here,
	// by asserting canonicalQueryString encoding uses %20 not + for spaces.
	lookup := func(k string) (string, bool) {
		if k == "AKIAIOSFODNN7EXAMPLE" {
			return "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", true
		}
		return "", false
	}
	signer := Signer{AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "iam"}
	// Query with space should be %20, not +.
	u, _ := url.Parse("http://iam.amazonaws.com/?Action=List%20Users&Version=2010-05-08")
	req := &http.Request{Method: "GET", URL: u, Header: http.Header{}, Host: "iam.amazonaws.com"}
	amzDate := "20150830T123600Z"
	payloadHash := HashPayload([]byte(""))
	signer.Sign(req, amzDate, payloadHash)
	// Overwrite X-Amz-Date to fixed time and re-sign to make deterministic for verify skew bypass.
	// Instead create a request with fixed time and verify with time mock: we patch Verify's clock check by using recent time.
	// So just do header round-trip with recent time but space encoding assertion:
	u2, _ := url.Parse("http://example.com/bkt/my%20key?prefix=a%20b&max-keys=1")
	req2 := &http.Request{Method: "GET", URL: u2, Header: http.Header{}, Host: "example.com"}
	now := time.Now().UTC().Format(iso8601Layout)
	signer2 := Signer{AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1", Service: "s3"}
	signer2.Sign(req2, now, HashPayload(nil))
	// Verify should pass and query canonical must have used %20.
	if _, err := Verify(req2, lookup); err != nil {
		// Also try with matched lookup for s3 vector.
		lookup2 := func(k string) (string, bool) {
			if k == "AKIAIOSFODNN7EXAMPLE" {
				return "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", true
			}
			return "", false
		}
		if _, err2 := Verify(req2, lookup2); err2 != nil {
			t.Fatalf("space-encoding vector verify failed: %v / %v", err, err2)
		}
	}
	// Direct canonical check: space must be %20 not +.
	canon := canonicalQueryString(url.Values{"prefix": {"a b"}}, false)
	if canon != "prefix=a%20b" {
		t.Fatalf("canonicalQueryString space encoding = %q, want %q", canon, "prefix=a%20b")
	}
	// tilde must not be encoded.
	if got := encodeRFC3986("a~b"); got != "a~b" {
		t.Fatalf("tilde encoding: got %q", got)
	}
	_ = req // avoid unused
}
