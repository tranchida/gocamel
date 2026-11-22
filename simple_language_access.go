package gocamel

import (
	"fmt"
	"reflect"
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

// findMatchingBracket finds the closing ']' for the '[' at openIdx,
// respecting single/double quotes and nested brackets. Returns -1 if unmatched.
func findMatchingBracket(s string, openIdx int) int {
	if openIdx < 0 || openIdx >= len(s) || s[openIdx] != '[' {
		return -1
	}
	depth := 0
	inSingle := false
	inDouble := false
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if inSingle {
			if ch == '\\' && i+1 < len(s) && (s[i+1] == '\'' || s[i+1] == '\\') {
				i++
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				i++
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
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// unquoteSimpleStringLiteral unquotes a single complete quoted string literal
// ('...' or "..."), returning false if s is not a single balanced literal.
func unquoteSimpleStringLiteral(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	quote := s[0]
	if (quote != '\'' && quote != '"') || s[len(s)-1] != quote {
		return "", false
	}
	var sb strings.Builder
	for i := 1; i < len(s)-1; i++ {
		ch := s[i]
		if ch == '\\' && i+1 < len(s)-1 {
			next := s[i+1]
			if next == quote || next == '\\' {
				sb.WriteByte(next)
				i++
				continue
			}
		}
		if ch == quote {
			// Unescaped quote inside string means s is not a single literal (e.g. 'a' == 'b').
			return "", false
		}
		sb.WriteByte(ch)
	}
	return sb.String(), true
}

func isValidMemberName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func resolveBracketIndex(indexExpr string, currentVal interface{}, exchange *Exchange) (interface{}, bool, error) {
	if unquoted, ok := unquoteSimpleStringLiteral(indexExpr); ok {
		return unquoted, false, nil
	}
	if numMatch := numericIndexRegex.FindStringSubmatch(indexExpr); numMatch != nil {
		idx, err := strconv.Atoi(numMatch[1])
		if err != nil {
			return nil, false, fmt.Errorf("invalid numeric index: %s", indexExpr)
		}
		return idx, false, nil
	}
	if lastMatch := lastIndexRegex.FindStringSubmatch(indexExpr); lastMatch != nil {
		offset := 0
		if lastMatch[2] != "" {
			var err error
			offset, err = strconv.Atoi(lastMatch[2])
			if err != nil {
				return nil, false, fmt.Errorf("invalid last offset: %s", lastMatch[2])
			}
		}
		if currentVal == nil {
			return nil, true, nil
		}
		v := reflect.ValueOf(currentVal)
		for v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return nil, true, nil
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Slice, reflect.Array:
			return v.Len() - 1 - offset, false, nil
		case reflect.Map:
			return nil, false, fmt.Errorf("'last' index not supported for maps")
		default:
			return nil, false, fmt.Errorf("cannot use 'last' index on type %v", v.Kind())
		}
	}
	idxVal, err := evaluateVariable(indexExpr, exchange)
	if err != nil {
		return nil, false, fmt.Errorf("invalid index expression: %s", indexExpr)
	}
	return idxVal, false, nil
}

// evaluateBracketNotation evaluates bracket notation like body['key'], body[0], body['key']['subkey']
func evaluateBracketNotation(expr string, exchange *Exchange) (interface{}, error) {
	expr = strings.TrimSpace(expr)
	var base, indexExpr, rest string
	bracketStart := -1

	// Find first opening bracket outside quotes; abort if '?.' precedes '['.
	inSingle, inDouble := false, false
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if inSingle {
			if ch == '\\' && i+1 < len(expr) && (expr[i+1] == '\'' || expr[i+1] == '\\') {
				i++
				continue
			}
			if ch == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if ch == '\\' && i+1 < len(expr) && (expr[i+1] == '"' || expr[i+1] == '\\') {
				i++
				continue
			}
			if ch == '"' {
				inDouble = false
			}
			continue
		}
		if ch == '\'' {
			inSingle = true
			continue
		}
		if ch == '"' {
			inDouble = true
			continue
		}
		if ch == '?' && i+1 < len(expr) && expr[i+1] == '.' {
			return nil, fmt.Errorf("null-safe operator precedes bracket in expression: %s", expr)
		}
		if ch == '[' {
			base = strings.TrimSpace(expr[:i])
			bracketStart = i
			break
		}
	}

	if bracketStart <= 0 {
		return nil, fmt.Errorf("no bracket found in expression")
	}

	bracketEnd := findMatchingBracket(expr, bracketStart)
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
		currentVal = exchange.GetIn().GetHeaders()
	case "exchangeProperty":
		currentVal = exchange.GetProperties()
	default:
		return nil, fmt.Errorf("unknown base in bracket notation: %s", base)
	}

	resolvedIndex, isNilTarget, err := resolveBracketIndex(indexExpr, currentVal, exchange)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if !isNilTarget {
		result, err = accessValueAtIndex(currentVal, resolvedIndex)
		if err != nil {
			return nil, err
		}
	}

	// Handle chained access
	if rest != "" {
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(result, rest[2:], exchange)
		}
		if strings.HasPrefix(rest, "[") || strings.HasPrefix(rest, ".") {
			return evaluateChainedAccess(result, rest, exchange)
		}
		return nil, fmt.Errorf("unexpected trailing characters in bracket notation: %s", rest)
	}

	return result, nil
}

