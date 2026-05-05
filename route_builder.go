package gocamel

import (
	"fmt"
	"strings"
)

// RouteBuilder provides a fluent DSL for constructing Camel routes.
// It wraps a Route and delegates method calls to build the processor pipeline.
// Errors during construction are accumulated and surfaced at Build() time.
type RouteBuilder struct {
	context   *CamelContext
	route     *Route
	container ProcessorContainer // Current processor container (route or nested EIP).
	err       error              // accumulated error during DSL construction.
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

// ProcessRef ajoute un processeur référencé par son nom dans le registre
func (b *RouteBuilder) ProcessRef(name string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		val, exists := b.context.GetComponentRegistry().Lookup(name)
		if !exists {
			return fmt.Errorf("processeur non trouvé dans le registre: %s", name)
		}
		processor, ok := val.(Processor)
		if !ok {
			return fmt.Errorf("l'objet trouvé n'est pas un processeur: %s", name)
		}
		return processor.Process(exchange)
	})
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

// Transacted active le mode transactionnel for la route.
// In GoCamel, cela garantit que les synchronisations de l'Exchange (comme la consommation du message source)
// sont exécutées with le statut approprié à la fin de la route.
func (b *RouteBuilder) Transacted() *RouteBuilder {
	b.route.SetTransacted(true)
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

// SplitDefinition is a fluent sub-DSL for configuring the Split EIP.
// It wraps an inner RouteBuilder whose container is the Splitter,
// allowing .Process(), .To(), etc. on each split part before calling .End().
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

// To ajoute un ou plusieurs endpoints de destination au conteneur actuel et reste dans le contexte du split
func (d *SplitDefinition) To(uris ...string) *SplitDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD ajoute un ou plusieurs endpoints dynamiques de destination et reste dans le contexte du split
func (d *SplitDefinition) ToD(uriTemplates ...string) *SplitDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// RecipientList adds a Recipient List EIP that sends each split part to dynamic recipients.
func (d *SplitDefinition) RecipientList(expression any) *SplitDefinition {
	rl := NewRecipientList(d.context)
	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	}
	d.container.AddProcessor(rl)
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

// WireTap ajoute un wiretap et reste dans le contexte du split
func (d *SplitDefinition) WireTap(uri string) *SplitDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End termine le bloc Split et revient au builder parent
func (d *SplitDefinition) End() *RouteBuilder {
	return d.parent
}

// Log ajoute un processeur qui log le message (structuré)
func (b *RouteBuilder) Log(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		logInfo(message,
			"exchange_id", fmt.Sprintf("%p", exchange),
			"route_id", b.route.ID,
		)
		return nil
	})
}

// LogBody ajoute un processeur qui log le corps du message (structuré)
func (b *RouteBuilder) LogBody(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		logInfo(message,
			"body", exchange.GetIn().GetBody(),
			"route_id", b.route.ID,
		)
		return nil
	})
}

// LogHeaders ajoute un processeur qui log les en-têtes du message (structuré)
func (b *RouteBuilder) LogHeaders(message string) *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		logInfo(message,
			"headers", exchange.GetIn().GetHeaders(),
			"route_id", b.route.ID,
		)
		return nil
	})
}

// LogSimple ajoute un processeur qui log un message évalué via le Simple Language
func (b *RouteBuilder) LogSimple(expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression for log: %w", err)
		return b
	}
	return b.ProcessFunc(func(exchange *Exchange) error {
		result, err := template.EvaluateAsString(exchange)
		if err != nil {
			logError("Error evaluating log expression",
				"error", err,
				"expression", expression,
				"route_id", b.route.ID,
			)
			return nil // Don't fail the exchange just because logging failed
		}
		logInfo("LogSimple", "message", result, "route_id", b.route.ID)
		return nil
	})
}

// EnableMetrics active la collecte des métriques Prometheus pour la route en cours
func (b *RouteBuilder) EnableMetrics() *RouteBuilder {
	b.route.EnableMetrics()
	return b
}

// EnableTracing active le traçage OpenTelemetry pour la route en cours
func (b *RouteBuilder) EnableTracing() *RouteBuilder {
	b.route.EnableTracing()
	return b
}

