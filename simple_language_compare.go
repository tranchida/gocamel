package gocamel

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// This file holds the value-comparison and type-predicate helpers used by
// the Simple Language evaluator: toBool, checkType, containsValue and
// compareValues. They are leaf utilities — they do not call back into
// evaluateVariable.

// toBool converts a value to boolean
func toBool(val interface{}) bool {
	if val == nil {
		return false
	}

	if b, ok := val.(bool); ok {
		return b
	}

	if s, ok := val.(string); ok {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || s == "false" || s == "0" || s == "no" || s == "<nil>" {
			return false
		}
		// Try to parse as number
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n != 0
		}
		return true
	}

	if n, ok := val.(float64); ok {
		return n != 0
	}

	if n, ok := val.(int); ok {
		return n != 0
	}

	return true
}

// checkType checks if value is of a specific type
func checkType(val interface{}, typeName string) bool {
	if val == nil {
		return typeName == "nil" || typeName == "Null"
	}

	switch strings.ToLower(typeName) {
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

// compareValues compares two values with the given operator
func compareValues(left interface{}, op string, right interface{}) (bool, error) {
	// Handle nil values - nil is only equal to nil, all other comparisons return false
	if left == nil {
		switch op {
		case "==":
			return right == nil, nil
		case "!=":
			return right != nil, nil
		default:
			// Any comparison with nil is false (e.g., nil > 5 = false)
			return false, nil
		}
	}
	if right == nil {
		switch op {
		case "==":
			return false, nil // left != nil
		case "!=":
			return true, nil // left != nil
		default:
			// Any comparison with nil is false
			return false, nil
		}
	}

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
