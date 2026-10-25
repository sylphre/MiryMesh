// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// Multipart uploads through the Master.
//
// An upload is created on one DataNode disk chosen by the placement engine. Its
// parts and the final object stay on that disk, so every later call (part,
// list, abort, complete) goes to the node that holds the upload. The Master
// remembers the node per upload ID in memory and, after a restart or for an
// upload it has not seen, asks the online nodes. Part bodies are streamed to
// the node and never held in Master memory.

const (
	// mpCompleteTimeout bounds CompleteMultipartUpload: the node copies every
	// part into the final object before it answers.
	mpCompleteTimeout = 30 * time.Minute
	// mpMaxReply limits the JSON documents read back from a DataNode.
	mpMaxReply = 8 << 20
)

func (r *RemoteStore) rememberUpload(uploadID, nodeID string) {
	r.mpMu.Lock()
	if r.mpNode == nil {
		r.mpNode = make(map[string]string)
	}
	r.mpNode[uploadID] = nodeID
	r.mpMu.Unlock()
}

func (r *RemoteStore) forgetUpload(uploadID string) {
	r.mpMu.Lock()
	delete(r.mpNode, uploadID)
	r.mpMu.Unlock()
}

func (r *RemoteStore) uploadNode(uploadID string) string {
	r.mpMu.Lock()
	defer r.mpMu.Unlock()
	return r.mpNode[uploadID]
}

func (r *RemoteStore) completeClient() *http.Client {
	r.mpMu.Lock()
	defer r.mpMu.Unlock()
	if r.mpComplete == nil {
		r.mpComplete = &http.Client{
			Timeout:   mpCompleteTimeout,
			Transport: &nodeTokenTransport{reg: r.reg, token: r.token, base: http.DefaultTransport},
		}
	}
	return r.mpComplete
}

func (r *RemoteStore) mpURL(addr, suffix string) string {
	return "http://" + addr + "/internal/multipart/" + suffix
}

func (r *RemoteStore) mpDo(client *http.Client, method, urlStr string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if r.token != "" {
		req.Header.Set("X-Internal-Token", r.token)
	}
	return client.Do(req)
}

func decodeMPReply(body io.Reader, v any) error {
	return json.NewDecoder(io.LimitReader(body, mpMaxReply)).Decode(v)
}

// mpRemoteError turns a failed DataNode reply into an error. A reply with an S3
// error code becomes an APIError, so the S3 client sees the same error as with
// a local store.
func mpRemoteError(resp *http.Response, what string) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &e) == nil && e.Code != "" {
		return APIError{Code: e.Code, Message: e.Message, Status: resp.StatusCode}
	}
	msg := strings.TrimSpace(string(b))
	if resp.StatusCode == http.StatusNotFound {
		msg += " (the DataNode may be too old for multipart uploads: run install.sh --update on it)"
	}
	return fmt.Errorf("%s: DataNode answered %d: %s", what, resp.StatusCode, msg)
}

// CreateMultipartUpload starts an upload on the node and disk the placement
// engine picks (the first target, like PutObject).
func (r *RemoteStore) CreateMultipartUpload(bucket, key, contentType string, meta map[string]string) (*MultipartUploadState, error) {
	if !r.BucketExists(bucket) {
		return nil, errNoSuchBucket
	}
	targets, err := r.selectTargets(bucket, 0)
	if err != nil {
		return nil, fmt.Errorf("insufficient storage: %w", err)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no targets")
	}
	tgt := targets[0]
	addr := r.nodeAddr(tgt.NodeID)
	if addr == "" {
		return nil, fmt.Errorf("node %s not found", tgt.NodeID)
	}
	q := url.Values{"bucket": {bucket}, "key": {key}}
	if tgt.Path != "" {
		q.Set("disk", tgt.Path)
	}
	payload, err := json.Marshal(map[string]any{"content_type": contentType, "meta": meta})
	if err != nil {
		return nil, err
	}
	resp, err := r.mpDo(r.client, http.MethodPost, r.mpURL(addr, "uploads?"+q.Encode()), bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, mpRemoteError(resp, "create multipart upload")
	}
	var st MultipartUploadState
	if err := decodeMPReply(resp.Body, &st); err != nil || st.UploadID == "" {
		return nil, fmt.Errorf("create multipart upload: bad reply from DataNode %s", tgt.NodeID)
	}
	r.rememberUpload(st.UploadID, tgt.NodeID)
	return &st, nil
}