// ErrorHandler configures a custom ErrorHandler for this route.
// This is a terminal DSL operation — it sets the handler on the route
// being built. Multiple calls will overwrite previous settings.
//
// Example:
//   route, err := ctx.CreateRouteBuilder().
//       From("direct:start").
//       ErrorHandler(NewDeadLetterChannel(ctx, "log:errors", MaxRedeliveries(3))).
//       ProcessFunc(myProcessor).
//       Build()
func (b *RouteBuilder) ErrorHandler(handler ErrorHandler) *RouteBuilder {
	b.route.SetErrorHandler(handler)
	return b
}

// DeadLetterChannel configures a DeadLetterChannel error handler for this route.
// This is a convenience method equivalent to:
//   ErrorHandler(NewDeadLetterChannel(b.context, deadLetterURI, opts...))
//
// Example:
//   route, err := ctx.CreateRouteBuilder().
//       From("direct:start").
//       DeadLetterChannel("file:errors", MaxRedeliveries(3), RedeliveryDelay(2*time.Second)).
//       ProcessFunc(myProcessor).
//       Build()
func (b *RouteBuilder) DeadLetterChannel(deadLetterURI string, opts ...DeadLetterChannelOption) *RouteBuilder {
	dlc := NewDeadLetterChannel(b.context, deadLetterURI, opts...)
	b.route.SetErrorHandler(dlc)
	return b
}

// Build finalise la construction de la route.
// Returns the route or nil if an error occurred during DSL construction.
func (b *RouteBuilder) Build() (*Route, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.route, nil
}

// SimpleSetBody sets the body using a Simple Language expression
func (b *RouteBuilder) SimpleSetBody(expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return b
	}

	b.route.AddProcessor(&SimpleLanguageProcessor{Template: template})
	return b
}

// SimpleSetHeader sets a header using a Simple Language expression
func (b *RouteBuilder) SimpleSetHeader(headerName string, expression string) *RouteBuilder {
	template, err := ParseSimpleTemplate(expression)
	if err != nil {
		b.err = fmt.Errorf("failed to parse simple expression: %w", err)
		return b
	}

	b.route.AddProcessor(&SimpleSetHeaderProcessor{
		HeaderName: headerName,
		Expression: template,
	})
	return b
}

// Stop arrête le traitement de l'échange actuel
func (b *RouteBuilder) Stop() *RouteBuilder {
	return b.ProcessFunc(func(exchange *Exchange) error {
		return ErrStopRouting
	})
}

// To ajoute un ou plusieurs endpoints de destination au conteneur actuel.
// Si plusieurs URIs sont fournies, un Multicast est créé automatiquement.
func (b *RouteBuilder) To(uris ...string) *RouteBuilder {
	if len(uris) == 0 {
		return b
	}
	if len(uris) == 1 {
		b.container.AddProcessor(createToProcessor(b.context, uris[0]))
	} else {
		m := NewMulticast()
		for _, uri := range uris {
			m.AddProcessor(createToProcessor(b.context, uri))
		}
		b.container.AddProcessor(m)
	}
	return b
}

// ToD ajoute un ou plusieurs endpoints dynamiques de destination au conteneur actuel.
// Si plusieurs templates sont fournis, un Multicast est créé automatiquement.
func (b *RouteBuilder) ToD(uriTemplates ...string) *RouteBuilder {
	if len(uriTemplates) == 0 {
		return b
	}
	if len(uriTemplates) == 1 {
		b.container.AddProcessor(createToDProcessor(b.context, uriTemplates[0]))
	} else {
		m := NewMulticast()
		for _, uriTemplate := range uriTemplates {
			m.AddProcessor(createToDProcessor(b.context, uriTemplate))
		}
		b.container.AddProcessor(m)
	}
	return b
}

// WireTap ajoute un processeur WireTap qui envoie une copie asynchrone
// de l'échange vers l'endpoint spécifié sans bloquer la route principale.
// La copie est indépendante : les modifications sur l'échange tapé n'affectent
// pas la route principale et vice-versa.
//
// Exemple:
//
//	route, err := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    WireTap("direct:audit").
//	    To("direct:result").
//	    Build()
func (b *RouteBuilder) WireTap(uri string) *RouteBuilder {
	b.container.AddProcessor(NewWireTap(b.context, uri))
	return b
}

