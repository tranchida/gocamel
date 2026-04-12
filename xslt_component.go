package gocamel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/wamuir/go-xslt"
)

var xsltMutex sync.Mutex

// XsltComponent représente le composant XSLT
type XsltComponent struct{}

// NewXsltComponent crée une nouvelle instance de XsltComponent
func NewXsltComponent() *XsltComponent {
	return &XsltComponent{}
}

// CreateEndpoint crée un nouvel endpoint XSLT
func (c *XsltComponent) CreateEndpoint(uri string) (Endpoint, error) {
	// Format de l'URI: xslt:chemin/vers/fichier.xsl
	path := strings.TrimPrefix(uri, "xslt:")
	if path == "" {
		return nil, fmt.Errorf("chemin de fichier manquant dans l'URI: %s", uri)
	}

	return &XsltEndpoint{
		uri:  uri,
		path: path,
		comp: c,
	}, nil
}

// XsltEndpoint représente un endpoint XSLT
type XsltEndpoint struct {
	uri  string
	path string
	comp *XsltComponent
}

// URI retourne l'URI de l'endpoint
func (e *XsltEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur XSLT
func (e *XsltEndpoint) CreateProducer() (Producer, error) {
	return &XsltProducer{
		path: e.path,
	}, nil
}

// CreateConsumer n'est pas supporté pour le composant XSLT
func (e *XsltEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant XSLT ne supporte pas les consommateurs")
}

// XsltProducer représente un producteur XSLT
type XsltProducer struct {
	path       string
	stylesheet *xslt.Stylesheet
}

// Start démarre le producteur XSLT
func (p *XsltProducer) Start(ctx context.Context) error {
	xsltMutex.Lock()
	defer xsltMutex.Unlock()

	// Lecture du fichier XSL
	xslContent, err := os.ReadFile(p.path)
	if err != nil {
		return fmt.Errorf("erreur lors de la lecture du fichier XSLT: %v", err)
	}

	stylesheet, err := xslt.NewStylesheet(xslContent)
	if err != nil {
		return fmt.Errorf("erreur lors du parsing du fichier XSLT: %v", err)
	}
	p.stylesheet = stylesheet

	return nil
}

// Stop arrête le producteur XSLT
func (p *XsltProducer) Stop() error {
	if p.stylesheet != nil {
		p.stylesheet.Close()
		p.stylesheet = nil
	}
	return nil
}

// Send effectue la transformation XSLT
func (p *XsltProducer) Send(exchange *Exchange) error {
	if p.stylesheet == nil {
		return fmt.Errorf("le producteur XSLT n'est pas démarré ou la feuille de style est invalide")
	}

	xsltMutex.Lock()
	defer xsltMutex.Unlock()

	// Récupération du XML à transformer
	var xmlContent []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		xmlContent = body
	case string:
		xmlContent = []byte(body)
	default:
		return fmt.Errorf("type de corps non supporté pour la transformation XSLT: %T", exchange.GetIn().GetBody())
	}

	// Transformation
	result, err := p.stylesheet.Transform(xmlContent)
	if err != nil {
		return fmt.Errorf("erreur lors de la transformation XSLT: %v", err)
	}

	exchange.GetIn().SetBody(result)
	return nil
}
