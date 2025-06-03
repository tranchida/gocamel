package gocamel

import (
	"context"
	"fmt"
	"sync"
)

// CamelContext représente le contexte principal de l'application
type CamelContext struct {
	ctx       context.Context
	cancel    context.CancelFunc
	routes    []*Route
	registry  *ComponentRegistry
	started   bool
	startLock sync.Mutex
}

// NewCamelContext crée une nouvelle instance de CamelContext
func NewCamelContext() *CamelContext {
	ctx, cancel := context.WithCancel(context.Background())
	return &CamelContext{
		ctx:      ctx,
		cancel:   cancel,
		routes:   make([]*Route, 0),
		registry: NewComponentRegistry(),
	}
}

// AddRoute ajoute une route au contexte
func (c *CamelContext) AddRoute(route *Route) {
	c.routes = append(c.routes, route)
}

// AddRoutes ajoute plusieurs routes au contexte
func (c *CamelContext) AddRoutes(routes ...*Route) {
	c.routes = append(c.routes, routes...)
}

// GetRoute récupère une route par son ID
func (c *CamelContext) GetRoute(id string) *Route {
	for _, route := range c.routes {
		if route.ID == id {
			return route
		}
	}
	return nil
}

// GetRoutes récupère toutes les routes
func (c *CamelContext) GetRoutes() []*Route {
	return c.routes
}

// Start démarre le contexte et toutes ses routes
func (c *CamelContext) Start() error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if c.started {
		return fmt.Errorf("le contexte est déjà démarré")
	}

	// Démarrage de toutes les routes
	for _, route := range c.routes {
		if err := route.Start(c.ctx); err != nil {
			return fmt.Errorf("erreur lors du démarrage de la route %s: %v", route.ID, err)
		}
	}

	c.started = true
	return nil
}

// Stop arrête le contexte et toutes ses routes
func (c *CamelContext) Stop() error {
	c.startLock.Lock()
	defer c.startLock.Unlock()

	if !c.started {
		return nil
	}

	// Arrêt de toutes les routes
	for _, route := range c.routes {
		if err := route.Stop(); err != nil {
			return fmt.Errorf("erreur lors de l'arrêt de la route %s: %v", route.ID, err)
		}
	}

	c.cancel()
	c.started = false
	return nil
}

// IsStarted vérifie si le contexte est démarré
func (c *CamelContext) IsStarted() bool {
	c.startLock.Lock()
	defer c.startLock.Unlock()
	return c.started
}

// GetContext récupère le contexte Go sous-jacent
func (c *CamelContext) GetContext() context.Context {
	return c.ctx
}

// GetComponentRegistry récupère le registre des composants
func (c *CamelContext) GetComponentRegistry() *ComponentRegistry {
	return c.registry
}

// CreateRoute crée une nouvelle route dans ce contexte
func (c *CamelContext) CreateRoute() *Route {
	route := NewRoute()
	route.context = c
	c.AddRoute(route)
	return route
}

// CreateRouteBuilder crée un nouveau constructeur de route
func (c *CamelContext) CreateRouteBuilder() *RouteBuilder {
	return NewRouteBuilder(c)
}

// AddComponent ajoute un composant au registre
func (c *CamelContext) AddComponent(name string, component Component) {
	c.registry.RegisterComponent(name, component)
}

// GetComponent récupère un composant par son nom
func (c *CamelContext) GetComponent(name string) (Component, error) {
	return c.registry.GetComponent(name)
}

// RemoveRoute supprime une route du contexte
func (c *CamelContext) RemoveRoute(route *Route) {
	for i, r := range c.routes {
		if r == route {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
}

// RemoveRouteByID supprime une route par son ID
func (c *CamelContext) RemoveRouteByID(id string) {
	for i, route := range c.routes {
		if route.ID == id {
			c.routes = append(c.routes[:i], c.routes[i+1:]...)
			break
		}
	}
}

// RemoveAllRoutes supprime toutes les routes
func (c *CamelContext) RemoveAllRoutes() {
	c.routes = make([]*Route, 0)
}

// GetRouteCount retourne le nombre de routes
func (c *CamelContext) GetRouteCount() int {
	return len(c.routes)
}

// GetStartedRouteCount retourne le nombre de routes démarrées
func (c *CamelContext) GetStartedRouteCount() int {
	count := 0
	for _, route := range c.routes {
		if route.IsStarted() {
			count++
		}
	}
	return count
}

// CreateEndpoint crée un endpoint à partir d'une URI
func (c *CamelContext) CreateEndpoint(uri string) (Endpoint, error) {
	return c.registry.CreateEndpoint(uri)
}