// Multicast commence un bloc Multicast EIP
func (b *RouteBuilder) Multicast() *MulticastDefinition {
	m := NewMulticast()
	b.container.AddProcessor(m)

	return &MulticastDefinition{
		RouteBuilder: &RouteBuilder{
			context:   b.context,
			route:     b.route,
			container: m,
		},
		parent:    b,
		multicast: m,
	}
}

// RecipientList adds a Recipient List EIP processor that sends the exchange
// to a list of recipients resolved dynamically at runtime.
//
// The expression can be:
//   - A string of comma-separated URIs (e.g. "direct:a,direct:b")
//   - A Simple Language expression (e.g. "${header.recipients}")
//   - A Go function returning []string
//
// Example:
//
//	// Static comma-separated list
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    RecipientList("direct:a,direct:b").
//	    Build()
//
//	// Simple Language expression
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    RecipientList("${header.recipientList}").
//	    Build()
//
//	// Go function
//	route, _ := ctx.CreateRouteBuilder().
//	    From("direct:start").
//	    RecipientList(func(e *Exchange) ([]string, error) {
//	        return []string{"direct:a", "direct:b"}, nil
//	    }).
//	    Build()
func (b *RouteBuilder) RecipientList(expression any) *RecipientListDefinition {
	rl := NewRecipientList(b.context)

	switch expr := expression.(type) {
	case string:
		// Heuristic: if it contains "${" treat as Simple Language expression
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	default:
		b.err = fmt.Errorf("unsupported RecipientList expression type: %T", expression)
		return &RecipientListDefinition{
			parent:        b,
			recipientList: rl,
		}
	}

	b.container.AddProcessor(rl)

	return &RecipientListDefinition{
		parent:        b,
		recipientList: rl,
	}
}

// RecipientListDefinition is a fluent sub-DSL for configuring the Recipient List EIP.
// Supports .ParallelProcessing(), .AggregationStrategy(), and .End() to return
// to the parent builder.
type RecipientListDefinition struct {
	parent        *RouteBuilder
	recipientList *RecipientList
}

// AggregationStrategy définit la stratégie d'agrégation pour la recipient list
func (d *RecipientListDefinition) AggregationStrategy(strategy AggregationStrategy) *RecipientListDefinition {
	d.recipientList.SetAggregationStrategy(strategy)
	return d
}

// ParallelProcessing active ou désactive le traitement parallèle
func (d *RecipientListDefinition) ParallelProcessing() *RecipientListDefinition {
	d.recipientList.SetParallelProcessing(true)
	return d
}

// End termine le bloc RecipientList et revient au builder parent
func (d *RecipientListDefinition) End() *RouteBuilder {
	return d.parent
}

// MulticastDefinition is a fluent sub-DSL for configuring the Multicast EIP.
// Supports .ParallelProcessing(), .AggregationStrategy(), .Pipeline() for
// grouping processors, and .End() to return to the parent builder.
type MulticastDefinition struct {
	*RouteBuilder
	parent    *RouteBuilder
	multicast *Multicast
}

// AggregationStrategy définit la stratégie d'agrégation pour le multicast
func (d *MulticastDefinition) AggregationStrategy(strategy AggregationStrategy) *MulticastDefinition {
	d.multicast.SetAggregationStrategy(strategy)
	return d
}

// ParallelProcessing active ou désactive le traitement parallèle
func (d *MulticastDefinition) ParallelProcessing() *MulticastDefinition {
	d.multicast.SetParallelProcessing(true)
	return d
}

// Pipeline commence un bloc Pipeline pour grouper des processeurs dans une branche de multicast
func (d *MulticastDefinition) Pipeline() *PipelineDefinition {
	p := NewPipeline()
	d.multicast.AddProcessor(p)

	return &PipelineDefinition{
		RouteBuilder: &RouteBuilder{
			context:   d.context,
			route:     d.route,
			container: p,
		},
		parent: d,
	}
}

