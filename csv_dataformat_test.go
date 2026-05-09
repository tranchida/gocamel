package gocamel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSVDataFormat_UnmarshalRows(t *testing.T) {
	format := NewCSVDataFormat()

	body, err := format.Unmarshal(nil, "name,age\nAlice,30\nBob,41\n")
	require.NoError(t, err)

	assert.Equal(t, [][]string{
		{"name", "age"},
		{"Alice", "30"},
		{"Bob", "41"},
	}, body)
}

func TestCSVDataFormat_UnmarshalMapsWithCapturedHeader(t *testing.T) {
	exchange := NewExchange(context.Background())
	format := NewCSVDataFormat(CSVUseMaps(true), CSVCaptureHeaderRecord(true))

	body, err := format.Unmarshal(exchange, "name,age\nAlice,30\nBob,41\n")
	require.NoError(t, err)

	assert.Equal(t, []map[string]string{
		{"name": "Alice", "age": "30"},
		{"name": "Bob", "age": "41"},
	}, body)

	header, ok := exchange.GetIn().GetHeader(CamelCsvHeaderRecord)
	require.True(t, ok)
	assert.Equal(t, []string{"name", "age"}, header)
}

func TestCSVDataFormat_UnmarshalWithDelimiterAndTrim(t *testing.T) {
	format := NewCSVDataFormat(
		CSVDelimiter("|"),
		CSVTrim(true),
		CSVSkipHeaderRecord(true),
	)

	body, err := format.Unmarshal(nil, "name|age\n Alice | 30 \n")
	require.NoError(t, err)

	assert.Equal(t, [][]string{{"Alice", "30"}}, body)
}

func TestCSVDataFormat_MarshalRows(t *testing.T) {
	format := NewCSVDataFormat()

	body, err := format.Marshal(nil, [][]string{
		{"name", "age"},
		{"Alice", "30"},
	})
	require.NoError(t, err)

	assert.Equal(t, "name,age\nAlice,30\n", body)
}

func TestCSVDataFormat_MarshalMapsWithHeader(t *testing.T) {
	format := NewCSVDataFormat(CSVHeader("name", "age"))

	body, err := format.Marshal(nil, []map[string]any{
		{"name": "Alice", "age": 30},
		{"name": "Bob", "age": 41},
	})
	require.NoError(t, err)

	assert.Equal(t, "name,age\nAlice,30\nBob,41\n", body)
}

func TestRouteBuilderCSVSteps(t *testing.T) {
	camel := NewCamelContext()
	camel.AddComponent("direct", NewDirectComponent())

	route, err := camel.CreateRouteBuilder().
		From("direct:start").
		UnmarshalCSV(CSVUseMaps(true)).
		ProcessFunc(func(exchange *Exchange) error {
			rows := exchange.GetIn().GetBody().([]map[string]string)
			rows[0]["status"] = "ok"
			exchange.GetIn().SetBody(rows)
			return nil
		}).
		MarshalCSV(CSVHeader("name", "status")).
		Build()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody("name\nAlice\n")

	err = route.Process(exchange)
	require.NoError(t, err)
	assert.Equal(t, "name,status\nAlice,ok\n", exchange.GetIn().GetBody())
}
