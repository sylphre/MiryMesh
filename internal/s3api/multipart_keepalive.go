// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"fmt"
	"net/http"
	"time"
)

// CompleteMultipartUpload keep-alive.
//
// Completing a multi-gigabyte upload takes minutes: the parts are copied into
// the final object before the answer exists. Amazon S3 answers 200 OK early,
// sends whitespace until the result is ready, and then sends the
// CompleteMultipartUploadResult or an Error document. An error can therefore
// arrive inside a 200 response. The S3 API reference documents this for
// CompleteMultipartUpload, and the AWS SDKs and the AWS CLI check for it.
// Without the whitespace, clients, proxies and load balancers give up on the
// silent connection.
//
// A result that is ready within the first interval is answered as usual,
// with the status code of an error.

// defaultCompleteKeepAlive is how long Complete may stay silent.
const defaultCompleteKeepAlive = 10 * time.Second

var keepAliveSpace = []byte(" ")

func (rt *Router) completeKeepAliveInterval() time.Duration {
	if rt.completeKeepAlive > 0 {
		return rt.completeKeepAlive
	}
	return defaultCompleteKeepAlive
}

type completeResult struct {
	meta ObjectMeta
	err  error
}

// runWithKeepAlive runs complete and waits for it. Once the first interval has
// passed it sends 200 OK with the headers from setHeaders, then whitespace
// every interval. It returns the writer for the rest of the response: after an
// early 200 that writer ignores WriteHeader, so the usual error paths render
// an embedded Error document.
//
// It always waits for complete to return, also when the client is gone: the
// handler still has to store the object's subresources afterwards.
func runWithKeepAlive(w http.ResponseWriter, interval time.Duration, setHeaders func(http.Header), complete func() (ObjectMeta, error)) (http.ResponseWriter, ObjectMeta, error) {
	done := make(chan completeResult, 1)
	go func() {
		var res completeResult
		defer func() {
			// The recovery middleware of the handler goroutine cannot see a
			// panic in this goroutine.
			if p := recover(); p != nil {
				res = completeResult{err: fmt.Errorf("complete multipart upload: panic: %v", p)}
			}
			done <- res
		}()
		res.meta, res.err = complete()
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	committed := false
	for {
		select {
		case res := <-done:
			if committed {
				return committedWriter{w}, res.meta, res.err
			}
			return w, res.meta, res.err
		case <-ticker.C:
			if !committed {
				setHeaders(w.Header())
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				committed = true
			}
			// A failed write means the client is gone; keep waiting anyway.
			if _, err := w.Write(keepAliveSpace); err == nil {
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
	}
}

// committedWriter is the response writer after an early 200 OK. The status is
// already on the wire, so WriteHeader does nothing and an error document
// becomes the body.
type committedWriter struct{ http.ResponseWriter }

func (committedWriter) WriteHeader(int) {}

func (c committedWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c committedWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
