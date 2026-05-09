package gocamel

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"maps"
	"reflect"
	"sort"
	"strings"
)

const (
	// CamelCsvHeaderRecord stores the CSV header record captured while unmarshalling.
	CamelCsvHeaderRecord = "CamelCsvHeaderRecord"
)

// CSVDataFormat configures CSV marshalling and unmarshalling.
type CSVDataFormat struct {
	Delimiter               rune
	Comment                 rune
	Header                  []string
	SkipHeaderRecord        bool
	CaptureHeaderRecord     bool
	UseMaps                 bool
	IgnoreEmptyLines        bool
	IgnoreSurroundingSpaces bool
	Trim                    bool
	RecordSeparator         string
}

// CSVOption configures a CSVDataFormat.
type CSVOption func(*CSVDataFormat)

// NewCSVDataFormat creates a CSV data format with defaults matching common CSV usage.
func NewCSVDataFormat(opts ...CSVOption) *CSVDataFormat {
	format := &CSVDataFormat{
		Delimiter:       ',',
		RecordSeparator: "\n",
	}
	for _, opt := range opts {
		opt(format)
	}
	return format
}

// CSVDelimiter sets the CSV delimiter. Only the first rune is used.
func CSVDelimiter(delimiter string) CSVOption {
	return func(format *CSVDataFormat) {
		for _, r := range delimiter {
			format.Delimiter = r
			return
		}
	}
}

// CSVHeader configures the CSV header row.
func CSVHeader(headers ...string) CSVOption {
	return func(format *CSVDataFormat) {
		format.Header = append([]string(nil), headers...)
	}
}

// CSVSkipHeaderRecord skips the header row while unmarshalling and omits it while marshalling.
func CSVSkipHeaderRecord(skip bool) CSVOption {
	return func(format *CSVDataFormat) {
		format.SkipHeaderRecord = skip
	}
}

// CSVCaptureHeaderRecord captures the first CSV row into the CamelCsvHeaderRecord header.
func CSVCaptureHeaderRecord(capture bool) CSVOption {
	return func(format *CSVDataFormat) {
		format.CaptureHeaderRecord = capture
	}
}

// CSVUseMaps makes unmarshalling produce []map[string]string instead of [][]string.
func CSVUseMaps(useMaps bool) CSVOption {
	return func(format *CSVDataFormat) {
		format.UseMaps = useMaps
	}
}

// CSVIgnoreEmptyLines skips fully empty records while unmarshalling.
func CSVIgnoreEmptyLines(ignore bool) CSVOption {
	return func(format *CSVDataFormat) {
		format.IgnoreEmptyLines = ignore
	}
}

// CSVIgnoreSurroundingSpaces trims leading spaces after delimiters while unmarshalling.
func CSVIgnoreSurroundingSpaces(ignore bool) CSVOption {
	return func(format *CSVDataFormat) {
		format.IgnoreSurroundingSpaces = ignore
	}
}

// CSVTrim trims leading and trailing blanks from every field.
func CSVTrim(trim bool) CSVOption {
	return func(format *CSVDataFormat) {
		format.Trim = trim
	}
}

// CSVRecordSeparator sets the line ending used while marshalling.
func CSVRecordSeparator(separator string) CSVOption {
	return func(format *CSVDataFormat) {
		if separator != "" {
			format.RecordSeparator = separator
		}
	}
}

// CSVComment sets the comment marker used while unmarshalling.
func CSVComment(comment rune) CSVOption {
	return func(format *CSVDataFormat) {
		format.Comment = comment
	}
}

