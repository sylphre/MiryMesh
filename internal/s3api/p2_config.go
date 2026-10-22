// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Handlers and validators for the persisted P2 subresources:
// ?acl (bucket/object), ?tagging (bucket/object), ?cors and ?lifecycle (bucket).

const (
	p2MaxBody           = 1 << 20
	p2MaxObjectTags     = 10
	p2MaxBucketTags     = 50
	p2MaxLifecycleRules = 1000
	p2XSINS             = "http://www.w3.org/2001/XMLSchema-instance"
)

func p2BadRequest(code, msg string) *APIError {
	return &APIError{Code: code, Message: msg, Status: http.StatusBadRequest}
}

func p2Malformed() *APIError {
	return p2BadRequest("MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema.")
}

// subStore returns the persistence backend, or a stub when the store has none.
func (rt *Router) subStore() SubresourceStore {
	if ss, ok := rt.store.(SubresourceStore); ok {
		return ss
	}
	return noSubresources{}
}

func writeSubresourceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errSubresourceUnsupported) {
		writeError(w, r, p2NotImplemented("Storing this configuration is not supported by the current storage backend"))
		return
	}
	writeStoreFailure(w, r, err)
}

// readP2Body reads a size-limited request body; it writes the error itself on failure.
func readP2Body(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, p2MaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, r, *p2BadRequest("EntityTooLarge", "Your proposed upload exceeds the maximum allowed size"))
			return nil, false
		}
		writeError(w, r, *p2BadRequest("IncompleteBody", "The request body terminated unexpectedly"))
		return nil, false
	}
	return b, true
}

func p2Esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// p2StripProlog trims whitespace and a leading <?xml ...?> declaration, since
// writeP2XML prepends its own.
func p2StripProlog(b []byte) string {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "<?xml") {
		if i := strings.Index(s, "?>"); i >= 0 {
			s = strings.TrimSpace(s[i+2:])
		}
	}
	return s
}

// ---------------------------------------------------------------- tagging

type p2Tag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

func (rt *Router) handleTagging(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !rt.requireTarget(w, r, bucket, key) {
		return
	}
	ss := rt.subStore()
	switch r.Method {
	case http.MethodGet:
		doc, found, err := ss.GetSubresource(bucket, key, subTagging)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		switch {
		case found:
			writeP2XML(w, http.StatusOK, doc)
		case key == "":
			writeError(w, r, errNoSuchTagSet)
		default:
			writeP2XML(w, http.StatusOK, emptyTaggingXML)
		}
	case http.MethodPut:
		body, ok := readP2Body(w, r)
		if !ok {
			return
		}
		doc, apiErr := canonicalTagging(body, key == "")
		if apiErr != nil {
			writeError(w, r, *apiErr)
			return
		}
		if err := ss.PutSubresource(bucket, key, subTagging, doc); err != nil {
			writeSubresourceError(w, r, err)
			return
		}
		if key == "" {
			w.WriteHeader(http.StatusNoContent) // PutBucketTagging answers 204
		} else {
			w.WriteHeader(http.StatusOK)
		}
	case http.MethodDelete:
		if err := ss.DeleteSubresource(bucket, key, subTagging); err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, errMethodNotAllowedP2)
	}
}

func validTagText(s string) bool {
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || unicode.IsSpace(c) || strings.ContainsRune("+-=._:/@", c) {
			continue
		}
		return false
	}
	return true
}

// canonicalTags validates a tag list and renders the canonical <Tagging> document.
func canonicalTags(tags []p2Tag, limit int) (string, *APIError) {
	if len(tags) > limit {
		return "", p2BadRequest("InvalidTag", fmt.Sprintf("Tag count cannot be greater than %d", limit))
	}
	seen := make(map[string]struct{}, len(tags))
	var sb strings.Builder
	sb.WriteString(`<Tagging xmlns="` + p2XMLNS + `"><TagSet>`)
	for _, t := range tags {
		if n := utf8.RuneCountInString(t.Key); n < 1 || n > 128 {
			return "", p2BadRequest("InvalidTag", "The TagKey you have provided is invalid")
		}
		if utf8.RuneCountInString(t.Value) > 256 {
			return "", p2BadRequest("InvalidTag", "The TagValue you have provided is invalid")
		}
		if !validTagText(t.Key) || !validTagText(t.Value) {
			return "", p2BadRequest("InvalidTag", "The tag contains characters that are not allowed")
		}
		if strings.HasPrefix(strings.ToLower(t.Key), "aws:") {
			return "", p2BadRequest("InvalidTag", "System tags cannot be added/updated by requester")
		}
		if _, dup := seen[t.Key]; dup {
			return "", p2BadRequest("InvalidTag", "Cannot provide multiple Tags with the same key")
		}
		seen[t.Key] = struct{}{}
		sb.WriteString("<Tag><Key>" + p2Esc(t.Key) + "</Key><Value>" + p2Esc(t.Value) + "</Value></Tag>")
	}
	sb.WriteString("</TagSet></Tagging>")
	return sb.String(), nil
}

