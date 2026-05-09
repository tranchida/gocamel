package gocamel

import (
	"fmt"
	"log/slog"
)

// DynamicRouter implements the Dynamic Router EIP.
// It routes the exchange through a sequence of endpoints resolved dynamically
// by calling a control function after each step. Routing ends when the
// control function returns an empty string or nil.
type DynamicRouter struct {
	context    *CamelContext
	controlFn  func(*Exchange) (string, error)
	expression string
}

// NewDynamicRouter creates a new DynamicRouter instance.
func NewDynamicRouter(context *CamelContext, controlFn func(*Exchange) (string, error)) *DynamicRouter {
	return &DynamicRouter{
		context:   context,
		controlFn: controlFn,
	}
}

// SetSimpleExpression sets a Simple Language expression for dynamic routing.
func (p *DynamicRouter) SetSimpleExpression(expression string) *DynamicRouter {
	p.expression = expression
	return p
}

// Process executes the dynamic router logic.
func (p *DynamicRouter) Process(exchange *Exchange) error {
	for {
		uri, err := p.resolveNextURI(exchange)
		if err != nil {
			return fmt.Errorf("dynamic router: error resolving next URI: %w", err)
		}

		if uri == "" {
			slog.Debug("dynamic router: no more endpoints, routing ends")
			break
		}

		slog.Debug("dynamic router: sending to next endpoint", "uri", uri)
		processor := createToProcessor(p.context, uri)
		if err := processor.Process(exchange); err != nil {
			return fmt.Errorf("dynamic router: error sending to %s: %w", uri, err)
		}

		// Prepare exchange for next step
		exchange.PropagateOutToIn()
		exchange.Out = NewMessage()
	}

	return nil
}

func (p *DynamicRouter) resolveNextURI(exchange *Exchange) (string, error) {
	if p.controlFn != nil {
		return p.controlFn(exchange)
	}

	if p.expression != "" {
		result, err := evaluateSimpleExpression(p.expression, exchange)
		if err != nil {
			return "", err
		}
		return Interpolate(result, exchange), nil
	}

	return "", nil
}

// NewSimpleDynamicRouter creates a new DynamicRouter using a Simple Language expression.
func NewSimpleDynamicRouter(context *CamelContext, expression string) *DynamicRouter {
	return &DynamicRouter{
		context:    context,
		expression: expression,
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
