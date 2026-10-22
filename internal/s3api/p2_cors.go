// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
)

// CORS: validation of PutBucketCors plus enforcement (preflight answers and
// Access-Control-* headers) from the stored configuration.

const p2MaxCORSRules = 100

type p2CORSRule struct {
	ID             string   `xml:"ID"`
	AllowedHeaders []string `xml:"AllowedHeader"`
	AllowedMethods []string `xml:"AllowedMethod"`
	AllowedOrigins []string `xml:"AllowedOrigin"`
	ExposeHeaders  []string `xml:"ExposeHeader"`
	MaxAgeSeconds  *int     `xml:"MaxAgeSeconds"`
}

type p2CORSConfig struct {
	XMLName xml.Name     `xml:"CORSConfiguration"`
	Rules   []p2CORSRule `xml:"CORSRule"`
}

var p2CORSMethods = map[string]bool{"GET": true, "PUT": true, "POST": true, "DELETE": true, "HEAD": true}

// p2CleanList trims every value and rejects empty ones.
func p2CleanList(vals []string) ([]string, bool) {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

func p2WriteElems(sb *strings.Builder, name string, vals []string) {
	for _, v := range vals {
		sb.WriteString("<" + name + ">" + p2Esc(v) + "</" + name + ">")
	}
}

// canonicalCORS validates a CORS configuration and renders it canonically.
func canonicalCORS(body []byte) (string, *APIError) {
	var cfg p2CORSConfig
	if err := xml.Unmarshal(body, &cfg); err != nil || len(cfg.Rules) == 0 {
		return "", p2Malformed()
	}
	if len(cfg.Rules) > p2MaxCORSRules {
		return "", p2BadRequest("InvalidRequest", "The number of CORS rules should not exceed allowed limit of 100 rules.")
	}
	var sb strings.Builder
	sb.WriteString(`<CORSConfiguration xmlns="` + p2XMLNS + `">`)
	for _, rule := range cfg.Rules {
		methods, ok1 := p2CleanList(rule.AllowedMethods)
		origins, ok2 := p2CleanList(rule.AllowedOrigins)
		headers, ok3 := p2CleanList(rule.AllowedHeaders)
		expose, ok4 := p2CleanList(rule.ExposeHeaders)
		if !ok1 || !ok2 || !ok3 || !ok4 || len(methods) == 0 || len(origins) == 0 || len(rule.ID) > 255 ||
			(rule.MaxAgeSeconds != nil && *rule.MaxAgeSeconds < 0) {
			return "", p2Malformed()
		}
		for _, m := range methods {
			if !p2CORSMethods[m] {
				return "", p2BadRequest("InvalidRequest", "Found unsupported HTTP method in CORS config.")
			}
		}
		for _, v := range append(append([]string{}, origins...), headers...) {
			if strings.Count(v, "*") > 1 {
				return "", p2BadRequest("InvalidRequest", "AllowedOrigin and AllowedHeader can not have more than one wildcard.")
			}
		}
		sb.WriteString("<CORSRule>")
		if rule.ID != "" {
			sb.WriteString("<ID>" + p2Esc(rule.ID) + "</ID>")
		}
		p2WriteElems(&sb, "AllowedHeader", headers)
		p2WriteElems(&sb, "AllowedMethod", methods)
		p2WriteElems(&sb, "AllowedOrigin", origins)
		p2WriteElems(&sb, "ExposeHeader", expose)
		if rule.MaxAgeSeconds != nil {
			sb.WriteString("<MaxAgeSeconds>" + strconv.Itoa(*rule.MaxAgeSeconds) + "</MaxAgeSeconds>")
		}
		sb.WriteString("</CORSRule>")
	}
	sb.WriteString("</CORSConfiguration>")
	return sb.String(), nil
}

// p2WildMatch matches pattern (at most one '*') against s.
func p2WildMatch(pattern, s string) bool {
	if i := strings.IndexByte(pattern, '*'); i >= 0 {
		pre, suf := pattern[:i], pattern[i+1:]
		return len(s) >= len(pre)+len(suf) && strings.HasPrefix(s, pre) && strings.HasSuffix(s, suf)
	}
	return pattern == s
}

func p2AnyMatch(patterns []string, s string, fold bool) bool {
	if fold {
		s = strings.ToLower(s)
	}
	for _, p := range patterns {
		if fold {
			p = strings.ToLower(p)
		}
		if p2WildMatch(p, s) {
			return true
		}
	}
	return false
}

func p2Contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// p2CORSMatch returns the first rule allowing origin, method and every requested header.
func p2CORSMatch(cfg *p2CORSConfig, origin, method string, reqHeaders []string) *p2CORSRule {
	for i := range cfg.Rules {
		rule := &cfg.Rules[i]
		if !p2AnyMatch(rule.AllowedOrigins, origin, false) || !p2Contains(rule.AllowedMethods, method) {
			continue
		}
		ok := true
		for _, h := range reqHeaders {
			if !p2AnyMatch(rule.AllowedHeaders, h, true) {
				ok = false
				break
			}
		}
		if ok {
			return rule
		}
	}
	return nil
}

func p2SetCORSHeaders(h http.Header, rule *p2CORSRule, origin string, preflight bool, reqHeaders []string) {
	allow := origin
	if p2Contains(rule.AllowedOrigins, "*") {
		allow = "*"
	}
	h.Set("Access-Control-Allow-Origin", allow)
	if allow != "*" {
		h.Add("Vary", "Origin")
	}
	if preflight {
		h.Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
		if len(reqHeaders) > 0 {
			h.Set("Access-Control-Allow-Headers", strings.Join(reqHeaders, ", "))
		}
		if rule.MaxAgeSeconds != nil {
			h.Set("Access-Control-Max-Age", strconv.Itoa(*rule.MaxAgeSeconds))
		}
		h.Add("Vary", "Access-Control-Request-Headers")
		h.Add("Vary", "Access-Control-Request-Method")
	}
	if len(rule.ExposeHeaders) > 0 {
		h.Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
	}
}

func p2DenyCORS(w http.ResponseWriter, r *http.Request) {
	writeXMLError(w, r, http.StatusForbidden, "AccessForbidden",
		"CORSResponse: This CORS request is not allowed. This is usually because the evaluation of Origin, request method / Access-Control-Request-Method or Access-Control-Request-Headers are not whitelisted by the resource's CORS spec.")
}

// applyCORS answers CORS preflights from the bucket's stored configuration and
// annotates ordinary cross-origin responses. It returns true when the request
// has been fully answered (preflight).
func (rt *Router) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	bucket, _, _ := splitPath(r.URL.Path)
	if bucket == "" {
		return false
	}
	ss, ok := rt.store.(SubresourceStore)
	if !ok {
		return false
	}
	reqMethod := r.Header.Get("Access-Control-Request-Method")
	preflight := r.Method == http.MethodOptions && reqMethod != ""
	raw, found, err := ss.GetSubresource(bucket, "", subCORS)
	if err != nil {
		if preflight {
			writeStoreFailure(w, r, err)
			return true
		}
		return false
	}
	var cfg p2CORSConfig
	if !found || xml.Unmarshal([]byte(raw), &cfg) != nil {
		if preflight {
			p2DenyCORS(w, r)
			return true
		}
		return false
	}
	if !preflight {
		if rule := p2CORSMatch(&cfg, origin, r.Method, nil); rule != nil {
			p2SetCORSHeaders(w.Header(), rule, origin, false, nil)
		}
		return false
	}
	var reqHeaders []string
	for _, h := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			reqHeaders = append(reqHeaders, h)
		}
	}
	rule := p2CORSMatch(&cfg, origin, strings.ToUpper(reqMethod), reqHeaders)
	if rule == nil {
		p2DenyCORS(w, r)
		return true
	}
	p2SetCORSHeaders(w.Header(), rule, origin, true, reqHeaders)
	w.WriteHeader(http.StatusOK)
	return true
}
