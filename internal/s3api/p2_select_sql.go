// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SQL engine of SelectObjectContent (?select).
//
// Supported grammar:
//
//	SELECT * | expr [AS name] {, expr [AS name]}
//	FROM S3Object[[*]] [[AS] alias]
//	[WHERE expr] [LIMIT n]
//
// Expressions: literals, field references (alias.field, _N, "quoted",
// a.b[0]), AND / OR / NOT, = != <> < <= > >=, LIKE [ESCAPE], IN (...),
// BETWEEN, IS [NOT] NULL, + - * / %, ||, CAST(x AS INT|FLOAT|STRING|BOOL),
// COALESCE, NULLIF, LOWER, UPPER, TRIM, CHAR_LENGTH, SUBSTRING and the
// aggregates COUNT / SUM / AVG / MIN / MAX (only as select items, no GROUP BY).
//
// CSV fields are strings; a comparison or calculation against a number
// converts the string to a number first. CSV fields are never NULL. Missing
// JSON fields evaluate to NULL.
//
// Evaluation errors are raised with panic(selError) and recovered once, in
// selParseQuery and selExecute, to keep the evaluator free of error plumbing.

type selKind uint8

const (
	selKNull selKind = iota
	selKStr
	selKNum
	selKBool
	selKJSON
)

// selVal is a SQL value. For selKNum, s holds the original text when the
// number came from the input (so it is echoed unchanged).
type selVal struct {
	kind selKind
	s    string
	f    float64
	b    bool
	j    interface{}
}

func selStrVal(s string) selVal  { return selVal{kind: selKStr, s: s} }
func selNumVal(f float64) selVal { return selVal{kind: selKNum, f: f} }
func selBoolVal(b bool) selVal   { return selVal{kind: selKBool, b: b} }

// selError is a query error that is reported to the client.
type selError struct{ code, msg string }

func (e selError) Error() string { return e.code + ": " + e.msg }

func (e selError) api() APIError {
	status := 400
	if e.code == "InternalError" {
		status = 500
	}
	return APIError{Code: e.code, Message: e.msg, Status: status}
}

func selFail(code, format string, args ...interface{}) {
	panic(selError{code: code, msg: fmt.Sprintf(format, args...)})
}

func (v selVal) numText() string {
	if v.s != "" {
		return v.s
	}
	if math.IsNaN(v.f) || math.IsInf(v.f, 0) {
		selFail("NumericOverflow", "the numeric result is out of range")
	}
	return strconv.FormatFloat(v.f, 'f', -1, 64)
}

func (v selVal) text() string {
	switch v.kind {
	case selKStr:
		return v.s
	case selKNum:
		return v.numText()
	case selKBool:
		return strconv.FormatBool(v.b)
	case selKJSON:
		return string(selMarshal(v.j))
	}
	return ""
}

// selMarshal encodes v as compact JSON without HTML escaping.
func selMarshal(v interface{}) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		selFail("InternalError", "We encountered an internal error. Please try again.")
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func selFromJSON(v interface{}) selVal {
	switch x := v.(type) {
	case nil:
		return selVal{}
	case string:
		return selStrVal(x)
	case bool:
		return selBoolVal(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			selFail("InvalidNumber", "the number %s is out of range", x.String())
		}
		return selVal{kind: selKNum, s: x.String(), f: f}
	}
	return selVal{kind: selKJSON, j: v}
}

