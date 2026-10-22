// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ID-addressed bucket configurations: ?analytics, ?inventory and ?metrics.
//
// Each kind is addressed by ?id=<ID> (PUT / GET / DELETE) and listed by GET
// without an id (paginated with continuation-token). All configurations of one
// kind of one bucket are kept in a single s3_subresources document (object_key
// = '', kind = "analytics" | "inventory" | "metrics") holding a JSON map of
// ID -> validated XML. MiryMesh stores and returns these configurations; it
// does not produce analytics exports, inventory reports or CloudWatch metrics
// from them.

const (
	p2MaxIDConfigs   = 1000 // per bucket and kind, as in S3
	p2IDConfigPage   = 100  // entries per List* page, as in S3
	p2MaxConfigIDLen = 64
)

var errNoSuchConfiguration = APIError{
	Code:    "NoSuchConfiguration",
	Message: "The specified configuration does not exist.",
	Status:  http.StatusNotFound,
}

// p2IDConfigMu serializes read-modify-write cycles on the per-kind documents.
var p2IDConfigMu sync.Mutex

type p2CfgS3Destination struct {
	Bucket string `xml:"Bucket"`
	Format string `xml:"Format"`
}

type p2CfgDestination struct {
	S3BucketDestination *p2CfgS3Destination `xml:"S3BucketDestination"`
}

type p2CfgSchedule struct {
	Frequency string `xml:"Frequency"`
}

// p2CfgPresence only records that an element exists.
type p2CfgPresence struct{}

// p2CfgDoc is the union of the fields validated across the three kinds.
type p2CfgDoc struct {
	XMLName                xml.Name
	ID                     string            `xml:"Id"`
	IsEnabled              string            `xml:"IsEnabled"`
	IncludedObjectVersions string            `xml:"IncludedObjectVersions"`
	Destination            *p2CfgDestination `xml:"Destination"`
	Schedule               *p2CfgSchedule    `xml:"Schedule"`
	StorageClassAnalysis   *p2CfgPresence    `xml:"StorageClassAnalysis"`
}

type p2IDConfigSpec struct {
	root     string // root element of one configuration
	list     string // root element of the List* answer
	validate func(*p2CfgDoc) bool
}

var p2IDConfigs = map[string]p2IDConfigSpec{
	"analytics": {
		root:     "AnalyticsConfiguration",
		list:     "ListBucketAnalyticsConfigurationResult",
		validate: func(d *p2CfgDoc) bool { return d.StorageClassAnalysis != nil },
	},
	"inventory": {
		root:     "InventoryConfiguration",
		list:     "ListInventoryConfigurationsResult",
		validate: p2ValidInventory,
	},
	"metrics": {
		root:     "MetricsConfiguration",
		list:     "ListMetricsConfigurationsResult",
		validate: func(*p2CfgDoc) bool { return true },
	},
}

func p2ValidInventory(d *p2CfgDoc) bool {
	dst := d.Destination
	if dst == nil || dst.S3BucketDestination == nil || strings.TrimSpace(dst.S3BucketDestination.Bucket) == "" {
		return false
	}
	switch strings.TrimSpace(dst.S3BucketDestination.Format) {
	case "CSV", "ORC", "Parquet":
	default:
		return false
	}
	if enabled := strings.TrimSpace(d.IsEnabled); enabled != "true" && enabled != "false" {
		return false
	}
	if v := strings.TrimSpace(d.IncludedObjectVersions); v != "All" && v != "Current" {
		return false
	}
	if d.Schedule == nil {
		return false
	}
	f := strings.TrimSpace(d.Schedule.Frequency)
	return f == "Daily" || f == "Weekly"
}

// p2ValidConfigID allows up to 64 letters, digits, '.', '-' and '_'.
func p2ValidConfigID(id string) bool {
	if id == "" || len(id) > p2MaxConfigIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// p2ParseIDConfig validates one configuration document and returns the XML to
// store (prolog stripped, S3 namespace added when the client omitted it).
func p2ParseIDConfig(spec p2IDConfigSpec, body []byte, id string) (string, *APIError) {
	raw := p2StripProlog(body)
	if raw == "" {
		return "", p2Malformed()
	}
	dec := xml.NewDecoder(strings.NewReader(raw))
	var doc p2CfgDoc
	if err := dec.Decode(&doc); err != nil {
		return "", p2Malformed()
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", p2Malformed()
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return "", p2Malformed()
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return "", p2Malformed()
			}
		}
	}
	if doc.XMLName.Local != spec.root || (doc.XMLName.Space != "" && doc.XMLName.Space != p2XMLNS) {
		return "", p2Malformed()
	}
	doc.ID = strings.TrimSpace(doc.ID)
	if doc.ID == "" || !spec.validate(&doc) {
		return "", p2Malformed()
	}
	if doc.ID != id {
		return "", p2BadRequest("InvalidArgument", "ID in the request body must match the ID in the query string")
	}
	if doc.XMLName.Space == "" {
		prefix := "<" + spec.root
		if strings.HasPrefix(raw, prefix) && len(raw) > len(prefix) {
			switch raw[len(prefix)] {
			case ' ', '>', '/', '\t', '\n', '\r':
				raw = prefix + ` xmlns="` + p2XMLNS + `"` + raw[len(prefix):]
			}
		}
	}
	return raw, nil
}

