package gocamel

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
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
		runes := []rune(strVal)
		startF := toFloat64(args[0])
		start := 0
		switch {
		case math.IsNaN(startF) || startF < 0:
			start = 0
		case startF >= float64(len(runes)):
			return "", nil
		default:
			start = int(startF)
		}

		if len(args) >= 2 {
			endF := toFloat64(args[1])
			var end int
			switch {
			case math.IsNaN(endF) || endF <= float64(start):
				return "", nil
			case endF >= float64(len(runes)):
				end = len(runes)
			default:
				end = int(endF)
			}
			return string(runes[start:end]), nil
		}
		return string(runes[start:]), nil
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
		// Uppercase first rune, lowercase rest
		if strVal == "" {
			return strVal, nil
		}
		_, size := utf8.DecodeRuneInString(strVal)
		return strings.ToUpper(strVal[:size]) + strings.ToLower(strVal[size:]), nil
	case "title":
		// Title case each word separated by spaces
		//lint:ignore SA1019 strings.Title has no dependency-free Unicode equivalent in the zero-dep core
		return strings.Title(strVal), nil
	case "padstart":
		return padString("padstart", strVal, args, true)
	case "padend":
		return padString("padend", strVal, args, false)
	default:
		return nil, fmt.Errorf("unknown function: %s", funcName)
	}
}

func padString(funcName, strVal string, args []interface{}, atStart bool) (string, error) {
	if len(args) < 1 {
		return strVal, nil
	}
	targetFloat := toFloat64(args[0])
	if math.IsNaN(targetFloat) || targetFloat <= 0 {
		return strVal, nil
	}
	if targetFloat > float64(MaxSimpleInputBytes) || len(strVal) > MaxSimpleInputBytes {
		return "", fmt.Errorf("%s target length %.0f exceeds MaxSimpleInputBytes (%d)", funcName, targetFloat, MaxSimpleInputBytes)
	}
	targetLen := int(targetFloat)
	padChar := " "
	if len(args) >= 2 {
		padChar = fmt.Sprintf("%v", args[1])
	}
	strRuneLen := utf8.RuneCountInString(strVal)
	if strRuneLen >= targetLen || padChar == "" {
		return strVal, nil
	}
	padRunes := []rune(padChar)
	needed := targetLen - strRuneLen
	repeats := needed / len(padRunes)
	rem := needed % len(padRunes)
	padding := strings.Repeat(padChar, repeats) + string(padRunes[:rem])
	if atStart {
		return padding + strVal, nil
	}
	return strVal + padding, nil
}
