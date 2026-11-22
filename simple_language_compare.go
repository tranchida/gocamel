package gocamel

import (
	"cmp"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// This file holds the value-comparison and type-predicate helpers used by
// the Simple Language evaluator: toBool, checkType, containsValue and
// compareValues. They are leaf utilities — they do not call back into
// evaluateVariable.

func parseFiniteFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// toBool converts a value to boolean
func toBool(val interface{}) bool {
	if val == nil {
		return false
	}

	switch v := val.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "" || s == "false" || s == "0" || s == "no" || s == "<nil>" {
			return false
		}
		if n, ok := parseFiniteFloat(s); ok {
			return n != 0
		}
		return true
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	case uintptr:
		return v != 0
	case float32:
		return v != 0 && !math.IsNaN(float64(v))
	case float64:
		return v != 0 && !math.IsNaN(v)
	}

	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		return f != 0 && !math.IsNaN(f)
	}

	return true
}

// checkType checks if value is of a specific type
func checkType(val interface{}, typeName string) bool {
	lowerType := strings.ToLower(typeName)
	if val == nil {
		return lowerType == "nil" || lowerType == "null"
	}

	switch lowerType {
	case "string":
		_, ok := val.(string)
		return ok
	case "int", "integer":
		switch val.(type) {
		case int, int8, int16, int32, int64:
			return true
		}
		return false
	case "float", "double", "number":
		switch val.(type) {
		case float32, float64:
			return true
		}
		return false
	case "bool", "boolean":
		_, ok := val.(bool)
		return ok
	case "map":
		v := reflect.ValueOf(val)
		return v.Kind() == reflect.Map
	case "slice", "array", "list":
		v := reflect.ValueOf(val)
		return v.Kind() == reflect.Slice || v.Kind() == reflect.Array
	case "struct":
		v := reflect.ValueOf(val)
		return v.Kind() == reflect.Struct
	default:
		// Check if type name matches the concrete type
		return fmt.Sprintf("%T", val) == typeName
	}
}

// containsValue checks if value is in the list
func containsValue(list []string, val interface{}) bool {
	valStr := fmt.Sprintf("%v", val)
	for _, item := range list {
		if item == valStr {
			return true
		}
	}
	return false
}

func classifyInt(val interface{}) (int64, uint64, bool, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), 0, false, true
	case int8:
		return int64(v), 0, false, true
	case int16:
		return int64(v), 0, false, true
	case int32:
		return int64(v), 0, false, true
	case int64:
		return v, 0, false, true
	case uint:
		return 0, uint64(v), true, true
	case uint8:
		return 0, uint64(v), true, true
	case uint16:
		return 0, uint64(v), true, true
	case uint32:
		return 0, uint64(v), true, true
	case uint64:
		return 0, v, true, true
	case uintptr:
		return 0, uint64(v), true, true
	}
	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), 0, false, true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return 0, rv.Uint(), true, true
	}
	return 0, 0, false, false
}

func classifyFloat(val interface{}) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	}
	rv := reflect.ValueOf(val)
	if rv.Kind() == reflect.Float32 || rv.Kind() == reflect.Float64 {
		return rv.Float(), true
	}
	return 0, false
}

func compareOrdered[T cmp.Ordered](left, right T, op string) (bool, error) {
	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case ">":
		return left > right, nil
	case ">=":
		return left >= right, nil
	case "<":
		return left < right, nil
	case "<=":
		return left <= right, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", op)
	}
}

func applyOrderSign(sign int, op string) (bool, error) {
	switch op {
	case "==":
		return sign == 0, nil
	case "!=":
		return sign != 0, nil
	case ">":
		return sign > 0, nil
	case ">=":
		return sign >= 0, nil
	case "<":
		return sign < 0, nil
	case "<=":
		return sign <= 0, nil
	default:
		return false, fmt.Errorf("unknown operator: %s", op)
	}
}

func flipOrderOp(op string) string {
	switch op {
	case ">":
		return "<"
	case ">=":
		return "<="
	case "<":
		return ">"
	case "<=":
		return ">="
	default:
		return op
	}
}

func isValidCompareOp(op string) bool {
	switch op {
	case "==", "!=", ">", ">=", "<", "<=":
		return true
	default:
		return false
	}
}

func compareInts(li int64, lu uint64, lUnsigned bool, ri int64, ru uint64, rUnsigned bool, op string) (bool, error) {
	switch {
	case !lUnsigned && !rUnsigned:
		return compareOrdered(li, ri, op)
	case lUnsigned && rUnsigned:
		return compareOrdered(lu, ru, op)
	case !lUnsigned && rUnsigned:
		if li < 0 {
			return applyOrderSign(-1, op)
		}
		return compareOrdered(uint64(li), ru, op) // #nosec G115 -- li >= 0 checked above
	default:
		if ri < 0 {
			return applyOrderSign(1, op)
		}
		return compareOrdered(lu, uint64(ri), op) // #nosec G115 -- ri >= 0 checked above
	}
}