// evaluateNullSafeBracketNotation evaluates null-safe bracket notation like body?.['key'], body?.['key']?.['subkey']
func evaluateNullSafeBracketNotation(expr string, exchange *Exchange) (interface{}, error) {
	expr = strings.TrimSpace(expr)

	var base string
	var prefixLen int
	switch {
	case strings.HasPrefix(expr, "body?.["):
		base = "body"
		prefixLen = len("body?.")
	case strings.HasPrefix(expr, "header?.["):
		base = "header"
		prefixLen = len("header?.")
	case strings.HasPrefix(expr, "exchangeProperty?.["):
		base = "exchangeProperty"
		prefixLen = len("exchangeProperty?.")
	default:
		return nil, fmt.Errorf("invalid null-safe bracket notation")
	}

	bracketEnd := findMatchingBracket(expr, prefixLen)
	if bracketEnd == -1 {
		return nil, fmt.Errorf("unmatched bracket in null-safe bracket notation")
	}

	indexExpr := strings.TrimSpace(expr[prefixLen+1 : bracketEnd])
	rest := strings.TrimSpace(expr[bracketEnd+1:])

	var currentVal interface{}
	switch base {
	case "body":
		currentVal = exchange.GetBody()
	case "header":
		currentVal = exchange.GetIn().GetHeaders()
	case "exchangeProperty":
		currentVal = exchange.GetProperties()
	}

	resolvedIndex, isNilTarget, err := resolveBracketIndex(indexExpr, currentVal, exchange)
	if err != nil {
		return nil, err
	}

	var result interface{}
	if currentVal != nil && !isNilTarget {
		result, err = accessValueAtIndex(currentVal, resolvedIndex)
		if err != nil {
			return nil, err
		}
	}

	// Handle chained access even when result is nil so trailing operators/syntax are never swallowed.
	if rest != "" {
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(result, rest[2:], exchange)
		}
		if strings.HasPrefix(rest, "[") || strings.HasPrefix(rest, ".") {
			return evaluateChainedAccess(result, rest, exchange)
		}
		return nil, fmt.Errorf("unexpected trailing characters in null-safe bracket notation: %s", rest)
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
	rest := strings.TrimSpace(match[3])

	if rest != "" && !strings.HasPrefix(rest, "?.") && !strings.HasPrefix(rest, ".") && !strings.HasPrefix(rest, "[") {
		return nil, fmt.Errorf("unexpected trailing characters in null-safe dot notation: %s", rest)
	}

	var currentVal interface{}
	switch base {
	case "body":
		currentVal = exchange.GetBody()
	case "header":
		currentVal, _ = exchange.GetHeader(propName)
		if rest == "" {
			return currentVal, nil
		}
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(currentVal, rest[2:], exchange)
		}
		return evaluateChainedAccess(currentVal, rest, exchange)
	case "exchangeProperty":
		currentVal, _ = exchange.GetProperty(propName)
		if rest == "" {
			return currentVal, nil
		}
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(currentVal, rest[2:], exchange)
		}
		return evaluateChainedAccess(currentVal, rest, exchange)
	default:
		return nil, fmt.Errorf("unknown base: %s", base)
	}

	var result interface{}
	if currentVal != nil {
		var err error
		result, err = accessProperty(currentVal, propName)
		if err != nil {
			return nil, err
		}
	}

	if rest != "" {
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(result, rest[2:], exchange)
		}
		return evaluateChainedAccess(result, rest, exchange)
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
	rest := strings.TrimSpace(match[2])

	if rest != "" && !strings.HasPrefix(rest, "?.") && !strings.HasPrefix(rest, ".") && !strings.HasPrefix(rest, "[") {
		return nil, fmt.Errorf("unexpected trailing characters in body property notation: %s", rest)
	}

	body := exchange.GetBody()
	var result interface{}
	if body != nil {
		var err error
		result, err = accessProperty(body, propName)
		if err != nil {
			return nil, err
		}
	}

	if rest != "" {
		if strings.HasPrefix(rest, "?.") {
			return evaluateNullSafeAccess(result, rest[2:], exchange)
		}
		return evaluateChainedAccess(result, rest, exchange)
	}

	return result, nil
}

