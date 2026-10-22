// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"compress/bzip2"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// POST /{Key+}?select&select-type=2 (SelectObjectContent).
//
// Supported: CSV and JSON (DOCUMENT / LINES) input, NONE / GZIP / BZIP2
// compression, CSV and JSON output, RequestProgress and the SQL subset of
// p2_select_sql.go. Not supported (answered with 501): Parquet input and
// ScanRange. CSV input accepts only the default quote / escape character (")
// and \n or \r\n record delimiters.

type selCSVInXML struct {
	FileHeaderInfo       string `xml:"FileHeaderInfo"`
	Comments             string `xml:"Comments"`
	FieldDelimiter       string `xml:"FieldDelimiter"`
	QuoteCharacter       string `xml:"QuoteCharacter"`
	QuoteEscapeCharacter string `xml:"QuoteEscapeCharacter"`
	RecordDelimiter      string `xml:"RecordDelimiter"`
}

type selJSONInXML struct {
	Type string `xml:"Type"`
}

type selCSVOutXML struct {
	FieldDelimiter       string `xml:"FieldDelimiter"`
	QuoteCharacter       string `xml:"QuoteCharacter"`
	QuoteEscapeCharacter string `xml:"QuoteEscapeCharacter"`
	QuoteFields          string `xml:"QuoteFields"`
	RecordDelimiter      string `xml:"RecordDelimiter"`
}

type selJSONOutXML struct {
	RecordDelimiter string `xml:"RecordDelimiter"`
}

type selRequestXML struct {
	XMLName        xml.Name
	Expression     string `xml:"Expression"`
	ExpressionType string `xml:"ExpressionType"`
	Progress       struct {
		Enabled string `xml:"Enabled"`
	} `xml:"RequestProgress"`
	Input struct {
		Compression string        `xml:"CompressionType"`
		CSV         *selCSVInXML  `xml:"CSV"`
		JSON        *selJSONInXML `xml:"JSON"`
		Parquet     *struct{}     `xml:"Parquet"`
	} `xml:"InputSerialization"`
	Output struct {
		CSV  *selCSVOutXML  `xml:"CSV"`
		JSON *selJSONOutXML `xml:"JSON"`
	} `xml:"OutputSerialization"`
	ScanRange *struct {
		Start string `xml:"Start"`
		End   string `xml:"End"`
	} `xml:"ScanRange"`
}

type selInputCfg struct {
	compression string // NONE, GZIP, BZIP2
	json        bool
	header      string // NONE, USE, IGNORE (CSV)
	comma       rune
	comment     rune // 0 = disabled
}

type selRequest struct {
	expression string
	progress   bool
	input      selInputCfg
	enc        selRowEncoder
}

func selBadRequest(msg string) *APIError {
	return p2BadRequest("InvalidRequest", msg)
}

func selNotSupported(msg string) *APIError {
	e := p2NotImplemented(msg)
	return &e
}

