package gocamel

import (
	"context"
	"fmt"
	"strings"

	"github.com/lestrrat-go/libxml2"
	"github.com/lestrrat-go/libxml2/xsd"
)

// XsdComponent représente le composant XSD
type XsdComponent struct{}

// NewXsdComponent crée une nouvelle instance de XsdComponent
func NewXsdComponent() *XsdComponent {
	return &XsdComponent{}
}

// CreateEndpoint crée un nouvel endpoint XSD
func (c *XsdComponent) CreateEndpoint(uri string) (Endpoint, error) {
	// Format de l'URI: xsd:chemin/vers/schema.xsd
	path := strings.TrimPrefix(uri, "xsd:")
	if path == "" {
		return nil, fmt.Errorf("chemin de fichier manquant dans l'URI: %s", uri)
	}

	return &XsdEndpoint{
		uri:  uri,
		path: path,
		comp: c,
	}, nil
}

// XsdEndpoint représente un endpoint XSD
type XsdEndpoint struct {
	uri  string
	path string
	comp *XsdComponent
}

// URI retourne l'URI de l'endpoint
func (e *XsdEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur XSD
func (e *XsdEndpoint) CreateProducer() (Producer, error) {
	return &XsdProducer{
		path: e.path,
	}, nil
}

// CreateConsumer n'est pas supporté pour le composant XSD
func (e *XsdEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant XSD ne supporte pas les consommateurs")
}

// XsdProducer représente un producteur XSD
type XsdProducer struct {
	path   string
	schema *xsd.Schema
}

// Start démarre le producteur XSD
func (p *XsdProducer) Start(ctx context.Context) error {
	// Parsing du schéma XSD
	schema, err := xsd.ParseFromFile(p.path)
	if err != nil {
		return fmt.Errorf("erreur lors du parsing du fichier XSD: %v", err)
	}
	p.schema = schema
	return nil
}

// Stop arrête le producteur XSD
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
		return fmt.Errorf("le producteur XSD n'est pas démarré ou le schéma est invalide")
	}

	// Récupération du XML à valider
	var xmlContent []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		xmlContent = body
	case string:
		xmlContent = []byte(body)
	default:
		return fmt.Errorf("type de corps non supporté pour la validation XSD: %T", exchange.GetIn().GetBody())
	}

	// Parsing du document XML
	doc, err := libxml2.Parse(xmlContent)
	if err != nil {
		return fmt.Errorf("erreur lors du parsing du document XML: %v", err)
	}
	defer doc.Free()

	// Validation
	if err := p.schema.Validate(doc); err != nil {
		return fmt.Errorf("erreur de validation XSD: %v", err)
	}

	return nil
}
