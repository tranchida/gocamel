package gocamel

import (
	"fmt"
	"math/rand"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SimpleLanguage represents the Simple Language expression evaluator
type SimpleLanguage struct {
	exchange *Exchange
}

// Expression defines the interface for evaluating expressions
type Expression interface {
	Evaluate(exchange *Exchange) (interface{}, error)
	EvaluateAsString(exchange *Exchange) (string, error)
}

// ExpressionFunc is a function type that implements Expression
type ExpressionFunc func(*Exchange) (interface{}, error)

// Evaluate implements the Expression interface
func (f ExpressionFunc) Evaluate(exchange *Exchange) (interface{}, error) {
	return f(exchange)
}

// EvaluateAsString implements the Expression interface
func (f ExpressionFunc) EvaluateAsString(exchange *Exchange) (string, error) {
	result, err := f(exchange)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", result), nil
}

// SimpleTemplate is a compiled simple language expression template
type SimpleTemplate struct {
	expression string
	isLiteral  bool
	parts      []templatePart
}

type templatePart struct {
	isVariable bool
	content    string
}

// Compile simple language regex patterns
var (
	// Matches ${expression}
	placeholderRegex = regexp.MustCompile(`\$\{([^}]+)\}`)

	// Matches body, header.name, exchangeProperty.name
	// Now supports names with hyphens and other characters
	dotNotationRegex = regexp.MustCompile(`^(body)$|^(header|[a-zA-Z]+)\.([a-zA-Z0-9_\-\.]+)$`)

	// Matches date:now:FORMAT
	dateFunctionRegex = regexp.MustCompile(`^date:now(?::([^:]+))?$`)

	// Matches random(MAX)
	randomFunctionRegex = regexp.MustCompile(`^random\((\d+)\)$`)

	// Matches uuid
	uuidFunctionRegex = regexp.MustCompile(`^uuid$`)

	// Comparison operators with proper order (multi-char operators first)
	comparisonRegex = regexp.MustCompile(`^(.+?)(==|!=|>=|<=|>|<)(.+)$`)

	// Bracket notation pattern: supports body['key'], body["key"], body[0], body[last], body[last-1]
	// Captures: base (body/header/etc), accessor (quotes or index expression), rest (for chaining)
	bracketNotationRegex = regexp.MustCompile(`^(body|header|exchangeProperty)(\?[\?\[]?|[\[]?)\[([^\]]+)\](.*)$`)

	// Null-safe dot notation: body?.field or header?.name
	nullSafeDotNotationRegex = regexp.MustCompile(`^(body|header|exchangeProperty)\?\.([a-zA-Z0-9_\-]+)(.*)$`)

	// Property access after body: body.property (for maps and structs)
	bodyPropertyRegex = regexp.MustCompile(`^body\.([a-zA-Z0-9_\-]+)(.*)$`)

	// Index access for numeric indices
	numericIndexRegex = regexp.MustCompile(`^(\d+)$`)

	// Index access for last keyword
	lastIndexRegex = regexp.MustCompile(`^last(-(\d+))?$`)
)

// ParseSimpleExpression parses a simple expression string into an Expression
func ParseSimpleExpression(expression string) (Expression, error) {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		return nil, err
	}
	return template, nil
}

// ParseSimpleTemplate parses a simple expression template
func ParseSimpleTemplate(expression string) (*SimpleTemplate, error) {
	template := &SimpleTemplate{
		expression: expression,
		parts:      make([]templatePart, 0),
	}

	// Check if it's a simple literal (no placeholders)
	if !placeholderRegex.MatchString(expression) {
		template.isLiteral = true
		template.parts = append(template.parts, templatePart{isVariable: false, content: expression})
		return template, nil
	}

	// Parse the template and extract placeholders
	lastIndex := 0
	matches := placeholderRegex.FindAllStringIndex(expression, -1)
	for _, match := range matches {
		start, end := match[0], match[1]

		// Add literal text before the placeholder
		if start > lastIndex {
			template.parts = append(template.parts, templatePart{
				isVariable: false,
				content:    expression[lastIndex:start],
			})
		}

		// Extract the expression inside ${...}
		innerExpr := expression[start+2 : end-1]
		template.parts = append(template.parts, templatePart{
			isVariable: true,
			content:    strings.TrimSpace(innerExpr),
		})

		lastIndex = end
	}

	// Add remaining literal text
	if lastIndex < len(expression) {
		template.parts = append(template.parts, templatePart{
			isVariable: false,
			content:    expression[lastIndex:],
		})
	}

	return template, nil
}

