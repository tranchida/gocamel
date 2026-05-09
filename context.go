package gocamel

import (
	"context"
	"fmt"
	"sync"
)

// CamelContext represents the main application context
type CamelContext struct {
	ctx          context.Context
	cancel       context.CancelFunc
	routes       []*Route
	registry     *ComponentRegistry
	started      bool
	startLock    sync.RWMutex
	routeCounter int
}

// NewCamelContext creates a new CamelContext instance
func NewCamelContext() *CamelContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &CamelContext{
		ctx:      ctx,
		cancel:   cancel,
		routes:   make([]*Route, 0),
		registry: NewComponentRegistry(),
	}
}

// AddRoute adds a route to the context
func (c *CamelContext) AddRoute(route *Route) {
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
	for _, route := range c.routes {
		if route.ID == id {
			return route
		}
	}
	return nil
}

// GetRoutes retrieves all routes
func (c *CamelContext) GetRoutes() []*Route {
	return c.routes
}

// Start starts the context and all its routes.
// If a route fails to start, previously started routes are stopped (rollback).
func (c *CamelContext) Start() error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if c.started {
		return fmt.Errorf("context is already started")
	}

	// Start all routes with rollback on failure
	for i, route := range c.routes {
		if err := route.Start(c.ctx); err != nil {
			// Rollback: stop already-started routes
			for j := i - 1; j >= 0; j-- {
				if stopErr := c.routes[j].Stop(); stopErr != nil {
					err = fmt.Errorf("%w; rollback stop error for route %s: %v", err, c.routes[j].ID, stopErr)
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
func (c *CamelContext) Stop() error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if !c.started {
		return nil
	}

	// Stop all routes, collecting errors
	var errs []error
	for _, route := range c.routes {
		if err := route.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", route.ID, err))
		}
	}

	c.cancel()
	c.started = false

	if len(errs) > 0 {
		return fmt.Errorf("errors stopping routes: %v", errs)
	}
	return nil
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
	for i, r := range c.routes {
		if r == route {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
}

// RemoveRouteByID removes a route by its ID
func (c *CamelContext) RemoveRouteByID(id string) {
	for i, route := range c.routes {
		if route.ID == id {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
}

// RemoveAllRoutes removes all routes
func (c *CamelContext) RemoveAllRoutes() {
	c.routes = make([]*Route, 0)
}

// GetRouteCount returns the number of routes
func (c *CamelContext) GetRouteCount() int {
	return len(c.routes)
}

// GetStartedRouteCount returns the number of started routes
func (c *CamelContext) GetStartedRouteCount() int {
	count := 0
	for _, route := range c.routes {
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
