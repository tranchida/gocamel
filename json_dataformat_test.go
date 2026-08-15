package gocamel

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPerson struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestJSONDataFormat_MarshalUnmarshalStruct(t *testing.T) {
	format := NewJSONDataFormat()

	p := testPerson{Name: "Alice", Age: 30}
	marshaled, err := format.Marshal(nil, p)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"Alice","age":30}`, marshaled.(string))

	// Unmarshal with typed struct
	typedFormat := NewJSONDataFormat(JSONUnmarshalType(testPerson{}))
	unmarshaled, err := typedFormat.Unmarshal(nil, marshaled)
	require.NoError(t, err)
	assert.Equal(t, testPerson{Name: "Alice", Age: 30}, unmarshaled)

	// Unmarshal with pointer type
	ptrFormat := NewJSONDataFormat(JSONUnmarshalType(&testPerson{}))
	unmarshaledPtr, err := ptrFormat.Unmarshal(nil, marshaled)
	require.NoError(t, err)
	assert.Equal(t, &testPerson{Name: "Alice", Age: 30}, unmarshaledPtr)
}

func TestJSONDataFormat_MarshalPrettyPrint(t *testing.T) {
	format := NewJSONDataFormat(JSONPrettyPrint(true))

	p := testPerson{Name: "Bob", Age: 25}
	marshaled, err := format.Marshal(nil, p)
	require.NoError(t, err)
	expected := "{\n  \"name\": \"Bob\",\n  \"age\": 25\n}"
	assert.Equal(t, expected, marshaled.(string))
}

func TestJSONDataFormat_UnmarshalGeneric(t *testing.T) {
	format := NewJSONDataFormat()

	jsonStr := `{"title":"Hello","count":42}`
	res, err := format.Unmarshal(nil, jsonStr)
	require.NoError(t, err)

	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Hello", m["title"])
	assert.Equal(t, float64(42), m["count"])
}

func TestJSONDataFormat_UnmarshalFromReaderAndBytes(t *testing.T) {
	format := NewJSONDataFormat(JSONUnmarshalType(testPerson{}))

	// From bytes
	res, err := format.Unmarshal(nil, []byte(`{"name":"Charlie","age":40}`))
	require.NoError(t, err)
	assert.Equal(t, testPerson{Name: "Charlie", Age: 40}, res)

	// From io.Reader
	reader := strings.NewReader(`{"name":"Diana","age":35}`)
	res, err = format.Unmarshal(nil, reader)
	require.NoError(t, err)
	assert.Equal(t, testPerson{Name: "Diana", Age: 35}, res)
}

func TestJSONDataFormat_MarshalNilAndEmpty(t *testing.T) {
	format := NewJSONDataFormat()

	res, err := format.Marshal(nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "null", res)

	_, err = format.Unmarshal(nil, "")
	assert.Error(t, err)
}

func TestJSONDataFormat_RouteIntegration(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var captured testPerson

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		MarshalJson().
		UnmarshalJson(JSONUnmarshalType(testPerson{})).
		ProcessFunc(func(e *Exchange) error {
			captured = e.GetIn().GetBody().(testPerson)
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, camelCtx.Start())
	defer camelCtx.Stop()

	endpoint, err := camelCtx.CreateEndpoint("direct:start")
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(testPerson{Name: "Eve", Age: 28})

	err = producer.Send(exchange)
	require.NoError(t, err)

	assert.Equal(t, "Eve", captured.Name)
	assert.Equal(t, 28, captured.Age)
}
