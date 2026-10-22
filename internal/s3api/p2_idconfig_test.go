// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	p2AnalyticsA1 = `<AnalyticsConfiguration><Id>a1</Id><StorageClassAnalysis></StorageClassAnalysis></AnalyticsConfiguration>`
	p2InventoryI1 = `<InventoryConfiguration><Id>i1</Id><IsEnabled>true</IsEnabled>` +
		`<Destination><S3BucketDestination><Bucket>arn:aws:s3:::dst</Bucket><Format>CSV</Format></S3BucketDestination></Destination>` +
		`<IncludedObjectVersions>All</IncludedObjectVersions><Schedule><Frequency>Daily</Frequency></Schedule></InventoryConfiguration>`
	p2MetricsM1 = `<MetricsConfiguration><Id>m1</Id></MetricsConfiguration>`
)

func TestP2IDConfigRoundTrip(t *testing.T) {
	cases := []struct {
		kind, id, root, list, body string
	}{
		{"analytics", "a1", "AnalyticsConfiguration", "ListBucketAnalyticsConfigurationResult", p2AnalyticsA1},
		{"inventory", "i1", "InventoryConfiguration", "ListInventoryConfigurationsResult", p2InventoryI1},
		{"metrics", "m1", "MetricsConfiguration", "ListMetricsConfigurationsResult", p2MetricsM1},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			rt := newP2Router(t)
			target := "/bkt?" + c.kind + "&id=" + c.id
			list := "/bkt?" + c.kind

			p2Expect(t, p2Do(rt, http.MethodGet, list, "", nil), http.StatusOK, "<IsTruncated>false</IsTruncated>")
			p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusNotFound, "<Code>NoSuchConfiguration</Code>")

			p2Expect(t, p2Do(rt, http.MethodPut, target, `<?xml version="1.0" encoding="UTF-8"?>`+c.body, nil), http.StatusOK, "")
			got := p2Do(rt, http.MethodGet, target, "", nil)
			p2Expect(t, got, http.StatusOK, "<"+c.root+` xmlns="`+p2XMLNS+`">`)
			p2Expect(t, got, http.StatusOK, "<Id>"+c.id+"</Id>")

			lr := p2Do(rt, http.MethodGet, list, "", nil)
			p2Expect(t, lr, http.StatusOK, "<"+c.list+" ")
			p2Expect(t, lr, http.StatusOK, "<Id>"+c.id+"</Id>")

			// Replacing the same ID is not a new entry.
			p2Expect(t, p2Do(rt, http.MethodPut, target, c.body, nil), http.StatusOK, "")

			p2Expect(t, p2Do(rt, http.MethodDelete, target, "", nil), http.StatusNoContent, "")
			p2Expect(t, p2Do(rt, http.MethodGet, target, "", nil), http.StatusNotFound, "<Code>NoSuchConfiguration</Code>")
			p2Expect(t, p2Do(rt, http.MethodDelete, target, "", nil), http.StatusNotFound, "<Code>NoSuchConfiguration</Code>")
			p2ExpectAbsent(t, p2Do(rt, http.MethodGet, list, "", nil), "<Id>")
		})
	}
}

