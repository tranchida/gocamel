package gocamel

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file holds the implementations of the prefixed Simple Language
// functions invoked via "${file:...}", "${date:...}" and "${base64:...}".
// The dispatch lives in simple_language_eval.go.

// evaluateFileFunction evaluates file-specific functions
func evaluateFileFunction(fn string, exchange *Exchange) (interface{}, error) {
	// Standard file headers
	name, _ := exchange.GetHeaderAsString(CamelFileName)
	path, _ := exchange.GetHeaderAsString(CamelFilePath)
	length, _ := exchange.GetHeaderAsInt(CamelFileLength)
	modified, _ := exchange.GetHeader(CamelFileLastModified)

	switch fn {
	case "name":
		return name, nil
	case "name.noext":
		if idx := strings.LastIndex(name, "."); idx != -1 {
			return name[:idx], nil
		}
		return name, nil
	case "name.ext", "ext":
		if idx := strings.LastIndex(name, "."); idx != -1 {
			return name[idx+1:], nil
		}
		return "", nil
	case "onlyname":
		// Only name without path
		if idx := strings.LastIndex(name, "/"); idx != -1 {
			return name[idx+1:], nil
		}
		if idx := strings.LastIndex(name, "\\"); idx != -1 {
			return name[idx+1:], nil
		}
		return name, nil
	case "onlyname.noext":
		onlyName := name
		if idx := strings.LastIndex(name, "/"); idx != -1 {
			onlyName = name[idx+1:]
		} else if idx := strings.LastIndex(name, "\\"); idx != -1 {
			onlyName = name[idx+1:]
		}
		if idx := strings.LastIndex(onlyName, "."); idx != -1 {
			return onlyName[:idx], nil
		}
		return onlyName, nil
	case "path":
		return path, nil
	case "absolute":
		// In GoCamel, Path is usually the full/absolute path if provided
		return path, nil
	case "absolute.path":
		return path, nil
	case "length", "size":
		return length, nil
	case "modified":
		if t, ok := modified.(time.Time); ok {
			return t.Format(time.RFC3339), nil
		}
		return modified, nil
	default:
		return nil, fmt.Errorf("unknown file function: %s", fn)
	}
}

// evaluateDateFunction evaluates advanced date functions
func evaluateDateFunction(command, offset, format string, exchange *Exchange) (interface{}, error) {
	var baseDate time.Time

	switch {
	case command == "now":
		baseDate = time.Now()
	case strings.HasPrefix(command, "header."):
		name := strings.TrimPrefix(command, "header.")
		val, ok := exchange.GetIn().GetHeader(name)
		if !ok {
			return nil, fmt.Errorf("date: header %s not found", name)
		}
		baseDate, ok = val.(time.Time)
		if !ok {
			return nil, fmt.Errorf("date: header %s is not a time.Time", name)
		}
	case strings.HasPrefix(command, "exchangeProperty.") || strings.HasPrefix(command, "property."):
		name := strings.TrimPrefix(command, "exchangeProperty.")
		name = strings.TrimPrefix(name, "property.")
		val, ok := exchange.GetProperty(name)
		if !ok {
			return nil, fmt.Errorf("date: property %s not found", name)
		}
		baseDate, ok = val.(time.Time)
		if !ok {
			return nil, fmt.Errorf("date: property %s is not a time.Time", name)
		}
	case command == "file":
		val, ok := exchange.GetIn().GetHeader(CamelFileLastModified)
		if !ok {
			return nil, fmt.Errorf("date: CamelFileLastModified header not found")
		}
		baseDate, ok = val.(time.Time)
		if !ok {
			return nil, fmt.Errorf("date: CamelFileLastModified is not a time.Time")
		}
	default:
		return nil, fmt.Errorf("unknown date command: %s", command)
	}

	// Apply offset if provided
	if offset != "" {
		var err error
		baseDate, err = applyDateOffset(baseDate, offset)
		if err != nil {
			return nil, fmt.Errorf("date: invalid offset %s: %w", offset, err)
		}
	}

	if format == "" {
		format = time.RFC3339
	}
	// Note: In a real implementation we might want to map Java/Camel date formats
	// to Go date formats (e.g. yyyy-MM-dd -> 2006-01-02).
	// For now we assume Go time format syntax.
	return baseDate.Format(format), nil
}

func applyDateOffset(t time.Time, offset string) (time.Time, error) {
	if offset == "" {
		return t, nil
	}

	sign := 1
	if offset[0] == '-' {
		sign = -1
	}

	// Remove the sign
	offset = offset[1:]

	// Extract unit
	unit := offset[len(offset)-1:]
	valueStr := offset[:len(offset)-1]

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return t, err
	}

	value *= sign

	switch unit {
	case "s":
		return t.Add(time.Duration(value) * time.Second), nil
	case "m":
		return t.Add(time.Duration(value) * time.Minute), nil
	case "h":
		return t.Add(time.Duration(value) * time.Hour), nil
	case "d":
		return t.AddDate(0, 0, value), nil
	default:
		return t, fmt.Errorf("unsupported date offset unit: %s", unit)
	}
}

// evaluateBase64Function evaluates base64 encoding and decoding functions
func evaluateBase64Function(command, target string, exchange *Exchange) (interface{}, error) {
	val, err := evaluateVariable(target, exchange)
	if err != nil {
		return nil, err
	}

	var data []byte
	switch v := val.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		data = []byte(fmt.Sprintf("%v", v))
	}

	switch command {
	case "encode":
		return base64.StdEncoding.EncodeToString(data), nil
	case "decode":
		decoded, err := base64.StdEncoding.DecodeString(string(data))
		if err != nil {
			return nil, fmt.Errorf("base64 decode error: %w", err)
		}
		return string(decoded), nil
	default:
		return nil, fmt.Errorf("unknown base64 command: %s", command)
	}
}
