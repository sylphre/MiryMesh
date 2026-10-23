// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
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
	engine *placement.Engine
	client *http.Client
	token  string
	// local bucket set (controller holds bucket existence)
	buckets      map[string]struct{}
	bucketPolicy map[string][]byte
	mu           sync.RWMutex
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

// local bucket helpers
func (r *RemoteStore) BucketExists(bucket string) bool { _, ok := r.buckets[bucket]; return ok }
func (r *RemoteStore) ListBuckets() []string {
	out := make([]string, 0, len(r.buckets))
	for k := range r.buckets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func (r *RemoteStore) CreateBucket(bucket string) error {
	if !isValidBucketName(bucket) {
		return errInvalidBucketName
	}
	if _, ok := r.buckets[bucket]; ok {
		return errBucketAlreadyOwned
	}
	r.buckets[bucket] = struct{}{}
	return nil
}
func (r *RemoteStore) DeleteBucket(bucket string) error {
	if _, ok := r.buckets[bucket]; !ok {
		return errNoSuchBucket
	}
	// Check not empty by fan-out list
	res, err := r.ListObjectsV2(bucket, "", "", "", 1)
	if err != nil {
		return err
	}
	if len(res.Objects) > 0 || len(res.CommonPrefixes) > 0 {
		return errBucketNotEmpty
	}
	delete(r.buckets, bucket)
	return nil
}
func (r *RemoteStore) Root() string                      { return "" }
func (r *RemoteStore) FilesystemCapacity() int64         { return 0 }
func (r *RemoteStore) FilesystemAvail() int64            { return 0 }
func (r *RemoteStore) FilesystemUsed() int64             { return 0 }
func (r *RemoteStore) IsOverQuota(additional int64) bool { return false }

func (r *RemoteStore) GetBucketPolicy(bucket string) ([]byte, error) {
	if _, ok := r.buckets[bucket]; !ok {
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
	if _, ok := r.buckets[bucket]; !ok {
		return errNoSuchBucket
	}
	cp := make([]byte, len(policyJSON))
	copy(cp, policyJSON)
	r.mu.Lock()
	r.bucketPolicy[bucket] = cp
	r.mu.Unlock()
	return nil
}

func (r *RemoteStore) DeleteBucketPolicy(bucket string) error {
	if _, ok := r.buckets[bucket]; !ok {
		return errNoSuchBucket
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bucketPolicy[bucket]; !ok {
		return errNoSuchBucketPolicy
	}
	delete(r.bucketPolicy, bucket)
	return nil
}

func (r *RemoteStore) selectTargets(bucket string, size int64) ([]placement.Target, error) {
	policy := placement.PolicySingle
	if r.resolver != nil {
		policy = r.resolver.PolicyFor(bucket)
	}
	return r.engine.Select(r.reg, policy, size)
}

func (r *RemoteStore) PutObject(bucket, key string, contentType string, meta map[string]string, body io.Reader) (ObjectMeta, error) {
	if _, ok := r.buckets[bucket]; !ok {
		return ObjectMeta{}, errNoSuchBucket
	}
	// Stream: use io.Pipe to avoid holding full body in memory where possible.
	// For MVP, still need size for placement — read size hint from ContentLength if available, else buffer.
	// We buffer to know targets, but stream via Pipe to DataNode.
	data, err := io.ReadAll(body)
	if err != nil {
		return ObjectMeta{}, err
	}
	targets, err := r.selectTargets(bucket, int64(len(data)))
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
	req, _ := http.NewRequest("PUT", urlStr, bytes.NewReader(data))
	req.Header.Set("Content-Type", contentType)
	for k, v := range meta {
		req.Header.Set("X-Amz-Meta-"+k, v)
	}
	if r.token != "" {
		req.Header.Set("X-Internal-Token", r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return ObjectMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return ObjectMeta{}, fmt.Errorf("remote put %d: %s", resp.StatusCode, string(body))
	}
	// Return meta from local computation (ETag via md5 already done in data path would be re-computed by datanode)
	// For MVP, return synthetic meta
	return ObjectMeta{Key: key, Size: int64(len(data)), ETag: resp.Header.Get("ETag"), ContentType: contentType, Meta: meta}, nil
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
	if _, ok := r.buckets[bucket]; !ok {
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
		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == 200 {
			meta := ObjectMeta{Key: key, ContentType: resp.Header.Get("Content-Type")}
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
	if _, ok := r.buckets[bucket]; !ok {
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
	if _, ok := r.buckets[bucket]; !ok {
		return ListObjectsV2Result{}, errNoSuchBucket
	}
	// Fan-out to nodes and merge (dedupe by key)
	seen := map[string]ObjectMeta{}
	var prefixes = map[string]bool{}
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
		var body struct {
			Objects        []ObjectMeta `json:"objects"`
			CommonPrefixes []string     `json:"prefixes"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		for _, o := range body.Objects {
			if _, ok := seen[o.Key]; !ok {
				seen[o.Key] = o
			}
		}
		for _, p := range body.CommonPrefixes {
			prefixes[p] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out ListObjectsV2Result
	for _, k := range keys {
		out.Objects = append(out.Objects, seen[k])
	}
	for p := range prefixes {
		out.CommonPrefixes = append(out.CommonPrefixes, p)
	}
	sort.Strings(out.CommonPrefixes)
	out.KeyCount = len(out.Objects)
	return out, nil
}

// Multipart: delegate to first target DataNode (DataNode's LocalStore handles multipart)
func (r *RemoteStore) CreateMultipartUpload(bucket, key, contentType string, meta map[string]string) (*MultipartUploadState, error) {
	if _, ok := r.buckets[bucket]; !ok {
		return nil, errNoSuchBucket
	}
	targets, err := r.selectTargets(bucket, 0)
	if err != nil {
		return nil, fmt.Errorf("insufficient storage: %w", err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no targets")
	}
	nodeAddr := r.nodeAddr(targets[0].NodeID)
	urlStr := fmt.Sprintf("http://%s/internal/multipart/%s/%s", nodeAddr, url.PathEscape(bucket), url.PathEscape(key))
	req, _ := http.NewRequest("POST", urlStr+"?uploads", nil)
	if r.token != "" {
		req.Header.Set("X-Internal-Token", r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote create multipart %d: %s", resp.StatusCode, string(body))
	}
	var st MultipartUploadState
	_ = json.NewDecoder(resp.Body).Decode(&st)
	return &st, nil
}
func (r *RemoteStore) UploadPart(uploadID string, partNumber int, body io.Reader) (PartRecord, error) {
	// UploadPart is called without bucket/key context — need to store mapping. For MVP, assume last target.
	// This path is used by Router's UploadPart which has uploadID only — delegate to any online node that has the upload.
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		urlStr := fmt.Sprintf("http://%s/internal/multipart/part?uploadId=%s&partNumber=%d", n.Addr, url.QueryEscape(uploadID), partNumber)
		req, _ := http.NewRequest("PUT", urlStr, body)
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == 200 {
			var rec PartRecord
			_ = json.NewDecoder(resp.Body).Decode(&rec)
			_ = resp.Body.Close()
			return rec, nil
		}
		_ = resp.Body.Close()
	}
	return PartRecord{}, fmt.Errorf("no node accepted part")
}
func (r *RemoteStore) ListParts(uploadID string) ([]PartRecord, error) {
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		urlStr := fmt.Sprintf("http://%s/internal/multipart/list?uploadId=%s", n.Addr, url.QueryEscape(uploadID))
		req, _ := http.NewRequest("GET", urlStr, nil)
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == 200 {
			var out []PartRecord
			_ = json.NewDecoder(resp.Body).Decode(&out)
			_ = resp.Body.Close()
			return out, nil
		}
		_ = resp.Body.Close()
	}
	return nil, fmt.Errorf("no such upload")
}
func (r *RemoteStore) ListMultipartUploads(bucket string) ([]*MultipartUploadState, error) {
	seen := map[string]*MultipartUploadState{}
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		urlStr := fmt.Sprintf("http://%s/internal/multipart/list-uploads?bucket=%s", n.Addr, url.QueryEscape(bucket))
		req, _ := http.NewRequest("GET", urlStr, nil)
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			continue
		}
		var list []*MultipartUploadState
		_ = json.NewDecoder(resp.Body).Decode(&list)
		_ = resp.Body.Close()
		for _, u := range list {
			if _, ok := seen[u.UploadID]; !ok {
				seen[u.UploadID] = u
			}
		}
	}
	out := make([]*MultipartUploadState, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	return out, nil
}
func (r *RemoteStore) AbortMultipartUpload(uploadID string) error {
	var lastErr error
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		urlStr := fmt.Sprintf("http://%s/internal/multipart/abort?uploadId=%s", n.Addr, url.QueryEscape(uploadID))
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
		if resp.StatusCode == 204 || resp.StatusCode == 200 {
			return nil
		}
		lastErr = fmt.Errorf("abort %d", resp.StatusCode)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no such upload")
}
func (r *RemoteStore) CompleteMultipartUpload(uploadID string, parts []CompletePart) (ObjectMeta, error) {
	b, _ := json.Marshal(map[string]any{"parts": parts})
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		urlStr := fmt.Sprintf("http://%s/internal/multipart/complete?uploadId=%s", n.Addr, url.QueryEscape(uploadID))
		req, _ := http.NewRequest("POST", urlStr, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if r.token != "" {
			req.Header.Set("X-Internal-Token", r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == 200 {
			var meta ObjectMeta
			_ = json.NewDecoder(resp.Body).Decode(&meta)
			_ = resp.Body.Close()
			return meta, nil
		}
		_ = resp.Body.Close()
	}
	return ObjectMeta{}, fmt.Errorf("complete failed")
}

func (r *RemoteStore) nodeAddr(nodeID string) string {
	for _, n := range r.reg.ListNodes() {
		if n.ID == nodeID {
			return n.Addr
		}
	}
	return ""
}

func (r *RemoteStore) GetUpload(uploadID string) *MultipartUploadState { return nil }
func (r *RemoteStore) MultipartDir(uploadID string) string             { return "" }

var _ ObjectStore = (*RemoteStore)(nil)
