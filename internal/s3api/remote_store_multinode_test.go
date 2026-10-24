// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
)

// These tests wire a RemoteStore to several real DataNode HTTP servers (each with
// its own disks and per-node token) and check the whole path: placement engine ->
// ?disk= -> DataNode -> disk, and back through GET failover, list merge and delete.

type mnDisk struct {
	path  string
	store ObjectStore
}

type mnNode struct {
	id    string
	srv   *httptest.Server
	disks []mnDisk
}

type mnCluster struct {
	reg    *datanode.Registry
	master string
	nodes  []*mnNode
}

// newMNCluster starts one DataNode server per id, each with disksPerNode disks,
// and registers the nodes and their disks in a shared registry.
func newMNCluster(t *testing.T, ids []string, disksPerNode int) *mnCluster {
	t.Helper()
	c := &mnCluster{
		reg:    datanode.NewRegistry(30*time.Second, 90*time.Second),
		master: "mn-master-token",
	}
	for _, id := range ids {
		n := &mnNode{id: id}
		byPath := make(map[string]ObjectStore, disksPerNode)
		var first ObjectStore
		for i := 0; i < disksPerNode; i++ {
			dir := t.TempDir()
			st, err := NewStorage(dir)
			if err != nil {
				t.Fatalf("NewStorage(%s): %v", dir, err)
			}
			var store ObjectStore = st
			if first == nil {
				first = store
			}
			byPath[dir] = store
			n.disks = append(n.disks, mnDisk{path: dir, store: store})
		}
		h := datanode.NewHandlerWithObjects(
			datanode.NewRegistry(30*time.Second, 90*time.Second),
			"",
			NewDataNodeStoreWithDisks(first, byPath),
		)
		// The controller authenticates with a token derived from the node id.
		h.SetNodeToken(datanode.NodeToken(c.master, id))
		mux := http.NewServeMux()
		h.RegisterRoutes(mux)
		n.srv = httptest.NewServer(mux)
		t.Cleanup(n.srv.Close)

		if err := c.reg.RegisterNode(id, strings.TrimPrefix(n.srv.URL, "http://")); err != nil {
			t.Fatalf("RegisterNode(%s): %v", id, err)
		}
		for _, d := range n.disks {
			if err := c.reg.UpsertDisk(id, d.path, 1<<30, 0, 0); err != nil {
				t.Fatalf("UpsertDisk(%s, %s): %v", id, d.path, err)
			}
		}
		c.nodes = append(c.nodes, n)
	}
	return c
}

func (c *mnCluster) node(t *testing.T, id string) *mnNode {
	t.Helper()
	for _, n := range c.nodes {
		if n.id == id {
			return n
		}
	}
	t.Fatalf("no node %q", id)
	return nil
}

// remote builds a RemoteStore over the cluster with a resolver whose default
// policy is policy. token is what the RemoteStore uses as the controller's token.
func (c *mnCluster) remote(policy, token string) (*RemoteStore, *placement.Resolver) {
	res, err := placement.NewResolver(c.reg, placement.New(1), policy, nil)
	if err != nil {
		panic(err)
	}
	return NewRemoteStoreWithResolver(c.reg, placement.New(1), res, token), res
}

// holders lists "node:disk" for every disk that holds bucket/key.
func (c *mnCluster) holders(bucket, key string) []string {
	var out []string
	for _, n := range c.nodes {
		for _, d := range n.disks {
			if _, err := d.store.HeadObject(bucket, key); err == nil {
				out = append(out, n.id+":"+d.path)
			}
		}
	}
	return out
}

func mnNodeOf(holder string) string {
	return strings.SplitN(holder, ":", 2)[0]
}

func mnPut(t *testing.T, rs *RemoteStore, bucket, key string) {
	t.Helper()
	if _, err := rs.PutObject(bucket, key, "text/plain", nil, strings.NewReader("payload-"+key)); err != nil {
		t.Fatalf("PutObject(%s/%s): %v", bucket, key, err)
	}
}

func mnGet(rs *RemoteStore, bucket, key string) (string, error) {
	_, rc, err := rs.GetObject(bucket, key)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return string(b), err
}

