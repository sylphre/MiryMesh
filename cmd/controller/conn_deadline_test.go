// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// Regression: ReadTimeout/WriteTimeout cut off uploads and Complete calls that
// took longer than the cap. With progress deadlines, a body that keeps moving
// and a handler that works longer than the idle limit both succeed.
func TestProgressDeadlinesAllowSlowBodyAndSlowHandler(t *testing.T) {
	const idle = 200 * time.Millisecond
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Busy without I/O for longer than idle, like a DataNode assembling parts.
		time.Sleep(3 * idle)
		_, _ = io.WriteString(w, strconv.FormatInt(n, 10))
	})
	srv := httptest.NewServer(withProgressDeadlines(h, idle))
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		// 6 chunks with gaps below idle: 600ms in total, three times idle.
		for i := 0; i < 6; i++ {
			if _, err := pw.Write(make([]byte, 1024)); err != nil {
				return
			}
			time.Sleep(idle / 2)
		}
		_ = pw.Close()
	}()
	resp, err := http.Post(srv.URL, "application/octet-stream", pr)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(b) != "6144" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, b)
	}
}

// A client that stops sending for longer than idle is disconnected.
func TestProgressDeadlinesDropStalledClient(t *testing.T) {
	const idle = 200 * time.Millisecond
	errc := make(chan error, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		errc <- err
	})
	srv := httptest.NewServer(withProgressDeadlines(h, idle))
	defer srv.Close()

	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() {
		if _, err := pw.Write([]byte("start")); err != nil {
			return
		}
		// Then nothing more: the pipe stays open until the test ends.
	}()
	go func() {
		resp, err := http.Post(srv.URL, "application/octet-stream", pr)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("a stalled body was read to the end without error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stalled client was not disconnected")
	}
}