// fetchUpload asks one node for an upload. found is false when the node does not know it.
func (r *RemoteStore) fetchUpload(addr, uploadID string) (st *MultipartUploadState, found bool, err error) {
	resp, err := r.mpDo(r.client, http.MethodGet, r.mpURL(addr, "uploads/"+url.PathEscape(uploadID)), nil, "")
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var s MultipartUploadState
		if err := decodeMPReply(resp.Body, &s); err != nil {
			return nil, false, err
		}
		return &s, true, nil
	case http.StatusNotFound:
		return nil, false, nil
	}
	return nil, false, mpRemoteError(resp, "look up multipart upload")
}

// lookupUpload finds the node that holds the upload and returns the upload.
// It returns errUploadGone when no online node knows it, and the error of a
// node that could not answer when the upload may be on that node.
func (r *RemoteStore) lookupUpload(uploadID string) (string, *MultipartUploadState, error) {
	if id := r.uploadNode(uploadID); id != "" {
		if addr := r.nodeAddr(id); addr != "" {
			st, found, err := r.fetchUpload(addr, uploadID)
			if err != nil {
				return "", nil, err
			}
			if found {
				return id, st, nil
			}
		}
		r.forgetUpload(uploadID)
	}
	var firstErr error
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		st, found, err := r.fetchUpload(n.Addr, uploadID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if found {
			r.rememberUpload(uploadID, n.ID)
			return n.ID, st, nil
		}
	}
	if firstErr != nil {
		return "", nil, firstErr
	}
	return "", nil, errUploadGone
}

// uploadAddr returns the address of the node that holds the upload, without a
// request when the node is already known.
func (r *RemoteStore) uploadAddr(uploadID string) (string, error) {
	if id := r.uploadNode(uploadID); id != "" {
		if addr := r.nodeAddr(id); addr != "" {
			return addr, nil
		}
		r.forgetUpload(uploadID)
	}
	id, _, err := r.lookupUpload(uploadID)
	if err != nil {
		return "", err
	}
	addr := r.nodeAddr(id)
	if addr == "" {
		return "", fmt.Errorf("node %s not found", id)
	}
	return addr, nil
}

// GetUpload returns the upload, or nil when no node knows it.
func (r *RemoteStore) GetUpload(uploadID string) *MultipartUploadState {
	_, st, err := r.lookupUpload(uploadID)
	if err != nil {
		if ae, ok := asAPIError(err); !ok || ae.Code != errUploadGone.Code {
			slog.Warn("multipart: could not look up upload", "upload", uploadID, "err", err)
		}
		return nil
	}
	return st
}

// UploadPart streams one part to the node that holds the upload.
func (r *RemoteStore) UploadPart(uploadID string, partNumber int, body io.Reader) (PartRecord, error) {
	addr, err := r.uploadAddr(uploadID)
	if err != nil {
		return PartRecord{}, err
	}
	target := r.mpURL(addr, fmt.Sprintf("uploads/%s/parts/%d", url.PathEscape(uploadID), partNumber))
	// The part size is not known here: the S3 layer may wrap the body in a
	// checksum or encryption reader. The body therefore goes out chunked. If the
	// source fails (for example a checksum mismatch found at its end), the
	// transport aborts the request without the final chunk, the DataNode discards
	// the part, and the original error is returned to the caller.
	src := &readErrTracker{src: body}
	resp, err := r.mpDo(r.bodyClient(), http.MethodPut, target, src, "application/octet-stream")
	if readErr := src.Err(); readErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return PartRecord{}, readErr
	}
	if err != nil {
		return PartRecord{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			r.forgetUpload(uploadID)
		}
		return PartRecord{}, mpRemoteError(resp, "upload part")
	}
	var rec PartRecord
	if err := decodeMPReply(resp.Body, &rec); err != nil {
		return PartRecord{}, fmt.Errorf("upload part: bad reply from DataNode: %w", err)
	}
	if rec.PartNumber == 0 {
		rec.PartNumber = partNumber
	}
	return rec, nil
}

