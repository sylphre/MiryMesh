// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sylphre/MiryMesh/internal/datanode"
)

// dataResponseHeaderTimeout is how long a DataNode may take to answer once the
// request has been sent. It replaces the 30s total timeout for object bodies,
// which would cut off any transfer that takes longer than that.
const dataResponseHeaderTimeout = 2 * time.Minute

// newDataClient returns the HTTP client used to move object bodies to and from
// DataNodes. It has no total timeout, so large objects can be streamed.
func newDataClient(reg *datanode.Registry, token string) *http.Client {
	var base http.RoundTripper = http.DefaultTransport
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		c := t.Clone()
		c.ResponseHeaderTimeout = dataResponseHeaderTimeout
		base = c
	}
	return &http.Client{Transport: &nodeTokenTransport{reg: reg, token: token, base: base}}
}

// bodyClient is the client for object bodies. A RemoteStore built without
// newDataClient falls back to the short-timeout client.
func (r *RemoteStore) bodyClient() *http.Client {
	if r.dataClient != nil {
		return r.dataClient
	}
	return r.client
}

// sizeHinted is implemented by readers that know how many bytes they yield.
type sizeHinted interface {
	SizeHint() int64
}

type sizedReader struct {
	io.Reader
	size int64
}

func (s *sizedReader) SizeHint() int64 { return s.size }

// withSizeHint tells the store how many bytes r will yield, so RemoteStore can
// pick a DataNode and send a Content-Length without buffering the body. A
// negative size means unknown and returns r unchanged.
func withSizeHint(r io.Reader, size int64) io.Reader {
	if size < 0 {
		return r
	}
	return &sizedReader{Reader: r, size: size}
}

// bodySizeHint returns the size set by withSizeHint, or -1 when unknown.
func bodySizeHint(r io.Reader) int64 {
	if h, ok := r.(sizeHinted); ok {
		return h.SizeHint()
	}
	return -1
}

// spoolBody copies r into a temporary file and returns it, rewound, with its
// size. cleanup closes and removes the file. Used when the size is unknown, so
// the body goes to disk instead of memory.
func spoolBody(r io.Reader) (*os.File, int64, func(), error) {
	f, err := os.CreateTemp("", "mirymesh-put-*")
	if err != nil {
		return nil, 0, nil, err
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	size, err := io.Copy(f, r)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		cleanup()
		return nil, 0, nil, err
	}
	return f, size, cleanup, nil
}

// readErrTracker remembers the first read error of the source other than
// io.EOF. The HTTP transport wraps such errors, and the caller (for example a
// failed checksum) needs the original. It is safe for concurrent use because
// the transport may still read the body after RoundTrip returns.
type readErrTracker struct {
	src io.Reader
	mu  sync.Mutex
	err error
}

func (t *readErrTracker) Read(p []byte) (int, error) {
	n, err := t.src.Read(p)
	if err != nil && err != io.EOF {
		t.mu.Lock()
		if t.err == nil {
			t.err = err
		}
		t.mu.Unlock()
	}
	return n, err
}

// Err returns the first read error that was not io.EOF.
func (t *readErrTracker) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// lastByteHold keeps the final byte of the source back until the source has
// ended with io.EOF. A verifier such as the checksum reader reports its error
// only on that last read. With a known Content-Length the DataNode therefore
// receives one byte too few and discards the object, instead of storing a body
// the verifier rejected.
type lastByteHold struct {
	src     io.Reader
	pending []byte
	scratch []byte
	err     error
}

func (l *lastByteHold) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		avail := len(l.pending)
		if l.err == nil {
			avail-- // keep the last byte back until the source ends cleanly
		} else if l.err != io.EOF {
			return 0, l.err
		}
		if avail > 0 {
			n := copy(p, l.pending[:avail])
			l.pending = l.pending[n:]
			return n, nil
		}
		if l.err != nil {
			return 0, io.EOF
		}
		if l.scratch == nil {
			l.scratch = make([]byte, 32*1024)
		}
		n, err := l.src.Read(l.scratch)
		l.pending = append(l.pending, l.scratch[:n]...)
		l.err = err
	}
}