func TestP2IDConfigValidation(t *testing.T) {
	rt := newP2Router(t)
	badInventory := strings.Replace(p2InventoryI1, "<Format>CSV</Format>", "<Format>XLS</Format>", 1)
	cases := []struct {
		name   string
		method string
		target string
		body   string
		status int
		want   string
	}{
		{"put without id", http.MethodPut, "/bkt?metrics", p2MetricsM1, http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"delete without id", http.MethodDelete, "/bkt?metrics", "", http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"id mismatch", http.MethodPut, "/bkt?metrics&id=other", p2MetricsM1, http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"id with space", http.MethodPut, "/bkt?metrics&id=bad%20id", p2MetricsM1, http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"id too long", http.MethodGet, "/bkt?metrics&id=" + strings.Repeat("x", 65), "", http.StatusBadRequest, "<Code>InvalidArgument</Code>"},
		{"not xml", http.MethodPut, "/bkt?metrics&id=m1", "not xml", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"empty body", http.MethodPut, "/bkt?metrics&id=m1", "", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"wrong root", http.MethodPut, "/bkt?analytics&id=m1", p2MetricsM1, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"trailing element", http.MethodPut, "/bkt?metrics&id=m1", p2MetricsM1 + "<Extra/>", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"analytics without storage class analysis", http.MethodPut, "/bkt?analytics&id=a1",
			`<AnalyticsConfiguration><Id>a1</Id></AnalyticsConfiguration>`, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"inventory bad format", http.MethodPut, "/bkt?inventory&id=i1", badInventory, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"inventory missing destination", http.MethodPut, "/bkt?inventory&id=i1",
			`<InventoryConfiguration><Id>i1</Id><IsEnabled>true</IsEnabled></InventoryConfiguration>`, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"missing bucket", http.MethodGet, "/nobucket?metrics", "", http.StatusNotFound, "<Code>NoSuchBucket</Code>"},
		{"bad method", http.MethodPost, "/bkt?metrics&id=m1", "", http.StatusMethodNotAllowed, "<Code>MethodNotAllowed</Code>"},
		{"object level stays 501", http.MethodGet, "/bkt/obj.txt?analytics&id=a1", "", http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, c.method, c.target, c.body, nil), c.status, c.want)
		})
	}
	// Nothing invalid may have been stored.
	p2ExpectAbsent(t, p2Do(rt, http.MethodGet, "/bkt?metrics", "", nil), "<Id>")
}

func TestP2IDConfigPagination(t *testing.T) {
	rt := newP2Router(t)
	for i := 0; i < p2IDConfigPage+5; i++ {
		id := fmt.Sprintf("m%03d", i)
		body := `<MetricsConfiguration><Id>` + id + `</Id></MetricsConfiguration>`
		p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?metrics&id="+id, body, nil), http.StatusOK, "")
	}
	first := p2Do(rt, http.MethodGet, "/bkt?metrics", "", nil)
	p2Expect(t, first, http.StatusOK, "<IsTruncated>true</IsTruncated>")
	p2Expect(t, first, http.StatusOK, "<NextContinuationToken>m099</NextContinuationToken>")
	p2Expect(t, first, http.StatusOK, "<Id>m000</Id>")
	p2Expect(t, first, http.StatusOK, "<Id>m099</Id>")
	p2ExpectAbsent(t, first, "<Id>m100</Id>")

	second := p2Do(rt, http.MethodGet, "/bkt?metrics&continuation-token=m099", "", nil)
	p2Expect(t, second, http.StatusOK, "<IsTruncated>false</IsTruncated>")
	p2Expect(t, second, http.StatusOK, "<ContinuationToken>m099</ContinuationToken>")
	p2Expect(t, second, http.StatusOK, "<Id>m100</Id>")
	p2Expect(t, second, http.StatusOK, "<Id>m104</Id>")
	p2ExpectAbsent(t, second, "<Id>m099</Id>")
	p2ExpectAbsent(t, second, "<NextContinuationToken>")
}

func TestP2IDConfigLimit(t *testing.T) {
	rt := newP2Router(t)
	for i := 0; i < p2MaxIDConfigs; i++ {
		id := fmt.Sprintf("c%04d", i)
		body := `<MetricsConfiguration><Id>` + id + `</Id></MetricsConfiguration>`
		if rr := p2Do(rt, http.MethodPut, "/bkt?metrics&id="+id, body, nil); rr.Code != http.StatusOK {
			t.Fatalf("put %s status=%d body=%s", id, rr.Code, rr.Body.String())
		}
	}
	over := `<MetricsConfiguration><Id>over</Id></MetricsConfiguration>`
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?metrics&id=over", over, nil), http.StatusBadRequest, "<Code>TooManyConfigurations</Code>")
	// Replacing an existing ID is still allowed at the limit.
	again := `<MetricsConfiguration><Id>c0000</Id></MetricsConfiguration>`
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt?metrics&id=c0000", again, nil), http.StatusOK, "")
}
