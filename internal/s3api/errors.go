// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/xml"
	"fmt"
	"net/http"
)

// APIError is a structured S3 error rendered as XML.
type APIError struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
	Status    int      `xml:"-"`
}

func (e APIError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

var (
	errNoSuchBucket       = APIError{Code: "NoSuchBucket", Message: "The specified bucket does not exist", Status: http.StatusNotFound}
	errNoSuchKey          = APIError{Code: "NoSuchKey", Message: "The specified key does not exist", Status: http.StatusNotFound}
	errBucketAlreadyOwned = APIError{Code: "BucketAlreadyOwnedByYou", Message: "Your previous request to create the named bucket succeeded and you already own it", Status: http.StatusConflict}
	errBucketNotEmpty     = APIError{Code: "BucketNotEmpty", Message: "The bucket you tried to delete is not empty", Status: http.StatusConflict}
	errAccessDenied       = APIError{Code: "AccessDenied", Message: "Access Denied", Status: http.StatusForbidden}
	errInvalidBucketName  = APIError{Code: "InvalidBucketName", Message: "The specified bucket is not valid", Status: http.StatusBadRequest}
	errInvalidRequest     = APIError{Code: "InvalidRequest", Message: "Invalid request", Status: http.StatusBadRequest}
	errSignatureMismatch  = APIError{Code: "SignatureDoesNotMatch", Message: "The request signature we calculated does not match the signature you provided", Status: http.StatusForbidden}
	errNotImplemented     = APIError{Code: "NotImplemented", Message: "A header you provided implies functionality that is not implemented", Status: http.StatusNotImplemented}
	errInsufficient       = APIError{Code: "InsufficientStorage", Message: "Insufficient storage. The placement engine could not place the object.", Status: 507}
)

func writeError(w http.ResponseWriter, r *http.Request, apiErr APIError) {
	if apiErr.Status == 0 {
		apiErr.Status = http.StatusInternalServerError
	}
	if apiErr.RequestID == "" {
		apiErr.RequestID = r.Header.Get("X-Amz-Request-Id")
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(apiErr.Status)
	_ = xml.NewEncoder(w).Encode(apiErr)
}

func writeXMLError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeError(w, r, APIError{Code: code, Message: message, Status: status})
}
