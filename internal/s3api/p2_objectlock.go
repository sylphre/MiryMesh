// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Object-level ?retention and ?legal-hold.
//
// Both are stored as s3_subresources documents of the object (kind =
// "retention" | "legal-hold"). While a legal hold is ON, or a retention period
// has not expired, the object cannot be deleted or overwritten: MiryMesh keeps
// no object versions, so an overwrite would destroy the protected data.
// GOVERNANCE retention can be bypassed with x-amz-bypass-governance-retention:
// true; COMPLIANCE retention never can.

const (
	lockKindRetention = "retention"
	lockKindLegalHold = "legal-hold"

	lockModeGovernance = "GOVERNANCE"
	lockModeCompliance = "COMPLIANCE"

	lockBypassHeader = "X-Amz-Bypass-Governance-Retention"
)

var (
	errNoSuchObjectLock = APIError{
		Code:    "NoSuchObjectLockConfiguration",
		Message: "The specified object does not have a ObjectLock configuration",
		Status:  http.StatusNotFound,
	}
	errObjectLocked = APIError{
		Code:    "AccessDenied",
		Message: "Access Denied because object protected by object lock.",
		Status:  http.StatusForbidden,
	}
	errObjectLockInternal = APIError{
		Code:    "InternalError",
		Message: "We encountered an internal error. Please try again.",
		Status:  http.StatusInternalServerError,
	}
)

// p2LockMu serializes read-modify-write cycles on retention documents.
var p2LockMu sync.Mutex

type p2RetentionDoc struct {
	XMLName         xml.Name
	Mode            string `xml:"Mode"`
	RetainUntilDate string `xml:"RetainUntilDate"`
}

type p2LegalHoldDoc struct {
	XMLName xml.Name
	Status  string `xml:"Status"`
}

// p2DecodeStrictXML decodes exactly one root element; anything after it
// (other than whitespace, comments and processing instructions) is rejected.
func p2DecodeStrictXML(body []byte, v interface{}) bool {
	raw := p2StripProlog(body)
	if raw == "" {
		return false
	}
	dec := xml.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(v); err != nil {
		return false
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return true
		}
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return false
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return false
			}
		}
	}
}

func p2RootMatches(name xml.Name, local string) bool {
	return name.Local == local && (name.Space == "" || name.Space == p2XMLNS)
}

// p2ParseRetention validates a Retention document. With allowEmpty, an empty
// <Retention/> is accepted and returned as mode == "" (it clears the retention).
func p2ParseRetention(body []byte, allowEmpty bool) (string, time.Time, *APIError) {
	var doc p2RetentionDoc
	if !p2DecodeStrictXML(body, &doc) || !p2RootMatches(doc.XMLName, "Retention") {
		return "", time.Time{}, p2Malformed()
	}
	mode := strings.TrimSpace(doc.Mode)
	date := strings.TrimSpace(doc.RetainUntilDate)
	if mode == "" && date == "" {
		if allowEmpty {
			return "", time.Time{}, nil
		}
		return "", time.Time{}, p2Malformed()
	}
	if mode != lockModeGovernance && mode != lockModeCompliance {
		return "", time.Time{}, p2Malformed()
	}
	until, err := time.Parse(time.RFC3339, date)
	if err != nil {
		return "", time.Time{}, p2Malformed()
	}
	return mode, until.UTC(), nil
}

func renderRetention(mode string, until time.Time) string {
	return `<Retention xmlns="` + p2XMLNS + `"><Mode>` + mode + `</Mode><RetainUntilDate>` +
		until.UTC().Format("2006-01-02T15:04:05.000Z") + `</RetainUntilDate></Retention>`
}

func p2ParseLegalHold(body []byte) (string, *APIError) {
	var doc p2LegalHoldDoc
	if !p2DecodeStrictXML(body, &doc) || !p2RootMatches(doc.XMLName, "LegalHold") {
		return "", p2Malformed()
	}
	status := strings.TrimSpace(doc.Status)
	if status != "ON" && status != "OFF" {
		return "", p2Malformed()
	}
	return status, nil
}

func renderLegalHold(status string) string {
	return `<LegalHold xmlns="` + p2XMLNS + `"><Status>` + status + `</Status></LegalHold>`
}

func p2Bypass(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(lockBypassHeader)), "true")
}

// loadRetention returns the stored retention of an object.
func loadRetention(ss SubresourceStore, bucket, key string) (string, time.Time, bool, error) {
	doc, found, err := ss.GetSubresource(bucket, key, lockKindRetention)
	if err != nil || !found {
		return "", time.Time{}, false, err
	}
	mode, until, apiErr := p2ParseRetention([]byte(doc), false)
	if apiErr != nil {
		return "", time.Time{}, false, errors.New("stored retention document is corrupt")
	}
	return mode, until, true, nil
}

