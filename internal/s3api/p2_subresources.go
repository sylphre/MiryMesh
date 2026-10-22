// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
)

// P2 subresources (?acl, ?tagging, ?cors, ...).
//
// ?acl, ?tagging, ?cors and ?lifecycle are persisted through SubresourceStore
// (table s3_subresources, in-memory when no DB is attached); their handlers
// live in p2_config.go and CORS enforcement in p2_cors.go. Everything MiryMesh
// cannot honour for real stays an explicit 501 NotImplemented instead of being
// silently accepted:
//   - ?versioning reports "unversioned" and refuses to enable (no version history),
//   - analytics/inventory/metrics/retention/legal-hold/select.

const p2XMLNS = "http://s3.amazonaws.com/doc/2006-03-01/"

// p2Subresources lists every query subresource handled by handleP2Subresource.
// Names that are not given a dedicated handler below answer 501.
var p2Subresources = []string{
	"versioning", "acl", "tagging", "cors", "lifecycle",
	"analytics", "inventory", "metrics", "retention", "legal-hold", "select",
}

const (
	p2OwnerID = "mirymesh"

	aclPrivateXML = `<AccessControlPolicy xmlns="` + p2XMLNS + `">` +
		`<Owner><ID>` + p2OwnerID + `</ID><DisplayName>` + p2OwnerID + `</DisplayName></Owner>` +
		`<AccessControlList><Grant>` +
		`<Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser">` +
		`<ID>` + p2OwnerID + `</ID><DisplayName>` + p2OwnerID + `</DisplayName></Grantee>` +
		`<Permission>FULL_CONTROL</Permission>` +
		`</Grant></AccessControlList></AccessControlPolicy>`

	versioningOffXML = `<VersioningConfiguration xmlns="` + p2XMLNS + `"></VersioningConfiguration>`
	emptyTaggingXML  = `<Tagging xmlns="` + p2XMLNS + `"><TagSet></TagSet></Tagging>`
)

var (
	errMethodNotAllowedP2 = APIError{Code: "MethodNotAllowed", Message: "The specified method is not allowed against this resource.", Status: http.StatusMethodNotAllowed}
	errNoSuchTagSet       = APIError{Code: "NoSuchTagSet", Message: "The TagSet does not exist", Status: http.StatusNotFound}

	// absentBucketConfig is the "no configuration" answer of bucket-level subresources.
	absentBucketConfig = map[string]APIError{
		"cors":      {Code: "NoSuchCORSConfiguration", Message: "The CORS configuration does not exist", Status: http.StatusNotFound},
		"lifecycle": {Code: "NoSuchLifecycleConfiguration", Message: "The lifecycle configuration does not exist", Status: http.StatusNotFound},
	}
)

func p2NotImplemented(msg string) APIError {
	return APIError{Code: "NotImplemented", Message: msg, Status: http.StatusNotImplemented}
}

// handleP2Subresource serves the P2 subresources. It returns false when the
// request carries none of them so the caller continues normal routing.
func (rt *Router) handleP2Subresource(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	sub := ""
	for _, name := range p2Subresources {
		if q.Has(name) {
			sub = name
			break
		}
	}
	if sub == "" {
		return false
	}
	bucket, key, _ := splitPath(r.URL.Path)
	if bucket == "" {
		writeError(w, r, errNotImplemented)
		return true
	}
	objectLevel := key != ""
	switch {
	case sub == "versioning" && !objectLevel:
		rt.handleVersioning(w, r, bucket)
	case sub == "acl":
		rt.handleACL(w, r, bucket, key)
	case sub == "tagging":
		rt.handleTagging(w, r, bucket, key)
	case (sub == "cors" || sub == "lifecycle") && !objectLevel:
		rt.handleBucketConfig(w, r, bucket, sub)
	default:
		writeError(w, r, errNotImplemented)
	}
	return true
}

func (rt *Router) handleVersioning(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.requireBucket(w, r, bucket) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeP2XML(w, http.StatusOK, versioningOffXML)
	case http.MethodPut:
		writeError(w, r, p2NotImplemented("Bucket versioning is not supported"))
	default:
		writeError(w, r, errMethodNotAllowedP2)
	}
}

// handleACL, handleTagging and handleBucketConfig (cors/lifecycle) are in p2_config.go.

// requireBucket writes NoSuchBucket and returns false when the bucket is missing.
func (rt *Router) requireBucket(w http.ResponseWriter, r *http.Request, bucket string) bool {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return false
	}
	return true
}

// requireTarget checks the bucket (key == "") or the object (key != "").
func (rt *Router) requireTarget(w http.ResponseWriter, r *http.Request, bucket, key string) bool {
	if key == "" {
		return rt.requireBucket(w, r, bucket)
	}
	if kerr, ok := validateObjectKey(key); !ok {
		writeError(w, r, kerr)
		return false
	}
	if !rt.requireBucket(w, r, bucket) {
		return false
	}
	if _, err := rt.store.HeadObject(bucket, key); err != nil {
		writeStoreFailure(w, r, err)
		return false
	}
	return true
}

// writeStoreFailure renders a store error as S3 XML without leaking internals.
func writeStoreFailure(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr APIError
	if errors.As(err, &apiErr) {
		writeError(w, r, apiErr)
		return
	}
	writeXMLError(w, r, http.StatusInternalServerError, "InternalError", "We encountered an internal error. Please try again.")
}

func writeP2XML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, xml.Header+body)
}
