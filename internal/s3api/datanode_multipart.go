// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// DataNodeStore serves multipart uploads for /internal/multipart/*. An upload
// lives on one disk: its parts, and the final object, are written by that
// disk's Storage. Calls that carry only an upload ID look for the disk that
// knows the upload.

var _ datanode.MultipartAPI = (*DataNodeStore)(nil)

// dnMPError converts a store error into one the DataNode API can send with its
// S3 code. Errors without a code are returned unchanged (HTTP 500).
func dnMPError(err error) error {
	if err == nil {
		return nil
	}
	var me *datanode.MultipartError
	if errors.As(err, &me) {
		return err
	}
	if ae, ok := asAPIError(err); ok {
		return &datanode.MultipartError{Code: ae.Code, Message: ae.Message, Status: ae.Status}
	}
	if isNoSpaceError(err) {
		return &datanode.MultipartError{Code: errInsufficient.Code, Message: errInsufficient.Message, Status: errInsufficient.Status}
	}
	return err
}

func dnMPInfo(st *MultipartUploadState) datanode.MultipartUploadInfo {
	return datanode.MultipartUploadInfo{
		UploadID:    st.UploadID,
		Bucket:      st.Bucket,
		Key:         st.Key,
		ContentType: st.ContentType,
		Meta:        cloneMeta(st.Meta),
		Initiated:   st.Initiated,
	}
}

func dnMPPart(p PartRecord) datanode.MultipartPartInfo {
	return datanode.MultipartPartInfo{PartNumber: p.PartNumber, ETag: p.ETag, Size: p.Size, LastMod: p.LastMod}
}

// findUpload returns the disk store that knows the upload. Only the immutable
// fields of the returned state may be read; use ListParts for the parts.
func (d *DataNodeStore) findUpload(uploadID string) (ObjectStore, *MultipartUploadState) {
	for _, s := range d.stores() {
		if st := s.GetUpload(uploadID); st != nil {
			return s, st
		}
	}
	return nil, nil
}

// multipartTarget picks the store for a new upload: the configured disk, or the
// default store when disk is empty. The bucket is created there if missing,
// like a normal write does.
func (d *DataNodeStore) multipartTarget(disk, bucket string) (ObjectStore, error) {
	s := d.store
	if disk != "" {
		d.mu.RLock()
		ds, ok := d.disks[filepath.Clean(disk)]
		d.mu.RUnlock()
		if !ok {
			return nil, &datanode.MultipartError{
				Code:    "InvalidArgument",
				Message: fmt.Sprintf("disk %q is not configured on this node", disk),
				Status:  http.StatusBadRequest,
			}
		}
		s = ds
	}
	if !s.BucketExists(bucket) {
		if err := s.CreateBucket(bucket); err != nil && !s.BucketExists(bucket) {
			return nil, err
		}
	}
	return s, nil
}

// CreateMultipartUpload starts an upload on the given disk.
func (d *DataNodeStore) CreateMultipartUpload(bucket, key, contentType string, meta map[string]string, disk string) (datanode.MultipartUploadInfo, error) {
	s, err := d.multipartTarget(disk, bucket)
	if err != nil {
		return datanode.MultipartUploadInfo{}, dnMPError(err)
	}
	st, err := s.CreateMultipartUpload(bucket, key, contentType, meta)
	if err != nil {
		return datanode.MultipartUploadInfo{}, dnMPError(err)
	}
	return dnMPInfo(st), nil
}

// GetMultipartUpload returns an upload with its parts.
func (d *DataNodeStore) GetMultipartUpload(uploadID string) (datanode.MultipartUploadInfo, error) {
	s, st := d.findUpload(uploadID)
	if st == nil {
		return datanode.MultipartUploadInfo{}, dnMPError(errUploadGone)
	}
	info := dnMPInfo(st)
	parts, err := s.ListParts(uploadID)
	if err != nil {
		return datanode.MultipartUploadInfo{}, dnMPError(err)
	}
	info.Parts = make(map[int]datanode.MultipartPartInfo, len(parts))
	for _, p := range parts {
		info.Parts[p.PartNumber] = dnMPPart(p)
	}
	return info, nil
}

// UploadPart streams one part to the disk that holds the upload.
func (d *DataNodeStore) UploadPart(uploadID string, partNumber int, r io.Reader) (datanode.MultipartPartInfo, error) {
	s, st := d.findUpload(uploadID)
	if st == nil {
		return datanode.MultipartPartInfo{}, dnMPError(errUploadGone)
	}
	rec, err := s.UploadPart(uploadID, partNumber, r)
	if err != nil {
		return datanode.MultipartPartInfo{}, dnMPError(err)
	}
	return dnMPPart(rec), nil
}

// ListParts lists the parts of an upload ordered by part number.
func (d *DataNodeStore) ListParts(uploadID string) ([]datanode.MultipartPartInfo, error) {
	s, st := d.findUpload(uploadID)
	if st == nil {
		return nil, dnMPError(errUploadGone)
	}
	parts, err := s.ListParts(uploadID)
	if err != nil {
		return nil, dnMPError(err)
	}
	out := make([]datanode.MultipartPartInfo, 0, len(parts))
	for _, p := range parts {
		out = append(out, dnMPPart(p))
	}
	return out, nil
}

// ListMultipartUploads lists the uploads of a bucket on every disk. A disk that
// does not have the bucket has no uploads for it.
func (d *DataNodeStore) ListMultipartUploads(bucket string) ([]datanode.MultipartUploadInfo, error) {
	seen := make(map[string]struct{})
	var out []datanode.MultipartUploadInfo
	for _, s := range d.stores() {
		ups, err := s.ListMultipartUploads(bucket)
		if err != nil {
			if errors.Is(err, errNoSuchBucket) {
				continue
			}
			return nil, dnMPError(err)
		}
		for _, st := range ups {
			if st == nil {
				continue
			}
			if _, dup := seen[st.UploadID]; dup {
				continue
			}
			seen[st.UploadID] = struct{}{}
			out = append(out, dnMPInfo(st))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Initiated.Equal(out[j].Initiated) {
			return out[i].Initiated.Before(out[j].Initiated)
		}
		return out[i].UploadID < out[j].UploadID
	})
	return out, nil
}

// AbortMultipartUpload removes an upload and its parts.
func (d *DataNodeStore) AbortMultipartUpload(uploadID string) error {
	s, st := d.findUpload(uploadID)
	if st == nil {
		return dnMPError(errUploadGone)
	}
	return dnMPError(s.AbortMultipartUpload(uploadID))
}

// CompleteMultipartUpload assembles the parts into the object on the same disk.
func (d *DataNodeStore) CompleteMultipartUpload(uploadID string, parts []datanode.MultipartCompletePart) (datanode.ObjectInfo, error) {
	s, st := d.findUpload(uploadID)
	if st == nil {
		return datanode.ObjectInfo{}, dnMPError(errUploadGone)
	}
	cp := make([]CompletePart, len(parts))
	for i, p := range parts {
		cp[i] = CompletePart{PartNumber: p.PartNumber, ETag: p.ETag}
	}
	m, err := s.CompleteMultipartUpload(uploadID, cp)
	if err != nil {
		return datanode.ObjectInfo{}, dnMPError(err)
	}
	return toObjectInfo(m), nil
}
