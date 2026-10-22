// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package metadata

// Master key rotation.
//
// Ciphertexts written by a Keyring look like "mk1:<kid>:<base64(nonce||sealed)>",
// where <kid> is a short fingerprint of the key that sealed them. A Keyring holds
// one current key (used for every new encryption) and any number of retired keys
// (decrypt-only), so the key can be rotated without downtime:
//
//  1. configure the new key as the master key and the old one as an old key,
//  2. restart (everything stays readable),
//  3. run "mirymesh rotate-master-key" to re-encrypt stored secrets,
//  4. drop the old key from the configuration.
//
// Ciphertexts written before rotation support existed carry no prefix (plain
// base64, which can never contain ':'); they are decrypted by trying each key.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const keyringPrefix = "mk1"

var (
	// ErrUnknownKey means no configured master key can open the ciphertext.
	ErrUnknownKey = errors.New("ciphertext was encrypted with a master key that is not configured (add it to auth.old_master_keys / MIRYMESH_MASTER_KEY_OLD)")
	// ErrMalformedCiphertext means the value is not a valid keyring ciphertext.
	ErrMalformedCiphertext = errors.New("malformed ciphertext")
)

// Keyring encrypts with the current master key and decrypts with the current or any retired key.
type Keyring struct {
	current   []byte
	currentID string
	byID      map[string][]byte
	ordered   [][]byte // current first, then retired keys; used for legacy (unprefixed) ciphertexts
}

// KeyID returns the short, non-reversible fingerprint stored next to ciphertexts.
func KeyID(key []byte) string {
	sum := sha256.Sum256(append([]byte("mirymesh/master-key-id/v1:"), key...))
	return hex.EncodeToString(sum[:4])
}

// NewKeyring builds a keyring from the current master key and retired keys.
// Blank and duplicate retired keys are ignored.
func NewKeyring(current string, old ...string) (*Keyring, error) {
	cur, err := MasterKeyBytes(current)
	if err != nil {
		return nil, err
	}
	kr := &Keyring{
		current:   cur,
		currentID: KeyID(cur),
		byID:      map[string][]byte{},
		ordered:   [][]byte{cur},
	}
	kr.byID[kr.currentID] = cur
	for i, o := range old {
		if strings.TrimSpace(o) == "" {
			continue
		}
		k, kerr := MasterKeyBytes(o)
		if kerr != nil {
			return nil, fmt.Errorf("old master key #%d: %w", i+1, kerr)
		}
		id := KeyID(k)
		if _, dup := kr.byID[id]; dup {
			continue
		}
		kr.byID[id] = k
		kr.ordered = append(kr.ordered, k)
	}
	return kr, nil
}

// CurrentKeyID returns the fingerprint of the key used for new encryptions.
func (k *Keyring) CurrentKeyID() string { return k.currentID }

// Encrypt seals plaintext with the current key.
func (k *Keyring) Encrypt(plaintext string) (string, error) {
	ct, err := EncryptGCM(k.current, plaintext)
	if err != nil {
		return "", err
	}
	return keyringPrefix + ":" + k.currentID + ":" + ct, nil
}

// Decrypt opens a ciphertext produced by Encrypt, or a legacy unprefixed one.
func (k *Keyring) Decrypt(ciphertext string) (string, error) {
	if strings.HasPrefix(ciphertext, keyringPrefix+":") {
		parts := strings.SplitN(ciphertext, ":", 3)
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return "", ErrMalformedCiphertext
		}
		key, ok := k.byID[parts[1]]
		if !ok {
			return "", fmt.Errorf("%w (key id %s)", ErrUnknownKey, parts[1])
		}
		return DecryptGCM(key, parts[2])
	}
	for _, key := range k.ordered {
		if pt, err := DecryptGCM(key, ciphertext); err == nil {
			return pt, nil
		}
	}
	return "", fmt.Errorf("%w (legacy ciphertext)", ErrUnknownKey)
}

// NeedsRotation reports whether the ciphertext is not sealed with the current key.
func (k *Keyring) NeedsRotation(ciphertext string) bool {
	return !strings.HasPrefix(ciphertext, keyringPrefix+":"+k.currentID+":")
}
