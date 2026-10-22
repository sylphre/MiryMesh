// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"html"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const selTestPeople = "name,age,city\nalice,30,tokyo\nbob,25,osaka\ncarol,41,tokyo\ndave,,kyoto\n"

const selTestUsers = `{"id":1,"user":{"name":"alice","tags":["a","b"]},"score":9.5}
{"id":2,"user":{"name":"bob","tags":[]},"score":7}
{"id":3,"user":{"name":"carol"},"score":null}
`

// selTestExec runs a query directly through the engine and returns one JSON
// line per output row.
func selTestExec(sqlText, input string, isJSON bool, header string) ([]string, error) {
	q, err := selParseQuery(sqlText)
	if err != nil {
		return nil, err
	}
	cfg := selInputCfg{compression: "NONE", json: isJSON, header: header, comma: ','}
	var buf bytes.Buffer
	enc := selJSONEnc{rd: "\n"}
	runErr := selExecute(context.Background(), q, selNewSource(cfg, strings.NewReader(input)), func(row selRow) error {
		enc.encode(&buf, row)
		return nil
	})
	if runErr != nil {
		return nil, runErr
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func TestSelectEngineCSV(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"where and projection", `SELECT name FROM S3Object WHERE city = 'tokyo'`, []string{`{"name":"alice"}`, `{"name":"carol"}`}},
		{"alias and numeric compare", `SELECT s.name FROM S3Object s WHERE s.age > 26`, []string{`{"name":"alice"}`, `{"name":"carol"}`}},
		{"cast and between", `SELECT name, CAST(age AS INT) + 1 AS next FROM S3Object WHERE age BETWEEN 25 AND 35`, []string{`{"name":"alice","next":31}`, `{"name":"bob","next":26}`}},
		{"like upper limit", `SELECT UPPER(name) AS n FROM S3Object WHERE name LIKE '%a%' LIMIT 2`, []string{`{"n":"ALICE"}`, `{"n":"CAROL"}`}},
		{"count and max", `SELECT COUNT(*) AS c, MAX(CAST(age AS INT)) AS m FROM S3Object WHERE age <> ''`, []string{`{"c":3,"m":41}`}},
		{"avg with in", `SELECT AVG(CAST(age AS FLOAT)) FROM S3Object WHERE name IN ('alice', 'bob')`, []string{`{"_1":27.5}`}},
		{"csv fields are never null", `SELECT name FROM S3Object WHERE age IS NULL`, nil},
		{"coalesce nullif", `SELECT COALESCE(NULLIF(city, 'tokyo'), 'T') AS c FROM S3Object LIMIT 2`, []string{`{"c":"T"}`, `{"c":"osaka"}`}},
		{"substring char_length", `SELECT SUBSTRING(name, 2, 3) AS s, CHAR_LENGTH(name) AS l FROM S3Object LIMIT 1`, []string{`{"s":"lic","l":5}`}},
		{"concatenation", `SELECT name || '-' || city AS x FROM S3Object LIMIT 1`, []string{`{"x":"alice-tokyo"}`}},
		{"star", `SELECT * FROM S3Object WHERE name = 'bob'`, []string{`{"name":"bob","age":"25","city":"osaka"}`}},
		{"positional", `SELECT _1 FROM S3Object LIMIT 1`, []string{`{"_1":"alice"}`}},
		{"limit zero", `SELECT name FROM S3Object LIMIT 0`, nil},
		{"trailing semicolon", `SELECT name FROM S3Object LIMIT 1;`, []string{`{"name":"alice"}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selTestExec(c.sql, selTestPeople, false, "USE")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSelectEngineJSON(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"nested path and array index", `SELECT s.user.name AS n, s.user.tags[0] AS t FROM S3Object s`, []string{`{"n":"alice","t":"a"}`, `{"n":"bob","t":null}`, `{"n":"carol","t":null}`}},
		{"null excluded by comparison", `SELECT s.id FROM S3Object s WHERE s.score > 8`, []string{`{"id":1}`}},
		{"star keeps document order", `SELECT * FROM S3Object s WHERE s.id = 2`, []string{`{"id":2,"user":{"name":"bob","tags":[]},"score":7}`}},
		{"aggregates skip null", `SELECT COUNT(s.score) AS c, SUM(s.score) AS t FROM S3Object s`, []string{`{"c":2,"t":16.5}`}},
		{"object value", `SELECT s.user FROM S3Object s WHERE s.id = 1`, []string{`{"user":{"name":"alice","tags":["a","b"]}}`}},
		{"missing field is null", `SELECT s.id FROM S3Object s WHERE s.missing IS NULL`, []string{`{"id":1}`, `{"id":2}`, `{"id":3}`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selTestExec(c.sql, selTestUsers, true, "NONE")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSelectEngineErrors(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		code string
	}{
		{"unknown column", `SELECT nope FROM S3Object`, "ColumnNotFound"},
		{"missing where expression", `SELECT name FROM S3Object WHERE`, "ParseUnexpectedToken"},
		{"mixed aggregate", `SELECT name, COUNT(*) FROM S3Object`, "UnsupportedSyntax"},
		{"aggregate in where", `SELECT name FROM S3Object WHERE COUNT(*) > 1`, "UnsupportedSyntax"},
		{"cast failure", `SELECT CAST(city AS INT) FROM S3Object`, "InvalidCast"},
		{"division by zero", `SELECT 1 / 0 FROM S3Object`, "DivisionByZero"},
		{"unknown function", `SELECT FOO(name) FROM S3Object`, "UnsupportedFunction"},
		{"wrong table", `SELECT name FROM Other`, "ParseExpectedTableName"},
		{"select without from", `SELECT name`, "ParseExpectedKeyword"},
		{"bad character", `SELECT name FROM S3Object WHERE name = #`, "LexerInvalidChar"},
		{"unterminated string", `SELECT 'abc FROM S3Object`, "LexerInvalidChar"},
		{"trailing tokens", `SELECT name FROM S3Object LIMIT 1 2`, "ParseUnexpectedToken"},
		{"negative limit", `SELECT name FROM S3Object LIMIT -1`, "ParseExpectedNumber"},
		{"wrong arity", `SELECT LOWER(name, city) FROM S3Object`, "IncorrectNumberOfArguments"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := selTestExec(c.sql, selTestPeople, false, "USE")
			var se selError
			if !errors.As(err, &se) {
				t.Fatalf("want a selError, got %v", err)
			}
			if se.code != c.code {
				t.Fatalf("code = %s (%s), want %s", se.code, se.msg, c.code)
			}
		})
	}
}

func TestSelectEngineDeepNesting(t *testing.T) {
	sqlText := "SELECT " + strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000) + " FROM S3Object"
	_, err := selTestExec(sqlText, selTestPeople, false, "USE")
	var se selError
	if !errors.As(err, &se) || se.code != "ParseExpressionTooDeep" {
		t.Fatalf("want ParseExpressionTooDeep, got %v", err)
	}
}

