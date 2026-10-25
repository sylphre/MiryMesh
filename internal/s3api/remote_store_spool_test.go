// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The Master keeps no files, so a body of unknown size is refused with 411
// instead of being spooled to a temporary file.
func TestRemoteStorePutObjectRefusesUnknownSize(t *testing.T) {
	c := newMNCluster(t, []string{"n1"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	// io.MultiReader hides the length.
	_, err := rs.PutObject("bkt", "unknown", "application/octet-stream", nil,
		io.MultiReader(strings.NewReader("hello")))
	var apiErr APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an APIError", err)
	}
	if apiErr.Status != http.StatusLengthRequired || apiErr.Code != "MissingContentLength" {
		t.Fatalf("got %d %s, want 411 MissingContentLength", apiErr.Status, apiErr.Code)
	}
	if _, _, getErr := rs.GetObject("bkt", "unknown"); getErr == nil {
		t.Error("object was stored although the size was unknown")
	}
}

func TestBodySizeHint(t *testing.T) {
	cases := []struct {
		name string
		r    io.Reader
		want int64
	}{
		{"hinted", withSizeHint(strings.NewReader("abc"), 42), 42},
		{"bytes.Reader", bytes.NewReader([]byte("abcd")), 4},
		{"strings.Reader", strings.NewReader("ab"), 2},
		{"empty bytes.Reader", bytes.NewReader(nil), 0},
		{"hidden length", io.MultiReader(strings.NewReader("abc")), -1},
	}
	for _, tc := range cases {
		if got := bodySizeHint(tc.r); got != tc.want {
			t.Errorf("%s: bodySizeHint = %d, want %d", tc.name, got, tc.want)
		}
	}
}
