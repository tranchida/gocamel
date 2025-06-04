package gocamel

import (
	"log"
)

// RouteBuilder facilite la création de routes
type RouteBuilder struct {
	context *CamelContext
	route   *Route
}

// NewRouteBuilder crée une nouvelle instance de RouteBuilder
func NewRouteBuilder(context *CamelContext) *RouteBuilder {
	return &RouteBuilder{
		context: context,
		route:   context.CreateRoute(),
	}
}

// From définit l'endpoint source de la route
func (b *RouteBuilder) From(uri string) *RouteBuilder {
	b.route.From(uri)
	return b
}

// Process ajoute un processeur à la route
func (b *RouteBuilder) Process(processor Processor) *RouteBuilder {
	b.route.AddProcessor(processor)
	return b
}

// ProcessFunc ajoute une fonction de traitement à la route
func (b *RouteBuilder) ProcessFunc(f func(*Exchange) error) *RouteBuilder {
	b.route.ProcessFunc(f)
	return b
}

// SetBody définit le corps du message de sortie
func (b *RouteBuilder) SetBody(body interface{}) *RouteBuilder {
	b.route.SetBody(body)
	return b
}

// SetHeader définit un en-tête du message de sortie
func (b *RouteBuilder) SetHeader(key string, value interface{}) *RouteBuilder {
	b.route.SetHeader(key, value)
	return b
}

// SetID définit l'ID de la route
func (b *RouteBuilder) SetID(id string) *RouteBuilder {
	b.route.SetID(id)
	return b
}

// SetDescription définit la description de la route
func (b *RouteBuilder) SetDescription(description string) *RouteBuilder {
	b.route.SetDescription(description)
	return b
}

// SetGroup définit le groupe de la route
func (b *RouteBuilder) SetGroup(group string) *RouteBuilder {
	b.route.SetGroup(group)
	return b
}

// Log ajoute un processeur qui log le message
func (b *RouteBuilder) Log(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		log.Printf("%s: %+v", message, exchange)
		return nil
	})
}

// LogBody ajoute un processeur qui log le corps du message
func (b *RouteBuilder) LogBody(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		log.Printf("%s: %v", message, exchange.GetIn().GetBody())
		return nil
	})
}

// LogHeaders ajoute un processeur qui log les en-têtes du message
func (b *RouteBuilder) LogHeaders(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		log.Printf("%s: %+v", message, exchange.GetIn().GetHeaders())
		return nil
	})
}

// Build finalise la construction de la route
func (b *RouteBuilder) Build() *Route {
	return b.route
}
