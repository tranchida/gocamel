package gocamel

import (
	"log"
)

// RouteBuilder facilite la création de routes
type RouteBuilder struct {
	context   *CamelContext
	route     *Route
	container ProcessorContainer
}

// NewRouteBuilder crée une nouvelle instance de RouteBuilder
func NewRouteBuilder(context *CamelContext) *RouteBuilder {
	route := context.CreateRoute()
	return &RouteBuilder{
		context:   context,
		route:     route,
		container: route,
	}
}

// From définit l'endpoint source de la route
func (b *RouteBuilder) From(uri string) *RouteBuilder {
	b.route.From(uri)
	return b
}

// Process ajoute un processeur au conteneur actuel
func (b *RouteBuilder) Process(processor Processor) *RouteBuilder {
	b.container.AddProcessor(processor)
	return b
}

// ProcessFunc ajoute une fonction de traitement au conteneur actuel
func (b *RouteBuilder) ProcessFunc(f func(*Exchange) error) *RouteBuilder {
	b.container.AddProcessor(ProcessorFunc(f))
	return b
}

// SetBody définit le corps du message de sortie
func (b *RouteBuilder) SetBody(body interface{}) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	})
}

// SetHeader définit un en-tête du message de sortie
func (b *RouteBuilder) SetHeader(key string, value interface{}) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	})
}

// SetHeaders définit plusieurs en-têtes du message de sortie
func (b *RouteBuilder) SetHeaders(headers map[string]any) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeaders(headers)
		return nil
	})
}

// SetHeadersFunc définit plusieurs en-têtes via une fonction
func (b *RouteBuilder) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		headers, err := f(exchange)
		if err != nil {
			return err
		}
		exchange.GetOut().SetHeaders(headers)
		return nil
	})
}

// SetProperty définit une propriété de l'échange
func (b *RouteBuilder) SetProperty(key string, value any) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.SetProperty(key, value)
		return nil
	})
}

// SetPropertyFunc définit une propriété via une fonction
func (b *RouteBuilder) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		val, err := f(exchange)
		if err != nil {
			return err
		}
		exchange.SetProperty(key, val)
		return nil
	})
}

// RemoveProperty supprime une propriété de l'échange
func (b *RouteBuilder) RemoveProperty(key string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.RemoveProperty(key)
		return nil
	})
}

// RemoveProperties supprime les propriétés correspondant au pattern fourni,
// sauf celles qui correspondent aux patterns d'exclusion fournis.
func (b *RouteBuilder) RemoveProperties(pattern string, excludePatterns ...string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.RemoveProperties(pattern, excludePatterns...)
		return nil
	})
}

// RemoveHeader supprime un en-tête du message entrant
func (b *RouteBuilder) RemoveHeader(name string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetIn().RemoveHeader(name)
		return nil
	})
}

// RemoveHeaders supprime les en-têtes correspondants au pattern fourni,
// sauf ceux qui correspondent aux patterns d'exclusion fournis.
func (b *RouteBuilder) RemoveHeaders(pattern string, excludePatterns ...string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		exchange.GetIn().RemoveHeaders(pattern, excludePatterns...)
		return nil
	})
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

// Aggregate ajoute un processeur Aggregator au conteneur actuel
func (b *RouteBuilder) Aggregate(aggregator *Aggregator) *RouteBuilder {
	b.container.AddProcessor(aggregator)
	return b
}

// Split commence un bloc Split EIP
func (b *RouteBuilder) Split(expression func(*Exchange) (any, error)) *SplitDefinition {
	s := NewSplitter(expression)
	b.container.AddProcessor(s)
	
	// On crée un nouveau RouteBuilder dont le conteneur est le splitter
	return &SplitDefinition{
		RouteBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: s,
		},
		parent:   b,
		splitter: s,
	}
}

// SplitDefinition permet de configurer le traitement de chaque partie du message splité
type SplitDefinition struct {
	*RouteBuilder
	parent   *RouteBuilder
	splitter *Splitter
}

// AggregationStrategy définit la stratégie d'agrégation pour le splitter
func (d *SplitDefinition) AggregationStrategy(strategy AggregationStrategy) *SplitDefinition {
	d.splitter.SetAggregationStrategy(strategy)
	return d
}

