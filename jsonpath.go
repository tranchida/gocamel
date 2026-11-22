package gocamel

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// JSONPathExpression represents a compiled JSONPath expression that can be evaluated on an Exchange.
type JSONPathExpression struct {
	path       string
	headerName string // Optional: if set, extracts from this header instead of exchange body
}

// JsonPath creates a new JSONPathExpression evaluated against the exchange body.
// Example: JsonPath("$.store.book[0].title")
func JsonPath(path string) *JSONPathExpression {
	return &JSONPathExpression{path: path}
}

// JsonPathHeader creates a new JSONPathExpression evaluated against a specific exchange header.
// Example: JsonPathHeader("OrderPayload", "$.orderId")
func JsonPathHeader(headerName, path string) *JSONPathExpression {
	return &JSONPathExpression{headerName: headerName, path: path}
}

// Evaluate implements Expression interface.
func (e *JSONPathExpression) Evaluate(exchange *Exchange) (any, error) {
	if exchange == nil {
		return nil, fmt.Errorf("jsonpath: nil exchange")
	}

	var source any
	if e.headerName != "" {
		hVal, exists := exchange.GetIn().GetHeader(e.headerName)
		if !exists {
			return nil, fmt.Errorf("jsonpath: header %q not found", e.headerName)
		}
		source = hVal
	} else {
		source = exchange.GetIn().GetBody()
	}

	return EvaluateJSONPath(e.path, source)
}

// EvaluateAsString implements Expression interface.
func (e *JSONPathExpression) EvaluateAsString(exchange *Exchange) (string, error) {
	val, err := e.Evaluate(exchange)
	if err != nil {
		return "", err
	}
	if val == nil {
		return "", nil
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	return fmt.Sprintf("%v", val), nil
}

// EvaluateJSONPath parses and evaluates a JSONPath string against an arbitrary Go value or JSON payload.
func EvaluateJSONPath(path string, source any) (any, error) {
	data, err := normalizeJSONSource(source)
	if err != nil {
		return nil, fmt.Errorf("jsonpath source error: %w", err)
	}

	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return data, nil
	}

	if !strings.HasPrefix(path, "$") {
		path = "$." + path
	}

	return evalPath(path, data)
}

func normalizeJSONSource(source any) (any, error) {
	if source == nil {
		return nil, nil
	}

	switch v := source.(type) {
	case []byte:
		var parsed any
		if err := json.Unmarshal(v, &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse JSON bytes: %w", err)
		}
		return parsed, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if len(trimmed) == 0 {
			return nil, nil
		}
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse JSON string: %w", err)
		}
		return parsed, nil
	case io.Reader:
		b, err := ReadLimited(v, DefaultMaxBodySize)
		if err != nil {
			return nil, err
		}
		var parsed any
		if err := json.Unmarshal(b, &parsed); err != nil {
			return nil, fmt.Errorf("failed to parse JSON reader: %w", err)
		}
		return parsed, nil
	case map[string]any, []any, bool, float64, int, int64:
		return source, nil
	default:
		// Convert struct or other Go type to generic map/slice via JSON marshal/unmarshal
		raw, err := json.Marshal(source)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize source to JSON: %w", err)
		}
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON struct: %w", err)
		}
		return parsed, nil
	}
}

type pathToken struct {
	isRecursive bool
	field       string
	hasField    bool
	index       *int
	isWildcard  bool
	isSlice     bool
	sliceStart  int
	hasStart    bool
	sliceEnd    int
	hasEnd      bool
}

