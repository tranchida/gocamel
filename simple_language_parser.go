package gocamel

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// maxSimpleEvalDepth bounds Simple Language expression nesting to prevent stack overflow
// on deeply nested parentheses, unary operators, or chained calls.
const maxSimpleEvalDepth = 256

type simpleTokenKind int

const (
	tokEOF simpleTokenKind = iota
	tokIdent
	tokMember
	tokNumber
	tokString
	tokDate
	tokFile
	tokBase64Prefix
	tokJSONPath
	tokDot
	tokNullSafe
	tokDotDot
	tokLBracket
	tokRBracket
	tokLParen
	tokRParen
	tokComma
	tokQuestion
	tokColon
	tokOr
	tokAnd
	tokEq
	tokNe
	tokGt
	tokGe
	tokLt
	tokLe
	tokNot
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokPercent
)

type simpleToken struct {
	kind       simpleTokenKind
	text       string
	raw        string
	start      int
	end        int
	dateCmd    string
	dateOffset string
	dateFormat string
}

// parseAndEvaluateSimple tokenizes, parses, and evaluates a Simple Language variable expression.
func parseAndEvaluateSimple(expr string, exchange *Exchange) (interface{}, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}

	tokens, err := tokenizeSimpleExpr(expr)
	if err != nil {
		return nil, err
	}

	p := &simpleParser{
		expr:   expr,
		tokens: tokens,
	}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if tok := p.peek(); tok.kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q in simple expression: %s", tok.raw, expr)
	}

	return evalSimpleNode(node, exchange, nil, 0)
}

func tokenizeSimpleExpr(expr string) ([]simpleToken, error) {
	var tokens []simpleToken
	prevKind := tokEOF
	i := 0
	n := len(expr)

	for i < n {
		// Immediately after '.' or '?.', read member name (or '[' after '?.') without
		// treating hyphenated names like x-request-id or keywords like 'and'/'in' as operators.
		if prevKind == tokDot || prevKind == tokNullSafe {
			if prevKind == tokNullSafe && expr[i] == '[' {
				tok := simpleToken{kind: tokLBracket, raw: "[", start: i, end: i + 1}
				tokens = append(tokens, tok)
				prevKind = tok.kind
				i++
				continue
			}
			if !isMemberStartByte(expr[i]) {
				return nil, fmt.Errorf("expected member name at position %d in expression: %s", i, expr)
			}
			start := i
			i++
			for i < n {
				ch := expr[i]
				if isMemberStartByte(ch) {
					i++
					continue
				}
				if ch == '-' && i+1 < n && isMemberStartByte(expr[i+1]) {
					i++
					continue
				}
				break
			}
			tok := simpleToken{
				kind:  tokMember,
				text:  expr[start:i],
				raw:   expr[start:i],
				start: start,
				end:   i,
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			continue
		}

		// Skip whitespace
		if expr[i] == ' ' || expr[i] == '\t' || expr[i] == '\n' || expr[i] == '\r' {
			i++
			continue
		}

		start := i
		ch := expr[i]

		// Two-character operators and delimiters
		if i+1 < n {
			two := expr[i : i+2]
			var twoKind simpleTokenKind
			matched := true
			switch two {
			case "?.":
				twoKind = tokNullSafe
			case "..":
				twoKind = tokDotDot
			case "||":
				twoKind = tokOr
			case "&&":
				twoKind = tokAnd
			case "==":
				twoKind = tokEq
			case "!=":
				twoKind = tokNe
			case ">=":
				twoKind = tokGe
			case "<=":
				twoKind = tokLe
			default:
				matched = false
			}
			if matched {
				tok := simpleToken{kind: twoKind, text: two, raw: two, start: start, end: start + 2}
				tokens = append(tokens, tok)
				prevKind = tok.kind
				i += 2
				continue
			}
		}

		// Single-character operators and delimiters
		var singleKind simpleTokenKind
		singleMatched := true
		switch ch {
		case '.':
			singleKind = tokDot
		case '[':
			singleKind = tokLBracket
		case ']':
			singleKind = tokRBracket
		case '(':
			singleKind = tokLParen
		case ')':
			singleKind = tokRParen
		case ',':
			singleKind = tokComma
		case '?':
			singleKind = tokQuestion
		case ':':
			singleKind = tokColon
		case '>':
			singleKind = tokGt
		case '<':
			singleKind = tokLt
		case '!':
			singleKind = tokNot
		case '+':
			singleKind = tokPlus
		case '-':
			singleKind = tokMinus
		case '*':
			singleKind = tokStar
		case '/':
			singleKind = tokSlash
		case '%':
			singleKind = tokPercent
		default:
			singleMatched = false
		}
		if singleMatched {
			tok := simpleToken{kind: singleKind, text: expr[start : start+1], raw: expr[start : start+1], start: start, end: start + 1}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i++
			continue
		}

		// Quoted string literal
		if ch == '\'' || ch == '"' {
			val, nextIdx, err := scanSimpleQuotedString(expr, i)
			if err != nil {
				return nil, err
			}
			tok := simpleToken{
				kind:  tokString,
				text:  val,
				raw:   expr[start:nextIdx],
				start: start,
				end:   nextIdx,
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = nextIdx
			continue
		}

		// Numeric literal
		if ch >= '0' && ch <= '9' {
			j := i + 1
			for j < n && expr[j] >= '0' && expr[j] <= '9' {
				j++
			}
			// Only consume '.' when followed by a digit so '100..199' keeps '..' intact.
			if j+1 < n && expr[j] == '.' && expr[j+1] >= '0' && expr[j+1] <= '9' {
				j++
				for j < n && expr[j] >= '0' && expr[j] <= '9' {
					j++
				}
			}
			if j < n && (expr[j] == 'e' || expr[j] == 'E') {
				k := j + 1
				if k < n && (expr[k] == '+' || expr[k] == '-') {
					k++
				}
				if k < n && expr[k] >= '0' && expr[k] <= '9' {
					j = k + 1
					for j < n && expr[j] >= '0' && expr[j] <= '9' {
						j++
					}
				}
			}
			tok := simpleToken{
				kind:  tokNumber,
				text:  expr[start:j],
				raw:   expr[start:j],
				start: start,
				end:   j,
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = j
			continue
		}

		// Prefixed functions: date:, file:, base64:, jsonpath:
		if strings.HasPrefix(expr[i:], "date:") {
			tok, nextIdx, err := scanDateToken(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = nextIdx
			continue
		}
		if strings.HasPrefix(expr[i:], "file:") {
			tok, nextIdx, err := scanFileToken(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = nextIdx
			continue
		}
		if strings.HasPrefix(expr[i:], "base64:") {
			tok, nextIdx, err := scanBase64PrefixToken(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = nextIdx
			continue
		}
		if strings.HasPrefix(expr[i:], "jsonpath:") {
			tok, nextIdx, err := scanJSONPathToken(expr, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = nextIdx
			continue
		}

		// Identifier / keyword
		if isIdentStartByte(ch) {
			j := i + 1
			for j < n && isIdentContByte(expr[j]) {
				j++
			}
			word := expr[start:j]
			tok := simpleToken{
				kind:  tokIdent,
				text:  word,
				raw:   word,
				start: start,
				end:   j,
			}
			tokens = append(tokens, tok)
			prevKind = tok.kind
			i = j
			continue
		}

		return nil, fmt.Errorf("unexpected character %q at position %d in expression: %s", ch, i, expr)
	}

	tokens = append(tokens, simpleToken{kind: tokEOF, start: n, end: n})
	return tokens, nil
}

func isIdentStartByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func isIdentContByte(b byte) bool {
	return isIdentStartByte(b) || (b >= '0' && b <= '9')
}

func isMemberStartByte(b byte) bool {
	return isIdentStartByte(b) || (b >= '0' && b <= '9')
}

func scanSimpleQuotedString(expr string, start int) (string, int, error) {
	quote := expr[start]
	var sb strings.Builder
	i := start + 1
	n := len(expr)
	for i < n {
		ch := expr[i]
		if ch == '\\' && i+1 < n {
			next := expr[i+1]
			if next == quote || next == '\\' {
				sb.WriteByte(next)
				i += 2
				continue
			}
			// Preserve other backslash escapes (e.g., regex \d, \s, \.) verbatim.
			sb.WriteByte(ch)
			sb.WriteByte(next)
			i += 2
			continue
		}
		if ch == quote {
			return sb.String(), i + 1, nil
		}
		sb.WriteByte(ch)
		i++
	}
	return "", 0, fmt.Errorf("unclosed string literal at position %d in expression: %s", start, expr)
}

func isMethodCallLookahead(expr string, dotIdx int) bool {
	if dotIdx < 0 || dotIdx >= len(expr) || expr[dotIdx] != '.' {
		return false
	}
	j := dotIdx + 1
	if j >= len(expr) || !isIdentStartByte(expr[j]) {
		return false
	}
	j++
	for j < len(expr) && isIdentContByte(expr[j]) {
		j++
	}
	return j < len(expr) && expr[j] == '('
}

func isDateCmdStopByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', ':', '(', ')', '[', ']', ',', '?', '\'', '"', '=', '!', '<', '>', '&', '|', '*', '/', '%', ';':
		return true
	}
	return false
}

func isDateFormatStopByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', ')', ']', ',', '?', '\'', '"', '=', '!', '<', '>', '&', '|', '*', '%', ';':
		return true
	}
	return false
}

func isValidDateOffsetSyntax(offset string) bool {
	if len(offset) < 3 || (offset[0] != '+' && offset[0] != '-') {
		return false
	}
	i := 1
	digits := 0
	for i < len(offset) && offset[i] >= '0' && offset[i] <= '9' {
		digits++
		i++
	}
	if digits == 0 || i >= len(offset) {
		return false
	}
	for i < len(offset) {
		b := offset[i]
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')) {
			return false
		}
		i++
	}
	return true
}

