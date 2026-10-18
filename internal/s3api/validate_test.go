// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import "testing"

func TestIsValidBucketNameExtra(t *testing.T) {
	if isValidBucketName("a") {
		t.Fatal("single char should be invalid")
	}
	if !isValidBucketName("abc") {
		t.Fatal("abc should be valid")
	}
}
