// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func serve(t *testing.T, code int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const releases = `[
 {"tag_name":"v1.0.0-rc.2","html_url":"https://example.invalid/rc2","prerelease":true,"body":"notes"},
 {"tag_name":"v1.0.0-rc.1","html_url":"https://example.invalid/rc1","prerelease":true},
 {"tag_name":"v0.9.0","html_url":"https://example.invalid/v0.9.0","prerelease":false},
 {"tag_name":"v9.9.9","draft":true}
]`

func TestCheckOffersNewerPrereleaseToPrereleaseBuild(t *testing.T) {
	c := New("v1.0.0-rc.1", true, false)
	c.apiURL = serve(t, 200, releases).URL
	st := c.Check(context.Background())
	if st.Latest != "v1.0.0-rc.2" || !st.UpdateAvailable || !st.Prerelease || st.Notes != "notes" {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestCheckHidesPrereleaseFromStableBuild(t *testing.T) {
	c := New("v0.9.0", true, false)
	c.apiURL = serve(t, 200, releases).URL
	st := c.Check(context.Background())
	if st.Latest != "v0.9.0" || st.UpdateAvailable {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestCheckShowsPrereleaseToStableBuildWhenOptedIn(t *testing.T) {
	c := New("v0.9.0", true, true)
	c.apiURL = serve(t, 200, releases).URL
	st := c.Check(context.Background())
	if st.Latest != "v1.0.0-rc.2" || !st.UpdateAvailable {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestCheckDevBuildNeverFlagsUpdate(t *testing.T) {
	c := New("dev", true, false)
	c.apiURL = serve(t, 200, releases).URL
	st := c.Check(context.Background())
	if st.Latest == "" || st.UpdateAvailable {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestCheckReportsLookupError(t *testing.T) {
	c := New("v1.0.0-rc.1", true, false)
	c.apiURL = serve(t, 500, "boom").URL
	st := c.Check(context.Background())
	if st.Error == "" || st.UpdateAvailable {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestDisabledCheckerMakesNoRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	c := New("v1.0.0-rc.1", false, false)
	c.apiURL = srv.URL
	st := c.Check(context.Background())
	if hits.Load() != 0 || st.Enabled {
		t.Fatalf("disabled checker contacted the server: hits=%d status=%+v", hits.Load(), st)
	}
}
