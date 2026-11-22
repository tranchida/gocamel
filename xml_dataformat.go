package gocamel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"reflect"
)

// XMLDataFormat configures XML marshalling and unmarshalling.
type XMLDataFormat struct {
	prettyPrint   bool
	prefix        string
	indent        string
	unmarshalType reflect.Type
	header        bool
}

// XMLOption configures an XMLDataFormat.
type XMLOption func(*XMLDataFormat)

// XMLPrettyPrint enables indented XML output with 2 spaces.
func XMLPrettyPrint(pretty bool) XMLOption {
	return func(f *XMLDataFormat) {
		f.prettyPrint = pretty
		if pretty && f.indent == "" {
			f.indent = "  "
		}
	}
}

// XMLIndent sets custom prefix and indent strings for XML formatting.
func XMLIndent(prefix, indent string) XMLOption {
	return func(f *XMLDataFormat) {
		f.prettyPrint = true
		f.prefix = prefix
		f.indent = indent
	}
}

// XMLUnmarshalType sets the target Go type or struct pointer to unmarshal XML into.
// Example: XMLUnmarshalType(MyStruct{}) or XMLUnmarshalType(&MyStruct{})
func XMLUnmarshalType(target any) XMLOption {
	return func(f *XMLDataFormat) {
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

// XMLHeader specifies whether to include the XML declaration header (<?xml version="1.0" encoding="UTF-8"?>).
func XMLHeader(includeHeader bool) XMLOption {
	return func(f *XMLDataFormat) {
		f.header = includeHeader
	}
}

// NewXMLDataFormat creates a new XMLDataFormat with the provided options.
func NewXMLDataFormat(opts ...XMLOption) *XMLDataFormat {
	format := &XMLDataFormat{
		header: true, // Default to including XML header
	}
	for _, opt := range opts {
		if opt != nil {
			opt(format)
		}
	}
	return format
}

// Marshal converts the exchange body into an XML string.
func (f *XMLDataFormat) Marshal(_ *Exchange, body any) (any, error) {
	if body == nil {
		return "", nil
	}

	var data []byte
	var err error

	if f.prettyPrint {
		indent := f.indent
		if indent == "" {
			indent = "  "
		}
		data, err = xml.MarshalIndent(body, f.prefix, indent)
	} else {
		data, err = xml.Marshal(body)
	}

	if err != nil {
		return nil, fmt.Errorf("xml marshal failed: %w", err)
	}

	if f.header {
		return xml.Header + string(data), nil
	}
	return string(data), nil
}

// Unmarshal converts XML bytes or string into Go objects.
//
// Security: DOCTYPE declarations are rejected to prevent denial-of-service
// attacks via recursive entity expansion (CWE-776, "Billion Laughs").
func (f *XMLDataFormat) Unmarshal(_ *Exchange, body any) (any, error) {
	data, err := bodyToBytes(body)
	if err != nil {
		return nil, fmt.Errorf("xml unmarshal expects XML string, []byte, or io.Reader: %w", err)
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("xml unmarshal: empty payload")
	}

	// Security check for DOCTYPE in the XML prolog using RawToken to avoid
	// namespace tracking and full-document double scanning.
	prologDecoder := xml.NewDecoder(bytes.NewReader(trimmed))
	prologDecoder.Strict = true

prologScan:
	for {
		tok, err := prologDecoder.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml unmarshal scan failed: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			if bytes.HasPrefix(bytes.TrimSpace(t), []byte("DOCTYPE")) {
				return nil, fmt.Errorf("xml unmarshal: DOCTYPE declarations are not allowed")
			}
		case xml.StartElement:
			break prologScan
		}
	}

	decoder := xml.NewDecoder(bytes.NewReader(trimmed))
	decoder.Strict = true

	if f.unmarshalType != nil {
		targetType := f.unmarshalType
		isPtr := targetType.Kind() == reflect.Ptr
		var elemType reflect.Type
		if isPtr {
			elemType = targetType.Elem()
		} else {
			elemType = targetType
		}

		valPtr := reflect.New(elemType)
		if err := decoder.Decode(valPtr.Interface()); err != nil {
			return nil, fmt.Errorf("xml unmarshal into %v failed: %w", targetType, err)
		}
		if err := ensureNoTrailingXMLTokens(decoder, "xml unmarshal"); err != nil {
			return nil, err
		}

		if isPtr {
			return valPtr.Interface(), nil
		}
		return valPtr.Elem().Interface(), nil
	}

	// Default unmarshalling into generic XML element tree
	var genericDoc GenericXMLNode
	if err := decoder.Decode(&genericDoc); err != nil {
		return nil, fmt.Errorf("xml unmarshal failed: %w", err)
	}
	if err := ensureNoTrailingXMLTokens(decoder, "xml unmarshal"); err != nil {
		return nil, err
	}

	return &genericDoc, nil
}

// ensureNoTrailingXMLTokens verifies that only valid XML epilog tokens
// (whitespace, comments, or processing instructions) follow the root element.
func ensureNoTrailingXMLTokens(decoder *xml.Decoder, op string) error {
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s failed: %w", op, err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return fmt.Errorf("%s failed: unexpected trailing data", op)
			}
		case xml.Comment, xml.ProcInst:
			// Comments and processing instructions are valid XML epilog tokens.
		default:
			return fmt.Errorf("%s failed: unexpected trailing token", op)
		}
	}
}

// GenericXMLNode represents a raw XML element node when no target type is supplied.
type GenericXMLNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr       `xml:",any,attr"`
	Content string           `xml:",chardata"`
	Nodes   []GenericXMLNode `xml:",any"`
}

// MarshalXml adds an XML marshal step to the route.
func (b *RouteBuilder) MarshalXml(opts ...XMLOption) *RouteBuilder {
	return b.Marshal(NewXMLDataFormat(opts...))
}

// UnmarshalXml adds an XML unmarshal step to the route.
func (b *RouteBuilder) UnmarshalXml(opts ...XMLOption) *RouteBuilder {
	return b.Unmarshal(NewXMLDataFormat(opts...))
}