// ListParts lists the parts of an upload ordered by part number.
func (r *RemoteStore) ListParts(uploadID string) ([]PartRecord, error) {
	addr, err := r.uploadAddr(uploadID)
	if err != nil {
		return nil, err
	}
	resp, err := r.mpDo(r.client, http.MethodGet, r.mpURL(addr, "uploads/"+url.PathEscape(uploadID)+"/parts"), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			r.forgetUpload(uploadID)
		}
		return nil, mpRemoteError(resp, "list parts")
	}
	var out []PartRecord
	if err := decodeMPReply(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("list parts: bad reply from DataNode: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PartNumber < out[j].PartNumber })
	return out, nil
}

func (r *RemoteStore) listNodeUploads(addr, bucket string) ([]*MultipartUploadState, error) {
	resp, err := r.mpDo(r.client, http.MethodGet, r.mpURL(addr, "uploads?bucket="+url.QueryEscape(bucket)), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, mpRemoteError(resp, "list multipart uploads")
	}
	var out []*MultipartUploadState
	if err := decodeMPReply(resp.Body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListMultipartUploads merges the in-progress uploads of all online nodes.
func (r *RemoteStore) ListMultipartUploads(bucket string) ([]*MultipartUploadState, error) {
	if !r.BucketExists(bucket) {
		return nil, errNoSuchBucket
	}
	seen := make(map[string]*MultipartUploadState)
	var firstErr error
	answered := 0
	for _, n := range r.reg.ListNodes() {
		if n.Status != datanode.StatusOnline {
			continue
		}
		list, err := r.listNodeUploads(n.Addr, bucket)
		if err != nil {
			slog.Warn("multipart: node could not list uploads", "node", n.ID, "bucket", bucket, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		answered++
		for _, u := range list {
			if u == nil || u.UploadID == "" {
				continue
			}
			if _, dup := seen[u.UploadID]; !dup {
				seen[u.UploadID] = u
				r.rememberUpload(u.UploadID, n.ID)
			}
		}
	}
	if answered == 0 && firstErr != nil {
		return nil, firstErr
	}
	out := make([]*MultipartUploadState, 0, len(seen))
	for _, u := range seen {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Initiated.Equal(out[j].Initiated) {
			return out[i].Initiated.Before(out[j].Initiated)
		}
		return out[i].UploadID < out[j].UploadID
	})
	return out, nil
}

// AbortMultipartUpload removes the upload and its parts from the node that holds it.
func (r *RemoteStore) AbortMultipartUpload(uploadID string) error {
	addr, err := r.uploadAddr(uploadID)
	if err != nil {
		return err
	}
	resp, err := r.mpDo(r.client, http.MethodDelete, r.mpURL(addr, "uploads/"+url.PathEscape(uploadID)), nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		r.forgetUpload(uploadID)
		return nil
	case http.StatusNotFound:
		r.forgetUpload(uploadID)
	}
	return mpRemoteError(resp, "abort multipart upload")
}

// CompleteMultipartUpload has the node assemble the parts into the object. The
// node copies the parts from disk to disk, so the call can take long for large
// objects and uses a client with a long timeout instead of the 30s one.
func (r *RemoteStore) CompleteMultipartUpload(uploadID string, parts []CompletePart) (ObjectMeta, error) {
	addr, err := r.uploadAddr(uploadID)
	if err != nil {
		return ObjectMeta{}, err
	}
	payload, err := json.Marshal(struct {
		Parts []CompletePart `json:"parts"`
	}{Parts: parts})
	if err != nil {
		return ObjectMeta{}, err
	}
	resp, err := r.mpDo(r.completeClient(), http.MethodPost, r.mpURL(addr, "uploads/"+url.PathEscape(uploadID)+"/complete"), bytes.NewReader(payload), "application/json")
	if err != nil {
		return ObjectMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			r.forgetUpload(uploadID)
		}
		return ObjectMeta{}, mpRemoteError(resp, "complete multipart upload")
	}
	var meta ObjectMeta
	if err := decodeMPReply(resp.Body, &meta); err != nil {
		return ObjectMeta{}, fmt.Errorf("complete multipart upload: bad reply from DataNode: %w", err)
	}
	r.forgetUpload(uploadID)
	return meta, nil
}