func TestSelectLikeMatch(t *testing.T) {
	cases := []struct {
		s, pat string
		esc    rune
		want   bool
	}{
		{"alice", "a%", -1, true},
		{"alice", "%ce", -1, true},
		{"alice", "a_ice", -1, true},
		{"alice", "a_ce", -1, false},
		{"", "%", -1, true},
		{"", "_", -1, false},
		{"100%", `100\%`, '\\', true},
		{"1000", `100\%`, '\\', false},
		{"a_b", `a\_b`, '\\', true},
		{"axb", `a\_b`, '\\', false},
		{"abcabc", "%abc", -1, true},
		{"日本語", "日_語", -1, true},
	}
	for _, c := range cases {
		if got := selLikeMatch([]rune(c.s), []rune(c.pat), c.esc); got != c.want {
			t.Errorf("selLikeMatch(%q, %q) = %v, want %v", c.s, c.pat, got, c.want)
		}
	}
}

// ---------------------------------------------------------------- HTTP level

type selTestEvent struct {
	headers map[string]string
	payload []byte
}

func (e selTestEvent) kind() string {
	if e.headers[":message-type"] == "error" {
		return "error"
	}
	return e.headers[":event-type"]
}

// selTestDecode is an independent event-stream decoder: it verifies both CRCs
// and the header encoding of every message.
func selTestDecode(t *testing.T, b []byte) []selTestEvent {
	t.Helper()
	var events []selTestEvent
	for len(b) > 0 {
		if len(b) < 16 {
			t.Fatalf("truncated message: %d bytes left", len(b))
		}
		total := int(binary.BigEndian.Uint32(b[0:4]))
		hl := int(binary.BigEndian.Uint32(b[4:8]))
		if crc32.ChecksumIEEE(b[:8]) != binary.BigEndian.Uint32(b[8:12]) {
			t.Fatal("prelude CRC mismatch")
		}
		if total > len(b) || total < 16+hl {
			t.Fatalf("bad total length %d (headers %d, available %d)", total, hl, len(b))
		}
		if crc32.ChecksumIEEE(b[:total-4]) != binary.BigEndian.Uint32(b[total-4:total]) {
			t.Fatal("message CRC mismatch")
		}
		ev := selTestEvent{headers: map[string]string{}, payload: b[12+hl : total-4]}
		h := b[12 : 12+hl]
		for len(h) > 0 {
			nl := int(h[0])
			if len(h) < 1+nl+3 {
				t.Fatal("truncated header")
			}
			name := string(h[1 : 1+nl])
			if h[1+nl] != 7 {
				t.Fatalf("header %s has type %d, want 7 (string)", name, h[1+nl])
			}
			vl := int(binary.BigEndian.Uint16(h[2+nl : 4+nl]))
			if len(h) < 4+nl+vl {
				t.Fatal("truncated header value")
			}
			ev.headers[name] = string(h[4+nl : 4+nl+vl])
			h = h[4+nl+vl:]
		}
		events = append(events, ev)
		b = b[total:]
	}
	return events
}