// Evaluate evaluates the template and returns the result
func (t *SimpleTemplate) Evaluate(exchange *Exchange) (interface{}, error) {
	if t.isLiteral {
		return t.parts[0].content, nil
	}

	// Build result from parts
	var result strings.Builder
	for _, part := range t.parts {
		if part.isVariable {
			value, err := evaluateVariable(part.content, exchange)
			if err != nil {
				return nil, err
			}
			result.WriteString(fmt.Sprintf("%v", value))
		} else {
			result.WriteString(part.content)
		}
	}

	return result.String(), nil
}

// EvaluateAsString evaluates the template and returns a string
func (t *SimpleTemplate) EvaluateAsString(exchange *Exchange) (string, error) {
	result, err := t.Evaluate(exchange)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", result), nil
}

// EvaluateAsBool evaluates the template and returns a boolean
func (t *SimpleTemplate) EvaluateAsBool(exchange *Exchange) (bool, error) {
	result, err := t.Evaluate(exchange)
	if err != nil {
		return false, err
	}

	// Handle boolean directly
	if b, ok := result.(bool); ok {
		return b, nil
	}

	// Convert string result to boolean
	if s, ok := result.(string); ok {
		s = strings.ToLower(strings.TrimSpace(s))
		// Check for explicit boolean strings
		if s == "true" || s == "1" || s == "yes" {
			return true, nil
		}
		if s == "false" || s == "0" || s == "no" || s == "" {
			return false, nil
		}
		// Try parsing as number
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n != 0, nil
		}
		// Non-numeric, non-boolean strings are considered truthy
		return true, nil
	}

	// Handle numeric result
	if n, ok := result.(float64); ok {
		return n != 0, nil
	}
	if n, ok := result.(int); ok {
		return n != 0, nil
	}

	return result != nil, nil
}