// loadLegalHold returns the stored legal hold status ("ON" / "OFF").
func loadLegalHold(ss SubresourceStore, bucket, key string) (string, bool, error) {
	doc, found, err := ss.GetSubresource(bucket, key, lockKindLegalHold)
	if err != nil || !found {
		return "", false, err
	}
	status, apiErr := p2ParseLegalHold([]byte(doc))
	if apiErr != nil {
		return "", false, errors.New("stored legal hold document is corrupt")
	}
	return status, true, nil
}

// checkObjectLock reports whether deleting or overwriting the object is
// forbidden. When blocked it returns the error to answer with.
func (rt *Router) checkObjectLock(r *http.Request, bucket, key string) (APIError, bool) {
	ss := rt.subStore()
	status, found, err := loadLegalHold(ss, bucket, key)
	if err != nil {
		return errObjectLockInternal, true
	}
	if found && status == "ON" {
		return errObjectLocked, true
	}
	mode, until, found, err := loadRetention(ss, bucket, key)
	if err != nil {
		return errObjectLockInternal, true
	}
	if found && time.Now().UTC().Before(until) {
		if mode == lockModeCompliance || !p2Bypass(r) {
			return errObjectLocked, true
		}
	}
	return APIError{}, false
}

// rejectLocked writes the error response and returns true when the object is protected.
func (rt *Router) rejectLocked(w http.ResponseWriter, r *http.Request, bucket, key string) bool {
	apiErr, locked := rt.checkObjectLock(r, bucket, key)
	if locked {
		writeError(w, r, apiErr)
	}
	return locked
}

// handleObjectLock serves GET / PUT of ?retention and ?legal-hold on an object.
func (rt *Router) handleObjectLock(w http.ResponseWriter, r *http.Request, bucket, key, sub string) {
	if !rt.requireTarget(w, r, bucket, key) {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodPut:
	default:
		writeError(w, r, errMethodNotAllowedP2)
		return
	}
	if sub == lockKindLegalHold {
		rt.handleLegalHold(w, r, bucket, key)
		return
	}
	rt.handleRetention(w, r, bucket, key)
}

func (rt *Router) handleLegalHold(w http.ResponseWriter, r *http.Request, bucket, key string) {
	ss := rt.subStore()
	if r.Method == http.MethodGet {
		status, found, err := loadLegalHold(ss, bucket, key)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		if !found {
			writeError(w, r, errNoSuchObjectLock)
			return
		}
		writeP2XML(w, http.StatusOK, renderLegalHold(status))
		return
	}
	body, ok := readP2Body(w, r)
	if !ok {
		return
	}
	status, apiErr := p2ParseLegalHold(body)
	if apiErr != nil {
		writeError(w, r, *apiErr)
		return
	}
	if err := ss.PutSubresource(bucket, key, lockKindLegalHold, renderLegalHold(status)); err != nil {
		writeSubresourceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (rt *Router) handleRetention(w http.ResponseWriter, r *http.Request, bucket, key string) {
	ss := rt.subStore()
	if r.Method == http.MethodGet {
		mode, until, found, err := loadRetention(ss, bucket, key)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		if !found {
			writeError(w, r, errNoSuchObjectLock)
			return
		}
		writeP2XML(w, http.StatusOK, renderRetention(mode, until))
		return
	}
	body, ok := readP2Body(w, r)
	if !ok {
		return
	}
	mode, until, apiErr := p2ParseRetention(body, true)
	if apiErr != nil {
		writeError(w, r, *apiErr)
		return
	}
	bypass := p2Bypass(r)
	now := time.Now().UTC()

	p2LockMu.Lock()
	defer p2LockMu.Unlock()
	curMode, curUntil, found, err := loadRetention(ss, bucket, key)
	if err != nil {
		writeStoreFailure(w, r, err)
		return
	}
	active := found && now.Before(curUntil)

	if mode == "" { // <Retention/> clears the retention
		if active && (curMode == lockModeCompliance || !bypass) {
			writeError(w, r, errObjectLocked)
			return
		}
		if found {
			if err := ss.DeleteSubresource(bucket, key, lockKindRetention); err != nil {
				writeStoreFailure(w, r, err)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if !until.After(now) {
		writeError(w, r, *p2BadRequest("InvalidArgument", "The retain until date must be in the future!"))
		return
	}
	if active {
		switch curMode {
		case lockModeCompliance:
			// COMPLIANCE can only be extended, never shortened or relaxed.
			if mode != lockModeCompliance || until.Before(curUntil) {
				writeError(w, r, errObjectLocked)
				return
			}
		case lockModeGovernance:
			if mode == lockModeGovernance && until.Before(curUntil) && !bypass {
				writeError(w, r, errObjectLocked)
				return
			}
		}
	}
	if err := ss.PutSubresource(bucket, key, lockKindRetention, renderRetention(mode, until)); err != nil {
		writeSubresourceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
