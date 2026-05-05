package gocamel

/*
#cgo pkg-config: libxml-2.0
#include <libxml/xmlerror.h>
*/
import "C"

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/wamuir/go-xslt"
)

var xsltMutex sync.Mutex

// XsltComponent represents the XSLT component
type XsltComponent struct{}

// NewXsltComponent creates a new XsltComponent
func NewXsltComponent() *XsltComponent {
	return &XsltComponent{}
}

// CreateEndpoint creates a new endpoint XSLT
func (c *XsltComponent) CreateEndpoint(uri string) (Endpoint, error) {
	// Format de l'URI: xslt:path/vers/file.xsl
	path := strings.TrimPrefix(uri, "xslt:")
	if path == "" {
		return nil, fmt.Errorf("path de file missing in l'URI: %s", uri)
	}

	// Security: validate path for directory traversal
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("path contains traversal sequence: %s", path)
	}
	if strings.Contains(path, "\x00") {
		return nil, fmt.Errorf("path contains null byte")
	}

	return &XsltEndpoint{
		uri:  uri,
		path: path,
		comp: c,
	}, nil
}

// XsltEndpoint represents a XSLT endpoint
type XsltEndpoint struct {
	uri  string
	path string
	comp *XsltComponent
}

// URI returns the URI de l'endpoint
func (e *XsltEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur XSLT
func (e *XsltEndpoint) CreateProducer() (Producer, error) {
	return &XsltProducer{
		path: e.path,
	}, nil
}

// CreateConsumer n'est pas supported for le composant XSLT
func (e *XsltEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant XSLT ne supporte pas les consommateurs")
}

// XsltProducer represents a producteur XSLT
type XsltProducer struct {
	path       string
	stylesheet *xslt.Stylesheet
}

// Start starts the producteur XSLT
func (p *XsltProducer) Start(ctx context.Context) error {
	xsltMutex.Lock()
	defer xsltMutex.Unlock()

	// reading du file XSL
	xslContent, err := os.ReadFile(p.path)
	if err != nil {
		return fmt.Errorf("error reading XSLT file: %v", err)
	}

	// Réinitialise l'état d'error global de libxml2 for éviter que des errors
	// résiduelles (ex. validation XSD) ne causent de faux échecs in make_style
	// qui appelle xmlGetLastError() après un xmlParseMemory réussi.
	C.xmlResetLastError()

	stylesheet, err := xslt.NewStylesheet(xslContent)
	if err != nil {
		return fmt.Errorf("error parsing XSLT: %v", err)
	}
	p.stylesheet = stylesheet

	return nil
}

// Stop stops the producteur XSLT
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
		return fmt.Errorf("le producteur XSLT n'est pas démarré ou la feuille de style est invalid")
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
		return fmt.Errorf("unsupported body type for for la transformation XSLT: %T", exchange.GetIn().GetBody())
	}

	// transformation
	result, err := p.stylesheet.Transform(xmlContent)
	if err != nil {
		return fmt.Errorf("error during la transformation XSLT: %v", err)
	}

	exchange.GetIn().SetBody(result)
	return nil
}