// evaluateVariable evaluates a single variable expression
func evaluateVariable(expr string, exchange *Exchange) (interface{}, error) {
	expr = strings.TrimSpace(expr)

	// Check for comparison operators first
	if compMatch := comparisonRegex.FindStringSubmatch(expr); compMatch != nil {
		left := strings.TrimSpace(compMatch[1])
		op := compMatch[2]
		right := strings.TrimSpace(compMatch[3])

		leftVal, err := evaluateVariable(left, exchange)
		if err != nil {
			return nil, err
		}

		rightVal, err := evaluateVariable(right, exchange)
		if err != nil {
			// Try to parse as literal
			if strings.HasPrefix(right, "'") && strings.HasSuffix(right, "'") {
				rightVal = strings.Trim(right, "'")
			} else if strings.HasPrefix(right, "\"") && strings.HasSuffix(right, "\"") {
				rightVal = strings.Trim(right, "\"")
			} else {
				return nil, err
			}
		}

		return compareValues(leftVal, op, rightVal)
	}

	// Check for null-safe bracket notation first (e.g., body?.['key'])
	if strings.Contains(expr, "?.") && strings.Contains(expr, "[") {
		result, err := evaluateNullSafeBracketNotation(expr, exchange)
		if err == nil {
			return result, nil
		}
		// If it fails, continue to other patterns
	}

	// Check for bracket notation (e.g., body['key'], body[0])
	if strings.Contains(expr, "[") {
		result, err := evaluateBracketNotation(expr, exchange)
		if err == nil {
			return result, nil
		}
		// If it fails, continue to other patterns
	}

	// Check for null-safe dot notation (e.g., body?.field)
	if strings.Contains(expr, "?.") {
		result, err := evaluateNullSafeDotNotation(expr, exchange)
		if err == nil {
			return result, nil
		}
		// If it fails, continue to other patterns
	}

	// Check for body.property (map/struct access)
	if strings.HasPrefix(expr, "body.") && !strings.HasPrefix(expr, "body?") {
		result, err := evaluateBodyProperty(expr, exchange)
		if err == nil {
			return result, nil
		}
	}

	// Check for variable access patterns - now more flexible
	if match := dotNotationRegex.FindStringSubmatch(expr); match != nil {
		return evaluateDotNotationRegex(match, exchange)
	}

	// Check for date function
	if dateMatch := dateFunctionRegex.FindStringSubmatch(expr); dateMatch != nil {
		format := dateMatch[1]
		if format == "" {
			format = time.RFC3339
		}
		return time.Now().Format(format), nil
	}

	// Check for random function
	if randomMatch := randomFunctionRegex.FindStringSubmatch(expr); randomMatch != nil {
		max, err := strconv.Atoi(randomMatch[1])
		if err != nil {
			return nil, fmt.Errorf("invalid random argument: %s", randomMatch[1])
		}
		return rand.Intn(max), nil
	}

	// Check for uuid function
	if uuidFunctionRegex.MatchString(expr) {
		return generateUUID(), nil
	}

	// Check if it's a literal string (quoted)
	if (strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) ||
		(strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"")) {
		return strings.Trim(expr, "'\""), nil
	}

	// Check if it's a numeric literal
	if num, err := strconv.ParseFloat(expr, 64); err == nil {
		return num, nil
	}

	// Check if it's a boolean literal
	if expr == "true" || expr == "TRUE" {
		return true, nil
	}
	if expr == "false" || expr == "FALSE" {
		return false, nil
	}

	// Legacy: also check header. and exchangeProperty. patterns directly
	if strings.HasPrefix(expr, "header.") {
		name := strings.TrimPrefix(expr, "header.")
		value, exists := exchange.GetHeader(name)
		if !exists {
			return nil, nil
		}
		return value, nil
	}
	if strings.HasPrefix(expr, "exchangeProperty.") {
		name := strings.TrimPrefix(expr, "exchangeProperty.")
		value, exists := exchange.GetProperty(name)
		if !exists {
			return nil, nil
		}
		return value, nil
	}

	return nil, fmt.Errorf("unknown expression: %s", expr)
}