// selSingleRune returns def for "" and rejects anything but exactly one character.
func selSingleRune(s string, def rune, field string) (rune, *APIError) {
	if s == "" {
		return def, nil
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || size != len(s) {
		return 0, selBadRequest(field + " must be a single character")
	}
	return r, nil
}

func selParseRequest(body []byte) (*selRequest, *APIError) {
	var doc selRequestXML
	if !p2DecodeStrictXML(body, &doc) || !p2RootMatches(doc.XMLName, "SelectObjectContentRequest") {
		return nil, p2Malformed()
	}
	if strings.TrimSpace(doc.Expression) == "" {
		return nil, p2BadRequest("MissingRequiredParameter", "Expression is required")
	}
	if !strings.EqualFold(strings.TrimSpace(doc.ExpressionType), "SQL") {
		return nil, p2BadRequest("InvalidExpressionType", "The ExpressionType must be SQL")
	}
	if doc.ScanRange != nil {
		return nil, selNotSupported("ScanRange is not supported")
	}
	in, inErr := selParseInput(&doc)
	if inErr != nil {
		return nil, inErr
	}
	enc, outErr := selParseOutput(&doc)
	if outErr != nil {
		return nil, outErr
	}
	return &selRequest{
		expression: doc.Expression,
		progress:   strings.EqualFold(strings.TrimSpace(doc.Progress.Enabled), "true"),
		input:      in,
		enc:        enc,
	}, nil
}

func selParseInput(doc *selRequestXML) (selInputCfg, *APIError) {
	in := &doc.Input
	cfg := selInputCfg{compression: "NONE", header: "NONE", comma: ','}
	switch c := strings.ToUpper(strings.TrimSpace(in.Compression)); c {
	case "", "NONE":
	case "GZIP", "BZIP2":
		cfg.compression = c
	default:
		return cfg, p2BadRequest("InvalidCompressionFormat", "CompressionType must be NONE, GZIP or BZIP2")
	}
	if in.Parquet != nil {
		return cfg, selNotSupported("Parquet input is not supported")
	}
	switch {
	case in.CSV == nil && in.JSON == nil:
		return cfg, p2BadRequest("MissingRequiredParameter", "InputSerialization needs CSV or JSON")
	case in.CSV != nil && in.JSON != nil:
		return cfg, selBadRequest("InputSerialization accepts exactly one of CSV and JSON")
	}
	if in.JSON != nil {
		switch strings.ToUpper(strings.TrimSpace(in.JSON.Type)) {
		case "", "DOCUMENT", "LINES":
		default:
			return cfg, selBadRequest("JSON Type must be DOCUMENT or LINES")
		}
		cfg.json = true
		return cfg, nil
	}
	c := in.CSV
	switch h := strings.ToUpper(strings.TrimSpace(c.FileHeaderInfo)); h {
	case "", "NONE":
	case "USE", "IGNORE":
		cfg.header = h
	default:
		return cfg, selBadRequest("FileHeaderInfo must be NONE, USE or IGNORE")
	}
	comma, apiErr := selSingleRune(c.FieldDelimiter, ',', "FieldDelimiter")
	if apiErr != nil {
		return cfg, apiErr
	}
	if comma == '"' || comma == '\r' || comma == '\n' {
		return cfg, selBadRequest("FieldDelimiter is not valid")
	}
	comment, apiErr := selSingleRune(c.Comments, 0, "Comments")
	if apiErr != nil {
		return cfg, apiErr
	}
	if comment == comma || comment == '"' || comment == '\r' || comment == '\n' {
		return cfg, selBadRequest("Comments is not valid")
	}
	if (c.QuoteCharacter != "" && c.QuoteCharacter != `"`) ||
		(c.QuoteEscapeCharacter != "" && c.QuoteEscapeCharacter != `"`) {
		return cfg, selBadRequest(`CSV input supports only the double quote as QuoteCharacter and QuoteEscapeCharacter`)
	}
	switch c.RecordDelimiter {
	case "", "\n", "\r\n":
	default:
		return cfg, selBadRequest(`CSV input supports only \n and \r\n as RecordDelimiter`)
	}
	cfg.comma, cfg.comment = comma, comment
	return cfg, nil
}

func selParseOutput(doc *selRequestXML) (selRowEncoder, *APIError) {
	out := &doc.Output
	switch {
	case out.CSV == nil && out.JSON == nil:
		return nil, p2BadRequest("MissingRequiredParameter", "OutputSerialization needs CSV or JSON")
	case out.CSV != nil && out.JSON != nil:
		return nil, selBadRequest("OutputSerialization accepts exactly one of CSV and JSON")
	}
	if out.JSON != nil {
		rd := out.JSON.RecordDelimiter
		if rd == "" {
			rd = "\n"
		}
		if utf8.RuneCountInString(rd) > 2 {
			return nil, selBadRequest("RecordDelimiter must be one or two characters")
		}
		return selJSONEnc{rd: rd}, nil
	}
	c := out.CSV
	enc := selCSVEnc{fd: c.FieldDelimiter, rd: c.RecordDelimiter, quote: c.QuoteCharacter, esc: c.QuoteEscapeCharacter}
	if enc.fd == "" {
		enc.fd = ","
	}
	if enc.rd == "" {
		enc.rd = "\n"
	}
	if enc.quote == "" {
		enc.quote = `"`
	}
	if enc.esc == "" {
		enc.esc = enc.quote
	}
	if utf8.RuneCountInString(enc.rd) > 2 {
		return nil, selBadRequest("RecordDelimiter must be one or two characters")
	}
	for _, f := range []struct{ name, val string }{
		{"FieldDelimiter", enc.fd}, {"QuoteCharacter", enc.quote}, {"QuoteEscapeCharacter", enc.esc},
	} {
		if _, apiErr := selSingleRune(f.val, 0, f.name); apiErr != nil {
			return nil, apiErr
		}
	}
	switch strings.ToUpper(strings.TrimSpace(c.QuoteFields)) {
	case "", "ASNEEDED":
	case "ALWAYS":
		enc.always = true
	default:
		return nil, selBadRequest("QuoteFields must be ASNEEDED or ALWAYS")
	}
	return enc, nil
}

// selCounter counts the bytes that pass through it.
type selCounter struct {
	r io.Reader
	n int64
}

func (c *selCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// selDecompReader reports any decompression failure as InvalidCompressionFormat.
type selDecompReader struct{ r io.Reader }

func (d selDecompReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, selError{code: "InvalidCompressionFormat", msg: "the object is not valid compressed data"}
	}
	return n, err
}

// selOpenInput applies the requested decompression on top of src.
func selOpenInput(src io.Reader, cfg selInputCfg) (io.Reader, error) {
	switch cfg.compression {
	case "GZIP":
		gz, gzErr := gzip.NewReader(src)
		if gzErr != nil {
			return nil, selError{code: "InvalidCompressionFormat", msg: "the object is not valid GZIP data"}
		}
		return selDecompReader{gz}, nil
	case "BZIP2":
		return selDecompReader{bzip2.NewReader(src)}, nil
	}
	return src, nil
}

func selNewSource(cfg selInputCfg, rd io.Reader) selSource {
	if cfg.json {
		return &selJSONSource{dec: json.NewDecoder(rd)}
	}
	cr := csv.NewReader(rd)
	cr.Comma = cfg.comma
	cr.Comment = cfg.comment
	cr.FieldsPerRecord = -1
	return &selCSVSource{r: cr, header: cfg.header}
}

func selAPIError(err error) APIError {
	var se selError
	if errors.As(err, &se) {
		return se.api()
	}
	return APIError{Code: "InternalError", Message: "We encountered an internal error. Please try again.", Status: http.StatusInternalServerError}
}

func (rt *Router) handleSelect(w http.ResponseWriter, r *http.Request, bucket, key string) {
	if r.Method != http.MethodPost {
		writeError(w, r, errMethodNotAllowedP2)
		return
	}
	if !rt.requireTarget(w, r, bucket, key) {
		return
	}
	body, ok := readP2Body(w, r)
	if !ok {
		return
	}
	req, reqErr := selParseRequest(body)
	if reqErr != nil {
		writeError(w, r, *reqErr)
		return
	}
	query, parseErr := selParseQuery(req.expression)
	if parseErr != nil {
		writeError(w, r, selAPIError(parseErr))
		return
	}
	objMeta, rc, getErr := rt.store.GetObject(bucket, key)
	if getErr != nil {
		writeStoreFailure(w, r, getErr)
		return
	}
	defer rc.Close()
	// SSE-C objects are queried through their plaintext.
	rc, _, decErr := rt.decryptForRead(r.Header, sseReqHeaders, bucket, key, objMeta.Size, rc)
	if decErr != nil {
		writeFailure(w, r, decErr)
		return
	}

	scanned := &selCounter{r: rc}
	decoded, openErr := selOpenInput(scanned, req.input)
	if openErr != nil {
		writeError(w, r, selAPIError(openErr))
		return
	}
	processed := &selCounter{r: decoded}
	stream := newSelStream(w, req.enc, req.progress, scanned, processed)
	runErr := selExecute(r.Context(), query, selNewSource(req.input, processed), stream.emit)
	stream.finish(r, runErr)
}
