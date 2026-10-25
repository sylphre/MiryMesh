// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
	"github.com/sylphre/MiryMesh/internal/placement"
)

// RemoteStore implements ObjectStore by fanning out to DataNodes via HTTP.
// Metadata (bucket list) is held locally in controller's bucket set; object data lives on DataNodes.
type RemoteStore struct {
	reg      *datanode.Registry
	resolver interface {
		PolicyFor(bucket string) placement.Policy
	}
	engine     *placement.Engine
	client     *http.Client
	dataClient *http.Client // object bodies: no total timeout, so large files can stream
	token      string
	// mpNode remembers which DataNode holds each multipart upload (upload ID -> node ID).
	// mpComplete is the client for CompleteMultipartUpload, which can run for minutes.
	mpMu       sync.Mutex
	mpNode     map[string]string
	mpComplete *http.Client
	// local bucket set (controller holds bucket existence)
	buckets      map[string]struct{}
	bucketPolicy map[string][]byte
	mu           sync.RWMutex

	// S3 subresource documents (multipart config and parts, SSE-C and checksum
	// records, ACL, tagging, CORS, lifecycle, quota). See remote_store_subresources.go.
	subresDB
}

// NewRemoteStore creates a RemoteStore. buckets may be empty and filled via CreateBucket.
// NewRemoteStore creates a RemoteStore. resolver may be nil (defaults to single).
func NewRemoteStore(reg *datanode.Registry, engine *placement.Engine, token string) *RemoteStore {
	return NewRemoteStoreWithResolver(reg, engine, nil, token)
}

// NewRemoteStoreWithResolver creates a RemoteStore with placement resolver for bucket policies.
func NewRemoteStoreWithResolver(reg *datanode.Registry, engine *placement.Engine, resolver interface {
	PolicyFor(bucket string) placement.Policy
}, token string) *RemoteStore {
	if engine == nil {
		engine = placement.New(0)
	}
	return &RemoteStore{
		reg:          reg,
		resolver:     resolver,
		engine:       engine,
		client:       &http.Client{Timeout: 30 * time.Second, Transport: &nodeTokenTransport{reg: reg, token: token, base: http.DefaultTransport}},
		dataClient:   newDataClient(reg, token),
		token:        token,
		buckets:      make(map[string]struct{}),
		bucketPolicy: make(map[string][]byte),
	}
}

func (r *RemoteStore) WithResolver(resolver interface {
	PolicyFor(bucket string) placement.Policy
}) {
	r.resolver = resolver
}

// local bucket helpers. r.mu guards both r.buckets and r.bucketPolicy.
// Never call another locking method of RemoteStore while holding r.mu.
func (r *RemoteStore) BucketExists(bucket string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.buckets[bucket]
	return ok
}
func (r *RemoteStore) ListBuckets() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.buckets))
	for k := range r.buckets {
		out = append(out, k)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}
func (r *RemoteStore) CreateBucket(bucket string) error {
	if !isValidBucketName(bucket) {
		return errInvalidBucketName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.buckets[bucket]; ok {
		return errBucketAlreadyOwned
	}
	if err := r.insertBucketRow(bucket); err != nil {
		return err
	}
	r.buckets[bucket] = struct{}{}
	return nil
}
func (r *RemoteStore) DeleteBucket(bucket string) error {
	if !r.BucketExists(bucket) {
		return errNoSuchBucket
	}
	// Check not empty by fan-out list. The network call runs without the lock.
	res, err := r.ListObjectsV2(bucket, "", "", "", 1)
	if err != nil {
		return err
	}
	if len(res.Objects) > 0 || len(res.CommonPrefixes) > 0 {
		return errBucketNotEmpty
	}
	if err = r.deleteBucketRow(bucket); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.buckets, bucket)
	delete(r.bucketPolicy, bucket)
	r.mu.Unlock()
	return nil
}
func (r *RemoteStore) Root() string                      { return "" }
func (r *RemoteStore) FilesystemCapacity() int64         { return 0 }
func (r *RemoteStore) FilesystemAvail() int64            { return 0 }
func (r *RemoteStore) FilesystemUsed() int64             { return 0 }
func (r *RemoteStore) IsOverQuota(additional int64) bool { return false }