// evaluateBracketNotation evaluates bracket notation like body['key'], body[0], body['key']['subkey']
func evaluateBracketNotation(expr string, exchange *Exchange) (interface{}, error) {
	// Parse the expression step by step
	// Format: base[accessor]rest where rest can be more brackets or empty

	// Remove any leading/trailing whitespace
	expr = strings.TrimSpace(expr)

	// Find the base (body, header, or exchangeProperty)
	var base, indexExpr, rest string
	var bracketStart int

	// Find first opening bracket that's not part of a null-safe operator
	for i := 0; i < len(expr); i++ {
		if expr[i] == '[' {
			// Check if this is after a null-safe operator (?.)
			if i >= 2 && expr[i-2:i] == "?." {
				continue // Skip, this is part of null-safe notation
			}
			base = strings.TrimSpace(expr[:i])
			bracketStart = i
			break
		}
	}

	if bracketStart == 0 {
		return nil, fmt.Errorf("no bracket found in expression")
	}

	// Find matching closing bracket
	bracketEnd := -1
	depth := 0
	for i := bracketStart; i < len(expr); i++ {
		if expr[i] == '[' {
			depth++
		} else if expr[i] == ']' {
			depth--
			if depth == 0 {
				bracketEnd = i
				break
			}
		}
	}

	if bracketEnd == -1 {
		return nil, fmt.Errorf("unmatched bracket in expression")
	}

	indexExpr = strings.TrimSpace(expr[bracketStart+1 : bracketEnd])
	rest = strings.TrimSpace(expr[bracketEnd+1:])

	// Get the base value
	var currentVal interface{}
	switch base {
	case "body":
		currentVal = exchange.GetBody()
	case "header":
		// For header['name'] pattern, we need the headers map - get from input message
		currentVal = exchange.GetIn().GetHeaders()
	case "exchangeProperty":
		currentVal = exchange.GetProperties()
	default:
		return nil, fmt.Errorf("unknown base in bracket notation: %s", base)
	}

	// Resolve the index
	var resolvedIndex interface{}
	if strings.HasPrefix(indexExpr, "'") && strings.HasSuffix(indexExpr, "'") {
		resolvedIndex = strings.Trim(indexExpr, "'")
	} else if strings.HasPrefix(indexExpr, "\"") && strings.HasSuffix(indexExpr, "\"") {
		resolvedIndex = strings.Trim(indexExpr, "\"")
	} else if numMatch := numericIndexRegex.FindStringSubmatch(indexExpr); numMatch != nil {
		idx, err := strconv.Atoi(numMatch[1])
		if err != nil {
			return nil, fmt.Errorf("invalid numeric index: %s", indexExpr)
		}
		resolvedIndex = idx
	} else if lastMatch := lastIndexRegex.FindStringSubmatch(indexExpr); lastMatch != nil {
		// Handle 'last' and 'last-N' indices
		offset := 0
		if lastMatch[2] != "" {
			var err error
			offset, err = strconv.Atoi(lastMatch[2])
			if err != nil {
				return nil, fmt.Errorf("invalid last offset: %s", lastMatch[2])
			}
		}

		// Get the length of the slice/array/map
		if currentVal == nil {
			return nil, nil
		}

		v := reflect.ValueOf(currentVal)
		switch v.Kind() {
		case reflect.Slice, reflect.Array:
			length := v.Len()
			resolvedIndex = length - 1 - offset
		case reflect.Map:
			// For maps, we can't easily support 'last', return error
			return nil, fmt.Errorf("'last' index not supported for maps")
		default:
			return nil, fmt.Errorf("cannot use 'last' index on type %v", v.Kind())
		}
	} else {
		// Try to evaluate the index expression
		idxVal, err := evaluateVariable(indexExpr, exchange)
		if err != nil {
			return nil, fmt.Errorf("invalid index expression: %s", indexExpr)
		}
		resolvedIndex = idxVal
	}

	// Access the value
	result, err := accessValueAtIndex(currentVal, resolvedIndex)
	if err != nil {
		return nil, err
	}

	// Handle chained access
	if rest != "" {
		if strings.HasPrefix(rest, "[") {
			// More bracket notation - create a temporary expression
			tempExpr := fmt.Sprintf("temp%spart%s", rest, "") // placeholder for chaining
			_ = tempExpr
			// Instead, we recursively evaluate: create a mock body with result
			return evaluateChainedAccess(result, rest, exchange)
		} else if strings.HasPrefix(rest, ".") {
			// Dot notation access
			return evaluateChainedAccess(result, rest, exchange)
		}
	}

	return result, nil
}