func canonicalTagging(body []byte, bucketLevel bool) (string, *APIError) {
	var t struct {
		XMLName xml.Name `xml:"Tagging"`
		Tags    []p2Tag  `xml:"TagSet>Tag"`
	}
	if err := xml.Unmarshal(body, &t); err != nil {
		return "", p2Malformed()
	}
	limit := p2MaxObjectTags
	if bucketLevel {
		limit = p2MaxBucketTags
	}
	return canonicalTags(t.Tags, limit)
}

// requestTagging parses the x-amz-tagging header of PutObject/CopyObject.
// It returns "" when the header is absent or empty.
func requestTagging(r *http.Request) (string, *APIError) {
	h := r.Header.Get("X-Amz-Tagging")
	if h == "" {
		return "", nil
	}
	vals, err := url.ParseQuery(h)
	if err != nil {
		return "", p2BadRequest("InvalidArgument", "The header 'x-amz-tagging' shall be encoded as UrlEncoded but the shape was invalid")
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tags []p2Tag
	for _, k := range keys {
		for _, v := range vals[k] {
			tags = append(tags, p2Tag{Key: k, Value: v})
		}
	}
	if len(tags) == 0 {
		return "", nil
	}
	return canonicalTags(tags, p2MaxObjectTags)
}

// commitObjectTags resets the per-object subresources after an object was
// (over)written — a new object never inherits old tags/ACL — and stores the
// tags that came with the request. It writes the error response itself.
func (rt *Router) commitObjectTags(w http.ResponseWriter, r *http.Request, bucket, key, tagsXML string) bool {
	ss := rt.subStore()
	if err := ss.PurgeObjectSubresources(bucket, key); err != nil {
		writeStoreFailure(w, r, err)
		return false
	}
	if tagsXML == "" {
		return true
	}
	if err := ss.PutSubresource(bucket, key, subTagging, tagsXML); err != nil {
		if errors.Is(err, errSubresourceUnsupported) {
			return true // backend cannot store tags: same as before persistence existed
		}
		writeStoreFailure(w, r, err)
		return false
	}
	return true
}

// -------------------------------------------------------------------- ACL

const (
	p2GroupAllUsers      = "http://acs.amazonaws.com/groups/global/AllUsers"
	p2GroupAuthUsers     = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"
	p2GroupLogDelivery   = "http://acs.amazonaws.com/groups/s3/LogDelivery"
	p2MaxACLGrants       = 100
	p2MalformedACLErrMsg = "The XML you provided was not well-formed or did not validate against our published schema"
)

type p2Grant struct {
	Type        string // CanonicalUser | Group | AmazonCustomerByEmail
	ID          string
	DisplayName string
	URI         string
	Email       string
	Permission  string
}

var p2GrantHeaders = []struct{ header, perm string }{
	{"X-Amz-Grant-Read", "READ"},
	{"X-Amz-Grant-Write", "WRITE"},
	{"X-Amz-Grant-Read-Acp", "READ_ACP"},
	{"X-Amz-Grant-Write-Acp", "WRITE_ACP"},
	{"X-Amz-Grant-Full-Control", "FULL_CONTROL"},
}

var p2Permissions = map[string]bool{"FULL_CONTROL": true, "READ": true, "WRITE": true, "READ_ACP": true, "WRITE_ACP": true}

func validGroupURI(u string) bool {
	return u == p2GroupAllUsers || u == p2GroupAuthUsers || u == p2GroupLogDelivery
}

func ownerGrant() p2Grant {
	return p2Grant{Type: "CanonicalUser", ID: p2OwnerID, DisplayName: p2OwnerID, Permission: "FULL_CONTROL"}
}

func groupGrant(uri, perm string) p2Grant {
	return p2Grant{Type: "Group", URI: uri, Permission: perm}
}

// cannedGrants expands a canned ACL name into grants.
func cannedGrants(name string, bucketLevel bool) ([]p2Grant, bool) {
	owner := ownerGrant()
	switch name {
	case "private", "bucket-owner-read", "bucket-owner-full-control", "aws-exec-read":
		return []p2Grant{owner}, true
	case "public-read":
		return []p2Grant{owner, groupGrant(p2GroupAllUsers, "READ")}, true
	case "public-read-write":
		return []p2Grant{owner, groupGrant(p2GroupAllUsers, "READ"), groupGrant(p2GroupAllUsers, "WRITE")}, true
	case "authenticated-read":
		return []p2Grant{owner, groupGrant(p2GroupAuthUsers, "READ")}, true
	case "log-delivery-write":
		if bucketLevel {
			return []p2Grant{owner, groupGrant(p2GroupLogDelivery, "READ_ACP"), groupGrant(p2GroupLogDelivery, "WRITE")}, true
		}
	}
	return nil, false
}

func renderACL(grants []p2Grant) string {
	var sb strings.Builder
	sb.WriteString(`<AccessControlPolicy xmlns="` + p2XMLNS + `"><Owner><ID>` + p2OwnerID +
		`</ID><DisplayName>` + p2OwnerID + `</DisplayName></Owner><AccessControlList>`)
	for _, g := range grants {
		sb.WriteString(`<Grant><Grantee xmlns:xsi="` + p2XSINS + `" xsi:type="` + g.Type + `">`)
		switch g.Type {
		case "CanonicalUser":
			sb.WriteString("<ID>" + p2Esc(g.ID) + "</ID>")
			if g.DisplayName != "" {
				sb.WriteString("<DisplayName>" + p2Esc(g.DisplayName) + "</DisplayName>")
			}
		case "Group":
			sb.WriteString("<URI>" + p2Esc(g.URI) + "</URI>")
		case "AmazonCustomerByEmail":
			sb.WriteString("<EmailAddress>" + p2Esc(g.Email) + "</EmailAddress>")
		}
		sb.WriteString("</Grantee><Permission>" + g.Permission + "</Permission></Grant>")
	}
	sb.WriteString("</AccessControlList></AccessControlPolicy>")
	return sb.String()
}

func parseGrantHeader(v, perm string) ([]p2Grant, *APIError) {
	invalid := func() *APIError { return p2BadRequest("InvalidArgument", "Invalid x-amz-grant-* header value") }
	var out []p2Grant
	for _, part := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, invalid()
		}
		val = strings.Trim(strings.TrimSpace(val), `"`)
		if val == "" {
			return nil, invalid()
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "id":
			out = append(out, p2Grant{Type: "CanonicalUser", ID: val, Permission: perm})
		case "uri":
			if !validGroupURI(val) {
				return nil, p2BadRequest("InvalidArgument", "Invalid group uri")
			}
			out = append(out, p2Grant{Type: "Group", URI: val, Permission: perm})
		case "emailaddress":
			out = append(out, p2Grant{Type: "AmazonCustomerByEmail", Email: val, Permission: perm})
		default:
			return nil, invalid()
		}
	}
	return out, nil
}

