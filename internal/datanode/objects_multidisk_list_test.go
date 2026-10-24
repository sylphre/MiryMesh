// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package datanode_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"
)

// listPage is the JSON the DataNode returns for a bucket listing.
type listPage struct {
	Objects []struct {
		Key string
	} `json:"objects"`
	Prefixes    []string `json:"prefixes"`
	IsTruncated bool     `json:"is_truncated"`
	Next        string   `json:"next_continuation_token"`
}

// listAll walks every page of a bucket listing and returns the entries in the
// order the DataNode returned them (objects and prefixes merged by name).
func listAll(t *testing.T, base, delimiter string, maxKeys int) []string {
	t.Helper()
	var out []string
	token := ""
	for i := 0; i < 100; i++ {
		q := url.Values{}
		q.Set("max-keys", strconv.Itoa(maxKeys))
		if delimiter != "" {
			q.Set("delimiter", delimiter)
		}
		if token != "" {
			q.Set("continuation-token", token)
		}
		code, body := multiDiskDo(t, http.MethodGet, base+"?"+q.Encode(), "")
		if code != http.StatusOK {
			t.Fatalf("list: got %d %q", code, body)
		}
		var page listPage
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}
		n := len(page.Objects) + len(page.Prefixes)
		if n > maxKeys {
			t.Fatalf("page has %d entries, max-keys=%d", n, maxKeys)
		}
		var entries []string
		for _, o := range page.Objects {
			entries = append(entries, o.Key)
		}
		entries = append(entries, page.Prefixes...)
		sortStrings(entries)
		out = append(out, entries...)
		if !page.IsTruncated {
			return out
		}
		if page.Next == "" {
			t.Fatalf("truncated page without next_continuation_token")
		}
		token = page.Next
	}
	t.Fatalf("pagination did not terminate")
	return nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestObjectsMultiDiskListPagination(t *testing.T) {
	srv, disk1, disk2 := multiDiskServer(t)
	put := func(disk, key string) {
		t.Helper()
		target := srv.URL + "/internal/objects/bkt/" + url.PathEscape(key) + "?disk=" + url.QueryEscape(disk)
		if code, body := multiDiskDo(t, http.MethodPut, target, "data-"+key); code != http.StatusOK {
			t.Fatalf("PUT %s on %s: got %d %q", key, disk, code, body)
		}
	}
	// "c.txt" exists on both disks and must be listed once.
	for _, k := range []string{"a.txt", "a/b.txt", "c.txt", "e.txt"} {
		put(disk1, k)
	}
	for _, k := range []string{"a/c.txt", "b.txt", "c.txt", "d/x.txt", "f.txt"} {
		put(disk2, k)
	}
	base := srv.URL + "/internal/objects/bkt"

	flat := []string{"a.txt", "a/b.txt", "a/c.txt", "b.txt", "c.txt", "d/x.txt", "e.txt", "f.txt"}
	delimited := []string{"a.txt", "a/", "b.txt", "c.txt", "d/", "e.txt", "f.txt"}
	for _, max := range []int{1, 2, 3, 100} {
		if got := listAll(t, base, "", max); !reflect.DeepEqual(got, flat) {
			t.Fatalf("flat max-keys=%d: got %v want %v", max, got, flat)
		}
		if got := listAll(t, base, "/", max); !reflect.DeepEqual(got, delimited) {
			t.Fatalf("delimiter max-keys=%d: got %v want %v", max, got, delimited)
		}
	}

	// ?disk= lists exactly that disk, with its own truncation info.
	code, body := multiDiskDo(t, http.MethodGet, base+"?max-keys=2&disk="+url.QueryEscape(disk2), "")
	if code != http.StatusOK {
		t.Fatalf("list ?disk=disk2: got %d %q", code, body)
	}
	var page listPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if len(page.Objects) != 2 || !page.IsTruncated || page.Next == "" {
		t.Fatalf("list ?disk=disk2: got %+v", page)
	}
}