func selTestRecords(events []selTestEvent) string {
	var sb strings.Builder
	for _, e := range events {
		if e.kind() == "Records" {
			sb.Write(e.payload)
		}
	}
	return sb.String()
}

func selTestReq(expr, in, out string) string {
	return `<SelectObjectContentRequest xmlns="` + p2XMLNS + `"><Expression>` + html.EscapeString(expr) +
		`</Expression><ExpressionType>SQL</ExpressionType><InputSerialization>` + in +
		`</InputSerialization><OutputSerialization>` + out + `</OutputSerialization></SelectObjectContentRequest>`
}

const selTestTarget = "?select&select-type=2"

func selTestPut(t *testing.T, rt *Router, key, body string) {
	t.Helper()
	p2Expect(t, p2Do(rt, http.MethodPut, "/bkt/"+key, body, nil), http.StatusOK, "")
}

func TestSelectObjectContentCSVToJSON(t *testing.T) {
	rt := newP2Router(t)
	selTestPut(t, rt, "people.csv", selTestPeople)

	rr := p2Do(rt, http.MethodPost, "/bkt/people.csv"+selTestTarget,
		selTestReq("SELECT name FROM S3Object WHERE city = 'tokyo'", `<CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV>`, `<JSON/>`), nil)
	p2Expect(t, rr, http.StatusOK, "")
	events := selTestDecode(t, rr.Body.Bytes())

	records := selTestRecords(events)
	if want := "{\"name\":\"alice\"}\n{\"name\":\"carol\"}\n"; records != want {
		t.Fatalf("records = %q, want %q", records, want)
	}
	if len(events) < 3 || events[len(events)-2].kind() != "Stats" || events[len(events)-1].kind() != "End" {
		t.Fatalf("response must end with Stats and End, got %d events", len(events))
	}
	stats := string(events[len(events)-2].payload)
	for _, want := range []string{
		"<BytesScanned>" + strconv.Itoa(len(selTestPeople)) + "</BytesScanned>",
		"<BytesProcessed>" + strconv.Itoa(len(selTestPeople)) + "</BytesProcessed>",
		"<BytesReturned>" + strconv.Itoa(len(records)) + "</BytesReturned>",
	} {
		if !strings.Contains(stats, want) {
			t.Errorf("stats %q lacks %s", stats, want)
		}
	}
	if got := events[0].headers[":content-type"]; got != "application/octet-stream" {
		t.Errorf("Records content-type = %q", got)
	}
	if got := events[len(events)-2].headers[":content-type"]; got != "text/xml" {
		t.Errorf("Stats content-type = %q", got)
	}
}