// evaluateNullSafeBracketNotation evaluates null-safe bracket notation like body?.['key'], body?.['key']?.['subkey']
func evaluateNullSafeBracketNotation(expr string, exchange *Exchange) (interface{}, error) {
	// Parse: base?.[accessor] or base?.['key'] etc.
	expr = strings.TrimSpace(expr)

	// Find the null-safe bracket pattern
	pattern := regexp.MustCompile(`^(body|header|exchangeProperty)\?\.\[([^\]]+)\](.*)$`)
	match := pattern.FindStringSubmatch(expr)
	if match == nil {
		return nil, fmt.Errorf("invalid null-safe bracket notation")
	}

	base := match[1]
	indexExpr := match[2]
	rest := match[3]

	// Get the base value
	var currentVal interface{}
	switch base {
	case "body":
		currentVal = exchange.GetBody()
	case "header":
		currentVal = exchange.GetIn().GetHeaders()
	case "exchangeProperty":
		currentVal = exchange.GetProperties()
	default:
		return nil, fmt.Errorf("unknown base: %s", base)
	}

	// If null, return nil (null-safe)
	if currentVal == nil {
		return nil, nil
	}

	// Resolve the index
	var resolvedIndex interface{}
	indexExpr = strings.TrimSpace(indexExpr)
	if strings.HasPrefix(indexExpr, "'") && strings.HasSuffix(indexExpr, "'") {
		resolvedIndex = strings.Trim(indexExpr, "'")
	} else if strings.HasPrefix(indexExpr, "\"") && strings.HasSuffix(indexExpr, "\"") {
		resolvedIndex = strings.Trim(indexExpr, "\"")
	} else if numMatch := numericIndexRegex.FindStringSubmatch(indexExpr); numMatch != nil {
		idx, err := strconv.Atoi(numMatch[1])
		if err != nil {
			return nil, fmt.Errorf("invalid numeric index: %s", indexExpr)
		}
		resolvedIndex = idx
	} else {
		idxVal, err := evaluateVariable(indexExpr, exchange)
		if err != nil {
			return nil, fmt.Errorf("invalid index expression: %s", indexExpr)
		}
		resolvedIndex = idxVal
	}

	// Access the value
	result, err := accessValueAtIndex(currentVal, resolvedIndex)
	if err != nil {
		return nil, err
	}

	// Handle chained access
	if rest != "" {
		// Remove leading operator (either ?. or [ or .)
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(result, rest[2:], exchange)
		} else if strings.HasPrefix(rest, "[") {
			return evaluateChainedAccess(result, rest, exchange)
		} else if strings.HasPrefix(rest, ".") {
			return evaluateChainedAccess(result, rest, exchange)
		}
	}

	return result, nil
}

// evaluateNullSafeDotNotation evaluates null-safe dot notation like body?.field
func evaluateNullSafeDotNotation(expr string, exchange *Exchange) (interface{}, error) {
	match := nullSafeDotNotationRegex.FindStringSubmatch(expr)
	if match == nil {
		return nil, fmt.Errorf("invalid null-safe dot notation")
	}

	base := match[1]
	propName := match[2]
	rest := match[3]

	// Get the base value
	var currentVal interface{}
	switch base {
	case "body":
		currentVal = exchange.GetBody()
	case "header":
		currentVal, _ = exchange.GetHeader(propName)
		if rest == "" {
			return currentVal, nil
		}
		return evaluateChainedAccess(currentVal, rest, exchange)
	case "exchangeProperty":
		currentVal, _ = exchange.GetProperty(propName)
		if rest == "" {
			return currentVal, nil
		}
		return evaluateChainedAccess(currentVal, rest, exchange)
	default:
		return nil, fmt.Errorf("unknown base: %s", base)
	}

	// If null, return nil (null-safe behavior)
	if currentVal == nil {
		return nil, nil
	}

	// Access the property on body
	result, err := accessProperty(currentVal, propName)
	if err != nil {
		return nil, err
	}

	// Handle chained access
	if rest != "" {
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(result, rest[2:], exchange)
		} else if strings.HasPrefix(rest, ".") {
			return evaluateChainedAccess(result, rest, exchange)
		} else if strings.HasPrefix(rest, "[") {
			return evaluateChainedAccess(result, rest, exchange)
		}
	}

	return result, nil
}

// evaluateBodyProperty evaluates body.property access (map/struct field access)
func evaluateBodyProperty(expr string, exchange *Exchange) (interface{}, error) {
	match := bodyPropertyRegex.FindStringSubmatch(expr)
	if match == nil {
		return nil, fmt.Errorf("invalid body property notation")
	}

	propName := match[1]
	rest := match[2]

	body := exchange.GetBody()
	if body == nil {
		return nil, nil
	}

	result, err := accessProperty(body, propName)
	if err != nil {
		return nil, err
	}

	// Handle chained access
	if rest != "" {
		return evaluateChainedAccess(result, rest, exchange)
	}

	return result, nil
}

