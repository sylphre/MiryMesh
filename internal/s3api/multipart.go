// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// uploadIndexPath returns path to the multipart index JSON file.
func (s *Storage) uploadIndexPath() string { return filepath.Join(s.multipartBase(), "uploads.json") }

// persistUploads writes the uploads map to disk atomically.
func (s *Storage) persistUploads() error {
	if err := os.MkdirAll(s.multipartBase(), 0o750); err != nil {
		return err
	}
	s.mu.RLock()
	data, err := json.Marshal(s.uploads)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := s.uploadIndexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.uploadIndexPath())
}

// loadUploads restores uploads from disk.
func (s *Storage) loadUploads() error {
	data, err := os.ReadFile(s.uploadIndexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var m map[string]*MultipartUploadState
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	s.mu.Lock()
	if s.uploads == nil {
		s.uploads = make(map[string]*MultipartUploadState)
	}
	for k, v := range m {
		// Only restore if multipart dir still exists (parts not cleaned)
		if _, err := os.Stat(s.multipartDir(k)); err == nil {
			s.uploads[k] = v
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *Storage) multipartBase() string { return filepath.Join(s.root, ".multipart") }
func (s *Storage) multipartDir(uploadID string) string {
	return filepath.Join(s.multipartBase(), uploadID)
}

func newUploadID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// CreateMultipartUpload initiates a multipart upload.
func (s *Storage) CreateMultipartUpload(bucket, key, contentType string, meta map[string]string) (*MultipartUploadState, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, errInvalidRequest
	}
	ct := contentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	uploadID := newUploadID()
	dir := s.multipartDir(uploadID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	st := &MultipartUploadState{
		UploadID:    uploadID,
		Bucket:      bucket,
		Key:         key,
		ContentType: ct,
		Meta:        cloneMeta(meta),
		Initiated:   time.Now().UTC(),
		Parts:       make(map[int]PartRecord),
	}
	s.mu.Lock()
	s.uploads[uploadID] = st
	s.mu.Unlock()
	_ = s.persistUploads()
	return st, nil
}

// UploadPart stores a part for an upload.
func (s *Storage) UploadPart(uploadID string, partNumber int, r io.Reader) (PartRecord, error) {
	if partNumber < 1 || partNumber > 10000 {
		return PartRecord{}, fmt.Errorf("partNumber %d out of range 1..10000", partNumber)
	}
	s.mu.RLock()
	st, ok := s.uploads[uploadID]
	s.mu.RUnlock()
	if !ok {
		return PartRecord{}, APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: 404}
	}
	dir := s.multipartDir(uploadID)
	path := filepath.Join(dir, fmt.Sprintf("part-%05d", partNumber))
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return PartRecord{}, err
	}
	h := md5.New()
	size, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return PartRecord{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return PartRecord{}, err
	}
	etag := hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return PartRecord{}, err
	}
	rec := PartRecord{PartNumber: partNumber, ETag: etag, Size: size, LastMod: time.Now().UTC(), Path: path}
	s.mu.Lock()
	st.Parts[partNumber] = rec
	s.mu.Unlock()
	_ = s.persistUploads()
	return rec, nil
}

// ListParts returns parts for an upload ordered by partNumber.
func (s *Storage) ListParts(uploadID string) ([]PartRecord, error) {
	s.mu.RLock()
	st, ok := s.uploads[uploadID]
	s.mu.RUnlock()
	if !ok {
		return nil, APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: 404}
	}
	s.mu.RLock()
	out := make([]PartRecord, 0, len(st.Parts))
	for _, p := range st.Parts {
		out = append(out, p)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].PartNumber < out[j].PartNumber })
	return out, nil
}

// ListMultipartUploads returns in-progress uploads for a bucket.
func (s *Storage) ListMultipartUploads(bucket string) ([]*MultipartUploadState, error) {
	if err := s.ensureBucketExists(bucket); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*MultipartUploadState
	for _, st := range s.uploads {
		if st.Bucket == bucket {
			cpy := *st
			out = append(out, &cpy)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Initiated.Before(out[j].Initiated) })
	return out, nil
}

