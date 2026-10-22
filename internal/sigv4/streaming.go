// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package sigv4

// Chunk and trailer signatures of aws-chunked ("streaming") uploads.
//
// A request whose X-Amz-Content-Sha256 is one of the STREAMING-* markers is
// signed once as a whole (the seed signature); every chunk of its body then
// carries a signature chained to the previous one.

import (
	"crypto/hmac"
	"strings"
)

// X-Amz-Content-Sha256 values of aws-chunked uploads.
const (
	PayloadStreamingSigned          = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	PayloadStreamingSignedTrailer   = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	PayloadStreamingUnsignedTrailer = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"
)

// emptySHA256 is the hex SHA-256 of the empty string.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// IsStreamingPayload reports whether v is a supported aws-chunked marker.
func IsStreamingPayload(v string) bool {
	switch v {
	case PayloadStreamingSigned, PayloadStreamingSignedTrailer, PayloadStreamingUnsignedTrailer:
		return true
	}
	return false
}

// StreamingIsSigned reports whether the chunks of v carry signatures.
func StreamingIsSigned(v string) bool {
	return v == PayloadStreamingSigned || v == PayloadStreamingSignedTrailer
}

// StreamingHasTrailer reports whether v announces trailing headers.
func StreamingHasTrailer(v string) bool {
	return v == PayloadStreamingSignedTrailer || v == PayloadStreamingUnsignedTrailer
}

// SignatureEqual compares two hex signatures in constant time.
func SignatureEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// StreamSigner computes the chained signatures of an aws-chunked body. Every
// call advances the chain, so the same sequence of calls must be made by the
// signer and the verifier.
type StreamSigner struct {
	key     []byte
	amzDate string
	scope   string
	prev    string
}

// NewStreamSigner starts a chain after the seed signature of the request.
// amzDate is the ISO8601 basic format "20060102T150405Z".
func (s Signer) NewStreamSigner(amzDate, seedSignature string) *StreamSigner {
	if len(amzDate) < 8 {
		return nil
	}
	dateStr := amzDate[:8]
	return &StreamSigner{
		key:     deriveSigningKey(s.SecretKey, dateStr, s.Region, s.Service),
		amzDate: amzDate,
		scope:   strings.Join([]string{dateStr, s.Region, s.Service, "aws4_request"}, "/"),
		prev:    seedSignature,
	}
}

// StreamSigner starts the chain of a request that Verify accepted. It returns
// nil when the result carries no signing context (presigned requests).
func (r VerifyResult) StreamSigner() *StreamSigner {
	if len(r.signingKey) == 0 || r.signature == "" {
		return nil
	}
	return &StreamSigner{key: r.signingKey, amzDate: r.amzDate, scope: r.scope, prev: r.signature}
}

// Chunk signs one chunk of data (the final, empty chunk uses nil).
func (s *StreamSigner) Chunk(data []byte) string {
	return s.ChunkSum(sha256Hex(data))
}

// ChunkSum signs one chunk given the hex SHA-256 of its data. It lets callers
// hash a large chunk incrementally instead of holding it in memory.
func (s *StreamSigner) ChunkSum(payloadSHA256Hex string) string {
	sts := strings.Join([]string{
		"AWS4-HMAC-SHA256-PAYLOAD",
		s.amzDate,
		s.scope,
		s.prev,
		emptySHA256,
		payloadSHA256Hex,
	}, "\n")
	s.prev = calcSignature(s.key, sts)
	return s.prev
}

// Trailer signs the trailing headers. canonical is every header as
// "lowercase-name:trimmed-value\n", in the order they were sent, without the
// x-amz-trailer-signature header itself.
func (s *StreamSigner) Trailer(canonical string) string {
	sts := strings.Join([]string{
		"AWS4-HMAC-SHA256-TRAILER",
		s.amzDate,
		s.scope,
		s.prev,
		sha256Hex([]byte(canonical)),
	}, "\n")
	s.prev = calcSignature(s.key, sts)
	return s.prev
}
