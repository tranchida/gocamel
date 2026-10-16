package gocamel

import (
	"errors"
	"fmt"
)

// RoutingSlip implements the Routing Slip EIP.
// It routes the same exchange through a dynamically resolved sequence of URIs.
// Each step can modify the exchange, and Out is propagated to In before the
// next step is invoked.
type RoutingSlip struct {
	StaticURIs       string
	SimpleExpression string
	URIFunc          func(*Exchange) ([]string, error)

	context *CamelContext
}

// NewRoutingSlip creates a new RoutingSlip instance.
func NewRoutingSlip(context *CamelContext) *RoutingSlip {
	return &RoutingSlip{
		context: context,
	}
}

// SetStaticURIs sets a comma-separated routing slip.
func (r *RoutingSlip) SetStaticURIs(uris string) *RoutingSlip {
	r.StaticURIs = uris
	return r
}

// SetSimpleExpression sets a Simple Language expression for routing slip resolution.
func (r *RoutingSlip) SetSimpleExpression(expression string) *RoutingSlip {
	r.SimpleExpression = expression
	return r
}

// SetURIFunc sets a Go function for routing slip resolution.
func (r *RoutingSlip) SetURIFunc(f func(*Exchange) ([]string, error)) *RoutingSlip {
	r.URIFunc = f
	return r
}

func (r *RoutingSlip) resolveURIs(exchange *Exchange) ([]string, error) {
	if r.URIFunc != nil {
		uris, err := r.URIFunc(exchange)
		if err != nil {
			return nil, fmt.Errorf("routing slip URI function error: %w", err)
		}
		LogDebug("routing slip resolved URIs via function", "count", len(uris), "uris", uris)
		return uris, nil
	}

	if r.SimpleExpression != "" {
		resolved, err := evaluateSimpleExpression(r.SimpleExpression, exchange)
		if err != nil {
			return nil, fmt.Errorf("routing slip simple expression error: %w", err)
		}
		uris := splitAndTrim(resolved)
		LogDebug("routing slip resolved URIs via simple expression",
			"expression", r.SimpleExpression,
			"count", len(uris),
			"uris", uris,
		)
		return uris, nil
	}

	if r.StaticURIs != "" {
		rawList := splitAndTrim(r.StaticURIs)
		uris := make([]string, 0, len(rawList))
		for _, raw := range rawList {
			uris = append(uris, InterpolateURI(raw, exchange))
		}
		LogDebug("routing slip resolved URIs via static string", "count", len(uris), "uris", uris)
		return uris, nil
	}

	return nil, nil
}

// Process executes the routing slip on the provided exchange.
func (r *RoutingSlip) Process(exchange *Exchange) error {
	uris, err := r.resolveURIs(exchange)
	if err != nil {
		return err
	}
	if len(uris) == 0 {
		LogDebug("routing slip resolved to empty list, skipping")
		return nil
	}

	for i, uri := range uris {
		exchange.SetProperty(CamelRoutingSlipIndex, i)
		exchange.SetProperty(CamelRoutingSlipSize, len(uris))
		exchange.SetProperty(CamelRoutingSlipComplete, i == len(uris)-1)

		// Resolve a started producer from the CamelContext producer cache
		// (created once per URI, reused across messages, stopped with the
		// context) instead of constructing a new lazyProducer per message, which
		// leaked a producer (and its connection) on every slip step.
		producer, release, err := r.context.acquireProducer(uri)
		if err != nil {
			return fmt.Errorf("routing slip producer error for %s: %w", uri, err)
		}
		propagateOutToInAndReset(exchange)
		err = producer.Send(exchange)
		release()
		if err != nil {
			if errors.Is(err, ErrStopRouting) {
				return err
			}
			return fmt.Errorf("routing slip error sending to %s: %w", uri, err)
		}
		propagateOutToInAndReset(exchange)
	}

	return nil
}

func propagateOutToInAndReset(exchange *Exchange) {
	if exchange.Out.Body != nil || len(exchange.Out.Headers) > 0 {
		exchange.PropagateOutToIn()
		exchange.Out = NewMessage()
	}
}
