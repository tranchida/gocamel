package gocamel

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CamelContext represents the main application context
type CamelContext struct {
	ctx           context.Context
	cancel        context.CancelFunc
	routes        []*Route
	routesLock    sync.RWMutex // Protects routes slice and routeCounter
	registry      *ComponentRegistry
	started       bool
	startLock     sync.RWMutex // Protects started lifecycle state
	routeCounter  int
	producerCache *ProducerCache
}

// NewCamelContext creates a new CamelContext instance
func NewCamelContext() *CamelContext {
	ctx, cancel := context.WithCancel(context.Background())
	c := &CamelContext{
		ctx:      ctx,
		cancel:   cancel,
		routes:   make([]*Route, 0),
		registry: NewComponentRegistry(),
	}
	c.producerCache = NewProducerCache(c)
	return c
}

// AddRoute adds a route to the context
func (c *CamelContext) AddRoute(route *Route) {
	c.routesLock.Lock()
	defer c.routesLock.Unlock()

	for _, r := range c.routes {
		if r == route {
			return // The route is already in the context
		}
	}

	if route.ID == "" {
		for {
			c.routeCounter++
			newID := fmt.Sprintf("route-%d", c.routeCounter)

			// Check if this ID is already in use
			idExists := false
			for _, r := range c.routes {
				if r.ID == newID {
					idExists = true
					break
				}
			}

			if !idExists {
				route.ID = newID
				break
			}
		}
	}

	c.routes = append(c.routes, route)
}

// AddRoutes adds multiple routes to the context
func (c *CamelContext) AddRoutes(routes ...*Route) {
	for _, route := range routes {
		c.AddRoute(route)
	}
}

// GetRoute retrieves a route by its ID
func (c *CamelContext) GetRoute(id string) *Route {
	c.routesLock.RLock()
	defer c.routesLock.RUnlock()

	for _, route := range c.routes {
		if route.ID == id {
			return route
		}
	}
	return nil
}

// GetRoutes retrieves all routes
func (c *CamelContext) GetRoutes() []*Route {
	c.routesLock.RLock()
	defer c.routesLock.RUnlock()

	out := make([]*Route, len(c.routes))
	copy(out, c.routes)
	return out
}

// Start starts the context and all its routes.
// If a route fails to start, previously started routes are stopped (rollback).
func (c *CamelContext) Start() error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if c.started {
		return fmt.Errorf("context is already started")
	}

	c.routesLock.RLock()
	routes := make([]*Route, len(c.routes))
	copy(routes, c.routes)
	c.routesLock.RUnlock()

	// Start all routes with rollback on failure
	for i, route := range routes {
		if err := route.Start(c.ctx); err != nil {
			// Rollback: stop already-started routes
			for j := i - 1; j >= 0; j-- {
				if stopErr := routes[j].Stop(); stopErr != nil {
					err = fmt.Errorf("%w; rollback stop error for route %s: %v", err, routes[j].ID, stopErr)
				}
			}
			return fmt.Errorf("error starting route %s: %w", route.ID, err)
		}
	}

	c.started = true
	return nil
}

// Stop stops the context and all its routes.
// All routes are stopped even if some fail; errors are collected and returned as a combined error.
//
// Stop has no deadline: if a consumer's Stop() blocks (e.g. an HTTP shutdown
// waiting on an idle keep-alive), Stop() blocks with it. Use GracefulStop
// to bound the total shutdown time.
func (c *CamelContext) Stop() error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if !c.started {
		return nil
	}

	c.routesLock.RLock()
	routes := make([]*Route, len(c.routes))
	copy(routes, c.routes)
	c.routesLock.RUnlock()

	var errs []error
	for _, route := range routes {
		if err := route.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", route.ID, err))
		}
	}

	c.cancel()
	c.started = false

	if err := c.producerCache.Stop(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors stopping routes: %v", errs)
	}
	return nil
}

// GracefulStop is like Stop but bounded by timeout. It cancels the context
// before stopping routes so consumers observing ctx.Done() unwind their work
// loops as early as possible, then waits up to timeout for every route to
// finish stopping. Routes still running after the deadline are abandoned and
// an error is returned so the caller can decide whether to escalate (e.g. log
// and exit).
func (c *CamelContext) GracefulStop(timeout time.Duration) error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if !c.started {
		return nil
	}

	// Cancel the parent context first so consumers using ctx.Done()
	// short-circuit their work loops immediately.
	c.cancel()

	c.routesLock.RLock()
	routes := make([]*Route, len(c.routes))
	copy(routes, c.routes)
	c.routesLock.RUnlock()

	done := make(chan error, 1)
	go func() {
		var errs []error
		for _, route := range routes {
			if err := route.Stop(); err != nil {
				errs = append(errs, fmt.Errorf("route %s: %w", route.ID, err))
			}
		}
		if len(errs) > 0 {
			done <- fmt.Errorf("errors stopping routes: %v", errs)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		c.started = false
		if cacheErr := c.producerCache.Stop(); cacheErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; producer cache stop error: %v", err, cacheErr)
			} else {
				err = cacheErr
			}
		}
		return err
	case <-time.After(timeout):
		// We mark the context as stopped to prevent re-entry, but the
		// background goroutine keeps running until the slow route(s) yield.
		c.started = false
		return fmt.Errorf("graceful stop timed out after %s; one or more routes are still shutting down", timeout)
	}
}

