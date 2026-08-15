package gocamel

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testBook struct {
	XMLName xml.Name `xml:"book"`
	Title   string   `xml:"title"`
	Author  string   `xml:"author"`
}

func TestXMLDataFormat_MarshalUnmarshalStruct(t *testing.T) {
	format := NewXMLDataFormat(XMLHeader(true), XMLPrettyPrint(true))

	b := testBook{XMLName: xml.Name{Local: "book"}, Title: "Go Integration", Author: "GoCamel"}
	marshaled, err := format.Marshal(nil, b)
	require.NoError(t, err)

	marshaledStr := marshaled.(string)
	assert.True(t, strings.HasPrefix(marshaledStr, xml.Header))
	assert.Contains(t, marshaledStr, "<book>")
	assert.Contains(t, marshaledStr, "<title>Go Integration</title>")

	// Unmarshal with typed struct
	typedFormat := NewXMLDataFormat(XMLUnmarshalType(testBook{}))
	unmarshaled, err := typedFormat.Unmarshal(nil, marshaledStr)
	require.NoError(t, err)
	assert.Equal(t, b, unmarshaled)

	// Unmarshal with pointer type
	ptrFormat := NewXMLDataFormat(XMLUnmarshalType(&testBook{}))
	unmarshaledPtr, err := ptrFormat.Unmarshal(nil, marshaledStr)
	require.NoError(t, err)
	assert.Equal(t, &b, unmarshaledPtr)
}

func TestXMLDataFormat_RejectDOCTYPE(t *testing.T) {
	format := NewXMLDataFormat()

	maliciousXML := `<?xml version="1.0"?>
<!DOCTYPE lolz [
 <!ENTITY lol "lol">
 <!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
]>
<book><title>&lol2;</title></book>`

	_, err := format.Unmarshal(nil, maliciousXML)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DOCTYPE declarations are not allowed")
}

func TestXMLDataFormat_GenericNodeUnmarshal(t *testing.T) {
	format := NewXMLDataFormat()

	xmlData := `<order id="123"><item>Widget</item></order>`
	res, err := format.Unmarshal(nil, xmlData)
	require.NoError(t, err)

	node, ok := res.(*GenericXMLNode)
	require.True(t, ok)
	assert.Equal(t, "order", node.XMLName.Local)
	require.Len(t, node.Attrs, 1)
	assert.Equal(t, "id", node.Attrs[0].Name.Local)
	assert.Equal(t, "123", node.Attrs[0].Value)
	require.Len(t, node.Nodes, 1)
	assert.Equal(t, "item", node.Nodes[0].XMLName.Local)
	assert.Equal(t, "Widget", node.Nodes[0].Content)
}

func TestXMLDataFormat_RouteIntegration(t *testing.T) {
	camelCtx := NewCamelContext()
	camelCtx.AddComponent("direct", NewDirectComponent())

	var captured testBook

	_, err := camelCtx.CreateRouteBuilder().
		From("direct:start").
		MarshalXml().
		UnmarshalXml(XMLUnmarshalType(testBook{})).
		ProcessFunc(func(e *Exchange) error {
			captured = e.GetIn().GetBody().(testBook)
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
	exchange.GetIn().SetBody(testBook{Title: "Camel in Action", Author: "Claus"})

	err = producer.Send(exchange)
	require.NoError(t, err)

	assert.Equal(t, "Camel in Action", captured.Title)
	assert.Equal(t, "Claus", captured.Author)
}
