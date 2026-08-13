package gocamel

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// This file implements property and index access for the Simple Language:
// bracket notation (body[0], body['key']), dot notation (body.field), and
// their null-safe variants (body?.field, body?.['key']). All helpers delegate
// to the leaf accessors accessValueAtIndex and accessProperty for the actual
// reflection work.

// evaluateSimpleVariableAccess handles common variable access patterns (bracket notation,
// null-safe access, dot notation, function chaining). Used from two places in evaluateVariable
// to avoid duplication.
func evaluateSimpleVariableAccess(expr string, exchange *Exchange) (interface{}, error) {
	// Check for null-safe bracket notation first (e.g., body?.['key'])
	if strings.Contains(expr, "?.") && strings.Contains(expr, "[") {
		result, err := evaluateNullSafeBracketNotation(expr, exchange)
		if err == nil {
			return result, nil
		}
	}

	// Check for bracket notation (e.g., body['key'], body[0])
	if strings.Contains(expr, "[") {
		result, err := evaluateBracketNotation(expr, exchange)
		if err == nil {
			return result, nil
		}
	}

	// Check for null-safe dot notation (e.g., body?.field)
	if strings.Contains(expr, "?.") {
		result, err := evaluateNullSafeDotNotation(expr, exchange)
		if err == nil {
			return result, nil
		}
	}

	// Check for body.property (map/struct access)
	if strings.HasPrefix(expr, "body.") && !strings.HasPrefix(expr, "body?") {
		result, err := evaluateBodyProperty(expr, exchange)
		if err == nil {
			return result, nil
		}
	}

	return nil, fmt.Errorf("no variable access pattern matched: %s", expr)
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
		val, found := mapLookup(v, keyStr)
		if !found {
			return nil, nil // Key not found, return nil
		}
		return val, nil

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
		val, found := mapLookup(v, propName)
		if !found {
			return nil, nil // Key not found
		}
		return val, nil

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

// mapLookup looks up keyStr in a reflect.Value of a map without panicking when
// the map's key type is not string (e.g. map[int]any). reflect.Value.MapIndex
// panics if the lookup key is not assignable to the map's key type, so the key
// string is converted to the map's key type where possible. It returns
// (nil, false) when the key is absent or cannot be represented in the map's
// key type.
func mapLookup(v reflect.Value, keyStr string) (interface{}, bool) {
	keyType := v.Type().Key()
	if keyType.Kind() == reflect.String {
		val := v.MapIndex(reflect.ValueOf(keyStr))
		if !val.IsValid() {
			return nil, false
		}
		return val.Interface(), true
	}

	keyVal, err := convertToKeyType(keyStr, keyType)
	if err != nil {
		return nil, false
	}
	val := v.MapIndex(keyVal)
	if !val.IsValid() {
		return nil, false
	}
	return val.Interface(), true
}

// convertToKeyType converts a string to a reflect.Value assignable to the given
// map key type. Supported key types are the signed and unsigned integer kinds;
// anything else returns an error.
func convertToKeyType(s string, t reflect.Type) (reflect.Value, error) {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(n).Convert(t), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(s, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(n).Convert(t), nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported map key type %v", t)
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