// getUpload returns upload state for handler validation.
func (s *Storage) getUpload(uploadID string) *MultipartUploadState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if st, ok := s.uploads[uploadID]; ok {
		cpy := *st
		return &cpy
	}
	return nil
}

// AbortMultipartUpload cancels an upload and removes parts.
func (s *Storage) AbortMultipartUpload(uploadID string) error {
	s.mu.Lock()
	_, ok := s.uploads[uploadID]
	if !ok {
		s.mu.Unlock()
		return APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: 404}
	}
	delete(s.uploads, uploadID)
	s.mu.Unlock()
	_ = s.persistUploads()
	_ = os.RemoveAll(s.multipartDir(uploadID))
	return nil
}

// CompleteMultipartUpload assembles parts into the final object.
func (s *Storage) CompleteMultipartUpload(uploadID string, parts []CompletePart) (ObjectMeta, error) {
	s.mu.RLock()
	st, ok := s.uploads[uploadID]
	s.mu.RUnlock()
	if !ok {
		return ObjectMeta{}, APIError{Code: "NoSuchUpload", Message: "The specified upload does not exist", Status: 404}
	}
	if len(parts) == 0 {
		return ObjectMeta{}, errInvalidRequest
	}
	if len(st.Parts) == 0 {
		return ObjectMeta{}, APIError{Code: "InvalidRequest", Message: "No parts uploaded", Status: 400}
	}
	// Validate ETags and compute final multipart ETag (hex(md5(concat md5s))-N).
	var partPaths []string
	var partETags [][]byte
	for _, p := range parts {
		rec, ok := st.Parts[p.PartNumber]
		if !ok {
			return ObjectMeta{}, APIError{Code: "InvalidPart", Message: fmt.Sprintf("Part %d not found", p.PartNumber), Status: 400}
		}
		want := strings.Trim(strings.ToLower(p.ETag), "\"")
		if strings.ToLower(rec.ETag) != want {
			return ObjectMeta{}, APIError{Code: "InvalidPart", Message: fmt.Sprintf("ETag mismatch for part %d", p.PartNumber), Status: 400}
		}
		if _, err := os.Stat(rec.Path); err != nil {
			return ObjectMeta{}, err
		}
		partPaths = append(partPaths, rec.Path)
		etagBytes, _ := hex.DecodeString(rec.ETag)
		partETags = append(partETags, etagBytes)
	}
	concat := make([]byte, 0, len(partETags)*16)
	for _, b := range partETags {
		concat = append(concat, b...)
	}
	finalMD5 := md5.Sum(concat)
	finalETag := fmt.Sprintf("%s-%d", hex.EncodeToString(finalMD5[:]), len(parts))
	// Stream concat parts to final object file (no in-memory assembled).
	dest := s.objectPath(st.Bucket, st.Key)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return ObjectMeta{}, err
	}
	tmp := dest + ".tmp-" + finalETag
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return ObjectMeta{}, err
	}
	var total int64
	for _, path := range partPaths {
		f, err := os.Open(path)
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return ObjectMeta{}, err
		}
		n, err := io.Copy(out, f)
		_ = f.Close()
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			return ObjectMeta{}, err
		}
		total += n
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return ObjectMeta{}, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return ObjectMeta{}, err
	}
	meta := ObjectMeta{Key: st.Key, Size: total, ETag: finalETag, ContentType: st.ContentType, LastMod: time.Now().UTC(), Meta: cloneMeta(st.Meta)}
	s.mu.Lock()
	if s.meta[st.Bucket] == nil {
		s.meta[st.Bucket] = make(map[string]ObjectMeta)
	}
	s.meta[st.Bucket][st.Key] = meta
	delete(s.uploads, uploadID)
	s.mu.Unlock()
	_ = s.persistUploads()
	_ = os.RemoveAll(s.multipartDir(uploadID))
	return meta, nil
}

// CompletePart is a part reference in CompleteMultipartUpload.
type CompletePart struct {
	PartNumber int
	ETag       string
}
