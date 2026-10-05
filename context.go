package gocamel

import (
	"container/list"
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
	c.AddComponent("mock", NewMockComponent())
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

// GetProducer retrieves a started producer from the global cache, creating it
// if necessary. The returned producer is pinned: the caller may keep it, so it
// is never evicted and stays started until the context stops. Internal EIPs
// use acquireProducer instead so that dynamic URIs do not pin the cache.
func (c *CamelContext) GetProducer(uri string) (Producer, error) {
	return c.producerCache.Get(uri)
}

// SetProducerCacheSize bounds the number of producers kept in the shared
// producer cache used by ToD, RecipientList, RoutingSlip, DynamicRouter,
// WireTap, Enrich and the DeadLetterChannel. Once the bound is exceeded the
// least recently used producers that are not in use are stopped. n <= 0
// disables the bound. Defaults to DefaultProducerCacheSize.
func (c *CamelContext) SetProducerCacheSize(n int) {
	c.producerCache.SetMaxSize(n)
}

// acquireProducer returns a started producer for uri together with a release
// function the caller must invoke once the send has completed. Until then the
// producer cannot be evicted.
func (c *CamelContext) acquireProducer(uri string) (Producer, func(), error) {
	return c.producerCache.acquire(uri)
}

// sendToURI sends exchange to uri through the shared producer cache.
func (c *CamelContext) sendToURI(uri string, exchange *Exchange) error {
	producer, release, err := c.acquireProducer(uri)
	if err != nil {
		return err
	}
	defer release()
	return producer.Send(exchange)
}

// DefaultProducerCacheSize is the default bound of the shared producer cache.
// Every distinct URI resolved by ToD or a RecipientList creates a started
// producer; without a bound, URIs that embed per-message values would grow the
// cache (and its open connections) for the lifetime of the context.
const DefaultProducerCacheSize = 1000

// ProducerCache caches started Producers thread-safely so they can be reused.
// It is bounded (see SetMaxSize): the least recently used producers that are
// neither in use nor pinned are stopped when the bound is exceeded.
type ProducerCache struct {
	context *CamelContext
	mu      sync.Mutex // Protects cache, lru, stopped, epoch and maxSize.
	cache   map[string]*cachedProducer
	lru     *list.List // of *cachedProducer; front is most recently used.
	entries sync.Map   // map[string]*producerEntry — per-URI creation locks
	stopped bool
	// epoch counts Stop calls. A producer created across a Stop was started
	// with the context that Stop cancelled; Resume runs right after Stop, so
	// the stopped flag alone cannot tell it apart from a fresh producer.
	epoch   uint64
	maxSize int
}

// cachedProducer is one cache slot. refs counts in-flight users (acquire);
// pinned marks producers handed out by Get, which callers may hold forever.
type cachedProducer struct {
	uri      string
	producer Producer
	refs     int
	pinned   bool
	elem     *list.Element
}

// producerEntry single-flights the creation of one URI's producer.
type producerEntry struct {
	once sync.Mutex
}

// NewProducerCache creates a new ProducerCache bounded by DefaultProducerCacheSize.
func NewProducerCache(context *CamelContext) *ProducerCache {
	return &ProducerCache{
		context: context,
		cache:   make(map[string]*cachedProducer),
		lru:     list.New(),
		maxSize: DefaultProducerCacheSize,
	}
}

// SetMaxSize changes the cache bound (n <= 0 means unbounded) and evicts
// idle producers above the new bound.
func (pc *ProducerCache) SetMaxSize(n int) {
	pc.mu.Lock()
	pc.maxSize = n
	evicted := pc.evictLocked()
	pc.mu.Unlock()
	stopEvicted(evicted)
}

// Size returns the number of producers currently cached.
func (pc *ProducerCache) Size() int {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return len(pc.cache)
}

// Get retrieves a started producer from the cache, creating and starting it if
// missing. The producer is pinned and never evicted (see CamelContext.GetProducer).
func (pc *ProducerCache) Get(uri string) (Producer, error) {
	entry, err := pc.lookupOrCreate(uri, func(e *cachedProducer) { e.pinned = true })
	if err != nil {
		return nil, err
	}
	return entry.producer, nil
}

// acquire retrieves a started producer and marks it in use until release is
// called. release is idempotent.
func (pc *ProducerCache) acquire(uri string) (Producer, func(), error) {
	entry, err := pc.lookupOrCreate(uri, func(e *cachedProducer) { e.refs++ })
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			pc.mu.Lock()
			entry.refs--
			evicted := pc.evictLocked()
			pc.mu.Unlock()
			stopEvicted(evicted)
		})
	}
	return entry.producer, release, nil
}