func TestSelectObjectContentCSVOutput(t *testing.T) {
	rt := newP2Router(t)
	selTestPut(t, rt, "q.csv", "name,note\n\"eve, jr\",\"say \"\"hi\"\"\"\nplain,ok\n")
	in := `<CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV>`

	rr := p2Do(rt, http.MethodPost, "/bkt/q.csv"+selTestTarget,
		selTestReq("SELECT name, note FROM S3Object", in, `<CSV/>`), nil)
	p2Expect(t, rr, http.StatusOK, "")
	if got, want := selTestRecords(selTestDecode(t, rr.Body.Bytes())), "\"eve, jr\",\"say \"\"hi\"\"\"\nplain,ok\n"; got != want {
		t.Fatalf("records = %q, want %q", got, want)
	}

	rr = p2Do(rt, http.MethodPost, "/bkt/q.csv"+selTestTarget,
		selTestReq("SELECT name, note FROM S3Object WHERE name = 'plain'", in,
			`<CSV><QuoteFields>ALWAYS</QuoteFields><FieldDelimiter>;</FieldDelimiter><RecordDelimiter>&#13;&#10;</RecordDelimiter></CSV>`), nil)
	p2Expect(t, rr, http.StatusOK, "")
	if got, want := selTestRecords(selTestDecode(t, rr.Body.Bytes())), "\"plain\";\"ok\"\r\n"; got != want {
		t.Fatalf("records = %q, want %q", got, want)
	}
}

func TestSelectObjectContentJSONInput(t *testing.T) {
	rt := newP2Router(t)
	selTestPut(t, rt, "data.json", "{\"b\":1,\"a\":\"x\"}\n{\"b\":2,\"a\":\"y\"}\n")

	// SELECT * to CSV keeps the key order of the document.
	rr := p2Do(rt, http.MethodPost, "/bkt/data.json"+selTestTarget,
		selTestReq("SELECT * FROM S3Object", `<JSON><Type>LINES</Type></JSON>`, `<CSV/>`), nil)
	p2Expect(t, rr, http.StatusOK, "")
	if got, want := selTestRecords(selTestDecode(t, rr.Body.Bytes())), "1,x\n2,y\n"; got != want {
		t.Fatalf("records = %q, want %q", got, want)
	}

	rr = p2Do(rt, http.MethodPost, "/bkt/data.json"+selTestTarget,
		selTestReq("SELECT s.a FROM S3Object s WHERE s.b >= 2", `<JSON><Type>DOCUMENT</Type></JSON>`, `<JSON><RecordDelimiter>,</RecordDelimiter></JSON>`), nil)
	p2Expect(t, rr, http.StatusOK, "")
	if got, want := selTestRecords(selTestDecode(t, rr.Body.Bytes())), `{"a":"y"},`; got != want {
		t.Fatalf("records = %q, want %q", got, want)
	}

	// A non-object record fails before the first flush, so it is a plain HTTP error.
	selTestPut(t, rt, "bad.json", "{\"b\":1}\n[1,2]\n")
	p2Expect(t, p2Do(rt, http.MethodPost, "/bkt/bad.json"+selTestTarget,
		selTestReq("SELECT * FROM S3Object", `<JSON/>`, `<JSON/>`), nil), http.StatusBadRequest, "<Code>JSONParsingError</Code>")
}