// Process ajoute un processeur au conteneur actuel et reste dans le contexte du split
func (d *SplitDefinition) Process(processor Processor) *SplitDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc ajoute une fonction de traitement au conteneur actuel et reste dans le contexte du split
func (d *SplitDefinition) ProcessFunc(f func(*Exchange) error) *SplitDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To ajoute un endpoint de destination au conteneur actuel et reste dans le contexte du split
func (d *SplitDefinition) To(uri string) *SplitDefinition {
	d.RouteBuilder.To(uri)
	return d
}

// SetBody définit le corps du message de sortie et reste dans le contexte du split
func (d *SplitDefinition) SetBody(body interface{}) *SplitDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// SetHeader définit un en-tête du message de sortie et reste dans le contexte du split
func (d *SplitDefinition) SetHeader(key string, value interface{}) *SplitDefinition {
	d.RouteBuilder.SetHeader(key, value)
	return d
}

// SetHeaders définit plusieurs en-têtes et reste dans le contexte du split
func (d *SplitDefinition) SetHeaders(headers map[string]any) *SplitDefinition {
	d.RouteBuilder.SetHeaders(headers)
	return d
}

// SetHeadersFunc définit plusieurs en-têtes via une fonction et reste dans le contexte du split
func (d *SplitDefinition) SetHeadersFunc(f func(*Exchange) (map[string]any, error)) *SplitDefinition {
	d.RouteBuilder.SetHeadersFunc(f)
	return d
}

// SetProperty définit une propriété de l'échange et reste dans le contexte du split
func (d *SplitDefinition) SetProperty(key string, value any) *SplitDefinition {
	d.RouteBuilder.SetProperty(key, value)
	return d
}

// SetPropertyFunc définit une propriété via une fonction et reste dans le contexte du split
func (d *SplitDefinition) SetPropertyFunc(key string, f func(*Exchange) (any, error)) *SplitDefinition {
	d.RouteBuilder.SetPropertyFunc(key, f)
	return d
}

// RemoveProperty supprime une propriété de l'échange et reste dans le contexte du split
func (d *SplitDefinition) RemoveProperty(key string) *SplitDefinition {
	d.RouteBuilder.RemoveProperty(key)
	return d
}

// RemoveProperties supprime les propriétés correspondant au pattern fourni et reste dans le contexte du split
func (d *SplitDefinition) RemoveProperties(pattern string, excludePatterns ...string) *SplitDefinition {
	d.RouteBuilder.RemoveProperties(pattern, excludePatterns...)
	return d
}

// RemoveHeader supprime un en-tête du message entrant et reste dans le contexte du split
func (d *SplitDefinition) RemoveHeader(name string) *SplitDefinition {
	d.RouteBuilder.RemoveHeader(name)
	return d
}

// RemoveHeaders supprime les en-têtes correspondants au pattern fourni et reste dans le contexte du split
func (d *SplitDefinition) RemoveHeaders(pattern string, excludePatterns ...string) *SplitDefinition {
	d.RouteBuilder.RemoveHeaders(pattern, excludePatterns...)
	return d
}

// Stop arrête le traitement de la partie actuelle et reste dans le contexte du split
func (d *SplitDefinition) Stop() *SplitDefinition {
	d.RouteBuilder.Stop()
	return d
}

// Aggregate ajoute un agrégateur et reste dans le contexte du split
func (d *SplitDefinition) Aggregate(aggregator *Aggregator) *SplitDefinition {
	d.RouteBuilder.Aggregate(aggregator)
	return d
}

// Log ajoute un log et reste dans le contexte du split
func (d *SplitDefinition) Log(message string) *SplitDefinition {
	d.RouteBuilder.Log(message)
	return d
}

// LogBody ajoute un log du corps et reste dans le contexte du split
func (d *SplitDefinition) LogBody(message string) *SplitDefinition {
	d.RouteBuilder.LogBody(message)
	return d
}

// LogHeaders ajoute un log des en-têtes et reste dans le contexte du split
func (d *SplitDefinition) LogHeaders(message string) *SplitDefinition {
	d.RouteBuilder.LogHeaders(message)
	return d
}

// End termine le bloc Split et revient au builder parent
func (d *SplitDefinition) End() *RouteBuilder {
	return d.parent
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

// Stop arrête le traitement de l'échange actuel
func (b *RouteBuilder) Stop() *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		return ErrStopRouting
	})
}

// To ajoute un endpoint de destination au conteneur actuel
func (b *RouteBuilder) To(uri string) *RouteBuilder {
	b.container.AddProcessor(createToProcessor(b.context, uri))
	return b
}