// evaluateNullSafeAccess handles null-safe chained access
func evaluateNullSafeAccess(currentVal interface{}, rest string, exchange *Exchange) (interface{}, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return currentVal, nil
	}

	// Check for bracket notation first
	if strings.HasPrefix(rest, "[") {
		bracketEnd := findMatchingBracket(rest, 0)
		if bracketEnd == -1 {
			return nil, fmt.Errorf("unmatched bracket")
		}

		indexExpr := strings.TrimSpace(rest[1:bracketEnd])
		remaining := strings.TrimSpace(rest[bracketEnd+1:])

		resolvedIndex, isNilTarget, err := resolveBracketIndex(indexExpr, currentVal, exchange)
		if err != nil {
			return nil, err
		}

		var result interface{}
		if currentVal != nil && !isNilTarget {
			result, err = accessValueAtIndex(currentVal, resolvedIndex)
			if err != nil {
				return nil, err
			}
		}

		if remaining != "" {
			if strings.HasPrefix(remaining, "?.") {
				return evaluateNullSafeAccess(result, remaining[2:], exchange)
			}
			if strings.HasPrefix(remaining, ".") || strings.HasPrefix(remaining, "[") {
				return evaluateChainedAccess(result, remaining, exchange)
			}
			return nil, fmt.Errorf("unexpected trailing characters in null-safe access: %s", remaining)
		}

		return result, nil
	}

	// Handle property name at start of rest — pick the earliest delimiter offset.
	nullSafeIdx := strings.Index(rest, "?.")
	dotIdx := strings.Index(rest, ".")
	bracketIdx := strings.Index(rest, "[")

	endIdx := len(rest)
	if nullSafeIdx != -1 && nullSafeIdx < endIdx {
		endIdx = nullSafeIdx
	}
	if dotIdx != -1 && dotIdx < endIdx {
		endIdx = dotIdx
	}
	if bracketIdx != -1 && bracketIdx < endIdx {
		endIdx = bracketIdx
	}

	propName := rest[:endIdx]
	remaining := strings.TrimSpace(rest[endIdx:])

	if !isValidMemberName(propName) {
		return nil, fmt.Errorf("invalid property name or trailing characters in null-safe access: %s", rest)
	}

	var result interface{}
	if currentVal != nil {
		var err error
		result, err = accessProperty(currentVal, propName)
		if err != nil {
			return nil, err
		}
	}

	if remaining != "" {
		if strings.HasPrefix(remaining, "?.") {
			return evaluateNullSafeAccess(result, remaining[2:], exchange)
		}
		if strings.HasPrefix(remaining, ".") || strings.HasPrefix(remaining, "[") {
			return evaluateChainedAccess(result, remaining, exchange)
		}
		return nil, fmt.Errorf("unexpected trailing characters in null-safe access: %s", remaining)
	}

	return result, nil
}