func tokenizePath(path string) ([]pathToken, error) {
	path = strings.TrimPrefix(path, "$")

	var tokens []pathToken
	i := 0
	n := len(path)

	for i < n {
		if path[i] == '.' {
			if i+1 < n && path[i+1] == '.' {
				// Recursive descent ..
				i += 2
				if i >= n {
					return nil, fmt.Errorf("incomplete recursive descent in jsonpath: %s", path)
				}
				if path[i] == '[' {
					tok, nextI, err := parseBracketToken(path, i)
					if err != nil {
						return nil, err
					}
					tok.isRecursive = true
					tokens = append(tokens, tok)
					i = nextI
					continue
				}
				// parse next field or wildcard
				field, nextI, err := readField(path, i)
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, pathToken{
					isRecursive: true,
					field:       field,
					hasField:    field != "*",
					isWildcard:  field == "*",
				})
				i = nextI
				continue
			}
			i++
			if i >= n {
				break
			}
		}

		if path[i] == '[' {
			tok, nextI, err := parseBracketToken(path, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, tok)
			i = nextI
			continue
		}

		// Plain field name or wildcard
		field, nextI, err := readField(path, i)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, pathToken{
			field:      field,
			hasField:   field != "*",
			isWildcard: field == "*",
		})
		i = nextI
	}

	return tokens, nil
}