func TestRemoteStoreMultiNodeRoundTrip(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2", "n3"}, 2)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	const total = 30
	keys := make([]string, 0, total)
	for i := 0; i < total; i++ {
		keys = append(keys, fmt.Sprintf("obj-%02d", i))
	}
	nodesUsed := map[string]bool{}
	for _, k := range keys {
		mnPut(t, rs, "bkt", k)
		h := c.holders("bkt", k)
		if len(h) != 1 {
			t.Fatalf("%s: want exactly one copy with policy single, got %v", k, h)
		}
		nodesUsed[mnNodeOf(h[0])] = true
	}
	if len(nodesUsed) < 2 {
		t.Fatalf("objects should spread over several nodes, only used %v", nodesUsed)
	}

	// GET walks the nodes until one has the object.
	for _, k := range keys {
		got, err := mnGet(rs, "bkt", k)
		if err != nil {
			t.Fatalf("GetObject(%s): %v", k, err)
		}
		if got != "payload-"+k {
			t.Fatalf("GetObject(%s) body = %q", k, got)
		}
	}

	// A single list merges what every node holds.
	all, err := rs.ListObjectsV2("bkt", "", "", "", 1000)
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	if len(all.Objects) != total || all.IsTruncated {
		t.Fatalf("list = %d objects, truncated=%v; want %d, false", len(all.Objects), all.IsTruncated, total)
	}
	listed := make([]string, 0, total)
	for _, o := range all.Objects {
		listed = append(listed, o.Key)
	}
	if !sort.StringsAreSorted(listed) {
		t.Fatalf("list is not sorted: %v", listed)
	}

	// Paging across nodes returns every key exactly once.
	seen := map[string]int{}
	token := ""
	for page := 0; ; page++ {
		if page > total {
			t.Fatalf("paging did not finish after %d pages", page)
		}
		res, err := rs.ListObjectsV2("bkt", "", "", token, 7)
		if err != nil {
			t.Fatalf("ListObjectsV2 page %d: %v", page, err)
		}
		if len(res.Objects) > 7 {
			t.Fatalf("page %d has %d objects, max-keys is 7", page, len(res.Objects))
		}
		for _, o := range res.Objects {
			seen[o.Key]++
		}
		if !res.IsTruncated {
			break
		}
		if res.NextContinuationToken == "" {
			t.Fatalf("page %d is truncated but has no continuation token", page)
		}
		token = res.NextContinuationToken
	}
	if len(seen) != total {
		t.Fatalf("paging saw %d distinct keys, want %d", len(seen), total)
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("key %s appeared %d times while paging", k, n)
		}
	}

	// DELETE reaches whichever node holds the object.
	for _, k := range keys[:total/2] {
		if err := rs.DeleteObject("bkt", k); err != nil {
			t.Fatalf("DeleteObject(%s): %v", k, err)
		}
		if h := c.holders("bkt", k); len(h) != 0 {
			t.Fatalf("%s still on %v after delete", k, h)
		}
		if _, err := mnGet(rs, "bkt", k); !errors.Is(err, errNoSuchKey) {
			t.Fatalf("GetObject(%s) after delete: err = %v, want errNoSuchKey", k, err)
		}
	}
	rest, err := rs.ListObjectsV2("bkt", "", "", "", 1000)
	if err != nil {
		t.Fatalf("ListObjectsV2 after delete: %v", err)
	}
	if len(rest.Objects) != total-total/2 {
		t.Fatalf("after delete: %d objects, want %d", len(rest.Objects), total-total/2)
	}
}

