// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// subQuota is the s3_subresources.kind of the bucket-level quota document.
const subQuota = "quota"

// quotaUsageTTL bounds how long a computed bucket usage is reused. Accepted
// writes are added to the cached value (reservation), so the limit holds
// between refreshes; the refresh corrects drift caused by deletes and failed writes.
const quotaUsageTTL = 3 * time.Second

// ErrQuotaUnsupported is returned when the storage backend cannot persist a quota.
var ErrQuotaUnsupported = errSubresourceUnsupported

type quotaDoc struct {
	MaxBytes int64 `json:"max_bytes"`
}

func subStoreOf(store ObjectStore) SubresourceStore {
	if ss, ok := store.(SubresourceStore); ok {
		return ss
	}
	return noSubresources{}
}

// GetBucketQuota returns the storage limit of bucket in bytes; 0 means unlimited.
func GetBucketQuota(store ObjectStore, bucket string) (int64, error) {
	body, found, err := subStoreOf(store).GetSubresource(bucket, "", subQuota)
	if err != nil || !found {
		return 0, err
	}
	var d quotaDoc
	if uerr := json.Unmarshal([]byte(body), &d); uerr != nil {
		return 0, uerr
	}
	if d.MaxBytes < 0 {
		return 0, nil
	}
	return d.MaxBytes, nil
}

// SetBucketQuota stores the storage limit of bucket in bytes; 0 removes the limit.
func SetBucketQuota(store ObjectStore, bucket string, maxBytes int64) error {
	if maxBytes < 0 {
		return errors.New("max_bytes must not be negative")
	}
	ss := subStoreOf(store)
	if maxBytes == 0 {
		return ss.DeleteSubresource(bucket, "", subQuota)
	}
	b, err := json.Marshal(quotaDoc{MaxBytes: maxBytes})
	if err != nil {
		return err
	}
	return ss.PutSubresource(bucket, "", subQuota, string(b))
}

// BucketUsage returns the bytes stored in bucket: all objects plus the parts of
// in-progress multipart uploads. The value may be up to quotaUsageTTL old.
func BucketUsage(store ObjectStore, bucket string) (int64, error) {
	return quotas.usage(store, bucket)
}

// CheckBucketQuota reports an InsufficientStorage (507) APIError when storing
// `incoming` more bytes in bucket would exceed its quota. key is the object that
// will be (over)written, so replacing an object only counts the size difference;
// pass "" for multipart parts. incoming < 0 means the size is unknown: the
// request is then only refused when the bucket is already full.
func CheckBucketQuota(store ObjectStore, bucket, key string, incoming int64) error {
	limit, err := GetBucketQuota(store, bucket)
	if err != nil {
		return err
	}
	if limit <= 0 {
		return nil
	}
	delta := incoming
	if incoming > 0 && key != "" {
		if m, herr := store.HeadObject(bucket, key); herr == nil {
			delta -= m.Size
		}
	}
	return quotas.reserve(store, bucket, limit, delta, incoming < 0)
}

func errBucketQuotaExceeded(limit int64) APIError {
	return APIError{
		Code:    errInsufficient.Code,
		Message: "The bucket storage quota (" + strconv.FormatInt(limit, 10) + " bytes) would be exceeded.",
		Status:  http.StatusInsufficientStorage,
	}
}

// requestBodySize is the payload size of a write request, or -1 when unknown.
// aws-chunked bodies report the decoded size in X-Amz-Decoded-Content-Length.
func requestBodySize(r *http.Request) int64 {
	if v := r.Header.Get("X-Amz-Decoded-Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return r.ContentLength
}

// rejectOverQuota writes the error response and returns true when the write must be refused.
func (rt *Router) rejectOverQuota(w http.ResponseWriter, r *http.Request, bucket, key string, incoming int64) bool {
	if err := CheckBucketQuota(rt.store, bucket, key, incoming); err != nil {
		writeFailure(w, r, err)
		return true
	}
	return false
}

type usageEntry struct {
	used int64
	at   time.Time
}

type quotaTracker struct {
	mu sync.Mutex
	m  map[string]usageEntry
}

var quotas = &quotaTracker{m: make(map[string]usageEntry)}

func quotaCacheKey(store ObjectStore, bucket string) string {
	return fmt.Sprintf("%p|%s", store, bucket)
}

// usage returns the cached usage when fresh, otherwise recomputes it.
func (t *quotaTracker) usage(store ObjectStore, bucket string) (int64, error) {
	k := quotaCacheKey(store, bucket)
	t.mu.Lock()
	e, ok := t.m[k]
	t.mu.Unlock()
	if ok && time.Since(e.at) <= quotaUsageTTL {
		return e.used, nil
	}
	used, err := computeBucketUsage(store, bucket)
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if cur, ok2 := t.m[k]; ok2 && time.Since(cur.at) <= quotaUsageTTL {
		return cur.used, nil
	}
	t.m[k] = usageEntry{used: used, at: time.Now()}
	return used, nil
}

// reserve checks the limit and, when the write is accepted, adds it to the cached usage.
func (t *quotaTracker) reserve(store ObjectStore, bucket string, limit, delta int64, unknownSize bool) error {
	if _, err := t.usage(store, bucket); err != nil {
		return err
	}
	k := quotaCacheKey(store, bucket)
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.m[k]
	if unknownSize {
		if e.used >= limit {
			return errBucketQuotaExceeded(limit)
		}
		return nil
	}
	if delta <= 0 {
		return nil
	}
	if e.used+delta > limit {
		return errBucketQuotaExceeded(limit)
	}
	e.used += delta
	t.m[k] = e
	return nil
}

// computeBucketUsage sums object sizes and the parts of unfinished multipart uploads.
func computeBucketUsage(store ObjectStore, bucket string) (int64, error) {
	var total int64
	token := ""
	for {
		res, err := store.ListObjectsV2(bucket, "", "", token, 1000)
		if err != nil {
			return 0, err
		}
		for _, o := range res.Objects {
			total += o.Size
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			break
		}
		token = res.NextContinuationToken
	}
	// Pending parts occupy disk until the upload is completed or aborted.
	if ups, err := store.ListMultipartUploads(bucket); err == nil {
		for _, u := range ups {
			parts, perr := store.ListParts(u.UploadID)
			if perr != nil {
				continue
			}
			for _, p := range parts {
				total += p.Size
			}
		}
	}
	return total, nil
}
