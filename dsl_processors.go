package gocamel

import "fmt"

// This file centralises the small Processor factories used by every fluent
// DSL builder (RouteBuilder, SplitDefinition, MulticastDefinition,
// PipelineDefinition, ChoiceBuilder, OtherwiseBuilder, TryDefinition,
// CatchDefinition, FinallyDefinition, Route).
//
// Each builder needs its own type-preserving wrappers (Go does not let an
// embedded method return the embedding type), but the *body* of those
// wrappers — the construction of the underlying Processor — is the same
// across all of them. The factories below are that single source of truth.
//
// Adding a new DSL verb that mutates the exchange is a two-step process:
//   1. add a factory here (returns a Processor),
//   2. add the typed wrappers in each builder that should expose it.
//
// The factories never depend on a specific builder; they only read/write
// through the Exchange and Message API.

// setBodyProcessor returns a Processor that writes body to the Out message.
func setBodyProcessor(body any) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetBody(body)
		return nil
	})
}

// setHeaderProcessor returns a Processor that writes a single header to the
// Out message.
func setHeaderProcessor(key string, value any) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeader(key, value)
		return nil
	})
}

// setHeadersProcessor returns a Processor that writes multiple headers to
// the Out message.
func setHeadersProcessor(headers map[string]any) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetOut().SetHeaders(headers)
		return nil
	})
}

// setHeadersFuncProcessor returns a Processor that computes a map of headers
// per exchange and writes them to the Out message. The function's error is
// propagated up the route.
func setHeadersFuncProcessor(f func(*Exchange) (map[string]any, error)) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		headers, err := f(exchange)
		if err != nil {
			return err
		}
		exchange.GetOut().SetHeaders(headers)
		return nil
	})
}

// setPropertyProcessor returns a Processor that stores value at key in the
// exchange's Properties map.
func setPropertyProcessor(key string, value any) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.SetProperty(key, value)
		return nil
	})
}

// setPropertyFuncProcessor returns a Processor that computes a property
// value per exchange. The function's error is propagated up the route.
func setPropertyFuncProcessor(key string, f func(*Exchange) (any, error)) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		val, err := f(exchange)
		if err != nil {
			return err
		}
		exchange.SetProperty(key, val)
		return nil
	})
}

// removePropertyProcessor returns a Processor that deletes a single property.
func removePropertyProcessor(key string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.RemoveProperty(key)
		return nil
	})
}

// removePropertiesProcessor returns a Processor that deletes every property
// matching pattern except those matching one of the exclusion patterns. The
// Camel wildcard '*' is supported.
func removePropertiesProcessor(pattern string, excludePatterns []string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.RemoveProperties(pattern, excludePatterns...)
		return nil
	})
}

// removeHeaderProcessor returns a Processor that deletes a single header
// from the In message.
func removeHeaderProcessor(name string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetIn().RemoveHeader(name)
		return nil
	})
}

// removeHeadersProcessor returns a Processor that deletes every header on
// the In message matching pattern except those matching one of the exclusion
// patterns.
func removeHeadersProcessor(pattern string, excludePatterns []string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		exchange.GetIn().RemoveHeaders(pattern, excludePatterns...)
		return nil
	})
}

// logProcessor returns a Processor that logs a structured info entry tagged
// with the exchange pointer and the route ID.
func logProcessor(message, routeID string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		logInfo(message,
			"exchange_id", fmt.Sprintf("%p", exchange),
			"route_id", routeID,
		)
		return nil
	})
}

// logBodyProcessor returns a Processor that logs the In body alongside a
// structured message and the route ID.
func logBodyProcessor(message, routeID string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		logInfo(message,
			"body", exchange.GetIn().GetBody(),
			"route_id", routeID,
		)
		return nil
	})
}

// logHeadersProcessor returns a Processor that logs the In headers alongside
// a structured message and the route ID.
func logHeadersProcessor(message, routeID string) Processor {
	return ProcessorFunc(func(exchange *Exchange) error {
		logInfo(message,
			"headers", exchange.GetIn().GetHeaders(),
			"route_id", routeID,
		)
		return nil
	})
}

// stopProcessor returns the sentinel Processor that aborts route execution
// via ErrStopRouting without flagging it as a failure.
func stopProcessor() Processor {
	return ProcessorFunc(func(*Exchange) error {
		return ErrStopRouting
	})
}

