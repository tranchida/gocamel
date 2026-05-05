package gocamel

import (
	"context"
)

// Endpoint represents a communication endpoint in a route.
// It acts as a factory for Producers (sinks) and Consumers (sources).
// Each endpoint is created from a URI by its parent Component.
type Endpoint interface {
	// URI returns the endpoint URI that was used to create it.
	URI() string
	// CreateProducer creates a Producer for sending messages to this endpoint.
	CreateProducer() (Producer, error)
	// CreateConsumer creates a Consumer for reading messages from this endpoint.
	// The provided Processor is the route pipeline that will process each consumed exchange.
	CreateConsumer(processor Processor) (Consumer, error)
}

// Producer represents a message producer (sink).
// It sends Exchanges to an external system.
type Producer interface {
	// Start initializes the producer. Called once before sending messages.
	Start(ctx context.Context) error
	// Stop tears down the producer. Called after the route stops.
	Stop() error
	// Send sends the given Exchange to the external system.
	Send(exchange *Exchange) error
}

// Consumer represents a message consumer (source).
// It reads from an external system and pushes Exchanges into the route pipeline.
type Consumer interface {
	// Start begins consuming messages. The provided context controls the consumer lifetime.
	Start(ctx context.Context) error
	// Stop gracefully shuts down the consumer.
	Stop() error
}

// Component represents a Camel component that creates Endpoints from URIs.
// Each component is registered under a URI scheme (e.g., "http", "ftp", "file").
type Component interface {
	// CreateEndpoint creates a new Endpoint from the given URI.
	// The URI scheme is used to look up the component in the ComponentRegistry.
	CreateEndpoint(uri string) (Endpoint, error)
}
