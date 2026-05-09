package gocamel

import (
	"fmt"
	"sync"
)

// Registry defines a generic registry for beans, components, and other objects.
// ComponentRegistry is the default implementation.
type Registry interface {
	// Bind registers an object under the given name.
	Bind(name string, value any)
	// Lookup retrieves an object by name. The second return value indicates if it was found.
	Lookup(name string) (any, bool)
	// Remove deletes the object with the given name from the registry.
	Remove(name string)
	// CreateEndpoint parses the URI, looks up the component by scheme, and creates an Endpoint.
	CreateEndpoint(uri string) (Endpoint, error)
}

// ComponentRegistry manages component registration and retrieval.
// It implements the Registry interface.
type ComponentRegistry struct {
	components map[string]any
	mu         sync.RWMutex
}

// NewComponentRegistry creates a new ComponentRegistry instance.
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		components: make(map[string]any),
	}
}

// Bind registers an object under a specific name.
func (r *ComponentRegistry) Bind(name string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components[name] = value
}

// Lookup retrieves an object by name.
func (r *ComponentRegistry) Lookup(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.components[name]
	return value, exists
}

// Remove deletes an object from the registry.
func (r *ComponentRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.components, name)
}

// RegisterComponent registers a component under a specific name (backwards compatibility).
func (r *ComponentRegistry) RegisterComponent(name string, component Component) {
	r.Bind(name, component)
}

// GetComponent retrieves a component by name (backwards compatibility).
func (r *ComponentRegistry) GetComponent(name string) (Component, error) {
	val, exists := r.Lookup(name)
	if !exists {
		return nil, fmt.Errorf("component not found: %s", name)
	}
	component, ok := val.(Component)
	if !ok {
		return nil, fmt.Errorf("the object found is not a component: %s", name)
	}
	return component, nil
}

// RemoveComponent deletes a component from the registry (backwards compatibility).
func (r *ComponentRegistry) RemoveComponent(name string) {
	r.Remove(name)
}

// HasComponent returns true if a component with the given name exists.
func (r *ComponentRegistry) HasComponent(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.components[name]
	return exists
}

// GetComponentNames returns the list of registered component names.
func (r *ComponentRegistry) GetComponentNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.components))
	for name := range r.components {
		names = append(names, name)
	}
	return names
}

// GetComponentCount returns the number of registered components.
func (r *ComponentRegistry) GetComponentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.components)
}

// Clear removes all objects from the registry.
func (r *ComponentRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = make(map[string]any)
}

// CreateEndpoint creates an endpoint from a URI.
func (r *ComponentRegistry) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid URI: %s", uri)
	}

	componentName := u.Scheme
	component, err := r.GetComponent(componentName)
	if err != nil {
		return nil, fmt.Errorf("error retrieving component %s: %v", componentName, err)
	}

	return component.CreateEndpoint(uri)
}