// Process ajoute un processeur au conteneur actuel et reste dans le contexte du multicast
func (d *MulticastDefinition) Process(processor Processor) *MulticastDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc ajoute une fonction de traitement au conteneur actuel et reste dans le contexte du multicast
func (d *MulticastDefinition) ProcessFunc(f func(*Exchange) error) *MulticastDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To ajoute un ou plusieurs endpoints de destination au conteneur actuel et reste dans le contexte du multicast
func (d *MulticastDefinition) To(uris ...string) *MulticastDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD ajoute un ou plusieurs endpoints dynamiques de destination et reste dans le contexte du multicast
func (d *MulticastDefinition) ToD(uriTemplates ...string) *MulticastDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// RecipientList sends to dynamically resolved recipients within this multicast branch.
func (d *MulticastDefinition) RecipientList(expression any) *MulticastDefinition {
	rl := NewRecipientList(d.context)
	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	}
	d.container.AddProcessor(rl)
	return d
}

// SetBody définit le corps du message de sortie et reste dans le contexte du multicast
func (d *MulticastDefinition) SetBody(body interface{}) *MulticastDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// SetHeader définit un en-tête du message de sortie et reste dans le contexte du multicast
func (d *MulticastDefinition) SetHeader(key string, value interface{}) *MulticastDefinition {
	d.RouteBuilder.SetHeader(key, value)
	return d
}

// WireTap ajoute un wiretap et reste dans le contexte du multicast
func (d *MulticastDefinition) WireTap(uri string) *MulticastDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End termine le bloc Multicast et revient au builder parent
func (d *MulticastDefinition) End() *RouteBuilder {
	return d.parent
}

// PipelineDefinition is a fluent sub-DSL for grouping processors inside a
// multicast branch. Call .End() to return to the MulticastDefinition.
type PipelineDefinition struct {
	*RouteBuilder
	parent *MulticastDefinition
}

// Process ajoute un processeur au conteneur actuel et reste dans le contexte du pipeline
func (d *PipelineDefinition) Process(processor Processor) *PipelineDefinition {
	d.RouteBuilder.Process(processor)
	return d
}

// ProcessFunc ajoute une fonction de traitement au conteneur actuel et reste dans le contexte du pipeline
func (d *PipelineDefinition) ProcessFunc(f func(*Exchange) error) *PipelineDefinition {
	d.RouteBuilder.ProcessFunc(f)
	return d
}

// To ajoute un ou plusieurs endpoints de destination au conteneur actuel et reste dans le contexte du pipeline
func (d *PipelineDefinition) To(uris ...string) *PipelineDefinition {
	d.RouteBuilder.To(uris...)
	return d
}

// ToD ajoute un ou plusieurs endpoints dynamiques de destination et reste dans le contexte du pipeline
func (d *PipelineDefinition) ToD(uriTemplates ...string) *PipelineDefinition {
	d.RouteBuilder.ToD(uriTemplates...)
	return d
}

// RecipientList sends to dynamically resolved recipients within this pipeline branch.
func (d *PipelineDefinition) RecipientList(expression any) *PipelineDefinition {
	rl := NewRecipientList(d.context)
	switch expr := expression.(type) {
	case string:
		if strings.Contains(expr, "${") {
			rl.SetSimpleExpression(expr)
		} else {
			rl.SetStaticURIs(expr)
		}
	case func(*Exchange) ([]string, error):
		rl.SetURIFunc(expr)
	}
	d.container.AddProcessor(rl)
	return d
}

// SetBody définit le corps du message de sortie et reste dans le contexte du pipeline
func (d *PipelineDefinition) SetBody(body interface{}) *PipelineDefinition {
	d.RouteBuilder.SetBody(body)
	return d
}

// WireTap ajoute un wiretap et reste dans le contexte du pipeline
func (d *PipelineDefinition) WireTap(uri string) *PipelineDefinition {
	d.RouteBuilder.WireTap(uri)
	return d
}

// End termine le bloc Pipeline et revient au builder multicast
func (d *PipelineDefinition) End() *MulticastDefinition {
	return d.parent
}

