// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"net/http"
	"testing"
)

const p2CleanupTags = `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag></TagSet></Tagging>`

func p2StoreOf(t *testing.T, rt *Router) SubresourceStore {
	t.Helper()
	ss, ok := rt.store.(SubresourceStore)
	if !ok {
		t.Fatalf("store %T does not implement SubresourceStore", rt.store)
	}
	return ss
}

func p2MustBeAbsent(t *testing.T, ss SubresourceStore, bucket, key, kind string) {
	t.Helper()
	_, found, err := ss.GetSubresource(bucket, key, kind)
	if err != nil {
		t.Fatalf("GetSubresource: %v", err)
	}
	if found {
		t.Fatalf("subresource %s/%s/%s should have been purged", bucket, key, kind)
	}
}

func TestP2DeleteObjectPurgesSubresources(t *testing.T) {
	rt := newP2Router(t)
	ss := p2StoreOf(t, rt)
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", p2CleanupTags, nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?acl", "", map[string]string{"X-Amz-Acl": "public-read"}), http.StatusOK, "")

	p2Expect(t, p2Do(rt, http.MethodDelete, "/bkt/obj.txt", "", nil), http.StatusNoContent, "")
	p2MustBeAbsent(t, ss, "bkt", "obj.txt", subTagging)
	p2MustBeAbsent(t, ss, "bkt", "obj.txt", subACL)
}

func TestP2BatchDeletePurgesSubresources(t *testing.T) {
	rt := newP2Router(t)
	ss := p2StoreOf(t, rt)
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", p2CleanupTags, nil), http.StatusOK, "")

	body := `<Delete><Object><Key>obj.txt</Key></Object></Delete>`
	p2Expect(t, p2Do(rt, http.MethodPost, "/bkt?delete", body, nil), http.StatusOK, "<Deleted>")
	p2MustBeAbsent(t, ss, "bkt", "obj.txt", subTagging)
}

func TestP2DeleteBucketPurgesSubresources(t *testing.T) {
	rt := newP2Router(t)
	ss := p2StoreOf(t, rt)
	if err := rt.store.CreateBucket("tmp"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	p2Expect(t, p2Do(rt, http.MethodPut, "/tmp?cors", p2TestCORS, nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/tmp?tagging", p2CleanupTags, nil), http.StatusNoContent, "")

	p2Expect(t, p2Do(rt, http.MethodDelete, "/tmp", "", nil), http.StatusNoContent, "")
	p2MustBeAbsent(t, ss, "tmp", "", subCORS)
	p2MustBeAbsent(t, ss, "tmp", "", subTagging)

	// A recreated bucket must not inherit the old configuration.
	if err := rt.store.CreateBucket("tmp"); err != nil {
		t.Fatalf("CreateBucket again: %v", err)
	}
	p2Expect(t, p2Do(rt, http.MethodGet, "/tmp?cors", "", nil), http.StatusNotFound, "<Code>NoSuchCORSConfiguration</Code>")
}

func TestP2CopyObjectTagging(t *testing.T) {
	rt := newP2Router(t)
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?tagging", p2CleanupTags, nil), http.StatusOK, "")
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/obj.txt?acl", "", map[string]string{"X-Amz-Acl": "public-read"}), http.StatusOK, "")

	// Default directive COPY: tags follow the copy, the ACL does not.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/copy1.txt", "", map[string]string{"X-Amz-Copy-Source": "/bkt/obj.txt"}), http.StatusOK, "CopyObjectResult")
	rr := p2Do(rt, http.MethodGet, "/bkt/copy1.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "<Key>env</Key><Value>prod</Value>")
	rr = p2Do(rt, http.MethodGet, "/bkt/copy1.txt?acl", "", nil)
	p2Expect(t, rr, http.StatusOK, "FULL_CONTROL")
	p2ExpectAbsent(t, rr, "AllUsers")

	// REPLACE: tags come from x-amz-tagging only.
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/copy2.txt", "", map[string]string{
		"X-Amz-Copy-Source":       "/bkt/obj.txt",
		"X-Amz-Tagging-Directive": "REPLACE",
		"X-Amz-Tagging":           "x=1",
	}), http.StatusOK, "CopyObjectResult")
	rr = p2Do(rt, http.MethodGet, "/bkt/copy2.txt?tagging", "", nil)
	p2Expect(t, rr, http.StatusOK, "<Key>x</Key><Value>1</Value>")
	p2ExpectAbsent(t, rr, "env")

	// The source keeps its own tags.
	p2Expect(t, p2Do(rt, http.MethodGet, "/bkt/obj.txt?tagging", "", nil), http.StatusOK, "<Key>env</Key>")
}
