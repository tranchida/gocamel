package gocamel

import (
	"context"
	"errors"
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
	starting      bool         // guarded by startLock
	stopping      bool         // true while Stop/GracefulStop is still tearing down
	startLock     sync.RWMutex // Protects lifecycle state, ctx and cancel
	routeCounter  int
	usedRouteIDs  map[string]bool // ids already assigned; guarded by routesLock
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
	c.registerStdlibComponents()
	return c
}

// registerStdlibComponents installs the zero-dependency components that live
// in the core module so From("direct:…") / From("timer:…") work without an
// explicit AddComponent. Callers can still AddComponent to replace a default.
func (c *CamelContext) registerStdlibComponents() {
	c.AddComponent("direct", NewDirectComponent())
	c.AddComponent("seda", NewSedaComponent())
	c.AddComponent("chan", NewChanComponent())
	c.AddComponent("timer", NewTimerComponent())
	httpComp := NewHTTPComponent()
	c.AddComponent("http", httpComp)
	c.AddComponent("https", httpComp)
	c.AddComponent("exec", NewExecComponent())
	c.AddComponent("template", NewTemplateComponent())
	c.AddComponent("sql", NewSQLComponent())
	c.AddComponent("sql-stored", NewSQLStoredComponent())
	c.AddComponent("net", NewNetComponent())
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

	if c.usedRouteIDs == nil {
		c.usedRouteIDs = make(map[string]bool, len(c.routes)+1)
	}
	if route.ID == "" {
		// Collision check against a set rather than a linear scan of every
		// route per candidate: generating N ids was O(N^2).
		for {
			c.routeCounter++
			newID := fmt.Sprintf("route-%d", c.routeCounter)
			if !c.usedRouteIDs[newID] {
				route.ID = newID
				break
			}
		}
	} else if c.usedRouteIDs[route.ID] {
		LogWarn("duplicate route ID; GetRoute returns the first route with this ID", "id", route.ID)
	}

	c.usedRouteIDs[route.ID] = true
	c.routes = append(c.routes, route)
}

// reassignRouteID updates a route's ID and the usedRouteIDs set. Called from
// Route.SetID so a later auto-generated ID can reuse the previous value.
func (c *CamelContext) reassignRouteID(route *Route, newID string) {
	c.routesLock.Lock()
	defer c.routesLock.Unlock()
	old := route.ID
	if old == newID {
		return
	}
	if c.usedRouteIDs == nil {
		c.usedRouteIDs = make(map[string]bool)
	}
	if old != "" {
		delete(c.usedRouteIDs, old)
	}
	if newID != "" && c.usedRouteIDs[newID] {
		LogWarn("duplicate route ID; GetRoute returns the first route with this ID", "id", newID)
	}
	route.ID = newID
	if newID != "" {
		c.usedRouteIDs[newID] = true
	}
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
func (c *CamelContext) Start() (err error) {
	c.startLock.Lock()
	if c.stopping {
		c.startLock.Unlock()
		return fmt.Errorf("context is still stopping")
	}
	if c.started || c.starting {
		c.startLock.Unlock()
		return fmt.Errorf("context is already started or changing lifecycle state")
	}
	c.starting = true
	ctx := c.ctx
	c.startLock.Unlock()
	defer func() {
		c.startLock.Lock()
		c.starting = false
		c.started = err == nil
		c.startLock.Unlock()
	}()
	c.producerCache.Resume()
	routes := c.GetRoutes()
	for i, route := range routes {
		if err := route.Start(ctx); err != nil {
			errs := []error{fmt.Errorf("error starting route %s: %w", route.ID, err)}
			for j := i - 1; j >= 0; j-- {
				errs = append(errs, routes[j].Stop())
			}
			errs = append(errs, c.producerCache.Stop())
			return errors.Join(errs...)
		}
	}
	return nil
}

// beginStop reserves the shutdown transition without holding a lock while
// consumers call application code or wait for their workers.
func (c *CamelContext) beginStop() (bool, error) {
	c.startLock.Lock()
	defer c.startLock.Unlock()
	if c.starting || c.stopping {
		return false, fmt.Errorf("context is changing lifecycle state")
	}
	if !c.started {
		return false, nil
	}
	c.stopping = true
	return true, nil
}

func (c *CamelContext) stopResources() error {
	var errs []error
	for _, route := range c.GetRoutes() {
		if err := route.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("route %s: %w", route.ID, err))
		}
	}
	if err := c.producerCache.Stop(); err != nil {
		errs = append(errs, err)
	}
	c.startLock.Lock()
	c.cancel()
	c.started = false
	c.resetContext()
	c.stopping = false
	c.startLock.Unlock()
	return errors.Join(errs...)
}

// Stop stops all routes and cached producers. Lifecycle queries remain usable
// from application processors during shutdown. Use GracefulStop to bound waiting.
func (c *CamelContext) Stop() error {
	stop, err := c.beginStop()
	if err != nil || !stop {
		return err
	}
	return c.stopResources()
}

// GracefulStop cancels the parent context and bounds the wait for shutdown.
// A timed-out shutdown continues in the background; Start rejects a restart
// until every resource has finished stopping.
func (c *CamelContext) GracefulStop(timeout time.Duration) error {
	stop, err := c.beginStop()
	if err != nil || !stop {
		return err
	}
	c.startLock.RLock()
	c.cancel()
	c.startLock.RUnlock()
	done := make(chan error, 1)
	go func() { done <- c.stopResources() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		c.startLock.Lock()
		if c.stopping {
			c.started = false
		}
		c.startLock.Unlock()
		return fmt.Errorf("graceful stop timed out after %s; one or more routes are still shutting down", timeout)
	}
}

