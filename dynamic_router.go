package gocamel

import (
	"fmt"
)

const dynamicRouterMaxIterations = 1000

// DynamicRouter implements the Dynamic Router EIP.
// It routes the exchange through a sequence of endpoints resolved dynamically
// by calling a control function after each step. Routing ends when the
// control function returns an empty string or nil.
type DynamicRouter struct {
	context       *CamelContext
	controlFn     func(*Exchange) (string, error)
	expression    string
	maxIterations int
}

// NewDynamicRouter creates a new DynamicRouter instance.
func NewDynamicRouter(context *CamelContext, controlFn func(*Exchange) (string, error)) *DynamicRouter {
	return &DynamicRouter{
		context:       context,
		controlFn:     controlFn,
		maxIterations: dynamicRouterMaxIterations,
	}
}

// SetSimpleExpression sets a Simple Language expression for dynamic routing.
func (p *DynamicRouter) SetSimpleExpression(expression string) *DynamicRouter {
	p.expression = expression
	return p
}

// Process executes the dynamic router logic.
func (p *DynamicRouter) Process(exchange *Exchange) error {
	limit := p.maxIterations
	if limit <= 0 {
		limit = dynamicRouterMaxIterations
	}

	for i := 0; i < limit; i++ {
		uri, err := p.resolveNextURI(exchange)
		if err != nil {
			return fmt.Errorf("dynamic router: error resolving next URI: %w", err)
		}

		if uri == "" {
			LogDebug("dynamic router: no more endpoints, routing ends")
			return nil
		}

		LogDebug("dynamic router: sending to next endpoint", "uri", uri)
		// Reuse the started producer from the CamelContext cache (created once
		// per URI, stopped with the context) instead of creating and tearing
		// down a producer — and potentially a connection — on every step of
		// every message. Mirrors ToD and WireTap.
		propagateOutToInAndReset(exchange)
		if err := p.context.sendToURI(uri, exchange); err != nil {
			return fmt.Errorf("dynamic router: error sending to %s: %w", uri, err)
		}

		// The step's response becomes the input of the next step (and of the
		// control function deciding it). Resetting Out without propagating it
		// discarded every endpoint's reply.
		propagateOutToInAndReset(exchange)
	}

	return fmt.Errorf("dynamic router: exceeded maximum of %d iterations", limit)
}

func (p *DynamicRouter) resolveNextURI(exchange *Exchange) (string, error) {
	if p.controlFn != nil {
		return p.controlFn(exchange)
	}

	if p.expression != "" {
		return evaluateSimpleExpression(p.expression, exchange)
	}

	return "", nil
}

// NewSimpleDynamicRouter creates a new DynamicRouter using a Simple Language expression.
func NewSimpleDynamicRouter(context *CamelContext, expression string) *DynamicRouter {
	return &DynamicRouter{
		context:       context,
		expression:    expression,
		maxIterations: dynamicRouterMaxIterations,
	}
}

// DynamicRouter adds a Dynamic Router EIP step.
// The expression can be a func(*Exchange) (string, error) or a Simple Language expression.
func (b *RouteBuilder) DynamicRouter(expression any) *RouteBuilder {
	var dr *DynamicRouter

	switch expr := expression.(type) {
	case func(*Exchange) (string, error):
		dr = NewDynamicRouter(b.context, expr)
	case string:
		dr = NewSimpleDynamicRouter(b.context, expr)
	default:
		b.err = fmt.Errorf("DynamicRouter: unsupported expression type %T (want func(*Exchange)(string,error) or string)", expression)
		return b
	}

	b.container.AddProcessor(dr)
	return b
}
