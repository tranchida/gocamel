package gocamel

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var multipleSpacesRegex = regexp.MustCompile(` +`)

// applyStringFunction implements the Simple Language string verbs invoked via
// chained calls like body.trim().uppercase(). It is the leaf of the chained
// function-call dispatch built in simple_language_eval.go.
func applyStringFunction(funcName string, val interface{}, args []interface{}) (interface{}, error) {
	if val == nil {
		return nil, nil
	}

	strVal := fmt.Sprintf("%v", val)

	switch strings.ToLower(funcName) {
	case "trim":
		return strings.TrimSpace(strVal), nil
	case "uppercase", "upper":
		return strings.ToUpper(strVal), nil
	case "lowercase", "lower":
		return strings.ToLower(strVal), nil
	case "size", "length":
		// Handle different types
		if s, ok := val.(string); ok {
			return len(s), nil
		}
		// Try to get length of slice/array/map
		v := reflect.ValueOf(val)
		switch v.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
			return v.Len(), nil
		case reflect.Ptr:
			if !v.IsNil() {
				return applyStringFunction(funcName, v.Elem().Interface(), args)
			}
		}
		return 0, nil
	case "substring":
		if len(args) < 1 {
			return strVal, nil
		}
		start := int(toFloat64(args[0]))
		if start < 0 {
			start = 0
		}
		if start >= len(strVal) {
			return "", nil
		}

		if len(args) >= 2 {
			end := int(toFloat64(args[1]))
			if end > len(strVal) {
				end = len(strVal)
			}
			if end <= start {
				return "", nil
			}
			return strVal[start:end], nil
		}
		return strVal[start:], nil
	case "replace":
		if len(args) < 2 {
			return strVal, nil
		}
		oldStr := fmt.Sprintf("%v", args[0])
		newStr := fmt.Sprintf("%v", args[1])
		return strings.ReplaceAll(strVal, oldStr, newStr), nil
	case "normalizewhitespace":
		// Replace tabs, newlines, and carriage returns with spaces
		normalized := strings.ReplaceAll(strVal, "\t", " ")
		normalized = strings.ReplaceAll(normalized, "\n", " ")
		normalized = strings.ReplaceAll(normalized, "\r", " ")
		// Collapse multiple spaces into a single space
		normalized = multipleSpacesRegex.ReplaceAllString(normalized, " ")
		return strings.TrimSpace(normalized), nil
	case "split":
		sep := ","
		if len(args) >= 1 {
			sep = fmt.Sprintf("%v", args[0])
		}
		limit := -1
		if len(args) >= 2 {
			limit = int(toFloat64(args[1]))
		}
		if limit < 0 {
			result := strings.Split(strVal, sep)
			return result, nil
		}
		// SplitN returns at most n substrings
		result := strings.SplitN(strVal, sep, limit+1)
		// Trim to the requested limit (SplitN may return limit+1 elements)
		if len(result) > limit {
			result = result[:limit]
		}
		return result, nil
	case "capitalize":
		// Uppercase first letter, lowercase rest
		if len(strVal) == 0 {
			return strVal, nil
		}
		return strings.ToUpper(string(strVal[0])) + strings.ToLower(strVal[1:]), nil
	case "title":
		// Title case each word separated by spaces
		//lint:ignore SA1019 strings.Title has no dependency-free Unicode equivalent in the zero-dep core
		return strings.Title(strVal), nil
	case "padstart":
		if len(args) < 1 {
			return strVal, nil
		}
		targetLen := int(toFloat64(args[0]))
		padChar := " "
		if len(args) >= 2 {
			padChar = fmt.Sprintf("%v", args[1])
		}
		if len(strVal) >= targetLen || padChar == "" {
			return strVal, nil
		}
		if targetLen > MaxSimpleInputBytes || len(strVal) > MaxSimpleInputBytes {
			return nil, fmt.Errorf("padstart target length %d exceeds MaxSimpleInputBytes (%d)", targetLen, MaxSimpleInputBytes)
		}
		needed := targetLen - len(strVal)
		repeats := (needed + len(padChar) - 1) / len(padChar)
		padding := strings.Repeat(padChar, repeats)
		if len(padding) > needed {
			padding = padding[:needed]
		}
		return padding + strVal, nil
	case "padend":
		if len(args) < 1 {
			return strVal, nil
		}
		targetLen := int(toFloat64(args[0]))
		padChar := " "
		if len(args) >= 2 {
			padChar = fmt.Sprintf("%v", args[1])
		}
		if len(strVal) >= targetLen || padChar == "" {
			return strVal, nil
		}
		if targetLen > MaxSimpleInputBytes || len(strVal) > MaxSimpleInputBytes {
			return nil, fmt.Errorf("padend target length %d exceeds MaxSimpleInputBytes (%d)", targetLen, MaxSimpleInputBytes)
		}
		needed := targetLen - len(strVal)
		repeats := (needed + len(padChar) - 1) / len(padChar)
		padding := strings.Repeat(padChar, repeats)
		if len(padding) > needed {
			padding = padding[:needed]
		}
		return strVal + padding, nil
	default:
		return nil, fmt.Errorf("unknown function: %s", funcName)
	}
}