// IsStarted checks whether the context is started (uses RLock for read-only access).
func (c *CamelContext) IsStarted() bool {
	c.startLock.RLock()
	defer c.startLock.RUnlock()
	return c.started
}

// resetContext replaces the cancelled root context so a later Start() observes
// a live ctx. Caller must hold startLock.
func (c *CamelContext) resetContext() {
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	c.cancel = cancel
	if c.producerCache != nil {
		c.producerCache.Resume()
	}
}

// GetContext retrieves the underlying Go context
func (c *CamelContext) GetContext() context.Context {
	c.startLock.RLock()
	defer c.startLock.RUnlock()
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

// RemoveRoute stops the route if it is running, then drops it from the context.
// The route ID is released so a later auto-generated ID can reuse it.
func (c *CamelContext) RemoveRoute(route *Route) error {
	if route == nil {
		return nil
	}
	c.startLock.Lock()
	if c.starting || c.stopping {
		c.startLock.Unlock()
		return fmt.Errorf("context is changing lifecycle state")
	}
	c.stopping = true
	c.startLock.Unlock()
	defer func() { c.startLock.Lock(); c.stopping = false; c.startLock.Unlock() }()
	stopErr := route.Stop()

	c.routesLock.Lock()
	c.detachRouteLocked(route)
	c.routesLock.Unlock()
	return stopErr
}

// RemoveRouteByID stops and removes the route with the given ID.
func (c *CamelContext) RemoveRouteByID(id string) error {
	c.routesLock.Lock()
	var route *Route
	for _, r := range c.routes {
		if r.ID == id {
			route = r
			break
		}
	}
	c.routesLock.Unlock()
	if route == nil {
		return nil
	}
	return c.RemoveRoute(route)
}

// RemoveAllRoutes stops every route and clears the context's route list.
func (c *CamelContext) RemoveAllRoutes() error {
	c.routesLock.Lock()
	routes := make([]*Route, len(c.routes))
	copy(routes, c.routes)
	c.routesLock.Unlock()

	var errs []error
	for _, route := range routes {
		if err := c.RemoveRoute(route); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// detachRouteLocked removes route from the slice and frees its ID.
// Caller must hold routesLock.
func (c *CamelContext) detachRouteLocked(route *Route) {
	for i, r := range c.routes {
		if r == route {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
	if c.usedRouteIDs != nil && route.ID != "" {
		delete(c.usedRouteIDs, route.ID)
	}
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
	entries sync.Map // map[string]*producerEntry — per-URI creation locks
	stopped bool
}

// producerEntry single-flights the creation of one URI's producer.
type producerEntry struct {
	once sync.Mutex
}

// NewProducerCache creates a new ProducerCache.
func NewProducerCache(context *CamelContext) *ProducerCache {
	return &ProducerCache{
		context: context,
		cache:   make(map[string]Producer),
	}
}

// Get retrieves a started producer from the cache, creating and starting it if missing.
//
// Creation happens outside the cache-wide lock: CreateProducer and Start can
// perform network I/O (connecting to a broker, opening a session), and holding
// the write lock across that would serialise every other endpoint behind the
// slowest one. A per-URI entry lock keeps creation single-flighted so a given
// producer is still built only once.
func (pc *ProducerCache) Get(uri string) (Producer, error) {
	pc.mu.RLock()
	if pc.stopped {
		pc.mu.RUnlock()
		return nil, fmt.Errorf("producer cache: context is stopping")
	}
	prod, exists := pc.cache[uri]
	pc.mu.RUnlock()
	if exists {
		return prod, nil
	}

	entry := pc.entryFor(uri)
	entry.once.Lock()
	defer entry.once.Unlock()

	pc.mu.RLock()
	if pc.stopped {
		pc.mu.RUnlock()
		return nil, fmt.Errorf("producer cache: context is stopping")
	}
	prod, exists = pc.cache[uri]
	pc.mu.RUnlock()
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

	if err := p.Start(pc.context.GetContext()); err != nil {
		return nil, fmt.Errorf("producer cache: producer start failed for %s: %w", uri, err)
	}

	pc.mu.Lock()
	if pc.stopped {
		pc.mu.Unlock()
		_ = p.Stop()
		return nil, fmt.Errorf("producer cache: context is stopping")
	}
	pc.cache[uri] = p
	pc.mu.Unlock()
	return p, nil
}

// entryFor returns the per-URI creation lock, allocating it on first use.
func (pc *ProducerCache) entryFor(uri string) *producerEntry {
	actual, _ := pc.entries.LoadOrStore(uri, &producerEntry{})
	return actual.(*producerEntry)
}

func (pc *ProducerCache) Stop() error {
	pc.mu.Lock()
	pc.stopped = true
	cache := pc.cache
	pc.cache = make(map[string]Producer)
	pc.entries.Clear()
	pc.mu.Unlock()

	var errs []error
	for uri, prod := range cache {
		if err := prod.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("producer cache: error stopping producer for %s: %w", uri, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("producer cache: errors stopping producers: %v", errs)
	}
	return nil
}

// Resume allows Get after CamelContext.Stop so a subsequent Start can create producers.
func (pc *ProducerCache) Resume() {
	pc.mu.Lock()
	pc.stopped = false
	pc.mu.Unlock()
}