func p2LoadIDConfigs(ss SubresourceStore, bucket, kind string) (map[string]string, error) {
	doc, found, err := ss.GetSubresource(bucket, "", kind)
	if err != nil {
		return nil, err
	}
	set := map[string]string{}
	if !found {
		return set, nil
	}
	if err := json.Unmarshal([]byte(doc), &set); err != nil {
		return nil, err
	}
	if set == nil {
		set = map[string]string{}
	}
	return set, nil
}

func p2SaveIDConfigs(ss SubresourceStore, bucket, kind string, set map[string]string) error {
	if len(set) == 0 {
		return ss.DeleteSubresource(bucket, "", kind)
	}
	b, err := json.Marshal(set)
	if err != nil {
		return err
	}
	return ss.PutSubresource(bucket, "", kind, string(b))
}

// handleIDConfig serves ?analytics, ?inventory and ?metrics on a bucket.
func (rt *Router) handleIDConfig(w http.ResponseWriter, r *http.Request, bucket, sub string) {
	spec := p2IDConfigs[sub]
	if !rt.requireBucket(w, r, bucket) {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodPut, http.MethodDelete:
	default:
		writeError(w, r, errMethodNotAllowedP2)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		if r.Method == http.MethodGet {
			rt.listIDConfigs(w, r, bucket, sub, spec)
			return
		}
		writeError(w, r, *p2BadRequest("InvalidArgument", "Argument id is required"))
		return
	}
	if !p2ValidConfigID(id) {
		writeError(w, r, *p2BadRequest("InvalidArgument", "The ID can contain up to 64 letters, numbers, periods, dashes and underscores"))
		return
	}
	ss := rt.subStore()
	switch r.Method {
	case http.MethodPut:
		body, ok := readP2Body(w, r)
		if !ok {
			return
		}
		raw, apiErr := p2ParseIDConfig(spec, body, id)
		if apiErr != nil {
			writeError(w, r, *apiErr)
			return
		}
		p2IDConfigMu.Lock()
		defer p2IDConfigMu.Unlock()
		set, err := p2LoadIDConfigs(ss, bucket, sub)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		if _, exists := set[id]; !exists && len(set) >= p2MaxIDConfigs {
			writeError(w, r, *p2BadRequest("TooManyConfigurations", "You are attempting to create a new configuration but have already reached the 1,000-configuration limit."))
			return
		}
		set[id] = raw
		if err := p2SaveIDConfigs(ss, bucket, sub, set); err != nil {
			writeSubresourceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		set, err := p2LoadIDConfigs(ss, bucket, sub)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		raw, ok := set[id]
		if !ok {
			writeError(w, r, errNoSuchConfiguration)
			return
		}
		writeP2XML(w, http.StatusOK, raw)
	case http.MethodDelete:
		p2IDConfigMu.Lock()
		defer p2IDConfigMu.Unlock()
		set, err := p2LoadIDConfigs(ss, bucket, sub)
		if err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		if _, ok := set[id]; !ok {
			writeError(w, r, errNoSuchConfiguration)
			return
		}
		delete(set, id)
		if err := p2SaveIDConfigs(ss, bucket, sub, set); err != nil {
			writeStoreFailure(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// listIDConfigs answers GET ?<kind> without an id: IDs in ascending order,
// p2IDConfigPage per page, continuation-token = the last ID of the previous page.
func (rt *Router) listIDConfigs(w http.ResponseWriter, r *http.Request, bucket, sub string, spec p2IDConfigSpec) {
	set, err := p2LoadIDConfigs(rt.subStore(), bucket, sub)
	if err != nil {
		writeStoreFailure(w, r, err)
		return
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	token := r.URL.Query().Get("continuation-token")
	start := sort.Search(len(ids), func(i int) bool { return ids[i] > token })
	end := start + p2IDConfigPage
	truncated := end < len(ids)
	if !truncated {
		end = len(ids)
	}
	var sb strings.Builder
	sb.WriteString("<" + spec.list + ` xmlns="` + p2XMLNS + `">`)
	sb.WriteString("<IsTruncated>" + strconv.FormatBool(truncated) + "</IsTruncated>")
	if token != "" {
		sb.WriteString("<ContinuationToken>" + p2Esc(token) + "</ContinuationToken>")
	}
	if truncated {
		sb.WriteString("<NextContinuationToken>" + p2Esc(ids[end-1]) + "</NextContinuationToken>")
	}
	for _, id := range ids[start:end] {
		sb.WriteString(set[id])
	}
	sb.WriteString("</" + spec.list + ">")
	writeP2XML(w, http.StatusOK, sb.String())
}