func scanDateToken(expr string, start int) (simpleToken, int, error) {
	j := start + len("date:")
	cmdStart := j
	n := len(expr)
	for j < n && !isDateCmdStopByte(expr[j]) {
		if expr[j] == '.' && isMethodCallLookahead(expr, j) {
			break
		}
		j++
	}
	cmdAndOffset := expr[cmdStart:j]
	if cmdAndOffset == "" {
		return simpleToken{}, 0, fmt.Errorf("empty date command at position %d", start)
	}

	var format string
	if j < n && expr[j] == ':' {
		j++
		if j < n && (expr[j] == '\'' || expr[j] == '"') {
			strVal, nextIdx, err := scanSimpleQuotedString(expr, j)
			if err != nil {
				return simpleToken{}, 0, err
			}
			format = strVal
			j = nextIdx
		} else {
			fmtStart := j
			for j < n && !isDateFormatStopByte(expr[j]) {
				if expr[j] == '.' && isMethodCallLookahead(expr, j) {
					break
				}
				j++
			}
			format = expr[fmtStart:j]
		}
	}

	cmd := cmdAndOffset
	offset := ""
	if plusIdx := strings.IndexByte(cmdAndOffset, '+'); plusIdx != -1 {
		cmd = cmdAndOffset[:plusIdx]
		offset = cmdAndOffset[plusIdx:]
		if !isValidDateOffsetSyntax(offset) {
			return simpleToken{}, 0, fmt.Errorf("date: invalid offset %s", offset)
		}
	} else if minusIdx := strings.IndexByte(cmdAndOffset, '-'); minusIdx != -1 {
		if minusIdx+1 < len(cmdAndOffset) && cmdAndOffset[minusIdx+1] >= '0' && cmdAndOffset[minusIdx+1] <= '9' {
			cmd = cmdAndOffset[:minusIdx]
			offset = cmdAndOffset[minusIdx:]
			if !isValidDateOffsetSyntax(offset) {
				return simpleToken{}, 0, fmt.Errorf("date: invalid offset %s", offset)
			}
		}
	}

	if cmd == "" {
		return simpleToken{}, 0, fmt.Errorf("empty date command in %q", expr[start:j])
	}

	return simpleToken{
		kind:       tokDate,
		text:       expr[start:j],
		raw:        expr[start:j],
		start:      start,
		end:        j,
		dateCmd:    cmd,
		dateOffset: offset,
		dateFormat: format,
	}, j, nil
}