func compareIntFloat(i int64, u uint64, isUnsigned bool, f float64, op string) (bool, error) {
	if !isValidCompareOp(op) {
		return false, fmt.Errorf("unknown operator: %s", op)
	}
	if math.IsNaN(f) {
		return op == "!=", nil
	}
	if math.IsInf(f, 1) {
		return applyOrderSign(-1, op)
	}
	if math.IsInf(f, -1) {
		return applyOrderSign(1, op)
	}

	const (
		twoPow63 = 9223372036854775808.0  // 2^63, exact in float64
		twoPow64 = 18446744073709551616.0 // 2^64, exact in float64
	)

	if !isUnsigned {
		if f >= twoPow63 {
			return applyOrderSign(-1, op)
		}
		if f < -twoPow63 {
			return applyOrderSign(1, op)
		}
		trunc := math.Trunc(f)
		tf := int64(trunc) // #nosec G115 -- trunc is bounded to [-2^63, 2^63)
		if i < tf {
			return applyOrderSign(-1, op)
		}
		if i > tf {
			return applyOrderSign(1, op)
		}
		if f > trunc {
			return applyOrderSign(-1, op)
		}
		if f < trunc {
			return applyOrderSign(1, op)
		}
		return applyOrderSign(0, op)
	}

	if f < 0 {
		return applyOrderSign(1, op)
	}
	if f >= twoPow64 {
		return applyOrderSign(-1, op)
	}
	trunc := math.Trunc(f)
	tu := uint64(trunc) // #nosec G115 -- trunc is bounded to [0, 2^64)
	if u < tu {
		return applyOrderSign(-1, op)
	}
	if u > tu {
		return applyOrderSign(1, op)
	}
	if f > trunc {
		return applyOrderSign(-1, op)
	}
	return applyOrderSign(0, op)
}

func compareIntAndString(i int64, u uint64, isUnsigned bool, s string, op string) (bool, error) {
	if si, err := strconv.ParseInt(s, 10, 64); err == nil {
		return compareInts(i, u, isUnsigned, si, 0, false, op)
	}
	if su, err := strconv.ParseUint(s, 10, 64); err == nil {
		return compareInts(i, u, isUnsigned, 0, su, true, op)
	}
	if sf, ok := parseFiniteFloat(s); ok {
		return compareIntFloat(i, u, isUnsigned, sf, op)
	}
	var leftStr string
	if isUnsigned {
		leftStr = strconv.FormatUint(u, 10)
	} else {
		leftStr = strconv.FormatInt(i, 10)
	}
	return compareOrdered(leftStr, s, op)
}

// compareValues compares two values with the given operator
func compareValues(left interface{}, op string, right interface{}) (bool, error) {
	// Handle nil values - nil is only equal to nil, all other comparisons return false
	if left == nil || right == nil {
		switch op {
		case "==":
			return left == nil && right == nil, nil
		case "!=":
			return left != nil || right != nil, nil
		case ">", ">=", "<", "<=":
			return false, nil
		default:
			return false, fmt.Errorf("unknown operator: %s", op)
		}
	}

	if !isValidCompareOp(op) {
		return false, fmt.Errorf("unknown operator: %s", op)
	}

	ls, lIsStr := left.(string)
	rs, rIsStr := right.(string)
	if lIsStr && rIsStr {
		if op == "==" || op == "!=" {
			return compareOrdered(ls, rs, op)
		}
		if li, err := strconv.ParseInt(ls, 10, 64); err == nil {
			if ri, err := strconv.ParseInt(rs, 10, 64); err == nil {
				return compareOrdered(li, ri, op)
			}
		}
		if lf, ok := parseFiniteFloat(ls); ok {
			if rf, ok := parseFiniteFloat(rs); ok {
				return compareOrdered(lf, rf, op)
			}
		}
		return compareOrdered(ls, rs, op)
	}

	li, lu, lUnsigned, lIsInt := classifyInt(left)
	ri, ru, rUnsigned, rIsInt := classifyInt(right)
	lf, lIsFloat := classifyFloat(left)
	rf, rIsFloat := classifyFloat(right)

	switch {
	case lIsInt && rIsInt:
		return compareInts(li, lu, lUnsigned, ri, ru, rUnsigned, op)
	case lIsInt && rIsFloat:
		return compareIntFloat(li, lu, lUnsigned, rf, op)
	case lIsFloat && rIsInt:
		return compareIntFloat(ri, ru, rUnsigned, lf, flipOrderOp(op))
	case lIsFloat && rIsFloat:
		return compareOrdered(lf, rf, op)
	case lIsInt && rIsStr:
		return compareIntAndString(li, lu, lUnsigned, rs, op)
	case lIsStr && rIsInt:
		return compareIntAndString(ri, ru, rUnsigned, ls, flipOrderOp(op))
	case lIsFloat && rIsStr:
		if math.IsNaN(lf) {
			return op == "!=", nil
		}
		if rf, ok := parseFiniteFloat(rs); ok {
			return compareOrdered(lf, rf, op)
		}
		return compareOrdered(fmt.Sprintf("%v", left), rs, op)
	case lIsStr && rIsFloat:
		if math.IsNaN(rf) {
			return op == "!=", nil
		}
		if lf, ok := parseFiniteFloat(ls); ok {
			return compareOrdered(lf, rf, op)
		}
		return compareOrdered(ls, fmt.Sprintf("%v", right), op)
	}

	leftStr := fmt.Sprintf("%v", left)
	rightStr := fmt.Sprintf("%v", right)
	if leftNum, ok := parseFiniteFloat(leftStr); ok {
		if rightNum, ok := parseFiniteFloat(rightStr); ok {
			return compareOrdered(leftNum, rightNum, op)
		}
	}
	return compareOrdered(leftStr, rightStr, op)
}
