// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

import (
	"errors"
	"strings"
	"testing"
)

const (
	testKeyA = "0123456789abcdef0123456789abcdef"
	testKeyB = "fedcba9876543210fedcba9876543210"
)

func mustKeyring(t *testing.T, current string, old ...string) *Keyring {
	t.Helper()
	kr, err := NewKeyring(current, old...)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return kr
}

func TestKeyringRoundTrip(t *testing.T) {
	kr := mustKeyring(t, testKeyA)
	ct, err := kr.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(ct, "mk1:"+kr.CurrentKeyID()+":") {
		t.Fatalf("ciphertext %q lacks the key id prefix", ct)
	}
	if kr.NeedsRotation(ct) {
		t.Error("ciphertext of the current key must not need rotation")
	}
	pt, err := kr.Decrypt(ct)
	if err != nil || pt != "secret" {
		t.Fatalf("Decrypt = %q, %v", pt, err)
	}
	ct2, err := kr.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct == ct2 {
		t.Error("two encryptions must use different nonces")
	}
}

func TestKeyringLegacyCiphertext(t *testing.T) {
	raw, err := MasterKeyBytes(testKeyA)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := EncryptGCM(raw, "legacy-secret")
	if err != nil {
		t.Fatal(err)
	}

	kr := mustKeyring(t, testKeyA)
	if pt, derr := kr.Decrypt(legacy); derr != nil || pt != "legacy-secret" {
		t.Fatalf("current key must open legacy ciphertext: %q, %v", pt, derr)
	}
	if !kr.NeedsRotation(legacy) {
		t.Error("legacy ciphertext must need rotation")
	}

	rotated := mustKeyring(t, testKeyB, testKeyA)
	if pt, derr := rotated.Decrypt(legacy); derr != nil || pt != "legacy-secret" {
		t.Fatalf("old key must open legacy ciphertext: %q, %v", pt, derr)
	}

	onlyNew := mustKeyring(t, testKeyB)
	if _, derr := onlyNew.Decrypt(legacy); !errors.Is(derr, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", derr)
	}
}

func TestKeyringRotationFlow(t *testing.T) {
	oldRing := mustKeyring(t, testKeyA)
	ct, err := oldRing.Encrypt("v")
	if err != nil {
		t.Fatal(err)
	}

	newRing := mustKeyring(t, testKeyB, testKeyA)
	if !newRing.NeedsRotation(ct) {
		t.Fatal("ciphertext of a retired key must need rotation")
	}
	pt, err := newRing.Decrypt(ct)
	if err != nil || pt != "v" {
		t.Fatalf("retired key must still decrypt: %q, %v", pt, err)
	}
	ct2, err := newRing.Encrypt(pt)
	if err != nil {
		t.Fatal(err)
	}
	if newRing.NeedsRotation(ct2) {
		t.Error("re-encrypted value must be on the current key")
	}

	if got, derr := mustKeyring(t, testKeyB).Decrypt(ct2); derr != nil || got != "v" {
		t.Fatalf("new key alone must decrypt: %q, %v", got, derr)
	}
	if _, derr := oldRing.Decrypt(ct2); !errors.Is(derr, ErrUnknownKey) {
		t.Fatalf("old key alone err = %v, want ErrUnknownKey", derr)
	}
}

func TestKeyringErrors(t *testing.T) {
	if _, err := NewKeyring(""); err == nil {
		t.Error("empty current key must be rejected")
	}
	if _, err := NewKeyring(testKeyA, "short"); err == nil || !strings.Contains(err.Error(), "old master key #1") {
		t.Errorf("short old key err = %v", err)
	}

	kr := mustKeyring(t, testKeyA)
	if _, err := kr.Decrypt("mk1:abc"); !errors.Is(err, ErrMalformedCiphertext) {
		t.Errorf("malformed err = %v", err)
	}

	ct, err := kr.Encrypt("v")
	if err != nil {
		t.Fatal(err)
	}
	tampered := ct[:len(ct)-4] + "AAAA"
	if _, err = kr.Decrypt(tampered); err == nil {
		t.Error("tampered ciphertext must not decrypt")
	}
}

func TestKeyringIgnoresDuplicateAndBlankOldKeys(t *testing.T) {
	kr := mustKeyring(t, testKeyA, testKeyA, "  ", testKeyB)
	if len(kr.byID) != 2 || len(kr.ordered) != 2 {
		t.Fatalf("keys = %d/%d, want 2", len(kr.byID), len(kr.ordered))
	}
}