func scanFileToken(expr string, start int) (simpleToken, int, error) {
	j := start + len("file:")
	fnStart := j
	n := len(expr)
	for j < n {
		ch := expr[j]
		if ch == '.' && isMethodCallLookahead(expr, j) {
			break
		}
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' {
			j++
			continue
		}
		break
	}
	fn := expr[fnStart:j]
	if fn == "" {
		return simpleToken{}, 0, fmt.Errorf("empty file function at position %d", start)
	}
	return simpleToken{
		kind:  tokFile,
		text:  fn,
		raw:   expr[start:j],
		start: start,
		end:   j,
	}, j, nil
}

func scanBase64PrefixToken(expr string, start int) (simpleToken, int, error) {
	j := start + len("base64:")
	cmdStart := j
	n := len(expr)
	for j < n && isIdentContByte(expr[j]) {
		j++
	}
	cmd := expr[cmdStart:j]
	if cmd == "" || j >= n || expr[j] != ':' {
		return simpleToken{}, 0, fmt.Errorf("invalid base64 function syntax at position %d", start)
	}
	j++ // consume ':'
	return simpleToken{
		kind:  tokBase64Prefix,
		text:  cmd,
		raw:   expr[start:j],
		start: start,
		end:   j,
	}, j, nil
}

func scanJSONPathToken(expr string, start int) (simpleToken, int, error) {
	j := start + len("jsonpath:")
	paramStart := j
	n := len(expr)

	// Optional header.<name>: prefix
	if strings.HasPrefix(expr[j:], "header.") {
		k := j + len("header.")
		for k < n && (isMemberStartByte(expr[k]) || expr[k] == '.' || expr[k] == '-') {
			k++
		}
		if k < n && expr[k] == ':' {
			j = k + 1
		}
	}

	bracketDepth := 0
	inSingle := false
	inDouble := false

	for j < n {
		ch := expr[j]
		if inSingle {
			if ch == '\\' && j+1 < n {
				j += 2
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			j++
			continue
		}
		if inDouble {
			if ch == '\\' && j+1 < n {
				j += 2
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			j++
			continue
		}
		if bracketDepth > 0 {
			switch ch {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '[':
				bracketDepth++
			case ']':
				bracketDepth--
			}
			j++
			continue
		}

		// bracketDepth == 0
		if ch == '[' {
			bracketDepth++
			j++
			continue
		}
		if ch == '.' && isMethodCallLookahead(expr, j) {
			break
		}
		if isIdentContByte(ch) || ch == '.' || ch == '$' || ch == '*' || ch == '-' {
			j++
			continue
		}
		break
	}

	if inSingle || inDouble || bracketDepth != 0 {
		return simpleToken{}, 0, fmt.Errorf("unclosed bracket or quote in jsonpath at position %d", start)
	}

	param := strings.TrimSpace(expr[paramStart:j])
	return simpleToken{
		kind:  tokJSONPath,
		text:  param,
		raw:   expr[start:j],
		start: start,
		end:   j,
	}, j, nil
}

// ==================== AST & RECURSIVE-DESCENT PARSER ====================

type simpleNode interface {
	span() (int, int)
}

type nodeSpan struct {
	start int
	end   int
}

func (s nodeSpan) span() (int, int) { return s.start, s.end }

type literalNode struct {
	nodeSpan
	val interface{}
}

type bodyNode struct{ nodeSpan }
type headerRootNode struct{ nodeSpan }
type propertyRootNode struct{ nodeSpan }
type uuidNode struct{ nodeSpan }
type lastIndexNode struct{ nodeSpan }

type headerOrPropDotNode struct {
	nodeSpan
	isProperty bool
	segments   []string
}

type parenNode struct {
	nodeSpan
	inner simpleNode
}

type mathWrapNode struct {
	nodeSpan
	inner simpleNode
}

type randomNode struct {
	nodeSpan
	arg simpleNode
}

type numericFuncNode struct {
	nodeSpan
	fn     string
	argSrc string
}

type minMaxNode struct {
	nodeSpan
	fn   string
	args []simpleNode
}

type dateNode struct {
	nodeSpan
	cmd    string
	offset string
	format string
}

type fileNode struct {
	nodeSpan
	fn string
}

type base64Node struct {
	nodeSpan
	cmd       string
	targetSrc string
}

type jsonpathNode struct {
	nodeSpan
	param string
}

type propAccessNode struct {
	nodeSpan
	target   simpleNode
	name     string
	nullSafe bool
}

type indexAccessNode struct {
	nodeSpan
	target   simpleNode
	index    simpleNode
	nullSafe bool
	usesLast bool
}

type methodCallNode struct {
	nodeSpan
	target   simpleNode
	name     string
	args     []simpleNode
	nullSafe bool
}

type unaryNotNode struct {
	nodeSpan
	operand simpleNode
}

type unaryNegNode struct {
	nodeSpan
	operand simpleNode
}

type unaryPlusNode struct {
	nodeSpan
	operand simpleNode
}

type mathNode struct {
	nodeSpan
	op    string
	left  simpleNode
	right simpleNode
}

type compareNode struct {
	nodeSpan
	op    string
	left  simpleNode
	right simpleNode
}

type stringOpNode struct {
	nodeSpan
	op     string
	negate bool
	left   simpleNode
	right  simpleNode
}

type inNode struct {
	nodeSpan
	negate bool
	left   simpleNode
	right  simpleNode
}

type isNode struct {
	nodeSpan
	negate   bool
	left     simpleNode
	typeName string
}

type rangeNode struct {
	nodeSpan
	negate bool
	left   simpleNode
	minVal int
	maxVal int
}

type logicalNode struct {
	nodeSpan
	op    string
	left  simpleNode
	right simpleNode
}

type ternaryNode struct {
	nodeSpan
	cond      simpleNode
	trueExpr  simpleNode
	falseExpr simpleNode
}

type simpleParser struct {
	expr         string
	tokens       []simpleToken
	pos          int
	depth        int
	bracketDepth int
}

func (p *simpleParser) enter() error {
	p.depth++
	if p.depth > maxSimpleEvalDepth {
		return fmt.Errorf("simple expression exceeds maximum nesting depth (%d)", maxSimpleEvalDepth)
	}
	return nil
}

func (p *simpleParser) leave() {
	p.depth--
}

func (p *simpleParser) peek() simpleToken {
	if p.pos >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.pos]
}

func (p *simpleParser) peekN(offset int) simpleToken {
	idx := p.pos + offset
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[idx]
}

func (p *simpleParser) consume() simpleToken {
	tok := p.peek()
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return tok
}

func (p *simpleParser) expect(kind simpleTokenKind, desc string) (simpleToken, error) {
	tok := p.peek()
	if tok.kind != kind {
		return simpleToken{}, fmt.Errorf("expected %s, got %q in expression: %s", desc, tok.raw, p.expr)
	}
	return p.consume(), nil
}

func (p *simpleParser) parseExpr() (simpleNode, error) {
	return p.parseTernary()
}

func (p *simpleParser) parseTernary() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	cond, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tokQuestion {
		p.consume()
		trueExpr, err := p.parseTernary()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tokColon, "':' in ternary expression"); err != nil {
			return nil, err
		}
		falseExpr, err := p.parseTernary()
		if err != nil {
			return nil, err
		}
		cs, _ := cond.span()
		_, fe := falseExpr.span()
		return &ternaryNode{
			nodeSpan:  nodeSpan{cs, fe},
			cond:      cond,
			trueExpr:  trueExpr,
			falseExpr: falseExpr,
		}, nil
	}
	return cond, nil
}