func (r *RemoteStore) GetBucketPolicy(bucket string) ([]byte, error) {
	if !r.BucketExists(bucket) {
		return nil, errNoSuchBucket
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.bucketPolicy[bucket]
	if !ok {
		return nil, errNoSuchBucketPolicy
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func (r *RemoteStore) PutBucketPolicy(bucket string, policyJSON []byte) error {
	if !r.BucketExists(bucket) {
		return errNoSuchBucket
	}
	if err := r.saveBucketPolicy(bucket, policyJSON); err != nil {
		return err
	}
	cp := make([]byte, len(policyJSON))
	copy(cp, policyJSON)
	r.mu.Lock()
	r.bucketPolicy[bucket] = cp
	r.mu.Unlock()
	return nil
}

func (r *RemoteStore) DeleteBucketPolicy(bucket string) error {
	if !r.BucketExists(bucket) {
		return errNoSuchBucket
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bucketPolicy[bucket]; !ok {
		return errNoSuchBucketPolicy
	}
	if err := r.saveBucketPolicy(bucket, nil); err != nil {
		return err
	}
	delete(r.bucketPolicy, bucket)
	return nil
}

func (r *RemoteStore) selectTargets(bucket string, size int64) ([]placement.Target, error) {
	policy := placement.PolicySingle
	if r.resolver != nil {
		policy = r.resolver.PolicyFor(bucket)
	}
	// A resolver that knows per-bucket placement rules (mode, allow-list, disks
	// reserved for other buckets) gets them applied; any other resolver keeps the
	// old policy-only behavior.
	if oc, ok := r.resolver.(interface {
		OptionsFor(bucket string) placement.SelectOptions
	}); ok {
		return r.engine.SelectWith(r.reg, policy, size, oc.OptionsFor(bucket))
	}
	return r.engine.Select(r.reg, policy, size)
}

func (r *RemoteStore) PutObject(bucket, key string, contentType string, meta map[string]string, body io.Reader) (ObjectMeta, error) {
	if !r.BucketExists(bucket) {
		return ObjectMeta{}, errNoSuchBucket
	}
	// Stream the body to the DataNode. The placement engine needs the size up
	// front: the S3 handler passes it with withSizeHint. The Master keeps no
	// files, so a body of unknown size is refused instead of spooled to disk.
	size := bodySizeHint(body)
	if size < 0 {
		return ObjectMeta{}, errBodySizeUnknown
	}
	targets, err := r.selectTargets(bucket, size)
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("insufficient storage: %w", err)
	}
	if len(targets) == 0 {
		return ObjectMeta{}, fmt.Errorf("no targets")
	}
	// Fan-out: for MVP, write to first target only; replication to others will be background via replication.Engine
	tgt := targets[0]
	nodeAddr := r.nodeAddr(tgt.NodeID)
	if nodeAddr == "" {
		return ObjectMeta{}, fmt.Errorf("node %s not found", tgt.NodeID)
	}
	urlStr := fmt.Sprintf("http://%s/internal/objects/%s/%s", nodeAddr, url.PathEscape(bucket), url.PathEscape(key))
	// Tell the DataNode which of its disks the placement engine picked.
	if tgt.Path != "" {
		urlStr += "?disk=" + url.QueryEscape(tgt.Path)
	}
	// A read error from the caller's body (for example a failed checksum) must
	// reach the caller unchanged, and the DataNode must never receive a complete
	// body for it: lastByteHold keeps the final byte back until the source ends
	// cleanly.
	src := &readErrTracker{src: body}
	var reqBody io.Reader = http.NoBody
	if size > 0 {
		reqBody = &lastByteHold{src: src}
	}
	req, err := http.NewRequest("PUT", urlStr, reqBody)
	if err != nil {
		return ObjectMeta{}, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	for k, v := range meta {
		req.Header.Set("X-Amz-Meta-"+k, v)
	}
	if r.token != "" {
		req.Header.Set("X-Internal-Token", r.token)
	}
	resp, err := r.bodyClient().Do(req)
	if err != nil {
		if readErr := src.Err(); readErr != nil {
			return ObjectMeta{}, readErr
		}
		return ObjectMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return ObjectMeta{}, fmt.Errorf("remote put %d: %s", resp.StatusCode, string(body))
	}
	// Return meta from local computation (ETag via md5 already done in data path would be re-computed by datanode)
	// For MVP, return synthetic meta
	return ObjectMeta{Key: key, Size: size, ETag: resp.Header.Get("ETag"), ContentType: contentType, Meta: meta}, nil
}

func (r *RemoteStore) GetObject(bucket, key string) (ObjectMeta, io.ReadCloser, error) {
	return r.getObjectFailover(bucket, key, false)
}
func (r *RemoteStore) HeadObject(bucket, key string) (ObjectMeta, error) {
	meta, rc, err := r.getObjectFailover(bucket, key, true)
	if err != nil {
		return ObjectMeta{}, err
	}
	_ = rc.Close()
	return meta, nil
}

func (r *RemoteStore) getObjectFailover(bucket, key string, head bool) (ObjectMeta, io.ReadCloser, error) {
	if !r.BucketExists(bucket) {
		return ObjectMeta{}, nil, errNoSuchBucket
	}
	nodes := r.reg.ListNodes()
	var lastErr error
	for _, n := range nodes {
		if n.Status != datanode.StatusOnline {
			continue
		}
		method := "GET"
		if head {
			method = "HEAD"
		}
		urlStr := fmt.Sprintf("http://%s/internal/objects/%s/%s", n.Addr, url.PathEscape(bucket), url.PathEscape(key))
		req, _ := http.NewRequest(method, urlStr, nil)
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.bodyClient().Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == 200 {
			meta := objectMetaFromResponse(key, resp)
			return meta, resp.Body, nil
		}
		_ = resp.Body.Close()
		if resp.StatusCode == 404 {
			lastErr = errNoSuchKey
			continue
		}
		lastErr = fmt.Errorf("remote %d", resp.StatusCode)
	}
	if lastErr != nil {
		return ObjectMeta{}, nil, lastErr
	}
	return ObjectMeta{}, nil, errNoSuchKey
}

func (r *RemoteStore) DeleteObject(bucket, key string) error {
	if !r.BucketExists(bucket) {
		return errNoSuchBucket
	}
	// Fan-out delete to all online nodes (M7 batch semantics)
	var lastErr error
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		urlStr := fmt.Sprintf("http://%s/internal/objects/%s/%s", n.Addr, url.PathEscape(bucket), url.PathEscape(key))
		req, _ := http.NewRequest("DELETE", urlStr, nil)
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
	}
	return lastErr
}

func (r *RemoteStore) ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (ListObjectsV2Result, error) {
	if !r.BucketExists(bucket) {
		return ListObjectsV2Result{}, errNoSuchBucket
	}
	// Fan-out: every online node returns its first maxKeys entries after the
	// continuation token; mergeListings keeps the first maxKeys of the union and
	// reports whether more entries remain.
	var pages []ListObjectsV2Result
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		q := url.Values{}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if delimiter != "" {
			q.Set("delimiter", delimiter)
		}
		if continuationToken != "" {
			q.Set("continuation-token", continuationToken)
		}
		if maxKeys > 0 {
			q.Set("max-keys", fmt.Sprintf("%d", maxKeys))
		}
		u := fmt.Sprintf("http://%s/internal/objects/%s?%s", n.Addr, url.PathEscape(bucket), q.Encode())
		req, _ := http.NewRequest("GET", u, nil)
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			// Node does not have the bucket (or failed): skip it, like an offline node.
			_ = resp.Body.Close()
			continue
		}
		var body struct {
			Objects               []ObjectMeta `json:"objects"`
			CommonPrefixes        []string     `json:"prefixes"`
			IsTruncated           bool         `json:"is_truncated"`
			NextContinuationToken string       `json:"next_continuation_token"`
		}
		decErr := json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if decErr != nil {
			continue
		}
		pages = append(pages, ListObjectsV2Result{
			Objects:               body.Objects,
			CommonPrefixes:        body.CommonPrefixes,
			IsTruncated:           body.IsTruncated,
			NextContinuationToken: body.NextContinuationToken,
		})
	}
	return mergeListings(pages, maxKeys), nil
}

func (r *RemoteStore) nodeAddr(nodeID string) string {
	for _, n := range r.reg.ListNodes() {
		if n.ID == nodeID {
			return n.Addr
		}
	}
	return ""
}

// GetUpload is in remote_store_multipart.go: it asks the DataNode that holds the upload.
func (r *RemoteStore) MultipartDir(uploadID string) string { return "" }

var _ ObjectStore = (*RemoteStore)(nil)