// Marshal converts structured data into a CSV string.
func (f *CSVDataFormat) Marshal(_ *Exchange, body any) (any, error) {
	rows, headers, err := f.rowsForMarshal(body)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Comma = f.delimiter()
	writer.UseCRLF = f.RecordSeparator == "\r\n"

	if len(headers) > 0 && !f.SkipHeaderRecord {
		if err := writer.Write(headers); err != nil {
			return "", fmt.Errorf("csv marshal header: %w", err)
		}
	}
	for _, row := range rows {
		if f.Trim {
			row = trimRecord(row)
		}
		if err := writer.Write(row); err != nil {
			return "", fmt.Errorf("csv marshal row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("csv marshal: %w", err)
	}

	out := buf.String()
	if f.RecordSeparator != "" && f.RecordSeparator != "\n" && f.RecordSeparator != "\r\n" {
		out = strings.ReplaceAll(out, "\n", f.RecordSeparator)
	}
	return out, nil
}

// Unmarshal parses CSV input into [][]string or []map[string]string when UseMaps is enabled.
func (f *CSVDataFormat) Unmarshal(exchange *Exchange, body any) (any, error) {
	reader, err := f.readerForBody(body)
	if err != nil {
		return nil, err
	}

	csvReader := csv.NewReader(reader)
	csvReader.Comma = f.delimiter()
	csvReader.FieldsPerRecord = -1
	csvReader.TrimLeadingSpace = f.IgnoreSurroundingSpaces
	if f.Comment != 0 {
		csvReader.Comment = f.Comment
	}

	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv unmarshal: %w", err)
	}
	if f.Trim {
		for i := range records {
			records[i] = trimRecord(records[i])
		}
	}
	if f.IgnoreEmptyLines {
		records = filterEmptyRecords(records)
	}

	headers := append([]string(nil), f.Header...)
	if len(records) > 0 && (f.CaptureHeaderRecord || (f.UseMaps && len(headers) == 0) || f.SkipHeaderRecord) {
		if len(headers) == 0 {
			headers = append([]string(nil), records[0]...)
		}
		if f.CaptureHeaderRecord && exchange != nil {
			exchange.GetIn().SetHeader(CamelCsvHeaderRecord, append([]string(nil), records[0]...))
		}
		records = records[1:]
	}

	if f.UseMaps {
		if len(headers) == 0 {
			return nil, fmt.Errorf("csv unmarshal: maps require a header")
		}
		return recordsToMaps(headers, records), nil
	}
	return records, nil
}

func (f *CSVDataFormat) delimiter() rune {
	if f.Delimiter == 0 {
		return ','
	}
	return f.Delimiter
}

func (f *CSVDataFormat) readerForBody(body any) (io.Reader, error) {
	switch v := body.(type) {
	case nil:
		return strings.NewReader(""), nil
	case string:
		return strings.NewReader(v), nil
	case []byte:
		return bytes.NewReader(v), nil
	case io.Reader:
		return v, nil
	default:
		return nil, fmt.Errorf("csv unmarshal: unsupported body type %T", body)
	}
}

func (f *CSVDataFormat) rowsForMarshal(body any) ([][]string, []string, error) {
	headers := append([]string(nil), f.Header...)

	switch v := body.(type) {
	case nil:
		return nil, headers, nil
	case [][]string:
		return cloneRecords(v), headers, nil
	case [][]any:
		return anyRowsToStrings(v), headers, nil
	case []map[string]string:
		if len(headers) == 0 {
			headers = sortedStringMapKeys(v)
		}
		return stringMapsToRows(headers, v), headers, nil
	case []map[string]any:
		if len(headers) == 0 {
			headers = sortedAnyMapKeys(v)
		}
		return anyMapsToRows(headers, v), headers, nil
	case map[string]string:
		if len(headers) == 0 {
			headers = sortedKeys(v)
		}
		return stringMapsToRows(headers, []map[string]string{v}), headers, nil
	case map[string]any:
		if len(headers) == 0 {
			headers = sortedKeys(v)
		}
		return anyMapsToRows(headers, []map[string]any{v}), headers, nil
	case []string:
		return [][]string{append([]string(nil), v...)}, headers, nil
	case []any:
		return [][]string{anyRowToStrings(v)}, headers, nil
	default:
		rows, ok := rowsFromStructs(body, headers)
		if ok {
			if len(headers) == 0 {
				headers = structFieldNames(body)
			}
			return rows, headers, nil
		}
		return nil, nil, fmt.Errorf("csv marshal: unsupported body type %T", body)
	}
}

func cloneRecords(records [][]string) [][]string {
	out := make([][]string, len(records))
	for i, record := range records {
		out[i] = append([]string(nil), record...)
	}
	return out
}

func anyRowsToStrings(rows [][]any) [][]string {
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = anyRowToStrings(row)
	}
	return out
}