// IsStarted checks whether the context is started (uses RLock for read-only access).
func (c *CamelContext) IsStarted() bool {
	c.startLock.RLock()
	defer c.startLock.RUnlock()
	return c.started
}

// GetContext retrieves the underlying Go context
func (c *CamelContext) GetContext() context.Context {
	return c.ctx
}

// GetComponentRegistry retrieves the component registry
func (c *CamelContext) GetComponentRegistry() *ComponentRegistry {
	return c.registry
}

// CreateRoute creates a new route in this context
func (c *CamelContext) CreateRoute() *Route {
	route := NewRoute()
	route.context = c
	c.AddRoute(route)
	return route
}

// CreateRouteBuilder creates a new route builder
func (c *CamelContext) CreateRouteBuilder() *RouteBuilder {
	return NewRouteBuilder(c)
}

// AddComponent adds a component to the registry
func (c *CamelContext) AddComponent(name string, component Component) {
	c.registry.RegisterComponent(name, component)
}

// GetComponent retrieves a component by name
func (c *CamelContext) GetComponent(name string) (Component, error) {
	return c.registry.GetComponent(name)
}

// RemoveRoute removes a route from the context
func (c *CamelContext) RemoveRoute(route *Route) {
	c.routesLock.Lock()
	defer c.routesLock.Unlock()

	for i, r := range c.routes {
		if r == route {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
}

// RemoveRouteByID removes a route by its ID
func (c *CamelContext) RemoveRouteByID(id string) {
	c.routesLock.Lock()
	defer c.routesLock.Unlock()

	for i, route := range c.routes {
		if route.ID == id {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
}

// RemoveAllRoutes removes all routes
func (c *CamelContext) RemoveAllRoutes() {
	c.routesLock.Lock()
	defer c.routesLock.Unlock()

	c.routes = make([]*Route, 0)
}

// GetRouteCount returns the number of routes
func (c *CamelContext) GetRouteCount() int {
	c.routesLock.RLock()
	defer c.routesLock.RUnlock()

	return len(c.routes)
}

// GetStartedRouteCount returns the number of started routes
func (c *CamelContext) GetStartedRouteCount() int {
	c.routesLock.RLock()
	routes := make([]*Route, len(c.routes))
	copy(routes, c.routes)
	c.routesLock.RUnlock()

	count := 0
	for _, route := range routes {
		if route.IsStarted() {
			count++
		}
	}
	return count
}

// CreateEndpoint creates an endpoint from a URI
func (c *CamelContext) CreateEndpoint(uri string) (Endpoint, error) {
	return c.registry.CreateEndpoint(uri)
}

// GetProducer retrieves a started producer from the global cache, creating it if necessary.
func (c *CamelContext) GetProducer(uri string) (Producer, error) {
	return c.producerCache.Get(uri)
}

// ProducerCache caches Producers thread-safely to reuse them.
type ProducerCache struct {
	context *CamelContext
	cache   map[string]Producer
	mu      sync.RWMutex
}

// NewProducerCache creates a new ProducerCache.
func NewProducerCache(context *CamelContext) *ProducerCache {
	return &ProducerCache{
		context: context,
		cache:   make(map[string]Producer),
	}
}

// Get retrieves a started producer from the cache, creating and starting it if missing.
func (pc *ProducerCache) Get(uri string) (Producer, error) {
	pc.mu.RLock()
	prod, exists := pc.cache[uri]
	pc.mu.RUnlock()
	if exists {
		return prod, nil
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Double-checked locking
	prod, exists = pc.cache[uri]
	if exists {
		return prod, nil
	}

	endpoint, err := pc.context.CreateEndpoint(uri)
	if err != nil {
		return nil, fmt.Errorf("producer cache: endpoint creation failed for %s: %w", uri, err)
	}

	p, err := endpoint.CreateProducer()
	if err != nil {
		return nil, fmt.Errorf("producer cache: producer creation failed for %s: %w", uri, err)
	}

	if err := p.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("producer cache: producer start failed for %s: %w", uri, err)
	}

	pc.cache[uri] = p
	return p, nil
}

// Stop stops all cached producers.
func (pc *ProducerCache) Stop() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	var errs []error
	for uri, prod := range pc.cache {
		if err := prod.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("producer cache: error stopping producer for %s: %w", uri, err))
		}
	}
	pc.cache = make(map[string]Producer) // clear

	if len(errs) > 0 {
		return fmt.Errorf("producer cache: errors stopping producers: %v", errs)
	}
	return nil
}