func (p *simpleParser) parseOr() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.kind == tokOr || (tok.kind == tokIdent && tok.text == "or") {
			p.consume()
			right, err := p.parseAnd()
			if err != nil {
				return nil, err
			}
			ls, _ := left.span()
			_, re := right.span()
			left = &logicalNode{nodeSpan: nodeSpan{ls, re}, op: "||", left: left, right: right}
			continue
		}
		break
	}
	return left, nil
}

func (p *simpleParser) parseAnd() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.kind == tokAnd || (tok.kind == tokIdent && tok.text == "and") {
			p.consume()
			right, err := p.parseEquality()
			if err != nil {
				return nil, err
			}
			ls, _ := left.span()
			_, re := right.span()
			left = &logicalNode{nodeSpan: nodeSpan{ls, re}, op: "&&", left: left, right: right}
			continue
		}
		break
	}
	return left, nil
}

func (p *simpleParser) parseEquality() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	left, err := p.parseRelational()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokEq || p.peek().kind == tokNe {
		op := p.consume().text
		right, err := p.parseRelational()
		if err != nil {
			return nil, err
		}
		ls, _ := left.span()
		_, re := right.span()
		left = &compareNode{nodeSpan: nodeSpan{ls, re}, op: op, left: left, right: right}
	}
	return left, nil
}

func isPredicateKeyword(word string) bool {
	switch word {
	case "contains", "startsWith", "endsWith", "regex", "in", "is", "range":
		return true
	}
	return false
}

func (p *simpleParser) parseRelational() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}

	for {
		tok := p.peek()
		switch tok.kind {
		case tokGt, tokGe, tokLt, tokLe:
			op := p.consume().text
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			ls, _ := left.span()
			_, re := right.span()
			left = &compareNode{nodeSpan: nodeSpan{ls, re}, op: op, left: left, right: right}
			continue
		case tokIdent:
			negate := false
			kw := tok.text
			if kw == "not" {
				next := p.peekN(1)
				if next.kind != tokIdent || !isPredicateKeyword(next.text) {
					return nil, fmt.Errorf("unexpected keyword %q before %q in expression: %s", tok.text, next.raw, p.expr)
				}
				p.consume()
				negate = true
				kw = p.peek().text
			}
			if !isPredicateKeyword(kw) {
				return left, nil
			}
			p.consume()
			ls, _ := left.span()

			switch kw {
			case "contains", "startsWith", "endsWith", "regex":
				right, err := p.parseAdditive()
				if err != nil {
					return nil, err
				}
				_, re := right.span()
				left = &stringOpNode{nodeSpan: nodeSpan{ls, re}, op: kw, negate: negate, left: left, right: right}
			case "in":
				right, err := p.parseAdditive()
				if err != nil {
					return nil, err
				}
				_, re := right.span()
				left = &inNode{nodeSpan: nodeSpan{ls, re}, negate: negate, left: left, right: right}
			case "is":
				typeName, endPos, err := p.parseTypeName()
				if err != nil {
					return nil, err
				}
				left = &isNode{nodeSpan: nodeSpan{ls, endPos}, negate: negate, left: left, typeName: typeName}
			case "range":
				minVal, maxVal, endPos, err := p.parseRangeBounds()
				if err != nil {
					return nil, err
				}
				left = &rangeNode{nodeSpan: nodeSpan{ls, endPos}, negate: negate, left: left, minVal: minVal, maxVal: maxVal}
			}
			continue
		}
		break
	}
	return left, nil
}

func (p *simpleParser) parseTypeName() (string, int, error) {
	tok := p.peek()
	if tok.kind == tokString {
		p.consume()
		if tok.text == "" {
			return "", 0, fmt.Errorf("empty type name in 'is' expression")
		}
		return tok.text, tok.end, nil
	}
	if tok.kind == tokIdent {
		p.consume()
		var sb strings.Builder
		sb.WriteString(tok.text)
		endPos := tok.end
		for p.peek().kind == tokDot && p.peekN(1).kind == tokMember {
			p.consume()
			mem := p.consume()
			sb.WriteByte('.')
			sb.WriteString(mem.text)
			endPos = mem.end
		}
		return sb.String(), endPos, nil
	}
	return "", 0, fmt.Errorf("expected type name after 'is', got %q", tok.raw)
}

