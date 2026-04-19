package gocamel

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestXsltComponent_CreateEndpoint(t *testing.T) {
	comp := NewXsltComponent()

	// Test URI valide
	endpoint, err := comp.CreateEndpoint("xslt:test.xsl")
	assert.NoError(t, err)
	assert.NotNil(t, endpoint)
	assert.Equal(t, "xslt:test.xsl", endpoint.URI())

	xsltEndpoint, ok := endpoint.(*XsltEndpoint)
	assert.True(t, ok)
	assert.Equal(t, "test.xsl", xsltEndpoint.path)

	// Test URI invalide (chemin manquant)
	endpoint, err = comp.CreateEndpoint("xslt:")
	assert.Error(t, err)
	assert.Nil(t, endpoint)
}

func TestXsltEndpoint_CreateProducerConsumer(t *testing.T) {
	endpoint := &XsltEndpoint{
		uri:  "xslt:test.xsl",
		path: "test.xsl",
		comp: NewXsltComponent(),
	}

	// Test CreateProducer
	producer, err := endpoint.CreateProducer()
	assert.NoError(t, err)
	assert.NotNil(t, producer)

	// Test CreateConsumer (non supporté)
	consumer, err := endpoint.CreateConsumer(nil)
	assert.Error(t, err)
	assert.Nil(t, consumer)
}

func TestXsltProducer_Send(t *testing.T) {
	// Création d'un fichier XSLT temporaire
	xslContent := `<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
	<xsl:template match="/">
		<output>
			<xsl:value-of select="input/text"/>
		</output>
	</xsl:template>
</xsl:stylesheet>`

	tmpfile, err := os.CreateTemp("", "test*.xsl")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(xslContent))
	assert.NoError(t, err)
	tmpfile.Close()

	producer := &XsltProducer{
		path: tmpfile.Name(),
	}
	err = producer.Start(context.Background())
	assert.NoError(t, err)
	defer producer.Stop()

	// Test de transformation réussie
	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(`<?xml version="1.0" encoding="UTF-8"?><input><text>Hello World</text></input>`)

	err = producer.Send(exchange)
	assert.NoError(t, err)

	result := exchange.GetIn().GetBody()
	if resultBytes, ok := result.([]byte); ok {
		assert.Contains(t, string(resultBytes), "<output>Hello World</output>")
	} else if resultStr, ok := result.(string); ok {
		assert.Contains(t, resultStr, "<output>Hello World</output>")
	} else {
		t.Fatalf("Unexpected type for body: %T", result)
	}

	// Test de transformation avec un corps de type non supporté
	exchange.GetIn().SetBody(123)
	err = producer.Send(exchange)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported body")
}

func TestXsltProducer_Send_MissingFile(t *testing.T) {
	producer := &XsltProducer{
		path: "missing.xsl",
	}

	err := producer.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "XSLT file")
}

func TestXsltProducer_Send_InvalidXslt(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "invalid*.xsl")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(`invalid xml`))
	assert.NoError(t, err)
	tmpfile.Close()

	producer := &XsltProducer{
		path: tmpfile.Name(),
	}

	err = producer.Start(context.Background())
	assert.Error(t, err)
assert.Contains(t, err.Error(), "parsing")
}
