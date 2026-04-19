package gocamel

import (
	"context"
	"fmt"
	"strings"

	"github.com/lestrrat-go/libxml2"
	"github.com/lestrrat-go/libxml2/xsd"
)

// XsdComponent represents the XSD component
type XsdComponent struct{}

// NewXsdComponent creates a new XsdComponent
func NewXsdComponent() *XsdComponent {
	return &XsdComponent{}
}

// CreateEndpoint creates a new endpoint XSD
func (c *XsdComponent) CreateEndpoint(uri string) (Endpoint, error) {
	// Format de l'URI: xsd:path/vers/schema.xsd
	path := strings.TrimPrefix(uri, "xsd:")
	if path == "" {
		return nil, fmt.Errorf("file path missing in URI: %s", uri)
	}

	// Security: validate path for directory traversal
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("path contains traversal sequence: %s", path)
	}
	if strings.Contains(path, "\x00") {
		return nil, fmt.Errorf("path contains null byte")
	}

	return &XsdEndpoint{
		uri:  uri,
		path: path,
		comp: c,
	}, nil
}

// XsdEndpoint represents a XSD endpoint
type XsdEndpoint struct {
	uri  string
	path string
	comp *XsdComponent
}

// URI returns the URI de l'endpoint
func (e *XsdEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur XSD
func (e *XsdEndpoint) CreateProducer() (Producer, error) {
	return &XsdProducer{
		path: e.path,
	}, nil
}

// CreateConsumer n'est pas supported for le composant XSD
func (e *XsdEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("XSD component does not support consumers")
}

// XsdProducer represents a producteur XSD
type XsdProducer struct {
	path   string
	schema *xsd.Schema
}

// Start starts the producteur XSD
func (p *XsdProducer) Start(ctx context.Context) error {
	// Parsing du schéma XSD
	schema, err := xsd.ParseFromFile(p.path)
	if err != nil {
		return fmt.Errorf("error reading XSD file: %v", err)
	}
	p.schema = schema
	return nil
}

// Stop stops the producteur XSD
func (p *XsdProducer) Stop() error {
	if p.schema != nil {
		p.schema.Free()
		p.schema = nil
	}
	return nil
}

// Send effectue la validation XSD
func (p *XsdProducer) Send(exchange *Exchange) error {
	if p.schema == nil {
		return fmt.Errorf("XSD producer is not started or schema is invalid")
	}

	// Récupération du XML à validr
	var xmlContent []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		xmlContent = body
	case string:
		xmlContent = []byte(body)
	default:
		return fmt.Errorf("unsupported body type for XSD validation: %T", exchange.GetIn().GetBody())
	}

	// Parsing du documents XML
	doc, err := libxml2.Parse(xmlContent)
	if err != nil {
		return fmt.Errorf("error parsing XML document: %v", err)
	}
	defer doc.Free()

	// validation
	if err := p.schema.Validate(doc); err != nil {
		return fmt.Errorf("XSD validation error: %v", err)
	}

	return nil
}