func (p *simpleParser) parseRangeBounds() (int, int, int, error) {
	tok := p.peek()
	if tok.kind == tokString {
		p.consume()
		parts := strings.Split(tok.text, "..")
		if len(parts) != 2 {
			return 0, 0, 0, fmt.Errorf("invalid range literal %q: expected 'min..max'", tok.text)
		}
		minVal, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid range lower bound %q: %w", parts[0], err)
		}
		maxVal, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid range upper bound %q: %w", parts[1], err)
		}
		return minVal, maxVal, tok.end, nil
	}

	minVal, _, err := p.parseRangeInt()
	if err != nil {
		return 0, 0, 0, err
	}
	if _, err := p.expect(tokDotDot, "'..' in range expression"); err != nil {
		return 0, 0, 0, err
	}
	maxVal, endPos, err := p.parseRangeInt()
	if err != nil {
		return 0, 0, 0, err
	}
	return minVal, maxVal, endPos, nil
}

func (p *simpleParser) parseRangeInt() (int, int, error) {
	sign := 1
	if p.peek().kind == tokMinus {
		p.consume()
		sign = -1
	}
	numTok, err := p.expect(tokNumber, "integer bound in range")
	if err != nil {
		return 0, 0, err
	}
	val, err := strconv.Atoi(numTok.text)
	if err != nil {
		return 0, 0, fmt.Errorf("range bound must be an integer, got %q", numTok.text)
	}
	return sign * val, numTok.end, nil
}

func (p *simpleParser) parseAdditive() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokPlus || p.peek().kind == tokMinus {
		op := p.consume().text
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		ls, _ := left.span()
		_, re := right.span()
		left = &mathNode{nodeSpan: nodeSpan{ls, re}, op: op, left: left, right: right}
	}
	return left, nil
}

func (p *simpleParser) parseMultiplicative() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokStar || p.peek().kind == tokSlash || p.peek().kind == tokPercent {
		op := p.consume().text
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		ls, _ := left.span()
		_, re := right.span()
		left = &mathNode{nodeSpan: nodeSpan{ls, re}, op: op, left: left, right: right}
	}
	return left, nil
}

func (p *simpleParser) parseUnary() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	tok := p.peek()
	switch tok.kind {
	case tokNot:
		p.consume()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		_, oe := operand.span()
		return &unaryNotNode{nodeSpan: nodeSpan{tok.start, oe}, operand: operand}, nil
	case tokMinus:
		p.consume()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		_, oe := operand.span()
		return &unaryNegNode{nodeSpan: nodeSpan{tok.start, oe}, operand: operand}, nil
	case tokPlus:
		p.consume()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		_, oe := operand.span()
		return &unaryPlusNode{nodeSpan: nodeSpan{tok.start, oe}, operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *simpleParser) parsePostfix() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	// header and exchangeProperty allow dotted key names (e.g. header.app.env,
	// header.x-request-id) as well as header?.field and header['key'].
	_, isHeader := node.(*headerRootNode)
	_, isProp := node.(*propertyRootNode)
	if isHeader || isProp {
		ns, _ := node.span()
		if p.peek().kind == tokDot && p.peekN(1).kind == tokMember && p.peekN(2).kind != tokLParen {
			var segments []string
			endPos := 0
			for p.peek().kind == tokDot && p.peekN(1).kind == tokMember && p.peekN(2).kind != tokLParen {
				p.consume() // '.'
				mem := p.consume()
				segments = append(segments, mem.text)
				endPos = mem.end
			}
			node = &headerOrPropDotNode{
				nodeSpan:   nodeSpan{ns, endPos},
				isProperty: isProp,
				segments:   segments,
			}
		} else if p.peek().kind == tokNullSafe && p.peekN(1).kind == tokMember && p.peekN(2).kind != tokLParen {
			p.consume() // '?.'
			mem := p.consume()
			node = &headerOrPropDotNode{
				nodeSpan:   nodeSpan{ns, mem.end},
				isProperty: isProp,
				segments:   []string{mem.text},
			}
		} else if p.peek().kind != tokLBracket && !(p.peek().kind == tokNullSafe && p.peekN(1).kind == tokLBracket) {
			name := "header"
			if isProp {
				name = "exchangeProperty"
			}
			return nil, fmt.Errorf("variable %q requires a field or index", name)
		}
	}

	for {
		tok := p.peek()
		ns, _ := node.span()

		switch tok.kind {
		case tokLBracket:
			p.consume()
			p.bracketDepth++
			idxExpr, err := p.parseExpr()
			p.bracketDepth--
			if err != nil {
				return nil, err
			}
			closeTok, err := p.expect(tokRBracket, "']'")
			if err != nil {
				return nil, err
			}
			node = &indexAccessNode{
				nodeSpan: nodeSpan{ns, closeTok.end},
				target:   node,
				index:    idxExpr,
				nullSafe: false,
				usesLast: nodeContainsLast(idxExpr),
			}

		case tokDot:
			p.consume()
			memTok, err := p.expect(tokMember, "member name after '.'")
			if err != nil {
				return nil, err
			}
			if p.peek().kind == tokLParen {
				args, closeTok, err := p.parseArgList()
				if err != nil {
					return nil, err
				}
				node = &methodCallNode{
					nodeSpan: nodeSpan{ns, closeTok.end},
					target:   node,
					name:     memTok.text,
					args:     args,
					nullSafe: false,
				}
			} else {
				node = &propAccessNode{
					nodeSpan: nodeSpan{ns, memTok.end},
					target:   node,
					name:     memTok.text,
					nullSafe: false,
				}
			}

		case tokNullSafe:
			p.consume()
			if p.peek().kind == tokLBracket {
				p.consume()
				p.bracketDepth++
				idxExpr, err := p.parseExpr()
				p.bracketDepth--
				if err != nil {
					return nil, err
				}
				closeTok, err := p.expect(tokRBracket, "']'")
				if err != nil {
					return nil, err
				}
				node = &indexAccessNode{
					nodeSpan: nodeSpan{ns, closeTok.end},
					target:   node,
					index:    idxExpr,
					nullSafe: true,
					usesLast: nodeContainsLast(idxExpr),
				}
			} else {
				memTok, err := p.expect(tokMember, "member name after '?.'")
				if err != nil {
					return nil, err
				}
				if p.peek().kind == tokLParen {
					args, closeTok, err := p.parseArgList()
					if err != nil {
						return nil, err
					}
					node = &methodCallNode{
						nodeSpan: nodeSpan{ns, closeTok.end},
						target:   node,
						name:     memTok.text,
						args:     args,
						nullSafe: true,
					}
				} else {
					node = &propAccessNode{
						nodeSpan: nodeSpan{ns, memTok.end},
						target:   node,
						name:     memTok.text,
						nullSafe: true,
					}
				}
			}

		default:
			return node, nil
		}
	}
}

