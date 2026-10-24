// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// DataNodeStore adapts an ObjectStore to the store interface served by the
// DataNode /internal/objects API. datanode cannot import s3api (s3api imports
// datanode), so the conversion between the two metadata types lives here.
type DataNodeStore struct {
	store ObjectStore
	// disks maps a cleaned, configured disk path to its store. It is read-only
	// after construction, so it needs no lock.
	disks map[string]ObjectStore
	// all lists the stores searched when a request names no disk: the default
	// store first, then the other disks in path order. Empty means single-disk.
	all []ObjectStore
}

// NewDataNodeStore wraps store for use with datanode.NewHandlerWithObjects.
// store must not be nil.
func NewDataNodeStore(store ObjectStore) *DataNodeStore {
	return &DataNodeStore{store: store}
}

// NewDataNodeStoreWithDisks is like NewDataNodeStore but also serves every disk
// in disks (keyed by its configured path) when a request carries ?disk=<path>.
// store is used when no disk is requested. store must not be nil.
func NewDataNodeStoreWithDisks(store ObjectStore, disks map[string]ObjectStore) *DataNodeStore {
	cleaned := make(map[string]ObjectStore, len(disks))
	for p, s := range disks {
		if s != nil {
			cleaned[filepath.Clean(p)] = s
		}
	}
	paths := make([]string, 0, len(cleaned))
	for p := range cleaned {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	all := []ObjectStore{store}
	for _, p := range paths {
		if cleaned[p] != store {
			all = append(all, cleaned[p])
		}
	}
	return &DataNodeStore{store: store, disks: cleaned, all: all}
}

// stores returns the stores to search when a request names no disk.
func (d *DataNodeStore) stores() []ObjectStore {
	if len(d.all) == 0 {
		return []ObjectStore{d.store}
	}
	return d.all
}

// Disk implements datanode.DiskResolver. It returns the store for one
// configured disk and creates bucket on that disk if it does not exist yet.
// Paths that are not configured disks are rejected, so a caller cannot make
// the DataNode write outside its configured directories.
func (d *DataNodeStore) Disk(path, bucket string) (datanode.ObjectAPI, error) {
	ds, ok := d.disks[filepath.Clean(path)]
	if !ok {
		return nil, fmt.Errorf("disk %q is not configured on this node", path)
	}
	if !ds.BucketExists(bucket) {
		if err := ds.CreateBucket(bucket); err != nil {
			return nil, fmt.Errorf("create bucket %q on disk %q: %w", bucket, path, err)
		}
	}
	return &DataNodeStore{store: ds}, nil
}

func toObjectInfo(m ObjectMeta) datanode.ObjectInfo {
	return datanode.ObjectInfo{
		Key:         m.Key,
		Size:        m.Size,
		ETag:        m.ETag,
		ContentType: m.ContentType,
		LastMod:     m.LastMod,
		Meta:        m.Meta,
	}
}

// PutObject stores an object.
func (d *DataNodeStore) PutObject(bucket, key, contentType string, meta map[string]string, r io.Reader) (datanode.ObjectInfo, error) {
	m, err := d.store.PutObject(bucket, key, contentType, meta, r)
	if err != nil {
		return datanode.ObjectInfo{}, err
	}
	return toObjectInfo(m), nil
}

// GetObject opens an object for reading.
// Without ?disk= every disk is searched, default disk first.
func (d *DataNodeStore) GetObject(bucket, key string) (datanode.ObjectInfo, io.ReadCloser, error) {
	var firstErr error
	for _, s := range d.stores() {
		m, rc, err := s.GetObject(bucket, key)
		if err == nil {
			return toObjectInfo(m), rc, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return datanode.ObjectInfo{}, nil, firstErr
}

// HeadObject returns object metadata.
// Without ?disk= every disk is searched, default disk first.
func (d *DataNodeStore) HeadObject(bucket, key string) (datanode.ObjectInfo, error) {
	var firstErr error
	for _, s := range d.stores() {
		m, err := s.HeadObject(bucket, key)
		if err == nil {
			return toObjectInfo(m), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return datanode.ObjectInfo{}, firstErr
}

// DeleteObject removes an object.
// Without ?disk= the object is removed from every disk that has the bucket.
// A disk without the bucket is not an error as long as one disk succeeded.
func (d *DataNodeStore) DeleteObject(bucket, key string) error {
	var realErr, bucketErr error
	deleted := false
	for _, s := range d.stores() {
		err := s.DeleteObject(bucket, key)
		switch {
		case err == nil:
			deleted = true
		case errors.Is(err, errNoSuchBucket):
			if bucketErr == nil {
				bucketErr = err
			}
		default:
			if realErr == nil {
				realErr = err
			}
		}
	}
	if realErr != nil {
		return realErr
	}
	if deleted {
		return nil
	}
	return bucketErr
}

// ListObjectsV2 lists objects and common prefixes.
// Without ?disk= every disk is listed and the pages are merged with
// mergeListings: duplicates are dropped (default disk wins), entries are
// ordered, at most maxKeys entries are returned, and IsTruncated /
// NextContinuationToken let the caller fetch the next page. A disk that does
// not have the bucket is skipped as long as one disk could list it.
func (d *DataNodeStore) ListObjectsV2(bucket, prefix, delimiter, continuationToken string, maxKeys int) (datanode.ObjectListing, error) {
	var firstErr error
	pages := make([]ListObjectsV2Result, 0, len(d.stores()))
	for _, s := range d.stores() {
		res, err := s.ListObjectsV2(bucket, prefix, delimiter, continuationToken, maxKeys)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pages = append(pages, res)
	}
	if len(pages) == 0 {
		return datanode.ObjectListing{}, firstErr
	}
	merged := mergeListings(pages, maxKeys)
	out := datanode.ObjectListing{
		CommonPrefixes:        merged.CommonPrefixes,
		IsTruncated:           merged.IsTruncated,
		NextContinuationToken: merged.NextContinuationToken,
	}
	for _, o := range merged.Objects {
		out.Objects = append(out.Objects, toObjectInfo(o))
	}
	return out, nil
}
