package gocamel

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestXsdComponent_CreateEndpoint(t *testing.T) {
	comp := NewXsdComponent()

	// Test URI valide
	endpoint, err := comp.CreateEndpoint("xsd:test.xsd")
	assert.NoError(t, err)
	assert.NotNil(t, endpoint)
	assert.Equal(t, "xsd:test.xsd", endpoint.URI())

	xsdEndpoint, ok := endpoint.(*XsdEndpoint)
	assert.True(t, ok)
	assert.Equal(t, "test.xsd", xsdEndpoint.path)

	// Test URI invalide (chemin manquant)
	endpoint, err = comp.CreateEndpoint("xsd:")
	assert.Error(t, err)
	assert.Nil(t, endpoint)
}

func TestXsdEndpoint_CreateProducerConsumer(t *testing.T) {
	endpoint := &XsdEndpoint{
		uri:  "xsd:test.xsd",
		path: "test.xsd",
		comp: NewXsdComponent(),
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

func TestXsdProducer_Send(t *testing.T) {
	// Création d'un fichier XSD temporaire
	xsdContent := `<?xml version="1.0" encoding="UTF-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">
	<xs:element name="note">
		<xs:complexType>
			<xs:sequence>
				<xs:element name="to" type="xs:string"/>
				<xs:element name="from" type="xs:string"/>
				<xs:element name="heading" type="xs:string"/>
				<xs:element name="body" type="xs:string"/>
			</xs:sequence>
		</xs:complexType>
	</xs:element>
</xs:schema>`

	tmpfile, err := os.CreateTemp("", "test*.xsd")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(xsdContent))
	assert.NoError(t, err)
	tmpfile.Close()

	producer := &XsdProducer{
		path: tmpfile.Name(),
	}
	err = producer.Start(context.Background())
	assert.NoError(t, err)
	defer producer.Stop()

	// Test de validation réussie
	validXml := `<?xml version="1.0" encoding="UTF-8"?>
<note>
	<to>Tove</to>
	<from>Jani</from>
	<heading>Reminder</heading>
	<body>Don't forget me this weekend!</body>
</note>`

	exchange := NewExchange(context.Background())
	exchange.GetIn().SetBody(validXml)

	err = producer.Send(exchange)
	assert.NoError(t, err)

	// Test de validation échouée (élément manquant)
	invalidXml := `<?xml version="1.0" encoding="UTF-8"?>
<note>
	<to>Tove</to>
	<from>Jani</from>
</note>`

	exchange.GetIn().SetBody(invalidXml)
	err = producer.Send(exchange)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "XSD validation")

	// Test avec un corps de type non supporté
	exchange.GetIn().SetBody(123)
	err = producer.Send(exchange)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported body")
}

func TestXsdProducer_Send_MissingFile(t *testing.T) {
	producer := &XsdProducer{
		path: "missing.xsd",
	}

	err := producer.Start(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "XSD file")
}
