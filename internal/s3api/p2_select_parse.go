// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.

package s3api

import (
	"strconv"
	"strings"
)

// Recursive-descent parser of the SelectObjectContent SQL subset described in
// p2_select_sql.go. Syntax errors are raised with selFail and recovered once
// in selParseQuery.

// selItem is one entry of the select list.
type selItem struct {
	expr selExpr
	agg  *selAggExpr // non-nil when the item is an aggregate call
	name string      // output column name
}

// selQuery is a parsed query.
type selQuery struct {
	star   bool
	items  []selItem
	alias  string
	where  selExpr
	limit  int64 // -1 = no limit
	hasAgg bool
}

type selParser struct {
	toks       []selTok
	pos        int
	depth      int
	refs       []*selRef
	aggAllowed bool
	aggs       int
}

const selMaxDepth = 200

// selReserved cannot be used as an unquoted field name.
var selReserved = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "LIMIT": true, "AND": true,
	"OR": true, "NOT": true, "AS": true, "IN": true, "LIKE": true,
	"BETWEEN": true, "IS": true, "ESCAPE": true,
}

var selAggFuncs = map[string]bool{"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true}

// selRecovered turns a recovered panic value into a query error. Anything that
// is not a selError is a bug and is reported as an internal error.
func selRecovered(rec interface{}) selError {
	if se, ok := rec.(selError); ok {
		return se
	}
	return selError{code: "InternalError", msg: "We encountered an internal error. Please try again."}
}

// selParseQuery parses the Expression of a SelectObjectContent request.
func selParseQuery(sqlText string) (q *selQuery, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			q, err = nil, selRecovered(rec)
		}
	}()
	text := strings.TrimRight(strings.TrimSpace(sqlText), "; \t\r\n")
	p := &selParser{toks: selLex(text)}
	return p.parseSelect(), nil
}

func selDescribe(t selTok) string {
	if t.kind == 'e' {
		return "end of query"
	}
	return strconv.Quote(t.text)
}

func selUnexpected(t selTok) {
	selFail("ParseUnexpectedToken", "unexpected %s", selDescribe(t))
}

func (p *selParser) peek() selTok { return p.toks[p.pos] }

func (p *selParser) next() selTok {
	t := p.toks[p.pos]
	if t.kind != 'e' {
		p.pos++
	}
	return t
}

func (p *selParser) isKw(kw string) bool {
	t := p.peek()
	return t.kind == 'i' && strings.EqualFold(t.text, kw)
}

func (p *selParser) acceptKw(kw string) bool {
	if p.isKw(kw) {
		p.pos++
		return true
	}
	return false
}

func (p *selParser) expectKw(kw string) {
	if !p.acceptKw(kw) {
		selFail("ParseExpectedKeyword", "expected %s but found %s", kw, selDescribe(p.peek()))
	}
}

func (p *selParser) isOp(op string) bool {
	t := p.peek()
	return t.kind == 'o' && t.text == op
}

func (p *selParser) acceptOp(op string) bool {
	if p.isOp(op) {
		p.pos++
		return true
	}
	return false
}

func (p *selParser) expectOp(op string) {
	if !p.acceptOp(op) {
		selFail("ParseExpectedToken", "expected %q but found %s", op, selDescribe(p.peek()))
	}
}

func (p *selParser) enter() {
	p.depth++
	if p.depth > selMaxDepth {
		selFail("ParseExpressionTooDeep", "the expression is nested too deeply")
	}
}

func (p *selParser) leave() { p.depth-- }

func (p *selParser) parseSelect() *selQuery {
	p.expectKw("SELECT")
	q := &selQuery{limit: -1}
	if p.acceptOp("*") {
		q.star = true
	} else {
		for {
			q.items = append(q.items, p.parseItem(len(q.items)+1))
			if !p.acceptOp(",") {
				break
			}
		}
	}
	p.expectKw("FROM")
	p.parseFrom(q)
	if p.acceptKw("WHERE") {
		q.where = p.parseExpr()
	}
	if p.acceptKw("LIMIT") {
		t := p.next()
		n, err := strconv.ParseInt(t.text, 10, 64)
		if t.kind != 'n' || err != nil || n < 0 {
			selFail("ParseExpectedNumber", "LIMIT requires a non-negative integer")
		}
		q.limit = n
	}
	if t := p.peek(); t.kind != 'e' {
		selUnexpected(t)
	}
	for _, ref := range p.refs {
		ref.stripAlias(q.alias)
	}
	for _, it := range q.items {
		if it.agg != nil {
			q.hasAgg = true
		}
	}
	if q.hasAgg {
		for _, it := range q.items {
			if it.agg == nil {
				selFail("UnsupportedSyntax", "aggregate functions cannot be mixed with plain columns (GROUP BY is not supported)")
			}
		}
	}
	return q
}

