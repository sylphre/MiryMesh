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

func TestParseChannel(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", ChannelAuto, true},
		{"auto", ChannelAuto, true},
		{" Release ", ChannelRelease, true},
		{"stable", ChannelRelease, true},
		{"PreRelease", ChannelPrerelease, true},
		{"pre", ChannelPrerelease, true},
		{"beta", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseChannel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseChannel(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestChannelReleaseHidesPrereleaseFromPrereleaseBuild(t *testing.T) {
	c := New("v1.0.0-rc.1", true, true)
	c.SetChannel(ChannelRelease)
	c.apiURL = serve(t, 200, releases).URL
	st := c.Check(context.Background())
	if st.Latest != "v0.9.0" || st.UpdateAvailable || st.IncludePrerelease || st.NotifyChannel != "release" {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestChannelPrereleaseOffersPrereleaseToStableBuild(t *testing.T) {
	c := New("v0.9.0", true, false)
	c.SetChannel(ChannelPrerelease)
	c.apiURL = serve(t, 200, releases).URL
	st := c.Check(context.Background())
	if st.Latest != "v1.0.0-rc.2" || !st.UpdateAvailable || !st.IncludePrerelease || st.NotifyChannel != "prerelease" {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestStatusReportsAutoChannel(t *testing.T) {
	st := New("v0.9.0", true, false).Status()
	if st.NotifyChannel != "auto" || st.IncludePrerelease {
		t.Fatalf("unexpected status: %+v", st)
	}
}

func TestSetChannelDropsCachedResult(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(releases))
	}))
	t.Cleanup(srv.Close)

	c := New("v0.9.0", true, false)
	c.apiURL = srv.URL
	if st := c.Check(context.Background()); st.Latest != "v0.9.0" {
		t.Fatalf("stable lookup: %+v", st)
	}
	c.SetChannel(ChannelPrerelease)
	st := c.Check(context.Background())
	if st.Latest != "v1.0.0-rc.2" || hits.Load() != 2 {
		t.Fatalf("expected a fresh lookup after SetChannel: hits=%d status=%+v", hits.Load(), st)
	}
}