func TestSelectObjectContentCompression(t *testing.T) {
	rt := newP2Router(t)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(selTestPeople))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	selTestPut(t, rt, "people.csv.gz", gz.String())

	rr := p2Do(rt, http.MethodPost, "/bkt/people.csv.gz"+selTestTarget,
		selTestReq("SELECT _2 FROM S3Object WHERE _1 = 'bob'", `<CompressionType>GZIP</CompressionType><CSV><FileHeaderInfo>IGNORE</FileHeaderInfo></CSV>`, `<JSON/>`), nil)
	p2Expect(t, rr, http.StatusOK, "")
	events := selTestDecode(t, rr.Body.Bytes())
	if got, want := selTestRecords(events), "{\"_2\":\"25\"}\n"; got != want {
		t.Fatalf("records = %q, want %q", got, want)
	}
	stats := string(events[len(events)-2].payload)
	if !strings.Contains(stats, "<BytesScanned>"+strconv.Itoa(gz.Len())+"</BytesScanned>") ||
		!strings.Contains(stats, "<BytesProcessed>"+strconv.Itoa(len(selTestPeople))+"</BytesProcessed>") {
		t.Errorf("scanned must count compressed bytes and processed the plain ones: %s", stats)
	}

	// Plain data declared as GZIP is rejected before any output.
	selTestPut(t, rt, "plain.csv", selTestPeople)
	p2Expect(t, p2Do(rt, http.MethodPost, "/bkt/plain.csv"+selTestTarget,
		selTestReq("SELECT * FROM S3Object", `<CompressionType>GZIP</CompressionType><CSV/>`, `<JSON/>`), nil),
		http.StatusBadRequest, "<Code>InvalidCompressionFormat</Code>")
}