func anyRowToStrings(row []any) []string {
	out := make([]string, len(row))
	for i, value := range row {
		out[i] = stringifyCSVValue(value)
	}
	return out
}

func stringMapsToRows(headers []string, records []map[string]string) [][]string {
	rows := make([][]string, len(records))
	for i, record := range records {
		row := make([]string, len(headers))
		for j, header := range headers {
			row[j] = record[header]
		}
		rows[i] = row
	}
	return rows
}

func anyMapsToRows(headers []string, records []map[string]any) [][]string {
	rows := make([][]string, len(records))
	for i, record := range records {
		row := make([]string, len(headers))
		for j, header := range headers {
			row[j] = stringifyCSVValue(record[header])
		}
		rows[i] = row
	}
	return rows
}

func recordsToMaps(headers []string, records [][]string) []map[string]string {
	out := make([]map[string]string, len(records))
	for i, record := range records {
		row := make(map[string]string, len(headers))
		for j, header := range headers {
			if j < len(record) {
				row[header] = record[j]
			} else {
				row[header] = ""
			}
		}
		out[i] = row
	}
	return out
}

func sortedStringMapKeys(records []map[string]string) []string {
	keySet := make(map[string]struct{})
	for _, record := range records {
		for key := range record {
			keySet[key] = struct{}{}
		}
	}
	return sortedKeys(keySet)
}

func sortedAnyMapKeys(records []map[string]any) []string {
	keySet := make(map[string]struct{})
	for _, record := range records {
		for key := range record {
			keySet[key] = struct{}{}
		}
	}
	return sortedKeys(keySet)
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := maps.Keys(m)
	out := make([]string, 0, len(m))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func trimRecord(record []string) []string {
	out := make([]string, len(record))
	for i, field := range record {
		out[i] = strings.TrimSpace(field)
	}
	return out
}

func filterEmptyRecords(records [][]string) [][]string {
	out := records[:0]
	for _, record := range records {
		empty := true
		for _, field := range record {
			if field != "" {
				empty = false
				break
			}
		}
		if !empty {
			out = append(out, record)
		}
	}
	return out
}

func stringifyCSVValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func rowsFromStructs(body any, headers []string) ([][]string, bool) {
	value := reflect.ValueOf(body)
	if value.Kind() != reflect.Slice {
		return nil, false
	}
	rows := make([][]string, value.Len())
	for i := 0; i < value.Len(); i++ {
		item := value.Index(i)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				rows[i] = make([]string, len(headers))
				continue
			}
			item = item.Elem()
		}
		if item.Kind() != reflect.Struct {
			return nil, false
		}
		fieldNames := headers
		if len(fieldNames) == 0 {
			fieldNames = exportedFieldNames(item.Type())
		}
		row := make([]string, len(fieldNames))
		for j, fieldName := range fieldNames {
			field := item.FieldByName(fieldName)
			if field.IsValid() && field.CanInterface() {
				row[j] = stringifyCSVValue(field.Interface())
			}
		}
		rows[i] = row
	}
	return rows, true
}

func structFieldNames(body any) []string {
	value := reflect.ValueOf(body)
	if value.Kind() != reflect.Slice || value.Len() == 0 {
		return nil
	}
	item := value.Index(0)
	if item.Kind() == reflect.Pointer {
		if item.IsNil() {
			return nil
		}
		item = item.Elem()
	}
	if item.Kind() != reflect.Struct {
		return nil
	}
	return exportedFieldNames(item.Type())
}

func exportedFieldNames(t reflect.Type) []string {
	fields := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath == "" {
			fields = append(fields, field.Name)
		}
	}
	return fields
}

// MarshalCSV adds a CSV marshal step to the route.
func (b *RouteBuilder) MarshalCSV(opts ...CSVOption) *RouteBuilder {
	return b.Marshal(NewCSVDataFormat(opts...))
}

// UnmarshalCSV adds a CSV unmarshal step to the route.
func (b *RouteBuilder) UnmarshalCSV(opts ...CSVOption) *RouteBuilder {
	return b.Unmarshal(NewCSVDataFormat(opts...))
}
