// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package replication

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/crc32"
	"io"
)

// ChecksumKind enumerates supported checksum algorithms.
type ChecksumKind string

const (
	ChecksumMD5    ChecksumKind = "md5" // ETag hex (existing)
	ChecksumSHA256 ChecksumKind = "sha256"
	ChecksumCRC32  ChecksumKind = "crc32"
)

// SumSHA256 returns hex SHA256 of data.
func SumSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// SumSHA256Reader hashes from reader without buffering whole body.
func SumSHA256Reader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// SumCRC32 returns CRC32 IEEE and hex.
func SumCRC32(data []byte) string {
	v := crc32.ChecksumIEEE(data)
	return hex8(v)
}

func hex8(v uint32) string {
	var b [8]byte
	const hd = "0123456789abcdef"
	for i := 7; i >= 0; i-- {
		b[i] = hd[v&0xf]
		v >>= 4
	}
	return string(b[:])
}