func TestRemoteStoreMultiNodeSurvivesDeadNode(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2", "n3"}, 1)
	rs, _ := c.remote("single", c.master)
	if err := rs.CreateBucket("bkt"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	var keys []string
	for i := 0; i < 24; i++ {
		k := fmt.Sprintf("obj-%02d", i)
		keys = append(keys, k)
		mnPut(t, rs, "bkt", k)
	}

	// n3 goes down but the registry still lists it as online (heartbeat not yet stale).
	c.node(t, "n3").srv.Close()

	var alive, dead int
	for _, k := range keys {
		h := c.holders("bkt", k)
		if len(h) != 1 {
			t.Fatalf("%s: want one copy, got %v", k, h)
		}
		got, err := mnGet(rs, "bkt", k)
		if mnNodeOf(h[0]) == "n3" {
			dead++
			if err == nil {
				t.Fatalf("%s lives on the dead node but GetObject succeeded", k)
			}
			continue
		}
		alive++
		if err != nil || got != "payload-"+k {
			t.Fatalf("GetObject(%s) with n3 down: body=%q err=%v", k, got, err)
		}
	}
	if alive == 0 || dead == 0 {
		t.Fatalf("test needs objects on both live and dead nodes (alive=%d dead=%d)", alive, dead)
	}

	// Listing skips the unreachable node and still returns the rest.
	res, err := rs.ListObjectsV2("bkt", "", "", "", 1000)
	if err != nil {
		t.Fatalf("ListObjectsV2 with n3 down: %v", err)
	}
	if len(res.Objects) != alive {
		t.Fatalf("list with n3 down = %d objects, want %d", len(res.Objects), alive)
	}
}

func TestRemoteStoreMultiNodeSameNodeAllowList(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2", "n3"}, 2)
	rs, res := c.remote("single", c.master)
	if err := rs.CreateBucket("pair"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	n2 := c.node(t, "n2")
	pair := []placement.DiskRef{
		{NodeID: "n2", Path: n2.disks[0].path},
		{NodeID: "n2", Path: n2.disks[1].path},
	}
	if err := res.SetBucketPolicy("pair", placement.PolicyDual); err != nil {
		t.Fatalf("SetBucketPolicy: %v", err)
	}
	if _, err := res.SetBucketRule("pair", placement.Rule{Mode: placement.ModeSameNode, Disks: pair}); err != nil {
		t.Fatalf("SetBucketRule: %v", err)
	}

	// Both replicas of dual + same_node sit on two different disks of n2.
	targets, err := res.Resolve("pair", 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(targets) != 2 || targets[0].NodeID != "n2" || targets[1].NodeID != "n2" || targets[0].Path == targets[1].Path {
		t.Fatalf("dual same_node targets = %+v, want two different disks of n2", targets)
	}

	// The copy the RemoteStore writes lands on one of those two disks, never elsewhere.
	disksUsed := map[string]bool{}
	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("obj-%02d", i)
		mnPut(t, rs, "pair", k)
		h := c.holders("pair", k)
		if len(h) != 1 || mnNodeOf(h[0]) != "n2" {
			t.Fatalf("%s: holders = %v, want one disk of n2", k, h)
		}
		disksUsed[h[0]] = true
		if got, err := mnGet(rs, "pair", k); err != nil || got != "payload-"+k {
			t.Fatalf("GetObject(%s): body=%q err=%v", k, got, err)
		}
	}
	if len(disksUsed) != 2 {
		t.Fatalf("both allowed disks should be used, got %v", disksUsed)
	}
}

func TestRemoteStoreMultiNodeReservedDisk(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2"}, 2)
	rs, res := c.remote("single", c.master)
	for _, b := range []string{"priv", "other"} {
		if err := rs.CreateBucket(b); err != nil {
			t.Fatalf("CreateBucket(%s): %v", b, err)
		}
	}
	n1 := c.node(t, "n1")
	reserved := placement.DiskRef{NodeID: "n1", Path: n1.disks[0].path}
	reservedKey := "n1:" + n1.disks[0].path

	if _, err := res.SetBucketRule("priv", placement.Rule{Mode: placement.ModeSpread, Disks: []placement.DiskRef{reserved}}); err != nil {
		t.Fatalf("SetBucketRule(priv): %v", err)
	}
	if err := res.Reserve("priv", reserved); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// Another bucket cannot take the reserved disk.
	if err := res.Reserve("other", reserved); !errors.Is(err, placement.ErrConflict) {
		t.Fatalf("Reserve for a second bucket: err = %v, want ErrConflict", err)
	}
	otherOnReserved := 0
	for i := 0; i < 40; i++ {
		k := fmt.Sprintf("o-%02d", i)
		mnPut(t, rs, "other", k)
		for _, h := range c.holders("other", k) {
			if h == reservedKey {
				otherOnReserved++
			}
		}
	}
	if otherOnReserved != 0 {
		t.Fatalf("%d objects of bucket other were written to the disk reserved for priv", otherOnReserved)
	}

	// The owner writes only to its disk.
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("p-%02d", i)
		mnPut(t, rs, "priv", k)
		h := c.holders("priv", k)
		if len(h) != 1 || h[0] != reservedKey {
			t.Fatalf("%s: holders = %v, want only %s", k, h, reservedKey)
		}
	}

	// Releasing the reservation does not free the disk: it is still in priv's
	// allow-list, so another bucket cannot reserve it.
	if !res.Release(reserved) {
		t.Fatal("Release reported no reservation")
	}
	if err := res.Reserve("other", reserved); err == nil {
		t.Fatal("disk is still in priv's allow-list, Reserve for other should fail")
	} else if !errors.Is(err, placement.ErrConflict) {
		t.Fatalf("Reserve after release: err = %v, want ErrConflict", err)
	}
}

func TestRemoteStoreMultiNodeRejectsWrongToken(t *testing.T) {
	c := newMNCluster(t, []string{"n1", "n2"}, 1)
	good, _ := c.remote("single", c.master)
	bad, _ := c.remote("single", "not-the-master-token")
	for _, rs := range []*RemoteStore{good, bad} {
		if err := rs.CreateBucket("bkt"); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
	}

	if _, err := bad.PutObject("bkt", "k", "text/plain", nil, strings.NewReader("x")); err == nil {
		t.Fatal("PutObject with a wrong controller token succeeded")
	}
	if h := c.holders("bkt", "k"); len(h) != 0 {
		t.Fatalf("object was stored despite the wrong token: %v", h)
	}

	mnPut(t, good, "bkt", "k")
	if _, err := mnGet(bad, "bkt", "k"); err == nil {
		t.Fatal("GetObject with a wrong controller token succeeded")
	}
	if got, err := mnGet(good, "bkt", "k"); err != nil || got != "payload-k" {
		t.Fatalf("GetObject with the right token: body=%q err=%v", got, err)
	}
}