func selToNumber(v selVal) (float64, bool) {
	switch v.kind {
	case selKNum:
		return v.f, true
	case selKStr:
		f, err := strconv.ParseFloat(strings.TrimSpace(v.s), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// selCompare orders a and b. ok is false when either side is NULL or the
// types cannot be compared (the SQL result is then NULL).
func selCompare(a, b selVal) (int, bool) {
	if a.kind == selKNull || b.kind == selKNull {
		return 0, false
	}
	switch {
	case a.kind == selKNum || b.kind == selKNum:
		x, okA := selToNumber(a)
		y, okB := selToNumber(b)
		if !okA || !okB {
			return 0, false
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	case a.kind == selKStr && b.kind == selKStr:
		return strings.Compare(a.s, b.s), true
	case a.kind == selKBool && b.kind == selKBool:
		switch {
		case a.b == b.b:
			return 0, true
		case !a.b:
			return -1, true
		}
		return 1, true
	}
	return 0, false
}

// ---------------------------------------------------------------- lexer

type selTok struct {
	kind byte // 'i' identifier, 'q' "quoted identifier", 's' 'string', 'n' number, 'o' operator, 'e' end
	text string
}

func selLex(src string) []selTok {
	rs := []rune(src)
	var toks []selTok
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '\'' || c == '"':
			text, next := selLexQuoted(rs, i)
			kind := byte('s')
			if c == '"' {
				kind = 'q'
			}
			toks = append(toks, selTok{kind, text})
			i = next
		case c >= '0' && c <= '9':
			j := selLexNumber(rs, i)
			toks = append(toks, selTok{'n', string(rs[i:j])})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i + 1
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_' || rs[j] == '$') {
				j++
			}
			toks = append(toks, selTok{'i', string(rs[i:j])})
			i = j
		default:
			if i+1 < len(rs) {
				switch two := string(rs[i : i+2]); two {
				case "<=", ">=", "<>", "!=", "||":
					toks = append(toks, selTok{'o', two})
					i += 2
					continue
				}
			}
			if !strings.ContainsRune("=<>+-*/%(),.[]", c) {
				selFail("LexerInvalidChar", "invalid character %q", c)
			}
			toks = append(toks, selTok{'o', string(c)})
			i++
		}
	}
	return append(toks, selTok{'e', ""})
}

// selLexQuoted reads a '...' or "..." token starting at rs[i]; a doubled
// quote stands for one quote. It returns the text and the index after it.
func selLexQuoted(rs []rune, i int) (string, int) {
	quote := rs[i]
	var sb strings.Builder
	for i++; i < len(rs); i++ {
		if rs[i] == quote {
			if i+1 < len(rs) && rs[i+1] == quote {
				sb.WriteRune(quote)
				i++
				continue
			}
			return sb.String(), i + 1
		}
		sb.WriteRune(rs[i])
	}
	selFail("LexerInvalidChar", "unterminated quoted text")
	return "", 0
}

func selIsDigit(c rune) bool { return c >= '0' && c <= '9' }

func selLexNumber(rs []rune, i int) int {
	j := i
	for j < len(rs) && selIsDigit(rs[j]) {
		j++
	}
	if j+1 < len(rs) && rs[j] == '.' && selIsDigit(rs[j+1]) {
		j++
		for j < len(rs) && selIsDigit(rs[j]) {
			j++
		}
	}
	if j < len(rs) && (rs[j] == 'e' || rs[j] == 'E') {
		k := j + 1
		if k < len(rs) && (rs[k] == '+' || rs[k] == '-') {
			k++
		}
		if k < len(rs) && selIsDigit(rs[k]) {
			for k < len(rs) && selIsDigit(rs[k]) {
				k++
			}
			j = k
		}
	}
	return j
}

// ---------------------------------------------------------------- AST

type selExpr interface {
	eval(r *selRecord) selVal
}

type selLit struct{ v selVal }

func (e selLit) eval(*selRecord) selVal { return e.v }

type selPathElem struct {
	name    string
	quoted  bool
	index   int
	isIndex bool
}

type selRef struct{ path []selPathElem }

func (e *selRef) lastName() string {
	last := e.path[len(e.path)-1]
	if last.isIndex {
		return ""
	}
	return last.name
}

// stripAlias drops a leading "S3Object" or table alias from a dotted path.
func (e *selRef) stripAlias(alias string) {
	if len(e.path) < 2 {
		return
	}
	first := e.path[0]
	if first.isIndex {
		return
	}
	if strings.EqualFold(first.name, "S3Object") || (alias != "" && strings.EqualFold(first.name, alias)) {
		e.path = e.path[1:]
	}
}

// selPositional parses _N (N >= 1).
func selPositional(name string) (int, bool) {
	if len(name) < 2 || name[0] != '_' {
		return 0, false
	}
	n, err := strconv.Atoi(name[1:])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func (e *selRef) eval(r *selRecord) selVal {
	if r.isJSON {
		return e.evalJSON(r)
	}
	first := e.path[0]
	if len(e.path) > 1 || first.isIndex {
		selFail("ParseInvalidPathComponent", "CSV records have no nested fields")
	}
	if !first.quoted {
		if n, ok := selPositional(first.name); ok {
			if n > len(r.vals) {
				return selVal{}
			}
			return selStrVal(r.vals[n-1])
		}
	}
	if r.cols == nil {
		selFail("ColumnNotFound", "column %q cannot be used because the CSV input has no header (use _N)", first.name)
	}
	idx, ok := r.cols.index[strings.ToLower(first.name)]
	if !ok {
		selFail("ColumnNotFound", "column %q does not exist", first.name)
	}
	if idx >= len(r.vals) {
		return selVal{}
	}
	return selStrVal(r.vals[idx])
}

func (e *selRef) evalJSON(r *selRecord) selVal {
	var cur interface{} = r.obj
	for _, el := range e.path {
		if el.isIndex {
			arr, ok := cur.([]interface{})
			if !ok || el.index >= len(arr) {
				return selVal{}
			}
			cur = arr[el.index]
			continue
		}
		m, ok := cur.(map[string]interface{})
		if !ok {
			return selVal{}
		}
		v, ok := m[el.name]
		if !ok {
			return selVal{}
		}
		cur = v
	}
	return selFromJSON(cur)
}

type selUnary struct {
	op string // "NOT" or "-"
	x  selExpr
}

func (e *selUnary) eval(r *selRecord) selVal {
	v := e.x.eval(r)
	if v.kind == selKNull {
		return v
	}
	if e.op == "NOT" {
		if v.kind != selKBool {
			return selVal{}
		}
		return selBoolVal(!v.b)
	}
	f, ok := selToNumber(v)
	if !ok {
		selFail("InvalidCast", "cannot negate %q", v.text())
	}
	return selNumVal(-f)
}

type selBin struct {
	op   string
	l, r selExpr
}

func (e *selBin) eval(rec *selRecord) selVal {
	switch e.op {
	case "AND":
		l := e.l.eval(rec)
		if l.kind == selKBool && !l.b {
			return selBoolVal(false)
		}
		rv := e.r.eval(rec)
		if rv.kind == selKBool && !rv.b {
			return selBoolVal(false)
		}
		if l.kind == selKBool && rv.kind == selKBool {
			return selBoolVal(true)
		}
		return selVal{}
	case "OR":
		l := e.l.eval(rec)
		if l.kind == selKBool && l.b {
			return selBoolVal(true)
		}
		rv := e.r.eval(rec)
		if rv.kind == selKBool && rv.b {
			return selBoolVal(true)
		}
		if l.kind == selKBool && rv.kind == selKBool {
			return selBoolVal(false)
		}
		return selVal{}
	}
	l := e.l.eval(rec)
	rv := e.r.eval(rec)
	switch e.op {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		c, ok := selCompare(l, rv)
		if !ok {
			return selVal{}
		}
		var res bool
		switch e.op {
		case "=":
			res = c == 0
		case "!=", "<>":
			res = c != 0
		case "<":
			res = c < 0
		case "<=":
			res = c <= 0
		case ">":
			res = c > 0
		default:
			res = c >= 0
		}
		return selBoolVal(res)
	case "||":
		if l.kind == selKNull || rv.kind == selKNull {
			return selVal{}
		}
		return selStrVal(l.text() + rv.text())
	}
	if l.kind == selKNull || rv.kind == selKNull {
		return selVal{}
	}
	x, okX := selToNumber(l)
	y, okY := selToNumber(rv)
	if !okX || !okY {
		selFail("InvalidCast", "operands of %s must be numeric", e.op)
	}
	switch e.op {
	case "+":
		return selNumVal(x + y)
	case "-":
		return selNumVal(x - y)
	case "*":
		return selNumVal(x * y)
	case "/":
		if y == 0 {
			selFail("DivisionByZero", "division by zero")
		}
		return selNumVal(x / y)
	case "%":
		if y == 0 {
			selFail("DivisionByZero", "division by zero")
		}
		return selNumVal(math.Mod(x, y))
	}
	return selVal{}
}

type selLike struct {
	x, pat, esc selExpr
	not         bool
}

func (e *selLike) eval(r *selRecord) selVal {
	x, pat := e.x.eval(r), e.pat.eval(r)
	if x.kind == selKNull || pat.kind == selKNull {
		return selVal{}
	}
	esc := rune(-1)
	if e.esc != nil {
		ec := []rune(e.esc.eval(r).text())
		if len(ec) != 1 {
			selFail("InvalidEscape", "the ESCAPE value must be a single character")
		}
		esc = ec[0]
	}
	return selBoolVal(selLikeMatch([]rune(x.text()), []rune(pat.text()), esc) != e.not)
}

type selLikeTok struct {
	kind byte // 'l' literal, '_' any one, '%' any run
	r    rune
}

// selLikeMatch implements SQL LIKE: % any run, _ any one character, esc
// (when >= 0) escapes the next pattern character.
func selLikeMatch(s, pat []rune, esc rune) bool {
	toks := make([]selLikeTok, 0, len(pat))
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch {
		case esc >= 0 && c == esc:
			if i+1 >= len(pat) {
				selFail("InvalidEscape", "the pattern ends with an escape character")
			}
			i++
			toks = append(toks, selLikeTok{'l', pat[i]})
		case c == '%':
			toks = append(toks, selLikeTok{'%', 0})
		case c == '_':
			toks = append(toks, selLikeTok{'_', 0})
		default:
			toks = append(toks, selLikeTok{'l', c})
		}
	}
	si, ti := 0, 0
	starTi, starSi := -1, 0
	for si < len(s) {
		switch {
		case ti < len(toks) && toks[ti].kind == '%':
			starTi, starSi = ti, si
			ti++
		case ti < len(toks) && (toks[ti].kind == '_' || (toks[ti].kind == 'l' && toks[ti].r == s[si])):
			si++
			ti++
		case starTi >= 0:
			starSi++
			si = starSi
			ti = starTi + 1
		default:
			return false
		}
	}
	for ti < len(toks) && toks[ti].kind == '%' {
		ti++
	}
	return ti == len(toks)
}

type selIn struct {
	x    selExpr
	list []selExpr
	not  bool
}

func (e *selIn) eval(r *selRecord) selVal {
	x := e.x.eval(r)
	if x.kind == selKNull {
		return selVal{}
	}
	unknown := false
	for _, it := range e.list {
		c, ok := selCompare(x, it.eval(r))
		if !ok {
			unknown = true
			continue
		}
		if c == 0 {
			return selBoolVal(!e.not)
		}
	}
	if unknown {
		return selVal{}
	}
	return selBoolVal(e.not)
}

type selBetween struct {
	x, lo, hi selExpr
	not       bool
}

func (e *selBetween) eval(r *selRecord) selVal {
	x := e.x.eval(r)
	cLo, okLo := selCompare(x, e.lo.eval(r))
	cHi, okHi := selCompare(x, e.hi.eval(r))
	if !okLo || !okHi {
		return selVal{}
	}
	return selBoolVal((cLo >= 0 && cHi <= 0) != e.not)
}

type selIsNull struct {
	x   selExpr
	not bool
}

func (e *selIsNull) eval(r *selRecord) selVal {
	return selBoolVal((e.x.eval(r).kind == selKNull) != e.not)
}

type selCast struct {
	x   selExpr
	typ string // INT, FLOAT, STRING, BOOL
}

func (e *selCast) eval(r *selRecord) selVal {
	v := e.x.eval(r)
	if v.kind == selKNull {
		return v
	}
	switch e.typ {
	case "INT":
		f, ok := selToNumber(v)
		if !ok {
			selFail("InvalidCast", "cannot cast %q to INT", v.text())
		}
		return selNumVal(math.Trunc(f))
	case "FLOAT":
		f, ok := selToNumber(v)
		if !ok {
			selFail("InvalidCast", "cannot cast %q to FLOAT", v.text())
		}
		return selNumVal(f)
	case "STRING":
		return selStrVal(v.text())
	}
	if v.kind == selKBool {
		return v
	}
	if v.kind == selKStr {
		switch strings.ToLower(strings.TrimSpace(v.s)) {
		case "true":
			return selBoolVal(true)
		case "false":
			return selBoolVal(false)
		}
	}
	selFail("InvalidCast", "cannot cast %q to BOOL", v.text())
	return selVal{}
}

// selFuncArity gives {min, max} argument counts of the scalar functions.
var selFuncArity = map[string][2]int{
	"COALESCE": {1, 32}, "NULLIF": {2, 2}, "LOWER": {1, 1}, "UPPER": {1, 1},
	"TRIM": {1, 1}, "CHAR_LENGTH": {1, 1}, "CHARACTER_LENGTH": {1, 1}, "SUBSTRING": {2, 3},
}

type selFunc struct {
	name string
	args []selExpr
}

func (e *selFunc) eval(r *selRecord) selVal {
	args := make([]selVal, len(e.args))
	for i, a := range e.args {
		args[i] = a.eval(r)
	}
	switch e.name {
	case "COALESCE":
		for _, v := range args {
			if v.kind != selKNull {
				return v
			}
		}
		return selVal{}
	case "NULLIF":
		if c, ok := selCompare(args[0], args[1]); ok && c == 0 {
			return selVal{}
		}
		return args[0]
	}
	if args[0].kind == selKNull {
		return selVal{}
	}
	s := args[0].text()
	switch e.name {
	case "LOWER":
		return selStrVal(strings.ToLower(s))
	case "UPPER":
		return selStrVal(strings.ToUpper(s))
	case "TRIM":
		return selStrVal(strings.TrimSpace(s))
	case "CHAR_LENGTH", "CHARACTER_LENGTH":
		return selNumVal(float64(utf8.RuneCountInString(s)))
	case "SUBSTRING":
		return selSubstring(s, args)
	}
	return selVal{}
}

func selClampInt(f float64) int64 {
	const limit = 1e12
	if f > limit {
		return limit
	}
	if f < -limit {
		return -limit
	}
	return int64(math.Floor(f))
}

// selSubstring implements SUBSTRING(s, from [, length]) with 1-based positions.
func selSubstring(s string, args []selVal) selVal {
	rs := []rune(s)
	if args[1].kind == selKNull {
		return selVal{}
	}
	fromF, okFrom := selToNumber(args[1])
	if !okFrom {
		selFail("InvalidCast", "the SUBSTRING position must be numeric")
	}
	start := selClampInt(fromF)
	end := int64(len(rs)) + 1
	if len(args) == 3 {
		if args[2].kind == selKNull {
			return selVal{}
		}
		lenF, okLen := selToNumber(args[2])
		if !okLen {
			selFail("InvalidCast", "the SUBSTRING length must be numeric")
		}
		n := selClampInt(lenF)
		if n < 0 {
			selFail("InvalidArgument", "the SUBSTRING length must not be negative")
		}
		if start+n < end {
			end = start + n
		}
	}
	if start < 1 {
		start = 1
	}
	if end <= start {
		return selStrVal("")
	}
	return selStrVal(string(rs[start-1 : end-1]))
}

// selAggExpr is an aggregate call. It is only valid as a whole select item.
type selAggExpr struct {
	fn  string // COUNT, SUM, AVG, MIN, MAX
	arg selExpr
}

func (e *selAggExpr) eval(*selRecord) selVal {
	selFail("UnsupportedSyntax", "aggregate function %s is only allowed as a select item", e.fn)
	return selVal{}
}