// evaluateChainedAccess handles chained access (bracket or dot notation)
func evaluateChainedAccess(currentVal interface{}, rest string, exchange *Exchange) (interface{}, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return currentVal, nil
	}

	// Check for bracket notation first
	if strings.HasPrefix(rest, "[") {
		bracketEnd := findMatchingBracket(rest, 0)
		if bracketEnd == -1 {
			return nil, fmt.Errorf("unmatched bracket in chained access")
		}

		indexExpr := strings.TrimSpace(rest[1:bracketEnd])
		remaining := strings.TrimSpace(rest[bracketEnd+1:])

		resolvedIndex, isNilTarget, err := resolveBracketIndex(indexExpr, currentVal, exchange)
		if err != nil {
			return nil, err
		}

		var result interface{}
		if currentVal != nil && !isNilTarget {
			result, err = accessValueAtIndex(currentVal, resolvedIndex)
			if err != nil {
				return nil, err
			}
		}

		if remaining != "" {
			return evaluateChainedAccess(result, remaining, exchange)
		}
		return result, nil
	}

	// Check for null-safe notation (must precede single '.' check)
	if strings.HasPrefix(rest, "?.") {
		return evaluateNullSafeAccess(currentVal, rest[2:], exchange)
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
		remaining := strings.TrimSpace(rest[endIdx:])

		if !isValidMemberName(propName) {
			return nil, fmt.Errorf("invalid property name or trailing characters in chained access: %s", rest)
		}

		var result interface{}
		if currentVal != nil {
			var err error
			result, err = accessProperty(currentVal, propName)
			if err != nil {
				return nil, err
			}
		}

		if remaining != "" {
			if strings.HasPrefix(remaining, "?.") {
				return evaluateNullSafeAccess(result, remaining[2:], exchange)
			}
			return evaluateChainedAccess(result, remaining, exchange)
		}
		return result, nil
	}

	return nil, fmt.Errorf("unexpected trailing characters in chained access: %s", rest)
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
	for v.Kind() == reflect.Ptr {
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
		// Struct field access: only exported, interfaceable fields are accessible.
		field := findExportedStructField(v, propName)
		if !field.IsValid() || !field.CanInterface() {
			return nil, nil // Field not found or unexported
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

// findExportedStructField looks up an exported struct field by exact name or
// case-insensitive fallback, traversing only exported embedded struct fields.
func findExportedStructField(v reflect.Value, propName string) reflect.Value {
	field := v.FieldByName(propName)
	if field.IsValid() && field.CanInterface() {
		return field
	}

	queue := []reflect.Value{v}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		t := cur.Type()
		for i := 0; i < t.NumField(); i++ {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			fv := cur.Field(i)
			if strings.EqualFold(sf.Name, propName) && fv.CanInterface() {
				return fv
			}
			if sf.Anonymous {
				for fv.Kind() == reflect.Ptr {
					if fv.IsNil() {
						break
					}
					fv = fv.Elem()
				}
				if fv.Kind() == reflect.Struct && fv.CanInterface() {
					queue = append(queue, fv)
				}
			}
		}
	}
	return reflect.Value{}
}

// mapLookup looks up keyStr in a reflect.Value of a map without panicking when
// the map's key type is not string (e.g. map[int]any or map[MyKey]any).
// reflect.Value.MapIndex panics if the lookup key is not assignable to the map's
// key type, so keyStr is converted to the map's key type where possible.
func mapLookup(v reflect.Value, keyStr string) (interface{}, bool) {
	keyType := v.Type().Key()
	if keyType.Kind() == reflect.String {
		val := v.MapIndex(reflect.ValueOf(keyStr).Convert(keyType))
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