func TestSelectObjectContentProgress(t *testing.T) {
	rt := newP2Router(t)
	selTestPut(t, rt, "people.csv", selTestPeople)
	req := strings.Replace(
		selTestReq("SELECT name FROM S3Object", `<CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV>`, `<JSON/>`),
		"<ExpressionType>", "<RequestProgress><Enabled>true</Enabled></RequestProgress><ExpressionType>", 1)
	rr := p2Do(rt, http.MethodPost, "/bkt/people.csv"+selTestTarget, req, nil)
	p2Expect(t, rr, http.StatusOK, "")
	var kinds []string
	for _, e := range selTestDecode(t, rr.Body.Bytes()) {
		kinds = append(kinds, e.kind())
	}
	if want := []string{"Records", "Progress", "Stats", "End"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
}

func TestSelectObjectContentEarlyErrorIsHTTP(t *testing.T) {
	rt := newP2Router(t)
	selTestPut(t, rt, "people.csv", selTestPeople)
	rr := p2Do(rt, http.MethodPost, "/bkt/people.csv"+selTestTarget,
		selTestReq("SELECT CAST(city AS INT) FROM S3Object", `<CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV>`, `<JSON/>`), nil)
	p2Expect(t, rr, http.StatusBadRequest, "<Code>InvalidCast</Code>")

	selTestPut(t, rt, "broken.csv", "a,b\n\"unterminated,1\n")
	p2Expect(t, p2Do(rt, http.MethodPost, "/bkt/broken.csv"+selTestTarget,
		selTestReq("SELECT * FROM S3Object", `<CSV/>`, `<JSON/>`), nil), http.StatusBadRequest, "<Code>CSVParsingError</Code>")
}

func TestSelectObjectContentMidStreamError(t *testing.T) {
	rt := newP2Router(t)
	var sb strings.Builder
	for i := 0; i < 6000; i++ {
		fmt.Fprintf(&sb, "row%05d,%d\n", i, i)
	}
	sb.WriteString("bad,x\n")
	selTestPut(t, rt, "big.csv", sb.String())

	rr := p2Do(rt, http.MethodPost, "/bkt/big.csv"+selTestTarget,
		selTestReq("SELECT _1, CAST(_2 AS INT) AS n FROM S3Object", `<CSV/>`, `<JSON/>`), nil)
	// The status line was sent with the first flushed Records event.
	p2Expect(t, rr, http.StatusOK, "")
	events := selTestDecode(t, rr.Body.Bytes())
	last := events[len(events)-1]
	if last.kind() != "error" || last.headers[":error-code"] != "InvalidCast" || last.headers[":error-message"] == "" {
		t.Fatalf("last event = %v, want an InvalidCast error event", last.headers)
	}
	if events[0].kind() != "Records" {
		t.Fatalf("first event = %s, want Records", events[0].kind())
	}
	for _, e := range events {
		if e.kind() == "End" || e.kind() == "Stats" {
			t.Fatalf("an errored stream must not contain %s", e.kind())
		}
	}
	if !strings.HasPrefix(selTestRecords(events), "{\"_1\":\"row00000\",\"n\":0}\n") {
		t.Errorf("unexpected first record: %.60q", selTestRecords(events))
	}
}

func TestSelectObjectContentRequestValidation(t *testing.T) {
	rt := newP2Router(t)
	selTestPut(t, rt, "people.csv", selTestPeople)
	const q = "SELECT name FROM S3Object"
	const csvIn = `<CSV><FileHeaderInfo>USE</FileHeaderInfo></CSV>`
	const jsonOut = `<JSON/>`
	ok := selTestReq(q, csvIn, jsonOut)
	target := "/bkt/people.csv" + selTestTarget

	cases := []struct {
		name   string
		method string
		target string
		body   string
		status int
		want   string
	}{
		{"bad method", http.MethodGet, target, "", http.StatusMethodNotAllowed, "<Code>MethodNotAllowed</Code>"},
		{"missing bucket", http.MethodPost, "/nobucket/people.csv" + selTestTarget, ok, http.StatusNotFound, "<Code>NoSuchBucket</Code>"},
		{"missing object", http.MethodPost, "/bkt/nope.csv" + selTestTarget, ok, http.StatusNotFound, ""},
		{"not xml", http.MethodPost, target, "SELECT 1", http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"wrong root", http.MethodPost, target, `<Other/>`, http.StatusBadRequest, "<Code>MalformedXML</Code>"},
		{"no expression", http.MethodPost, target, selTestReq("", csvIn, jsonOut), http.StatusBadRequest, "<Code>MissingRequiredParameter</Code>"},
		{"bad expression type", http.MethodPost, target, strings.Replace(ok, "<ExpressionType>SQL", "<ExpressionType>PQL", 1), http.StatusBadRequest, "<Code>InvalidExpressionType</Code>"},
		{"no input", http.MethodPost, target, selTestReq(q, "", jsonOut), http.StatusBadRequest, "<Code>MissingRequiredParameter</Code>"},
		{"two inputs", http.MethodPost, target, selTestReq(q, "<CSV/><JSON/>", jsonOut), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"no output", http.MethodPost, target, selTestReq(q, csvIn, ""), http.StatusBadRequest, "<Code>MissingRequiredParameter</Code>"},
		{"two outputs", http.MethodPost, target, selTestReq(q, csvIn, "<CSV/><JSON/>"), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"parquet", http.MethodPost, target, selTestReq(q, "<Parquet/>", jsonOut), http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"scan range", http.MethodPost, target, strings.Replace(ok, "</SelectObjectContentRequest>", "<ScanRange><Start>0</Start><End>5</End></ScanRange></SelectObjectContentRequest>", 1), http.StatusNotImplemented, "<Code>NotImplemented</Code>"},
		{"bad compression", http.MethodPost, target, selTestReq(q, "<CompressionType>ZIP</CompressionType><CSV/>", jsonOut), http.StatusBadRequest, "<Code>InvalidCompressionFormat</Code>"},
		{"custom quote", http.MethodPost, target, selTestReq(q, `<CSV><QuoteCharacter>'</QuoteCharacter></CSV>`, jsonOut), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"bad header info", http.MethodPost, target, selTestReq(q, `<CSV><FileHeaderInfo>SOMETIMES</FileHeaderInfo></CSV>`, jsonOut), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"long delimiter", http.MethodPost, target, selTestReq(q, `<CSV><FieldDelimiter>ab</FieldDelimiter></CSV>`, jsonOut), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"bad json type", http.MethodPost, target, selTestReq(q, `<JSON><Type>TREE</Type></JSON>`, jsonOut), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"bad quote fields", http.MethodPost, target, selTestReq(q, csvIn, `<CSV><QuoteFields>SOMETIMES</QuoteFields></CSV>`), http.StatusBadRequest, "<Code>InvalidRequest</Code>"},
		{"bad sql", http.MethodPost, target, selTestReq("SELECT FROM S3Object", csvIn, jsonOut), http.StatusBadRequest, "<Code>ParseUnexpectedToken</Code>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p2Expect(t, p2Do(rt, c.method, c.target, c.body, nil), c.status, c.want)
		})
	}
}
