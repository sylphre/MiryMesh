// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package sigv4

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

// The example of "Signature Calculations for the Authorization Header:
// Transferring Payload in Multiple Chunks" in the Amazon S3 documentation.
func TestStreamSignerDocumentationExample(t *testing.T) {
	s := Signer{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:    "us-east-1",
		Service:   "s3",
	}
	ss := s.NewStreamSigner("20130524T000000Z", "4f232c4386841ef735655705268965c44a0e4690baa4adea153f7db9fa80a0a9")
	steps := []struct {
		data []byte
		want string
	}{
		{bytes.Repeat([]byte("a"), 65536), "ad80c730a21e5b8d04586a2213dd63b9a0e99e0e2307b0ade35a65485a288648"},
		{bytes.Repeat([]byte("a"), 1024), "0055627c9e194cb4542bae2aa5492e3c1575bbb81b612b7d234b86a503ef5497"},
		{nil, "b6c6ea8a5354eaf15b3cb7646744f4275b71ea724fed81ceb9323e279d449df9"},
	}
	for i, st := range steps {
		if got := ss.Chunk(st.data); got != st.want {
			t.Fatalf("chunk %d signature=%s want %s", i, got, st.want)
		}
	}
}

func TestVerifyStreamingPayload(t *testing.T) {
	s := Signer{AccessKey: "AK", SecretKey: "secret", Region: "us-east-1", Service: "s3"}
	lookup := func(k string) (string, bool) { return "secret", k == "AK" }
	for _, marker := range []string{PayloadStreamingSigned, PayloadStreamingSignedTrailer, PayloadStreamingUnsignedTrailer} {
		req, err := http.NewRequest(http.MethodPut, "http://example.com/b/k", nil)
		if err != nil {
			t.Fatal(err)
		}
		amzDate := time.Now().UTC().Format(iso8601Layout)
		seed := s.Sign(req, amzDate, marker)
		res, verr := Verify(req, lookup)
		if verr != nil {
			t.Fatalf("%s: %v", marker, verr)
		}
		if res.PayloadHash != marker {
			t.Fatalf("PayloadHash=%q want %q", res.PayloadHash, marker)
		}
		if !IsStreamingPayload(marker) {
			t.Fatalf("%s must be a streaming payload", marker)
		}
		// The verifier and the signer must agree on the whole chain.
		data := []byte("chunk")
		if got, want := res.StreamSigner().Chunk(data), s.NewStreamSigner(amzDate, seed).Chunk(data); got != want {
			t.Fatalf("%s: chunk signature %s want %s", marker, got, want)
		}
	}
}

func TestVerifyRejectsUnknownStreamingMarker(t *testing.T) {
	s := Signer{AccessKey: "AK", SecretKey: "secret", Region: "us-east-1", Service: "s3"}
	lookup := func(k string) (string, bool) { return "secret", k == "AK" }
	req, err := http.NewRequest(http.MethodPut, "http://example.com/b/k", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Sign(req, time.Now().UTC().Format(iso8601Layout), "STREAMING-AWS4-ECDSA-P256-SHA256-PAYLOAD")
	if _, verr := Verify(req, lookup); verr == nil {
		t.Fatal("an unsupported STREAMING-* marker must be rejected")
	}
}

func TestStreamingMarkerHelpers(t *testing.T) {
	cases := []struct {
		marker  string
		signed  bool
		trailer bool
	}{
		{PayloadStreamingSigned, true, false},
		{PayloadStreamingSignedTrailer, true, true},
		{PayloadStreamingUnsignedTrailer, false, true},
	}
	for _, c := range cases {
		if StreamingIsSigned(c.marker) != c.signed || StreamingHasTrailer(c.marker) != c.trailer {
			t.Errorf("%s: signed=%v trailer=%v", c.marker, StreamingIsSigned(c.marker), StreamingHasTrailer(c.marker))
		}
	}
	if IsStreamingPayload("UNSIGNED-PAYLOAD") || IsStreamingPayload("") {
		t.Error("only STREAMING-* markers are streaming payloads")
	}
}