type p2ACPGrant struct {
	Grantee struct {
		Attrs       []xml.Attr `xml:",any,attr"`
		ID          string     `xml:"ID"`
		DisplayName string     `xml:"DisplayName"`
		URI         string     `xml:"URI"`
		Email       string     `xml:"EmailAddress"`
	} `xml:"Grantee"`
	Permission string `xml:"Permission"`
}

func parseACPBody(body []byte) ([]p2Grant, *APIError) {
	bad := func() *APIError { return p2BadRequest("MalformedACLError", p2MalformedACLErrMsg) }
	var acp struct {
		XMLName xml.Name `xml:"AccessControlPolicy"`
		Owner   struct {
			ID string `xml:"ID"`
		} `xml:"Owner"`
		Grants []p2ACPGrant `xml:"AccessControlList>Grant"`
	}
	if err := xml.Unmarshal(body, &acp); err != nil || acp.Owner.ID == "" || len(acp.Grants) > p2MaxACLGrants {
		return nil, bad()
	}
	out := make([]p2Grant, 0, len(acp.Grants))
	for _, g := range acp.Grants {
		if !p2Permissions[g.Permission] {
			return nil, bad()
		}
		typ := ""
		for _, a := range g.Grantee.Attrs {
			if a.Name.Local == "type" {
				typ = a.Value
			}
		}
		if typ == "" {
			switch {
			case g.Grantee.URI != "":
				typ = "Group"
			case g.Grantee.Email != "":
				typ = "AmazonCustomerByEmail"
			default:
				typ = "CanonicalUser"
			}
		}
		grant := p2Grant{Type: typ, Permission: g.Permission}
		switch typ {
		case "CanonicalUser":
			if g.Grantee.ID == "" {
				return nil, bad()
			}
			grant.ID, grant.DisplayName = g.Grantee.ID, g.Grantee.DisplayName
		case "Group":
			if !validGroupURI(g.Grantee.URI) {
				return nil, p2BadRequest("InvalidArgument", "Invalid group uri")
			}
			grant.URI = g.Grantee.URI
		case "AmazonCustomerByEmail":
			if g.Grantee.Email == "" {
				return nil, bad()
			}
			grant.Email = g.Grantee.Email
		default:
			return nil, bad()
		}
		out = append(out, grant)
	}
	return out, nil
}