// lookupOrCreate returns the cache slot for uri, creating and starting the
// producer if needed. claim runs under the cache lock on the returned slot so
// the slot cannot be evicted between lookup and use.
//
// Creation happens outside the cache-wide lock: CreateProducer and Start can
// perform network I/O (connecting to a broker, opening a session), and holding
// the lock across that would serialise every other endpoint behind the
// slowest one. A per-URI entry lock keeps creation single-flighted so a given
// producer is normally built only once.
func (pc *ProducerCache) lookupOrCreate(uri string, claim func(*cachedProducer)) (*cachedProducer, error) {
	if entry, err := pc.lookup(uri, claim); entry != nil || err != nil {
		return entry, err
	}

	lock := pc.entryFor(uri)
	lock.once.Lock()
	defer lock.once.Unlock()
	// The creation lock is only needed while the producer is missing; dropping
	// it once the slot is published keeps entries from growing with every
	// distinct URI ever seen.
	defer pc.entries.CompareAndDelete(uri, lock)

	if entry, err := pc.lookup(uri, claim); entry != nil || err != nil {
		return entry, err
	}
	// Capture the epoch before reading the context the producer starts with:
	// a Stop after this point makes the insert below fail. Resume runs only
	// after the context was replaced, so a live epoch implies a live context.
	pc.mu.Lock()
	if pc.stopped {
		pc.mu.Unlock()
		return nil, fmt.Errorf("producer cache: context is stopping")
	}
	epoch := pc.epoch
	pc.mu.Unlock()

	endpoint, err := pc.context.CreateEndpoint(uri)
	if err != nil {
		return nil, fmt.Errorf("producer cache: endpoint creation failed for %s: %w", RedactURI(uri), err)
	}
	p, err := endpoint.CreateProducer()
	if err != nil {
		return nil, fmt.Errorf("producer cache: producer creation failed for %s: %w", RedactURI(uri), err)
	}
	if err := p.Start(pc.context.GetContext()); err != nil {
		return nil, fmt.Errorf("producer cache: producer start failed for %s: %w", RedactURI(uri), err)
	}

	pc.mu.Lock()
	if pc.stopped || pc.epoch != epoch {
		pc.mu.Unlock()
		_ = p.Stop()
		return nil, fmt.Errorf("producer cache: context is stopping")
	}
	if existing, ok := pc.cache[uri]; ok {
		// Another creator won a race across an eviction; keep its producer.
		pc.lru.MoveToFront(existing.elem)
		claim(existing)
		pc.mu.Unlock()
		_ = p.Stop()
		return existing, nil
	}
	entry := &cachedProducer{uri: uri, producer: p}
	entry.elem = pc.lru.PushFront(entry)
	pc.cache[uri] = entry
	claim(entry)
	evicted := pc.evictLocked()
	pc.mu.Unlock()
	stopEvicted(evicted)
	return entry, nil
}

// lookup returns the cached slot for uri (claimed), nil if missing, or an
// error when the cache is stopped.
func (pc *ProducerCache) lookup(uri string, claim func(*cachedProducer)) (*cachedProducer, error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.stopped {
		return nil, fmt.Errorf("producer cache: context is stopping")
	}
	entry, ok := pc.cache[uri]
	if !ok {
		return nil, nil
	}
	pc.lru.MoveToFront(entry.elem)
	claim(entry)
	return entry, nil
}

// evictLocked removes least recently used idle, unpinned slots until the
// cache fits maxSize, returning them so the caller stops them outside the
// lock. Slots in use are skipped: the cache may temporarily exceed its bound.
// Caller must hold mu.
func (pc *ProducerCache) evictLocked() []*cachedProducer {
	if pc.maxSize <= 0 || len(pc.cache) <= pc.maxSize {
		return nil
	}
	var evicted []*cachedProducer
	for e := pc.lru.Back(); e != nil && len(pc.cache) > pc.maxSize; {
		prev := e.Prev()
		entry := e.Value.(*cachedProducer)
		if entry.refs == 0 && !entry.pinned {
			pc.lru.Remove(e)
			delete(pc.cache, entry.uri)
			evicted = append(evicted, entry)
		}
		e = prev
	}
	return evicted
}

func stopEvicted(evicted []*cachedProducer) {
	for _, entry := range evicted {
		if err := entry.producer.Stop(); err != nil {
			LogWarn("producer cache: error stopping evicted producer", "uri", RedactURI(entry.uri), "error", err)
		}
	}
}

// entryFor returns the per-URI creation lock, allocating it on first use.
func (pc *ProducerCache) entryFor(uri string) *producerEntry {
	actual, _ := pc.entries.LoadOrStore(uri, &producerEntry{})
	return actual.(*producerEntry)
}

func (pc *ProducerCache) Stop() error {
	pc.mu.Lock()
	pc.stopped = true
	pc.epoch++
	cache := pc.cache
	pc.cache = make(map[string]*cachedProducer)
	pc.lru.Init()
	pc.entries.Clear()
	pc.mu.Unlock()

	var errs []error
	for uri, entry := range cache {
		if err := entry.producer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("producer cache: error stopping producer for %s: %w", RedactURI(uri), err))
		}
	}
	return errors.Join(errs...)
}

// Resume allows Get after CamelContext.Stop so a subsequent Start can create producers.
func (pc *ProducerCache) Resume() {
	pc.mu.Lock()
	pc.stopped = false
	pc.mu.Unlock()
}