func (p *selParser) parseItem(n int) selItem {
	before := p.aggs
	p.aggAllowed = true
	e := p.parseExpr()
	p.aggAllowed = false
	it := selItem{expr: e}
	if a, ok := e.(*selAggExpr); ok && p.aggs == before+1 {
		it.agg = a
	} else if p.aggs != before {
		selFail("UnsupportedSyntax", "an aggregate function must be a whole select item")
	}
	if p.acceptKw("AS") {
		t := p.next()
		if t.kind != 'i' && t.kind != 'q' {
			selFail("ParseExpectedIdentForAlias", "expected a name after AS but found %s", selDescribe(t))
		}
		it.name = t.text
	} else if ref, ok := e.(*selRef); ok && ref.lastName() != "" {
		it.name = ref.lastName()
	} else {
		it.name = "_" + strconv.Itoa(n)
	}
	return it
}

func (p *selParser) parseFrom(q *selQuery) {
	t := p.next()
	if t.kind != 'i' || !strings.EqualFold(t.text, "S3Object") {
		selFail("ParseExpectedTableName", "FROM must reference S3Object")
	}
	if p.acceptOp("[") {
		p.expectOp("*")
		p.expectOp("]")
	}
	if p.acceptKw("AS") {
		a := p.next()
		if a.kind != 'i' && a.kind != 'q' {
			selFail("ParseExpectedIdentForAlias", "expected an alias after AS but found %s", selDescribe(a))
		}
		q.alias = a.text
		return
	}
	if a := p.peek(); a.kind == 'q' || (a.kind == 'i' && !selReserved[strings.ToUpper(a.text)]) {
		q.alias = p.next().text
	}
}

func (p *selParser) parseExpr() selExpr {
	p.enter()
	defer p.leave()
	return p.parseOr()
}

func (p *selParser) parseOr() selExpr {
	l := p.parseAnd()
	for p.acceptKw("OR") {
		l = &selBin{op: "OR", l: l, r: p.parseAnd()}
	}
	return l
}

func (p *selParser) parseAnd() selExpr {
	l := p.parseNot()
	for p.acceptKw("AND") {
		l = &selBin{op: "AND", l: l, r: p.parseNot()}
	}
	return l
}

func (p *selParser) parseNot() selExpr {
	p.enter()
	defer p.leave()
	if p.acceptKw("NOT") {
		return &selUnary{op: "NOT", x: p.parseNot()}
	}
	return p.parseCmp()
}

func (p *selParser) parseCmp() selExpr {
	l := p.parseAdd()
	for {
		t := p.peek()
		if t.kind == 'o' {
			switch t.text {
			case "=", "!=", "<>", "<", "<=", ">", ">=":
				p.next()
				l = &selBin{op: t.text, l: l, r: p.parseAdd()}
				continue
			}
			return l
		}
		if t.kind != 'i' {
			return l
		}
		save := p.pos
		not := false
		if strings.EqualFold(t.text, "NOT") {
			p.next()
			not = true
			t = p.peek()
			if t.kind != 'i' {
				p.pos = save
				return l
			}
		}
		switch strings.ToUpper(t.text) {
		case "LIKE":
			p.next()
			like := &selLike{x: l, pat: p.parseAdd(), not: not}
			if p.acceptKw("ESCAPE") {
				like.esc = p.parseAdd()
			}
			l = like
		case "IN":
			p.next()
			p.expectOp("(")
			in := &selIn{x: l, not: not}
			for {
				in.list = append(in.list, p.parseExpr())
				if !p.acceptOp(",") {
					break
				}
			}
			p.expectOp(")")
			l = in
		case "BETWEEN":
			p.next()
			lo := p.parseAdd()
			p.expectKw("AND")
			l = &selBetween{x: l, lo: lo, hi: p.parseAdd(), not: not}
		case "IS":
			if not {
				p.pos = save
				return l
			}
			p.next()
			isNot := p.acceptKw("NOT")
			p.expectKw("NULL")
			l = &selIsNull{x: l, not: isNot}
		default:
			p.pos = save
			return l
		}
	}
}

func (p *selParser) parseAdd() selExpr {
	l := p.parseMul()
	for {
		t := p.peek()
		if t.kind == 'o' && (t.text == "+" || t.text == "-" || t.text == "||") {
			p.next()
			l = &selBin{op: t.text, l: l, r: p.parseMul()}
			continue
		}
		return l
	}
}

func (p *selParser) parseMul() selExpr {
	l := p.parseUnary()
	for {
		t := p.peek()
		if t.kind == 'o' && (t.text == "*" || t.text == "/" || t.text == "%") {
			p.next()
			l = &selBin{op: t.text, l: l, r: p.parseUnary()}
			continue
		}
		return l
	}
}