// evaluateNullSafeAccess handles null-safe chained access
func evaluateNullSafeAccess(currentVal interface{}, rest string, exchange *Exchange) (interface{}, error) {
	if currentVal == nil {
		return nil, nil
	}

	if rest == "" {
		return currentVal, nil
	}

	// Check for bracket notation first
	if strings.HasPrefix(rest, "[") {
		// Extract the index and rest
		bracketEnd := strings.Index(rest, "]")
		if bracketEnd == -1 {
			return nil, fmt.Errorf("unmatched bracket")
		}

		indexExpr := strings.TrimSpace(rest[1:bracketEnd])
		remaining := rest[bracketEnd+1:]

		// Resolve index
		var resolvedIndex interface{}
		if strings.HasPrefix(indexExpr, "'") && strings.HasSuffix(indexExpr, "'") {
			resolvedIndex = strings.Trim(indexExpr, "'")
		} else if strings.HasPrefix(indexExpr, "\"") && strings.HasSuffix(indexExpr, "\"") {
			resolvedIndex = strings.Trim(indexExpr, "\"")
		} else if num, err := strconv.Atoi(indexExpr); err == nil {
			resolvedIndex = num
		} else {
			return nil, fmt.Errorf("invalid index in null-safe chain: %s", indexExpr)
		}

		result, err := accessValueAtIndex(currentVal, resolvedIndex)
		if err != nil {
			return nil, err
		}

		if remaining != "" {
			if strings.HasPrefix(remaining, "?.") {
				return evaluateNullSafeAccess(result, remaining[2:], exchange)
			} else if strings.HasPrefix(remaining, ".") {
				return evaluateChainedAccess(result, remaining, exchange)
			} else if strings.HasPrefix(remaining, "[") {
				return evaluateChainedAccess(result, remaining, exchange)
			}
		}

		return result, nil
	}

	// Handle property name at start of rest - check for nested null-safe operator first
	nullSafeIdx := strings.Index(rest, "?.")
	dotIdx := strings.Index(rest, ".")
	bracketIdx := strings.Index(rest, "[")

	endIdx := len(rest)
	if nullSafeIdx != -1 {
		endIdx = nullSafeIdx
	} else if dotIdx != -1 {
		endIdx = dotIdx
	} else if bracketIdx != -1 {
		endIdx = bracketIdx
	}

	propName := rest[:endIdx]
	remaining := rest[endIdx:]

	result, err := accessProperty(currentVal, propName)
	if err != nil {
		return nil, err
	}

	if remaining != "" {
		if strings.HasPrefix(remaining, "?.") {
			return evaluateNullSafeAccess(result, remaining[2:], exchange)
		} else if strings.HasPrefix(remaining, ".") {
			return evaluateChainedAccess(result, remaining, exchange)
		} else if strings.HasPrefix(remaining, "[") {
			return evaluateChainedAccess(result, remaining, exchange)
		}
	}

	return result, nil
}

