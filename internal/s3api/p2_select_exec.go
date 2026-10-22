// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Record sources and the query executor of SelectObjectContent (?select).

// selCols holds the header of a CSV input (FileHeaderInfo = USE).
type selCols struct {
	names []string
	index map[string]int // lower-cased name -> column position (first one wins)
}

func selMakeCols(names []string) *selCols {
	c := &selCols{names: append([]string(nil), names...), index: make(map[string]int, len(names))}
	for i, n := range names {
		k := strings.ToLower(n)
		if _, dup := c.index[k]; !dup {
			c.index[k] = i
		}
	}
	return c
}

// selRecord is one input record: a CSV row (vals) or a JSON object (obj, raw).
type selRecord struct {
	isJSON bool
	obj    map[string]interface{}
	raw    []byte
	vals   []string
	cols   *selCols
}

// selSource yields records and returns io.EOF after the last one.
type selSource interface {
	Next() (*selRecord, error)
}

// selSourceError maps a reader / parser failure to a client-visible selError.
// Unknown failures (e.g. the object store failing) are returned unchanged and
// end up as InternalError.
func selSourceError(err error, code string) error {
	var se selError
	if errors.As(err, &se) {
		return se
	}
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return selError{code: code, msg: pe.Error()}
	}
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	if errors.As(err, &syn) || errors.As(err, &typ) || errors.Is(err, io.ErrUnexpectedEOF) {
		return selError{code: code, msg: "the input is malformed: " + err.Error()}
	}
	return err
}

type selCSVSource struct {
	r      *csv.Reader
	header string // NONE, USE or IGNORE
	cols   *selCols
	seen   bool
}

func (s *selCSVSource) Next() (*selRecord, error) {
	for {
		fields, readErr := s.r.Read()
		if errors.Is(readErr, io.EOF) {
			return nil, io.EOF
		}
		if readErr != nil {
			return nil, selSourceError(readErr, "CSVParsingError")
		}
		if !s.seen {
			s.seen = true
			switch s.header {
			case "USE":
				s.cols = selMakeCols(fields)
				continue
			case "IGNORE":
				continue
			}
		}
		return &selRecord{vals: fields, cols: s.cols}, nil
	}
}

// selJSONSource reads a stream of JSON objects. DOCUMENT and LINES inputs are
// both a sequence of whitespace-separated top-level values to the decoder.
type selJSONSource struct{ dec *json.Decoder }

func (s *selJSONSource) Next() (*selRecord, error) {
	var raw json.RawMessage
	if decErr := s.dec.Decode(&raw); decErr != nil {
		if errors.Is(decErr, io.EOF) {
			return nil, io.EOF
		}
		return nil, selSourceError(decErr, "JSONParsingError")
	}
	inner := json.NewDecoder(bytes.NewReader(raw))
	inner.UseNumber()
	var obj map[string]interface{}
	if objErr := inner.Decode(&obj); objErr != nil || obj == nil {
		return nil, selError{code: "JSONParsingError", msg: "every JSON record must be an object"}
	}
	return &selRecord{isJSON: true, obj: obj, raw: raw}, nil
}

// selJSONKeys lists the top-level keys of a JSON object in document order.
func selJSONKeys(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	var keys []string
	seen := make(map[string]bool)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		k, ok := tok.(string)
		if !ok {
			break
		}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			break
		}
	}
	return keys
}

// selRow is one output row.
type selRow struct {
	names []string
	vals  []selVal
	raw   []byte // original JSON of "SELECT *" over JSON input
}

func selBuildRow(q *selQuery, rec *selRecord) selRow {
	if !q.star {
		row := selRow{names: make([]string, len(q.items)), vals: make([]selVal, len(q.items))}
		for i, it := range q.items {
			row.names[i] = it.name
			row.vals[i] = it.expr.eval(rec)
		}
		return row
	}
	if rec.isJSON {
		keys := selJSONKeys(rec.raw)
		row := selRow{names: keys, vals: make([]selVal, len(keys)), raw: rec.raw}
		for i, k := range keys {
			row.vals[i] = selFromJSON(rec.obj[k])
		}
		return row
	}
	row := selRow{names: make([]string, len(rec.vals)), vals: make([]selVal, len(rec.vals))}
	for i, v := range rec.vals {
		if rec.cols != nil && i < len(rec.cols.names) {
			row.names[i] = rec.cols.names[i]
		} else {
			row.names[i] = "_" + strconv.Itoa(i+1)
		}
		row.vals[i] = selStrVal(v)
	}
	return row
}

// selAggState accumulates one aggregate select item.
type selAggState struct {
	agg   *selAggExpr
	count int64
	sum   float64
	best  selVal
	have  bool
}

func (s *selAggState) add(rec *selRecord) {
	if s.agg.arg == nil { // COUNT(*)
		s.count++
		return
	}
	v := s.agg.arg.eval(rec)
	if v.kind == selKNull {
		return
	}
	switch s.agg.fn {
	case "COUNT":
		s.count++
	case "SUM", "AVG":
		f, ok := selToNumber(v)
		if !ok {
			selFail("InvalidCast", "cannot aggregate the non-numeric value %q", v.text())
		}
		s.sum += f
		s.count++
	case "MIN", "MAX":
		if !s.have {
			s.best, s.have = v, true
			return
		}
		c, ok := selCompare(v, s.best)
		if !ok {
			selFail("InvalidCast", "the values of %s are not comparable", s.agg.fn)
		}
		if (s.agg.fn == "MIN" && c < 0) || (s.agg.fn == "MAX" && c > 0) {
			s.best = v
		}
	}
}

func (s *selAggState) result() selVal {
	switch s.agg.fn {
	case "COUNT":
		return selNumVal(float64(s.count))
	case "SUM":
		if s.count == 0 {
			return selVal{}
		}
		return selNumVal(s.sum)
	case "AVG":
		if s.count == 0 {
			return selVal{}
		}
		return selNumVal(s.sum / float64(s.count))
	}
	if !s.have {
		return selVal{}
	}
	return s.best
}

// selExecute runs q over src and passes every output row to emit. Query
// errors come back as selError; other errors come from src or emit.
func selExecute(ctx context.Context, q *selQuery, src selSource, emit func(selRow) error) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = selRecovered(rec)
		}
	}()
	var aggs []*selAggState
	if q.hasAgg {
		for _, it := range q.items {
			aggs = append(aggs, &selAggState{agg: it.agg})
		}
	}
	var emitted int64
	for q.hasAgg || q.limit < 0 || emitted < q.limit {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rec, nextErr := src.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		if q.where != nil {
			if w := q.where.eval(rec); w.kind != selKBool || !w.b {
				continue
			}
		}
		if q.hasAgg {
			for _, a := range aggs {
				a.add(rec)
			}
			continue
		}
		if emitErr := emit(selBuildRow(q, rec)); emitErr != nil {
			return emitErr
		}
		emitted++
	}
	if q.hasAgg && q.limit != 0 {
		row := selRow{names: make([]string, len(aggs)), vals: make([]selVal, len(aggs))}
		for i, a := range aggs {
			row.names[i] = q.items[i].name
			row.vals[i] = a.result()
		}
		return emit(row)
	}
	return nil
}
