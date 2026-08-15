package gocamel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(v); err != nil {
			return nil, err
		}
		var parsed any
		if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
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
	index       *int
	isWildcard  bool
	isSlice     bool
	sliceStart  int
	sliceEnd    int
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
				// parse next field or wildcard
				field, nextI, err := readField(path, i)
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, pathToken{
					isRecursive: true,
					field:       field,
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
			// Bracket notation [0], ['field'], ["field"], [*], [start:end]
			end := strings.IndexByte(path[i:], ']')
			if end == -1 {
				return nil, fmt.Errorf("unclosed bracket in jsonpath: %s", path)
			}
			content := path[i+1 : i+end]
			i += end + 1

			content = strings.TrimSpace(content)
			if content == "*" {
				tokens = append(tokens, pathToken{isWildcard: true})
			} else if (strings.HasPrefix(content, "'") && strings.HasSuffix(content, "'")) ||
				(strings.HasPrefix(content, "\"") && strings.HasSuffix(content, "\"")) {
				field := content[1 : len(content)-1]
				tokens = append(tokens, pathToken{field: field})
			} else if strings.Contains(content, ":") {
				// Slice notation [start:end]
				parts := strings.Split(content, ":")
				startIdx := 0
				endIdx := -1
				if parts[0] != "" {
					if s, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
						startIdx = s
					}
				}
				if len(parts) > 1 && parts[1] != "" {
					if e, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
						endIdx = e
					}
				}
				tokens = append(tokens, pathToken{isSlice: true, sliceStart: startIdx, sliceEnd: endIdx})
			} else {
				// Integer index
				idx, err := strconv.Atoi(content)
				if err != nil {
					return nil, fmt.Errorf("invalid array index %q in jsonpath", content)
				}
				tokens = append(tokens, pathToken{index: &idx})
			}
			continue
		}

		// Plain field name or wildcard
		field, nextI, err := readField(path, i)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, pathToken{
			field:      field,
			isWildcard: field == "*",
		})
		i = nextI
	}

	return tokens, nil
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

	for _, tok := range tokens {
		var next []any

		if tok.isRecursive {
			for _, item := range current {
				findRecursive(item, tok.field, tok.isWildcard, &next)
			}
			current = next
			continue
		}

		for _, item := range current {
			if item == nil {
				continue
			}

			if tok.isWildcard {
				switch v := item.(type) {
				case map[string]any:
					for _, val := range v {
						next = append(next, val)
					}
				case []any:
					next = append(next, v...)
				}
				continue
			}

			if tok.index != nil {
				if arr, ok := item.([]any); ok {
					idx := *tok.index
					if idx < 0 {
						idx = len(arr) + idx
					}
					if idx >= 0 && idx < len(arr) {
						next = append(next, arr[idx])
					}
				}
				continue
			}

			if tok.isSlice {
				if arr, ok := item.([]any); ok {
					start := tok.sliceStart
					if start < 0 {
						start = len(arr) + start
					}
					if start < 0 {
						start = 0
					}
					end := tok.sliceEnd
					if end < 0 {
						end = len(arr) + end + 1
					}
					if end > len(arr) || tok.sliceEnd == -1 {
						end = len(arr)
					}
					if start <= end && start < len(arr) {
						next = append(next, arr[start:end]...)
					}
				}
				continue
			}

			if tok.field != "" {
				if m, ok := item.(map[string]any); ok {
					if val, exists := m[tok.field]; exists {
						next = append(next, val)
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

func findRecursive(node any, field string, isWildcard bool, results *[]any) {
	if node == nil {
		return
	}

	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			if isWildcard || k == field {
				*results = append(*results, val)
			}
			findRecursive(val, field, isWildcard, results)
		}
	case []any:
		for _, elem := range v {
			findRecursive(elem, field, isWildcard, results)
		}
	}
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
