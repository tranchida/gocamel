package gocamel

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

// Build finalise la construction de la route
func (b *RouteBuilder) Build() *Route {
	return b.route
}