// buildACL turns a PutAcl request (canned header, grant headers or body —
// exactly one of them) into the canonical ACL document.
func buildACL(r *http.Request, body []byte, bucketLevel bool) (string, *APIError) {
	canned := strings.TrimSpace(r.Header.Get("X-Amz-Acl"))
	var headerGrants []p2Grant
	for _, gh := range p2GrantHeaders {
		v := strings.TrimSpace(r.Header.Get(gh.header))
		if v == "" {
			continue
		}
		gs, apiErr := parseGrantHeader(v, gh.perm)
		if apiErr != nil {
			return "", apiErr
		}
		headerGrants = append(headerGrants, gs...)
	}
	hasBody := len(bytes.TrimSpace(body)) > 0
	sources := 0
	for _, present := range []bool{canned != "", len(headerGrants) > 0, hasBody} {
		if present {
			sources++
		}
	}
	switch {
	case sources == 0:
		return "", p2BadRequest("MissingSecurityHeader", "Your request was missing a required header")
	case sources > 1:
		return "", p2BadRequest("InvalidRequest", "Specifying more than one of a canned ACL, grant headers and an ACL body is not allowed")
	}
	switch {
	case canned != "":
		grants, ok := cannedGrants(canned, bucketLevel)
		if !ok {
			return "", p2BadRequest("InvalidArgument", "Invalid canned ACL")
		}
		return renderACL(grants), nil
	case len(headerGrants) > 0:
		return renderACL(headerGrants), nil
	}
	grants, apiErr := parseACPBody(body)
	if apiErr != nil {
		return "", apiErr
	}
	return renderACL(grants), nil
}

func (rt *Router) handleACL(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if !rt.requireTarget(w, r, bucket, key) {
		return
	}
	ss := rt.subStore()
	switch r.Method {
	case http.MethodGet:
		doc, found, err := ss.GetSubresource(bucket, key, subACL)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		if !found {
			doc = aclPrivateXML
		}
		writeP2XML(w, http.StatusOK, doc)
	case http.MethodPut:
		body, ok := readP2Body(w, r)
		if !ok {
			return
		}
		doc, apiErr := buildACL(r, body, key == "")
		if apiErr != nil {
			writeError(w, r, *apiErr)
			return
		}
		if err := ss.PutSubresource(bucket, key, subACL, doc); err != nil {
			writeSubresourceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		writeError(w, r, errMethodNotAllowedP2)
	}
}

// ------------------------------------------------- bucket cors / lifecycle

func (rt *Router) handleBucketConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	if !rt.requireBucket(w, r, bucket) {
		return
	}
	ss := rt.subStore()
	switch r.Method {
	case http.MethodGet:
		doc, found, err := ss.GetSubresource(bucket, "", sub)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		if !found {
			writeError(w, r, absentBucketConfig[sub])
			return
		}
		writeP2XML(w, http.StatusOK, doc)
	case http.MethodPut:
		body, ok := readP2Body(w, r)
		if !ok {
			return
		}
		var doc string
		var apiErr *APIError
		if sub == subCORS {
			doc, apiErr = canonicalCORS(body)
		} else {
			doc, apiErr = canonicalLifecycle(body)
		}
		if apiErr != nil {
			writeError(w, r, *apiErr)
			return
		}
		if err := ss.PutSubresource(bucket, "", sub, doc); err != nil {
			writeSubresourceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := ss.DeleteSubresource(bucket, "", sub); err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, errMethodNotAllowedP2)
	}
}