// evaluateChainedAccess handles chained access (bracket or dot notation)
func evaluateChainedAccess(currentVal interface{}, rest string, exchange *Exchange) (interface{}, error) {
	if currentVal == nil {
		return nil, nil
	}

	if rest == "" {
		return currentVal, nil
	}

	// Check for bracket notation first
	if strings.HasPrefix(rest, "[") {
		bracketEnd := strings.Index(rest, "]")
		if bracketEnd == -1 {
			return nil, fmt.Errorf("unmatched bracket in chained access")
		}

		indexExpr := strings.TrimSpace(rest[1:bracketEnd])
		remaining := rest[bracketEnd+1:]

		// Resolve index
		var resolvedIndex interface{}
		if strings.HasPrefix(indexExpr, "'") && strings.HasSuffix(indexExpr, "'") {
			resolvedIndex = strings.Trim(indexExpr, "'")
		} else if strings.HasPrefix(indexExpr, "\"") && strings.HasSuffix(indexExpr, "\"") {
			resolvedIndex = strings.Trim(indexExpr, "\"")
		} else if num, err := strconv.Atoi(indexExpr); err == nil {
			resolvedIndex = num
		} else if lastMatch := lastIndexRegex.FindStringSubmatch(indexExpr); lastMatch != nil {
			// Handle 'last' keyword
			offset := 0
			if lastMatch[2] != "" {
				offset, _ = strconv.Atoi(lastMatch[2])
			}

			v := reflect.ValueOf(currentVal)
			switch v.Kind() {
			case reflect.Slice, reflect.Array:
				length := v.Len()
				resolvedIndex = length - 1 - offset
			default:
				return nil, fmt.Errorf("cannot use 'last' on type %v", v.Kind())
			}
		} else {
			idxVal, err := evaluateVariable(indexExpr, exchange)
			if err != nil {
				return nil, fmt.Errorf("invalid index in chain: %s", indexExpr)
			}
			resolvedIndex = idxVal
		}

		result, err := accessValueAtIndex(currentVal, resolvedIndex)
		if err != nil {
			return nil, err
		}

		if remaining != "" {
			return evaluateChainedAccess(result, remaining, exchange)
		}
		return result, nil
	}

	// Check for dot notation
	if strings.HasPrefix(rest, ".") {
		rest = rest[1:] // skip the dot

		dotIdx := strings.Index(rest, ".")
		bracketIdx := strings.Index(rest, "[")
		nullSafeIdx := strings.Index(rest, "?.")

		endIdx := len(rest)
		if dotIdx != -1 {
			endIdx = dotIdx
		}
		if bracketIdx != -1 && bracketIdx < endIdx {
			endIdx = bracketIdx
		}
		if nullSafeIdx != -1 && nullSafeIdx < endIdx {
			endIdx = nullSafeIdx
		}

		propName := rest[:endIdx]
		remaining := rest[endIdx:]

		result, err := accessProperty(currentVal, propName)
		if err != nil {
			return nil, err
		}

		if remaining != "" {
			if strings.HasPrefix(remaining, "?.") {
				return evaluateNullSafeAccess(result, remaining[2:], exchange)
			}
			return evaluateChainedAccess(result, remaining, exchange)
		}
		return result, nil
	}

	// Check for null-safe notation
	if strings.HasPrefix(rest, "?.") {
		return evaluateNullSafeAccess(currentVal, rest[2:], exchange)
	}

	return currentVal, nil
}

// accessValueAtIndex accesses a value from a map, slice, or array by index/key
func accessValueAtIndex(container interface{}, index interface{}) (interface{}, error) {
	if container == nil {
		return nil, nil
	}

	v := reflect.ValueOf(container)

	switch v.Kind() {
	case reflect.Map:
		// For maps, index must be a string
		keyStr, ok := index.(string)
		if !ok {
			// Try to convert to string
			keyStr = fmt.Sprintf("%v", index)
		}
		keyVal := reflect.ValueOf(keyStr)
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			return nil, nil // Key not found, return nil
		}
		return val.Interface(), nil

	case reflect.Slice, reflect.Array:
		// For slices/arrays, index must be numeric
		var idx int
		switch i := index.(type) {
		case int:
			idx = i
		case int64:
			idx = int(i)
		case float64:
			idx = int(i)
		default:
			return nil, fmt.Errorf("slice index must be numeric, got %T", index)
		}

		if idx < 0 || idx >= v.Len() {
			return nil, nil // Index out of bounds, return nil
		}
		return v.Index(idx).Interface(), nil

	case reflect.Ptr:
		if v.IsNil() {
			return nil, nil
		}
		// Dereference and try again
		return accessValueAtIndex(v.Elem().Interface(), index)

	default:
		return nil, fmt.Errorf("cannot index type %v", v.Kind())
	}
}

// accessProperty accesses a property on a map or struct
func accessProperty(container interface{}, propName string) (interface{}, error) {
	if container == nil {
		return nil, nil
	}

	v := reflect.ValueOf(container)

	// Dereference pointers
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Map:
		// Map access
		keyVal := reflect.ValueOf(propName)
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			return nil, nil // Key not found
		}
		return val.Interface(), nil

	case reflect.Struct:
		// Struct field access
		field := v.FieldByName(propName)
		if !field.IsValid() {
			// Try case-insensitive match
			field = v.FieldByNameFunc(func(s string) bool {
				return strings.EqualFold(s, propName)
			})
		}
		if !field.IsValid() {
			return nil, nil // Field not found
		}
		return field.Interface(), nil

	default:
		// For other types, try to access as if it's a map[string]interface{}
		if m, ok := container.(map[string]interface{}); ok {
			if val, exists := m[propName]; exists {
				return val, nil
			}
			return nil, nil
		}
		return nil, fmt.Errorf("cannot access property on type %v", v.Kind())
	}
}

