// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// Deadlines of the S3 listener.
//
// S3 bodies can be gigabytes, and CompleteMultipartUpload can run for minutes
// while a DataNode assembles the parts. http.Server ReadTimeout and
// WriteTimeout cap the whole request, so any transfer or handler slower than
// the cap is cut off. The S3 server therefore bounds only the request headers
// with ReadHeaderTimeout and uses progress deadlines for the rest: every body
// read and every response write moves the connection deadline
// s3ProgressTimeout into the future. A peer that stops sending or reading for
// that long is disconnected. A slow transfer that keeps moving is not, and a
// handler that is busy without I/O is not limited.
const (
	s3ReadHeaderTimeout = 30 * time.Second
	s3IdleTimeout       = 120 * time.Second
	s3ProgressTimeout   = 2 * time.Minute
)

// withProgressDeadlines wraps next so that request body reads and response
// writes extend the connection deadlines by idle. It must be the handler the
// http.Server calls directly: http.ResponseController needs that server's
// ResponseWriter.
func withProgressDeadlines(next http.Handler, idle time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc, idle: idle}
		}
		next.ServeHTTP(&deadlineWriter{ResponseWriter: w, rc: rc, idle: idle}, r)
		// The server flushes the buffered end of the response after the handler
		// returns. An error here means the connection is gone, and the server
		// sees that on its flush.
		_ = extendDeadline(rc.SetWriteDeadline, idle)
	})
}

// extendDeadline sets a deadline idle from now. Writers without a connection
// (httptest.ResponseRecorder) answer http.ErrNotSupported, which is fine here.
func extendDeadline(set func(time.Time) error, idle time.Duration) error {
	if err := set(time.Now().Add(idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

// deadlineBody extends the read deadline before every read of the body.
type deadlineBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	if err := extendDeadline(b.rc.SetReadDeadline, b.idle); err != nil {
		return 0, err
	}
	// The server writes "100 Continue" from inside the first Read, so writes
	// need a fresh deadline as well.
	if err := extendDeadline(b.rc.SetWriteDeadline, b.idle); err != nil {
		return 0, err
	}
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		// The body is finished. Once it is consumed the server watches the
		// connection with a background read, and an expired deadline there would
		// cancel the request context while the handler is still working (for
		// example while a multipart object is assembled). Clearing can only fail
		// on a broken connection, which the next write reports.
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

// deadlineWriter extends the write deadline before every write.
type deadlineWriter struct {
	http.ResponseWriter
	rc   *http.ResponseController
	idle time.Duration
}

func (w *deadlineWriter) Write(p []byte) (int, error) {
	if err := extendDeadline(w.rc.SetWriteDeadline, w.idle); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}

// Flush sends buffered data, for example the keep-alive whitespace of
// CompleteMultipartUpload. http.Flusher has no error result; a failed flush
// shows up as an error on the next Write.
func (w *deadlineWriter) Flush() {
	if extendDeadline(w.rc.SetWriteDeadline, w.idle) != nil {
		return
	}
	_ = w.rc.Flush()
}

// Unwrap lets http.ResponseController reach the server's writer.
func (w *deadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