type p2LifecycleRule struct {
	ID         string `xml:"ID"`
	Status     string `xml:"Status"`
	Expiration *struct {
		Days                      *int   `xml:"Days"`
		Date                      string `xml:"Date"`
		ExpiredObjectDeleteMarker *bool  `xml:"ExpiredObjectDeleteMarker"`
	} `xml:"Expiration"`
	Transitions []struct {
		Days         *int   `xml:"Days"`
		Date         string `xml:"Date"`
		StorageClass string `xml:"StorageClass"`
	} `xml:"Transition"`
	NoncurrentExpiration  *struct{}  `xml:"NoncurrentVersionExpiration"`
	NoncurrentTransitions []struct{} `xml:"NoncurrentVersionTransition"`
	AbortMultipart        *struct {
		DaysAfterInitiation int `xml:"DaysAfterInitiation"`
	} `xml:"AbortIncompleteMultipartUpload"`
}

// canonicalLifecycle validates a lifecycle configuration and returns it
// unchanged (minus the XML declaration) so GET echoes exactly what was set.
func canonicalLifecycle(body []byte) (string, *APIError) {
	var cfg struct {
		XMLName xml.Name          `xml:"LifecycleConfiguration"`
		Rules   []p2LifecycleRule `xml:"Rule"`
	}
	if err := xml.Unmarshal(body, &cfg); err != nil || len(cfg.Rules) == 0 {
		return "", p2Malformed()
	}
	if len(cfg.Rules) > p2MaxLifecycleRules {
		return "", p2BadRequest("InvalidRequest", "The number of lifecycle rules must not exceed the allowed limit of 1000 rules")
	}
	ids := make(map[string]struct{}, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		if rule.Status != "Enabled" && rule.Status != "Disabled" {
			return "", p2Malformed()
		}
		if len(rule.ID) > 255 {
			return "", p2BadRequest("InvalidArgument", "ID length should not exceed allowed limit of 255")
		}
		if rule.ID != "" {
			if _, dup := ids[rule.ID]; dup {
				return "", p2BadRequest("InvalidArgument", "Rule ID must be unique. Found same ID for more than one rule")
			}
			ids[rule.ID] = struct{}{}
		}
		if exp := rule.Expiration; exp != nil {
			n := 0
			if exp.Days != nil {
				n++
			}
			if exp.Date != "" {
				n++
			}
			if exp.ExpiredObjectDeleteMarker != nil {
				n++
			}
			if n != 1 {
				return "", p2Malformed()
			}
			if exp.Days != nil && *exp.Days < 1 {
				return "", p2BadRequest("InvalidArgument", "'Days' for Expiration action must be a positive integer")
			}
			if exp.Date != "" {
				if _, err := time.Parse(time.RFC3339, exp.Date); err != nil {
					return "", p2BadRequest("InvalidArgument", "'Date' must be in ISO 8601 format")
				}
			}
		}
		if a := rule.AbortMultipart; a != nil && a.DaysAfterInitiation < 1 {
			return "", p2BadRequest("InvalidArgument", "'DaysAfterInitiation' for AbortIncompleteMultipartUpload action must be a positive integer")
		}
		hasAction := rule.Expiration != nil || len(rule.Transitions) > 0 ||
			rule.NoncurrentExpiration != nil || len(rule.NoncurrentTransitions) > 0 || rule.AbortMultipart != nil
		if !hasAction {
			return "", p2Malformed()
		}
	}
	return p2StripProlog(body), nil
}