func (p *selParser) parseUnary() selExpr {
	p.enter()
	defer p.leave()
	if p.acceptOp("-") {
		return &selUnary{op: "-", x: p.parseUnary()}
	}
	if p.acceptOp("+") {
		return p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *selParser) parsePrimary() selExpr {
	t := p.next()
	switch t.kind {
	case 'n':
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			selFail("InvalidNumber", "the number %s is out of range", t.text)
		}
		return selLit{v: selVal{kind: selKNum, s: t.text, f: f}}
	case 's':
		return selLit{v: selStrVal(t.text)}
	case 'q':
		return p.parseRef(t)
	case 'i':
		return p.parseIdentExpr(t)
	case 'o':
		if t.text == "(" {
			e := p.parseExpr()
			p.expectOp(")")
			return e
		}
	}
	selUnexpected(t)
	return nil
}

func (p *selParser) parseIdentExpr(t selTok) selExpr {
	up := strings.ToUpper(t.text)
	if p.isOp("(") {
		switch {
		case up == "CAST":
			return p.parseCast()
		case selAggFuncs[up]:
			return p.parseAgg(up)
		}
		if _, ok := selFuncArity[up]; ok {
			return p.parseFunc(up)
		}
		selFail("UnsupportedFunction", "function %s is not supported", t.text)
	}
	switch up {
	case "NULL":
		return selLit{}
	case "TRUE":
		return selLit{v: selBoolVal(true)}
	case "FALSE":
		return selLit{v: selBoolVal(false)}
	}
	if selReserved[up] {
		selUnexpected(t)
	}
	return p.parseRef(t)
}

func (p *selParser) parseCast() selExpr {
	p.expectOp("(")
	x := p.parseExpr()
	p.expectKw("AS")
	t := p.next()
	if t.kind != 'i' {
		selUnexpected(t)
	}
	var typ string
	switch strings.ToUpper(t.text) {
	case "INT", "INTEGER":
		typ = "INT"
	case "FLOAT", "DOUBLE", "REAL", "DECIMAL", "NUMERIC":
		typ = "FLOAT"
	case "STRING", "VARCHAR", "CHAR", "TEXT":
		typ = "STRING"
	case "BOOL", "BOOLEAN":
		typ = "BOOL"
	default:
		selFail("CastFailed", "CAST to %s is not supported", t.text)
	}
	p.expectOp(")")
	return &selCast{x: x, typ: typ}
}

func (p *selParser) parseAgg(fn string) selExpr {
	if !p.aggAllowed {
		selFail("UnsupportedSyntax", "aggregate function %s is only allowed as a select item", fn)
	}
	p.expectOp("(")
	agg := &selAggExpr{fn: fn}
	if fn == "COUNT" && p.acceptOp("*") {
		// COUNT(*): no argument
	} else {
		p.aggAllowed = false
		agg.arg = p.parseExpr()
		p.aggAllowed = true
	}
	p.expectOp(")")
	p.aggs++
	return agg
}

func (p *selParser) parseFunc(name string) selExpr {
	p.expectOp("(")
	var args []selExpr
	if !p.isOp(")") {
		args = append(args, p.parseExpr())
		for {
			switch {
			case p.acceptOp(","):
			case name == "SUBSTRING" && len(args) == 1 && p.acceptKw("FROM"):
			case name == "SUBSTRING" && len(args) == 2 && p.acceptKw("FOR"):
			default:
				goto done
			}
			args = append(args, p.parseExpr())
		}
	}
done:
	p.expectOp(")")
	ar := selFuncArity[name]
	if len(args) < ar[0] || len(args) > ar[1] {
		selFail("IncorrectNumberOfArguments", "%s takes %d to %d arguments", name, ar[0], ar[1])
	}
	return &selFunc{name: name, args: args}
}

// parseRef reads the rest of a field path (.name, [index], ['name']) whose
// first component was already consumed.
func (p *selParser) parseRef(first selTok) selExpr {
	ref := &selRef{path: []selPathElem{{name: first.text, quoted: first.kind == 'q'}}}
	for {
		switch {
		case p.acceptOp("."):
			t := p.next()
			if t.kind != 'i' && t.kind != 'q' {
				selUnexpected(t)
			}
			ref.path = append(ref.path, selPathElem{name: t.text, quoted: t.kind == 'q'})
		case p.acceptOp("["):
			t := p.next()
			switch t.kind {
			case 'n':
				idx, err := strconv.Atoi(t.text)
				if err != nil || idx < 0 {
					selFail("ParseInvalidPathComponent", "invalid array index %s", t.text)
				}
				ref.path = append(ref.path, selPathElem{index: idx, isIndex: true})
			case 's':
				ref.path = append(ref.path, selPathElem{name: t.text, quoted: true})
			default:
				selUnexpected(t)
			}
			p.expectOp("]")
		default:
			p.refs = append(p.refs, ref)
			return ref
		}
	}
}
