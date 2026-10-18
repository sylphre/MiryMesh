// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package sigv4

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// encodeRFC3986 is the signer-side alias — actual implementation lives in verify.go
// but sign.go needs the same encoder to produce matching canonical strings.
// We duplicate the logic here to avoid import cycles; keep them in sync.
func signerEncodeRFC3986(s string) string {
	// Use the same isUnreserved / encoding as verify.go
	isUnreservedLocal := func(b byte) bool {
		return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' || b == '.' || b == '_' || b == '~'
	}
	needs := false
	for i := 0; i < len(s); i++ {
		if !isUnreservedLocal(s[i]) {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) * 3)
	const hexDigits = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreservedLocal(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0F])
		}
	}
	return b.String()
}

// Signer signs a request with SigV4, used in tests to generate valid
// Authorization headers without importing AWS SDK.
type Signer struct {
	AccessKey string
	SecretKey string
	Region    string
	Service   string // e.g. "s3"
}

// Sign adds Authorization, X-Amz-Date, X-Amz-Content-Sha256 headers to req
// for header-based auth and returns the signature. amzDate must be in
// ISO8601 format "20060102T150405Z".
func (s Signer) Sign(req *http.Request, amzDate string, payloadHash string) string {
	dateStr := amzDate[:8]
	credentialScope := strings.Join([]string{dateStr, s.Region, s.Service, "aws4_request"}, "/")
	credential := s.AccessKey + "/" + credentialScope

	// Ensure required headers.
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.Host == "" {
		// net/http sets Host from URL if empty; ensure header logic sees it.
		req.Host = req.URL.Host
	}

	// SignedHeaders: host;x-amz-content-sha256;x-amz-date (sorted lower)
	signedHeaders := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	sort.Strings(signedHeaders)

	canonical, _ := buildCanonicalWithHeaders(req, signedHeaders, payloadHash, false)
	hashedCanon := sha256Hex([]byte(canonical))
	stringToSign := buildStringToSign(amzDate, credentialScope, hashedCanon)
	signingKey := deriveSigningKey(s.SecretKey, dateStr, s.Region, s.Service)
	sig := calcSignature(signingKey, stringToSign)

	auth := algoHeader + " Credential=" + credential + ", SignedHeaders=" + strings.Join(signedHeaders, ";") + ", Signature=" + sig
	req.Header.Set("Authorization", auth)
	return sig
}

// Presign adds SigV4 query parameters to req URL for presigned URL auth.
func (s Signer) Presign(req *http.Request, amzDate string, expiresSeconds int) {
	dateStr := amzDate[:8]
	credentialScope := strings.Join([]string{dateStr, s.Region, s.Service, "aws4_request"}, "/")
	credential := s.AccessKey + "/" + credentialScope

	q := req.URL.Query()
	q.Set("X-Amz-Algorithm", algoHeader)
	q.Set("X-Amz-Credential", credential)
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strings.TrimSpace(strings.Repeat(" ", 0))) // placeholder
	// Overwrite with actual value.
	q.Set("X-Amz-Expires", itoa(expiresSeconds))
	q.Set("X-Amz-SignedHeaders", "host")
	// Temporarily set to compute signature without X-Amz-Signature.
	req.URL.RawQuery = encodeQuerySorted(q)

	if req.Host == "" {
		req.Host = req.URL.Host
	}
	signedHeaders := []string{"host"}
	canonical, _ := buildCanonicalRequestPresigned(req, signedHeaders)
	hashedCanon := sha256Hex([]byte(canonical))
	stringToSign := buildStringToSign(amzDate, credentialScope, hashedCanon)
	signingKey := deriveSigningKey(s.SecretKey, dateStr, s.Region, s.Service)
	sig := calcSignature(signingKey, stringToSign)

	q.Set("X-Amz-Signature", sig)
	req.URL.RawQuery = encodeQuerySorted(q)
}

func encodeQuerySorted(q url.Values) string {
	var keys []string
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, signerEncodeRFC3986(k)+"="+signerEncodeRFC3986(v))
		}
	}
	return strings.Join(parts, "&")
}

func itoa(i int) string {
	// Avoid fmt import in sign.go for minimal deps.
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// HashPayload returns the hex SHA256 of payload bytes.
func HashPayload(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