func (p *simpleParser) parseArgList() ([]simpleNode, simpleToken, error) {
	if _, err := p.expect(tokLParen, "'('"); err != nil {
		return nil, simpleToken{}, err
	}
	var args []simpleNode
	if p.peek().kind != tokRParen {
		for {
			arg, err := p.parseExpr()
			if err != nil {
				return nil, simpleToken{}, err
			}
			args = append(args, arg)
			if p.peek().kind == tokComma {
				p.consume()
				continue
			}
			break
		}
	}
	closeTok, err := p.expect(tokRParen, "')'")
	if err != nil {
		return nil, simpleToken{}, err
	}
	return args, closeTok, nil
}

func (p *simpleParser) parsePrimary() (simpleNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer p.leave()

	tok := p.peek()
	switch tok.kind {
	case tokLParen:
		openTok := p.consume()
		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		closeTok, err := p.expect(tokRParen, "')'")
		if err != nil {
			return nil, err
		}
		return &parenNode{nodeSpan: nodeSpan{openTok.start, closeTok.end}, inner: inner}, nil

	case tokString:
		p.consume()
		return &literalNode{nodeSpan: nodeSpan{tok.start, tok.end}, val: tok.text}, nil

	case tokNumber:
		p.consume()
		num, err := strconv.ParseFloat(tok.text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %w", tok.text, err)
		}
		return &literalNode{nodeSpan: nodeSpan{tok.start, tok.end}, val: num}, nil

	case tokDate:
		p.consume()
		return &dateNode{
			nodeSpan: nodeSpan{tok.start, tok.end},
			cmd:      tok.dateCmd,
			offset:   tok.dateOffset,
			format:   tok.dateFormat,
		}, nil

	case tokFile:
		p.consume()
		return &fileNode{nodeSpan: nodeSpan{tok.start, tok.end}, fn: tok.text}, nil

	case tokBase64Prefix:
		p.consume()
		target, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		ts, te := target.span()
		return &base64Node{
			nodeSpan:  nodeSpan{tok.start, te},
			cmd:       tok.text,
			targetSrc: p.expr[ts:te],
		}, nil

	case tokJSONPath:
		p.consume()
		return &jsonpathNode{nodeSpan: nodeSpan{tok.start, tok.end}, param: tok.text}, nil

	case tokIdent:
		p.consume()
		switch tok.text {
		case "true", "TRUE":
			return &literalNode{nodeSpan: nodeSpan{tok.start, tok.end}, val: true}, nil
		case "false", "FALSE":
			return &literalNode{nodeSpan: nodeSpan{tok.start, tok.end}, val: false}, nil
		case "nil", "null", "NULL":
			return &literalNode{nodeSpan: nodeSpan{tok.start, tok.end}, val: nil}, nil
		case "body":
			return &bodyNode{nodeSpan: nodeSpan{tok.start, tok.end}}, nil
		case "header":
			return &headerRootNode{nodeSpan: nodeSpan{tok.start, tok.end}}, nil
		case "exchangeProperty":
			return &propertyRootNode{nodeSpan: nodeSpan{tok.start, tok.end}}, nil
		case "uuid":
			endPos := tok.end
			if p.peek().kind == tokLParen {
				p.consume()
				closeTok, err := p.expect(tokRParen, "')' after uuid(")
				if err != nil {
					return nil, err
				}
				endPos = closeTok.end
			}
			return &uuidNode{nodeSpan: nodeSpan{tok.start, endPos}}, nil
		case "last":
			if p.bracketDepth <= 0 {
				return nil, fmt.Errorf("unknown expression: %s", tok.text)
			}
			return &lastIndexNode{nodeSpan: nodeSpan{tok.start, tok.end}}, nil
		case "math":
			if _, err := p.expect(tokLParen, "'(' after math"); err != nil {
				return nil, err
			}
			inner, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			closeTok, err := p.expect(tokRParen, "')' closing math(...)")
			if err != nil {
				return nil, err
			}
			return &mathWrapNode{nodeSpan: nodeSpan{tok.start, closeTok.end}, inner: inner}, nil
		case "random":
			if _, err := p.expect(tokLParen, "'(' after random"); err != nil {
				return nil, err
			}
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			closeTok, err := p.expect(tokRParen, "')' closing random(...)")
			if err != nil {
				return nil, err
			}
			return &randomNode{nodeSpan: nodeSpan{tok.start, closeTok.end}, arg: arg}, nil
		case "round", "floor", "ceil", "abs", "sqrt", "sin", "cos", "tan":
			if _, err := p.expect(tokLParen, "'(' after "+tok.text); err != nil {
				return nil, err
			}
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			closeTok, err := p.expect(tokRParen, "')' closing "+tok.text+"(...)")
			if err != nil {
				return nil, err
			}
			as, ae := arg.span()
			return &numericFuncNode{
				nodeSpan: nodeSpan{tok.start, closeTok.end},
				fn:       tok.text,
				argSrc:   p.expr[as:ae],
			}, nil
		case "min", "max":
			args, closeTok, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			if len(args) == 0 {
				return nil, fmt.Errorf("%s requires at least 1 argument", tok.text)
			}
			return &minMaxNode{
				nodeSpan: nodeSpan{tok.start, closeTok.end},
				fn:       tok.text,
				args:     args,
			}, nil
		default:
			return nil, fmt.Errorf("unknown expression: %s", tok.text)
		}

	default:
		return nil, fmt.Errorf("unexpected token %q in expression: %s", tok.raw, p.expr)
	}
}