func findClosingBracket(path string, openIdx int) (int, error) {
	inSingle := false
	inDouble := false
	for j := openIdx + 1; j < len(path); j++ {
		ch := path[j]
		if inSingle {
			if ch == '\\' && j+1 < len(path) {
				j++
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' && j+1 < len(path) {
				j++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case ']':
			return j, nil
		}
	}
	return -1, fmt.Errorf("unclosed bracket in jsonpath: %s", path)
}

func parseBracketToken(path string, openIdx int) (pathToken, int, error) {
	closeIdx, err := findClosingBracket(path, openIdx)
	if err != nil {
		return pathToken{}, 0, err
	}
	content := strings.TrimSpace(path[openIdx+1 : closeIdx])
	nextIdx := closeIdx + 1

	if content == "*" {
		return pathToken{isWildcard: true}, nextIdx, nil
	}

	field, isQuoted, err := parseQuotedField(content)
	if err != nil {
		return pathToken{}, 0, err
	}
	if isQuoted {
		return pathToken{field: field, hasField: true}, nextIdx, nil
	}

	if strings.Contains(content, ":") {
		tok, err := parseSliceToken(content)
		if err != nil {
			return pathToken{}, 0, err
		}
		return tok, nextIdx, nil
	}

	idx, err := strconv.Atoi(content)
	if err != nil {
		return pathToken{}, 0, fmt.Errorf("invalid array index %q in jsonpath", content)
	}
	return pathToken{index: &idx}, nextIdx, nil
}

func parseQuotedField(content string) (string, bool, error) {
	if len(content) == 0 || (content[0] != '\'' && content[0] != '"') {
		return "", false, nil
	}
	quote := content[0]
	if len(content) < 2 || content[len(content)-1] != quote {
		return "", true, fmt.Errorf("invalid quoted field %q in jsonpath", content)
	}

	var b strings.Builder
	b.Grow(len(content) - 2)
	for i := 1; i < len(content)-1; i++ {
		ch := content[i]
		if ch == '\\' {
			if i+1 >= len(content)-1 {
				return "", true, fmt.Errorf("unterminated quoted field %q in jsonpath", content)
			}
			next := content[i+1]
			if next == quote || next == '\\' {
				b.WriteByte(next)
				i++
				continue
			}
			b.WriteByte(ch)
			continue
		}
		if ch == quote {
			return "", true, fmt.Errorf("unexpected quote in field %q in jsonpath", content)
		}
		b.WriteByte(ch)
	}
	return b.String(), true, nil
}

func parseSliceToken(content string) (pathToken, error) {
	parts := strings.Split(content, ":")
	if len(parts) != 2 {
		return pathToken{}, fmt.Errorf("invalid slice %q in jsonpath", content)
	}

	tok := pathToken{isSlice: true}
	startStr := strings.TrimSpace(parts[0])
	if startStr != "" {
		s, err := strconv.Atoi(startStr)
		if err != nil {
			return pathToken{}, fmt.Errorf("invalid slice start %q in jsonpath: %w", startStr, err)
		}
		tok.sliceStart = s
		tok.hasStart = true
	}

	endStr := strings.TrimSpace(parts[1])
	if endStr != "" {
		e, err := strconv.Atoi(endStr)
		if err != nil {
			return pathToken{}, fmt.Errorf("invalid slice end %q in jsonpath: %w", endStr, err)
		}
		tok.sliceEnd = e
		tok.hasEnd = true
	}

	return tok, nil
}

func resolveArrayIndex(length, idx int) (int, bool) {
	if idx < 0 {
		idx = length + idx
	}
	if idx < 0 || idx >= length {
		return 0, false
	}
	return idx, true
}

func resolveSliceBounds(length int, tok pathToken) (int, int) {
	start := 0
	if tok.hasStart {
		start = tok.sliceStart
		if start < 0 {
			start = length + start
		}
		if start < 0 {
			start = 0
		}
		if start > length {
			start = length
		}
	}

	end := length
	if tok.hasEnd {
		end = tok.sliceEnd
		if end < 0 {
			end = length + end
		}
		if end < 0 {
			end = 0
		}
		if end > length {
			end = length
		}
	}

	return start, end
}

func readField(path string, start int) (string, int, error) {
	i := start
	for i < len(path) && path[i] != '.' && path[i] != '[' {
		i++
	}
	field := path[start:i]
	if field == "" && i < len(path) && path[i] != '[' {
		return "", i, fmt.Errorf("empty field in jsonpath at offset %d", start)
	}
	return field, i, nil
}

func evalPath(path string, data any) (any, error) {
	tokens, err := tokenizePath(path)
	if err != nil {
		return nil, err
	}

	current := []any{data}
	var state jsonPathEvalState

	for _, tok := range tokens {
		var next []any

		if tok.isRecursive {
			for _, item := range current {
				if err := findRecursive(item, tok, &next, 0, make(map[containerID]struct{}), &state); err != nil {
					return nil, err
				}
			}
			current = next
			continue
		}

		for _, item := range current {
			if err := state.visit(); err != nil {
				return nil, err
			}
			if item == nil {
				continue
			}

			if tok.isWildcard {
				switch v := item.(type) {
				case map[string]any:
					for _, val := range v {
						if err := appendJSONPathResults(&next, val); err != nil {
							return nil, err
						}
					}
				case []any:
					if err := appendJSONPathResults(&next, v...); err != nil {
						return nil, err
					}
				}
				continue
			}

			if tok.index != nil {
				if arr, ok := item.([]any); ok {
					if idx, valid := resolveArrayIndex(len(arr), *tok.index); valid {
						if err := appendJSONPathResults(&next, arr[idx]); err != nil {
							return nil, err
						}
					}
				}
				continue
			}

			if tok.isSlice {
				if arr, ok := item.([]any); ok {
					start, end := resolveSliceBounds(len(arr), tok)
					if start < end {
						if err := appendJSONPathResults(&next, arr[start:end]...); err != nil {
							return nil, err
						}
					}
				}
				continue
			}

			if tok.hasField {
				if m, ok := item.(map[string]any); ok {
					if val, exists := m[tok.field]; exists {
						if err := appendJSONPathResults(&next, val); err != nil {
							return nil, err
						}
					}
				}
			}
		}

		current = next
	}

	if len(current) == 0 {
		return nil, nil
	}
	if len(current) == 1 {
		return current[0], nil
	}
	return current, nil
}

const (
	// maxJSONPathDepth bounds the nesting depth explored by recursive descent
	// (`$..field`). Exceeding it is reported as an error instead of exhausting the
	// goroutine stack, which the Go runtime cannot recover from.
	maxJSONPathDepth = 1000
	// maxJSONPathResults bounds the number of items collected in a step to
	// prevent combinatorial explosion from chained recursive/wildcard selectors.
	maxJSONPathResults = 100_000
	// maxJSONPathVisits bounds total node visits across an evaluation to prevent
	// exponential traversal of shared-reference DAGs and chained wildcards.
	maxJSONPathVisits = 1_000_000
)

type jsonPathEvalState struct {
	visits int
}

func (s *jsonPathEvalState) visit() error {
	s.visits++
	if s.visits > maxJSONPathVisits {
		return fmt.Errorf("jsonpath: evaluation exceeded maximum node visits %d", maxJSONPathVisits)
	}
	return nil
}

func appendJSONPathResults(results *[]any, vals ...any) error {
	if len(vals) > maxJSONPathResults-len(*results) {
		return fmt.Errorf("jsonpath: evaluation exceeded maximum results %d", maxJSONPathResults)
	}
	*results = append(*results, vals...)
	return nil
}

// containerID identifies a map or slice by identity. For slices, len is part
// of the identity because the same backing array can be viewed through
// headers of different lengths; maps use len = -1.
type containerID struct {
	ptr uintptr
	len int
}

// findRecursive collects the values matching tok across node and all of its
// nested containers.
//
// Cycles: a container that is already being visited on the current path
// (an ancestor of node) is skipped, so self-referencing bodies produce a
// finite result. Containers that are merely shared between sibling branches
// (a DAG) are still visited on each path up to maxJSONPathVisits /
// maxJSONPathResults, so non-cyclic results are unchanged within bounds.
//
// Depth: descending more than maxJSONPathDepth container levels returns an
// error and the results collected so far are discarded by the caller.
func findRecursive(node any, tok pathToken, results *[]any, depth int, path map[containerID]struct{}, state *jsonPathEvalState) error {
	if err := state.visit(); err != nil {
		return err
	}

	switch v := node.(type) {
	case map[string]any:
		if depth > maxJSONPathDepth {
			return fmt.Errorf("jsonpath: recursive descent exceeded maximum depth %d", maxJSONPathDepth)
		}
		id := containerID{ptr: reflect.ValueOf(v).Pointer(), len: -1}
		if _, onPath := path[id]; onPath {
			return nil
		}
		path[id] = struct{}{}
		defer delete(path, id)

		for k, val := range v {
			if tok.isWildcard || (tok.hasField && k == tok.field) {
				if err := appendJSONPathResults(results, val); err != nil {
					return err
				}
			}
			if err := findRecursive(val, tok, results, depth+1, path, state); err != nil {
				return err
			}
		}
	case []any:
		if depth > maxJSONPathDepth {
			return fmt.Errorf("jsonpath: recursive descent exceeded maximum depth %d", maxJSONPathDepth)
		}
		id := containerID{ptr: reflect.ValueOf(v).Pointer(), len: len(v)}
		if _, onPath := path[id]; onPath {
			return nil
		}
		path[id] = struct{}{}
		defer delete(path, id)

		if tok.index != nil {
			if idx, valid := resolveArrayIndex(len(v), *tok.index); valid {
				if err := appendJSONPathResults(results, v[idx]); err != nil {
					return err
				}
			}
		} else if tok.isSlice {
			start, end := resolveSliceBounds(len(v), tok)
			if start < end {
				if err := appendJSONPathResults(results, v[start:end]...); err != nil {
					return err
				}
			}
		}

		for _, elem := range v {
			if tok.isWildcard {
				if err := appendJSONPathResults(results, elem); err != nil {
					return err
				}
			}
			if err := findRecursive(elem, tok, results, depth+1, path, state); err != nil {
				return err
			}
		}
	}
	return nil
}

// evaluateJSONPathFunction handles Simple Language ${jsonpath:...} calls.
func evaluateJSONPathFunction(param string, exchange *Exchange) (any, error) {
	param = strings.TrimSpace(param)
	if param == "" {
		return nil, fmt.Errorf("jsonpath: empty path expression")
	}

	// Check if a header is specified: header.<name>:$.path
	if strings.HasPrefix(param, "header.") {
		colonIdx := strings.Index(param, ":")
		if colonIdx != -1 {
			headerName := strings.TrimPrefix(param[:colonIdx], "header.")
			path := param[colonIdx+1:]
			return JsonPathHeader(headerName, path).Evaluate(exchange)
		}
	}

	return JsonPath(param).Evaluate(exchange)
}