// evaluateDotNotationRegex evaluates variables using the regex match groups
func evaluateDotNotationRegex(match []string, exchange *Exchange) (interface{}, error) {
	// match[0] = full match
	// match[1] = "body" (if body matched) or empty
	// match[2] = "header" or empty (for body)
	// match[3] = the rest after .

	if match[1] == "body" {
		return exchange.GetBody(), nil
	}

	if match[2] == "header" && len(match[3]) > 0 {
		value, exists := exchange.GetHeader(match[3])
		if !exists {
			return nil, nil
		}
		return value, nil
	}

	if match[2] == "exchangeProperty" && len(match[3]) > 0 {
		value, exists := exchange.GetProperty(match[3])
		if !exists {
			return nil, nil
		}
		return value, nil
	}

	return nil, fmt.Errorf("unknown variable pattern")
}

// compareValues compares two values with the given operator
func compareValues(left interface{}, op string, right interface{}) (bool, error) {
	leftStr := fmt.Sprintf("%v", left)
	rightStr := fmt.Sprintf("%v", right)

	// Try numeric comparison
	leftNum, leftErr := strconv.ParseFloat(leftStr, 64)
	rightNum, rightErr := strconv.ParseFloat(rightStr, 64)

	if leftErr == nil && rightErr == nil {
		switch op {
		case "==":
			return leftNum == rightNum, nil
		case "!=":
			return leftNum != rightNum, nil
		case ">":
			return leftNum > rightNum, nil
		case ">=":
			return leftNum >= rightNum, nil
		case "<":
			return leftNum < rightNum, nil
		case "<=":
			return leftNum <= rightNum, nil
		}
	} else {
		// String comparison
		switch op {
		case "==":
			return leftStr == rightStr, nil
		case "!=":
			return leftStr != rightStr, nil
		case ">":
			return leftStr > rightStr, nil
		case ">=":
			return leftStr >= rightStr, nil
		case "<":
			return leftStr < rightStr, nil
		case "<=":
			return leftStr <= rightStr, nil
		}
	}

	return false, fmt.Errorf("unknown operator: %s", op)
}

// generateUUID generates a simple UUID v4
func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant is 10
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// SimpleSetBodyProcessor sets the body using a Simple Expression
type SimpleSetBodyProcessor struct {
	Expression Expression
}

// Process implements the Processor interface for SimpleSetBody
func (p *SimpleSetBodyProcessor) Process(exchange *Exchange) error {
	result, err := p.Expression.Evaluate(exchange)
	if err != nil {
		return err
	}
	exchange.GetOut().SetBody(result)
	exchange.GetOut().SetHeaders(exchange.GetIn().GetHeaders())
	return nil
}

// SimpleSetHeaderProcessor sets a header using a Simple Expression
type SimpleSetHeaderProcessor struct {
	HeaderName string
	Expression Expression
}

// Process implements the Processor interface for SimpleSetHeader
func (p *SimpleSetHeaderProcessor) Process(exchange *Exchange) error {
	result, err := p.Expression.Evaluate(exchange)
	if err != nil {
		return err
	}
	exchange.GetOut().SetHeader(p.HeaderName, result)
	return nil
}

// SimpleLanguageProcessor is a generic processor that evaluates a Simple expression
// and sets the result to the body
type SimpleLanguageProcessor struct {
	Template *SimpleTemplate
}

// Process implements the Processor interface
func (p *SimpleLanguageProcessor) Process(exchange *Exchange) error {
	result, err := p.Template.EvaluateAsString(exchange)
	if err != nil {
		return fmt.Errorf("simple language evaluation error: %w", err)
	}
	exchange.GetOut().SetBody(result)
	exchange.GetOut().SetHeaders(exchange.GetIn().GetHeaders())
	return nil
}
