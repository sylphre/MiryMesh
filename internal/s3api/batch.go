// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"io"
	"net/http"
)

func (rt *Router) handleDeleteObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeXMLError(w, r, 400, "MalformedXML", err.Error())
		return
	}
	type objectXML struct {
		Key       string `xml:"Key"`
		VersionID string `xml:"VersionId"`
	}
	type reqXML struct {
		XMLName xml.Name    `xml:"Delete"`
		Quiet   bool        `xml:"Quiet"`
		Objects []objectXML `xml:"Object"`
	}
	var req reqXML
	if err := xml.Unmarshal(body, &req); err != nil {
		writeXMLError(w, r, 400, "MalformedXML", err.Error())
		return
	}
	if len(req.Objects) == 0 {
		writeXMLError(w, r, 400, "MalformedXML", "no objects")
		return
	}
	if len(req.Objects) > 1000 {
		writeError(w, r, APIError{Code: "MalformedXML", Message: "The XML you provided was not well-formed or did not validate against our published schema (too many keys)", Status: 400})
		return
	}
	type deletedXML struct {
		Key string `xml:"Key"`
	}
	type errorXML struct {
		Key     string `xml:"Key"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	type respXML struct {
		XMLName xml.Name     `xml:"DeleteResult"`
		Xmlns   string       `xml:"xmlns,attr"`
		Deleted []deletedXML `xml:"Deleted"`
		Errors  []errorXML   `xml:"Error"`
	}
	resp := respXML{Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/"}
	for _, obj := range req.Objects {
		if obj.Key == "" {
			resp.Errors = append(resp.Errors, errorXML{Key: obj.Key, Code: "InvalidRequest", Message: "Key is empty"})
			continue
		}
		// versionId ignored (unversioned)
		_ = rt.store.DeleteObject(bucket, obj.Key)
		if !req.Quiet {
			resp.Deleted = append(resp.Deleted, deletedXML{Key: obj.Key})
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(200)
	_ = xml.NewEncoder(w).Encode(resp)
}
