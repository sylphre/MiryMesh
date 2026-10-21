// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/json"
	"io"
	"net/http"
)

const maxPolicyBytes = 20 * 1024 // AWS limit 20KiB

func (rt *Router) handleGetBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	b, err := rt.store.GetBucketPolicy(bucket)
	if err != nil {
		writeXMLError(w, r, http.StatusNotFound, "NoSuchBucketPolicy", "The bucket policy does not exist")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (rt *Router) handlePutBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPolicyBytes+1))
	if err != nil {
		writeXMLError(w, r, http.StatusBadRequest, "MalformedPolicy", "Policy document is malformed")
		return
	}
	if len(body) > maxPolicyBytes {
		writeXMLError(w, r, http.StatusBadRequest, "MalformedPolicy", "Policy document too large (max 20KiB)")
		return
	}
	if len(body) == 0 {
		writeXMLError(w, r, http.StatusBadRequest, "MalformedPolicy", "Policy document is empty")
		return
	}
	if err := validatePolicyJSON(body); err != nil {
		writeXMLError(w, r, http.StatusBadRequest, "MalformedPolicy", err.Error())
		return
	}
	if err := rt.store.PutBucketPolicy(bucket, body); err != nil {
		if apiErr, ok := err.(APIError); ok {
			writeError(w, r, apiErr)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (rt *Router) handleDeleteBucketPolicy(w http.ResponseWriter, r *http.Request, bucket string) {
	if !rt.store.BucketExists(bucket) {
		writeError(w, r, errNoSuchBucket)
		return
	}
	if err := rt.store.DeleteBucketPolicy(bucket); err != nil {
		writeXMLError(w, r, http.StatusNotFound, "NoSuchBucketPolicy", "The bucket policy does not exist")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validatePolicyJSON(body []byte) error {
	var doc struct {
		Version   string `json:"Version"`
		Statement []struct {
			Effect    string `json:"Effect"`
			Principal any    `json:"Principal"`
			Action    any    `json:"Action"`
			Resource  any    `json:"Resource"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return err
	}
	if doc.Version != "" && doc.Version != "2012-10-17" && doc.Version != "2008-10-17" {
		// Allow any Version for compatibility, but warn — don't reject
	}
	if len(doc.Statement) == 0 {
		return errMalformedPolicy("Statement must not be empty")
	}
	for _, s := range doc.Statement {
		if s.Effect != "Allow" && s.Effect != "Deny" {
			return errMalformedPolicy("Statement Effect must be Allow or Deny")
		}
	}
	return nil
}

func errMalformedPolicy(msg string) error {
	return &s3Error{Code: "MalformedPolicy", Message: msg, HTTPStatus: http.StatusBadRequest}
}
