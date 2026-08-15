package gocamel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// JSONDataFormat configures JSON marshalling and unmarshalling.
type JSONDataFormat struct {
	prettyPrint   bool
	prefix        string
	indent        string
	unmarshalType reflect.Type
	useNumber     bool
}

// JSONOption configures a JSONDataFormat.
type JSONOption func(*JSONDataFormat)

// JSONPrettyPrint enables indented JSON output with 2 spaces.
func JSONPrettyPrint(pretty bool) JSONOption {
	return func(f *JSONDataFormat) {
		f.prettyPrint = pretty
		if pretty && f.indent == "" {
			f.indent = "  "
		}
	}
}

// JSONIndent sets custom prefix and indent strings for JSON formatting.
func JSONIndent(prefix, indent string) JSONOption {
	return func(f *JSONDataFormat) {
		f.prettyPrint = true
		f.prefix = prefix
		f.indent = indent
	}
}

// JSONUnmarshalType sets the target Go type or struct pointer to unmarshal JSON into.
// Example: JSONUnmarshalType(MyStruct{}) or JSONUnmarshalType(&MyStruct{})
func JSONUnmarshalType(target any) JSONOption {
	return func(f *JSONDataFormat) {
		if target == nil {
			f.unmarshalType = nil
			return
		}
		if t, ok := target.(reflect.Type); ok {
			f.unmarshalType = t
			return
		}
		f.unmarshalType = reflect.TypeOf(target)
	}
}

// JSONUseNumber causes the Decoder to unmarshal a number into an interface{} as a json.Number instead of as a float64.
func JSONUseNumber(useNumber bool) JSONOption {
	return func(f *JSONDataFormat) {
		f.useNumber = useNumber
	}
}

// NewJSONDataFormat creates a new JSONDataFormat with the provided options.
func NewJSONDataFormat(opts ...JSONOption) *JSONDataFormat {
	format := &JSONDataFormat{}
	for _, opt := range opts {
		if opt != nil {
			opt(format)
		}
	}
	return format
}

// Marshal converts the exchange body into a JSON string.
func (f *JSONDataFormat) Marshal(_ *Exchange, body any) (any, error) {
	if body == nil {
		return "null", nil
	}

	var data []byte
	var err error

	if f.prettyPrint {
		indent := f.indent
		if indent == "" {
			indent = "  "
		}
		data, err = json.MarshalIndent(body, f.prefix, indent)
	} else {
		data, err = json.Marshal(body)
	}

	if err != nil {
		return nil, fmt.Errorf("json marshal failed: %w", err)
	}

	return string(data), nil
}

// Unmarshal converts JSON bytes or string into Go objects.
func (f *JSONDataFormat) Unmarshal(_ *Exchange, body any) (any, error) {
	data, err := bodyToBytes(body)
	if err != nil {
		return nil, fmt.Errorf("json unmarshal expects JSON string, []byte, or io.Reader: %w", err)
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("json unmarshal: empty payload")
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if f.useNumber {
		decoder.UseNumber()
	}

	if f.unmarshalType != nil {
		targetType := f.unmarshalType
		isPtr := targetType.Kind() == reflect.Ptr
		var elemType reflect.Type
		if isPtr {
			elemType = targetType.Elem()
		} else {
			elemType = targetType
		}

		// Allocate a new instance
		valPtr := reflect.New(elemType)
		if err := decoder.Decode(valPtr.Interface()); err != nil {
			return nil, fmt.Errorf("json unmarshal into %v failed: %w", targetType, err)
		}

		if isPtr {
			return valPtr.Interface(), nil
		}
		return valPtr.Elem().Interface(), nil
	}

	// Default unmarshalling into generic any
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return result, nil
}

// MarshalJson adds a JSON marshal step to the route.
func (b *RouteBuilder) MarshalJson(opts ...JSONOption) *RouteBuilder {
	return b.Marshal(NewJSONDataFormat(opts...))
}

// UnmarshalJson adds a JSON unmarshal step to the route.
func (b *RouteBuilder) UnmarshalJson(opts ...JSONOption) *RouteBuilder {
	return b.Unmarshal(NewJSONDataFormat(opts...))
}