func nodeContainsLast(n simpleNode) bool {
	switch v := n.(type) {
	case *lastIndexNode:
		return true
	case *parenNode:
		return nodeContainsLast(v.inner)
	case *mathWrapNode:
		return nodeContainsLast(v.inner)
	case *unaryNotNode:
		return nodeContainsLast(v.operand)
	case *unaryNegNode:
		return nodeContainsLast(v.operand)
	case *unaryPlusNode:
		return nodeContainsLast(v.operand)
	case *mathNode:
		return nodeContainsLast(v.left) || nodeContainsLast(v.right)
	case *compareNode:
		return nodeContainsLast(v.left) || nodeContainsLast(v.right)
	case *logicalNode:
		return nodeContainsLast(v.left) || nodeContainsLast(v.right)
	case *minMaxNode:
		for _, a := range v.args {
			if nodeContainsLast(a) {
				return true
			}
		}
		return false
	case *ternaryNode:
		return nodeContainsLast(v.cond) || nodeContainsLast(v.trueExpr) || nodeContainsLast(v.falseExpr)
	}
	return false
}

// ==================== AST EVALUATOR ====================

type bracketSliceState struct {
	hasLast bool
	lastIdx int
	badKind reflect.Kind
}

func evalSimpleNode(node simpleNode, exchange *Exchange, sliceState *bracketSliceState, depth int) (interface{}, error) {
	depth++
	if depth > maxSimpleEvalDepth {
		return nil, fmt.Errorf("simple expression evaluation exceeded maximum depth (%d)", maxSimpleEvalDepth)
	}

	switch n := node.(type) {
	case *literalNode:
		return n.val, nil

	case *bodyNode:
		if exchange == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return exchange.GetBody(), nil

	case *headerRootNode:
		if exchange == nil || exchange.GetIn() == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return exchange.GetIn().GetHeaders(), nil

	case *propertyRootNode:
		if exchange == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return exchange.GetProperties(), nil

	case *headerOrPropDotNode:
		if exchange == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return evalHeaderOrPropDot(n, exchange)

	case *uuidNode:
		return generateUUID(), nil

	case *lastIndexNode:
		if sliceState == nil {
			return nil, fmt.Errorf("'last' index is only valid inside brackets")
		}
		if sliceState.hasLast {
			return float64(sliceState.lastIdx), nil
		}
		if sliceState.badKind == reflect.Map {
			return nil, fmt.Errorf("'last' index not supported for maps")
		}
		return nil, fmt.Errorf("cannot use 'last' index on type %v", sliceState.badKind)

	case *parenNode:
		return evalSimpleNode(n.inner, exchange, sliceState, depth)

	case *mathWrapNode:
		return evalSimpleNode(n.inner, exchange, sliceState, depth)

	case *randomNode:
		argVal, err := evalSimpleNode(n.arg, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		maxVal, ok := parseRandomMax(argVal)
		if !ok {
			return nil, fmt.Errorf("invalid random argument: %v", argVal)
		}
		if maxVal <= 0 {
			return nil, fmt.Errorf("random argument must be positive, got %d", maxVal)
		}
		// Deliberately non-cryptographic: Camel-compatible ${random(n)} helper (gosec G404).
		return rand.Intn(maxVal), nil // #nosec G404

	case *numericFuncNode:
		return evaluateNumericFunction(n.fn, n.argSrc, exchange)

	case *minMaxNode:
		var best float64
		for i, arg := range n.args {
			v, err := evalSimpleNode(arg, exchange, sliceState, depth)
			if err != nil {
				return nil, err
			}
			f := toFloat64(v)
			if i == 0 || (n.fn == "min" && f < best) || (n.fn == "max" && f > best) {
				best = f
			}
		}
		return best, nil

	case *dateNode:
		if exchange == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return evaluateDateFunction(n.cmd, n.offset, n.format, exchange)

	case *fileNode:
		if exchange == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return evaluateFileFunction(n.fn, exchange)

	case *base64Node:
		return evaluateBase64Function(n.cmd, n.targetSrc, exchange)

	case *jsonpathNode:
		if exchange == nil {
			return nil, fmt.Errorf("exchange cannot be nil")
		}
		return evaluateJSONPathFunction(n.param, exchange)

	case *propAccessNode:
		targetVal, err := evalSimpleNode(n.target, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if targetVal == nil {
			return nil, nil
		}
		return accessProperty(targetVal, n.name)

	case *indexAccessNode:
		targetVal, err := evalSimpleNode(n.target, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if targetVal == nil && (n.nullSafe || n.usesLast) {
			return nil, nil
		}
		var childSliceState *bracketSliceState
		if targetVal != nil && n.usesLast {
			v := reflect.ValueOf(targetVal)
			for v.Kind() == reflect.Ptr {
				if v.IsNil() {
					return nil, nil
				}
				v = v.Elem()
			}
			if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
				childSliceState = &bracketSliceState{hasLast: true, lastIdx: v.Len() - 1}
			} else {
				childSliceState = &bracketSliceState{hasLast: false, badKind: v.Kind()}
			}
		}
		idxVal, err := evalSimpleNode(n.index, exchange, childSliceState, depth)
		if err != nil {
			return nil, err
		}
		return accessValueAtIndex(targetVal, idxVal)

	case *methodCallNode:
		targetVal, err := evalSimpleNode(n.target, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if n.nullSafe && targetVal == nil {
			return nil, nil
		}
		argVals := make([]interface{}, len(n.args))
		for i, arg := range n.args {
			av, err := evalSimpleNode(arg, exchange, sliceState, depth)
			if err != nil {
				return nil, err
			}
			argVals[i] = av
		}
		return applyStringFunction(n.name, targetVal, argVals)

	case *unaryNotNode:
		val, err := evalSimpleNode(n.operand, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		return !toBool(val), nil

	case *unaryNegNode:
		val, err := evalSimpleNode(n.operand, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		return -toFloat64(val), nil

	case *unaryPlusNode:
		val, err := evalSimpleNode(n.operand, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		return toFloat64(val), nil

	case *mathNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		rightVal, err := evalSimpleNode(n.right, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		l, r := toFloat64(leftVal), toFloat64(rightVal)
		switch n.op {
		case "+":
			return l + r, nil
		case "-":
			return l - r, nil
		case "*":
			return l * r, nil
		case "/":
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return l / r, nil
		case "%":
			if r == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return math.Mod(l, r), nil
		default:
			return nil, fmt.Errorf("unknown math operator: %s", n.op)
		}

	case *compareNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		rightVal, err := evalSimpleNode(n.right, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		return compareValues(leftVal, n.op, rightVal)

	case *stringOpNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		rightVal, err := evalSimpleNode(n.right, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if leftVal == nil || rightVal == nil {
			return n.negate, nil
		}
		leftStr := fmt.Sprintf("%v", leftVal)
		rightStr := fmt.Sprintf("%v", rightVal)
		var matched bool
		switch n.op {
		case "contains":
			matched = strings.Contains(leftStr, rightStr)
		case "startsWith":
			matched = strings.HasPrefix(leftStr, rightStr)
		case "endsWith":
			matched = strings.HasSuffix(leftStr, rightStr)
		case "regex":
			if len(rightStr) > MaxSimpleInputBytes {
				return false, fmt.Errorf("regex pattern length %d exceeds MaxSimpleInputBytes (%d)", len(rightStr), MaxSimpleInputBytes)
			}
			re, err := regexp.Compile(rightStr)
			if err != nil {
				return false, fmt.Errorf("invalid regex pattern: %w", err)
			}
			matched = re.MatchString(leftStr)
		default:
			return false, fmt.Errorf("unknown string operator: %s", n.op)
		}
		if n.negate {
			return !matched, nil
		}
		return matched, nil

	case *inNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		rightVal, err := evalSimpleNode(n.right, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if leftVal == nil || rightVal == nil {
			return n.negate, nil
		}
		items := extractInItems(rightVal)
		matched := containsValue(items, leftVal)
		if n.negate {
			return !matched, nil
		}
		return matched, nil

	case *isNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		matched := checkType(leftVal, n.typeName)
		if n.negate {
			return !matched, nil
		}
		return matched, nil

	case *rangeNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		numVal, ok := parseNumericForRange(leftVal)
		matched := ok && numVal >= float64(n.minVal) && numVal <= float64(n.maxVal)
		if n.negate {
			return !matched, nil
		}
		return matched, nil

	case *logicalNode:
		leftVal, err := evalSimpleNode(n.left, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if n.op == "||" {
			if toBool(leftVal) {
				return true, nil
			}
			rightVal, err := evalSimpleNode(n.right, exchange, sliceState, depth)
			if err != nil {
				return nil, err
			}
			return toBool(rightVal), nil
		}
		// &&
		if !toBool(leftVal) {
			return false, nil
		}
		rightVal, err := evalSimpleNode(n.right, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		return toBool(rightVal), nil

	case *ternaryNode:
		condVal, err := evalSimpleNode(n.cond, exchange, sliceState, depth)
		if err != nil {
			return nil, err
		}
		if toBool(condVal) {
			return evalSimpleNode(n.trueExpr, exchange, sliceState, depth)
		}
		return evalSimpleNode(n.falseExpr, exchange, sliceState, depth)

	default:
		return nil, fmt.Errorf("unknown AST node %T", node)
	}
}

func evalHeaderOrPropDot(n *headerOrPropDotNode, exchange *Exchange) (interface{}, error) {
	// Resolve longest dot-joined header/property key first so flat dotted names
	// (e.g. header.app.env) take precedence, falling back to nested struct/map access.
	for k := len(n.segments); k >= 1; k-- {
		key := strings.Join(n.segments[:k], ".")
		var val interface{}
		var exists bool
		if n.isProperty {
			val, exists = exchange.GetProperty(key)
		} else {
			val, exists = exchange.GetHeader(key)
		}
		if !exists {
			continue
		}
		for _, seg := range n.segments[k:] {
			var err error
			val, err = accessProperty(val, seg)
			if err != nil {
				return nil, err
			}
		}
		return val, nil
	}
	return nil, nil
}

func parseRandomMax(val interface{}) (int, bool) {
	switch v := val.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v {
			return 0, false
		}
		return int(v), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		return i, err == nil
	default:
		return 0, false
	}
}

func extractInItems(rightVal interface{}) []string {
	if s, ok := rightVal.(string); ok {
		parts := strings.Split(s, ",")
		for i, item := range parts {
			parts[i] = strings.TrimSpace(item)
		}
		return parts
	}
	v := reflect.ValueOf(rightVal)
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		items := make([]string, v.Len())
		for i := 0; i < v.Len(); i++ {
			items[i] = fmt.Sprintf("%v", v.Index(i).Interface())
		}
		return items
	}
	return []string{fmt.Sprintf("%v", rightVal)}
}

func parseNumericForRange(val interface{}) (float64, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case bool:
		return 0, false
	default:
		rv := reflect.ValueOf(val)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
			reflect.Float32, reflect.Float64:
			return toFloat64(val), true
		}
		return 0, false
	}
}
