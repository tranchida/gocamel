package gocamel

import (
	"fmt"
	"strings"
	"sync"
)

// Registry defines a generic registry for beans, components, and other objects
type Registry interface {
	Bind(name string, value any)
	Lookup(name string) (any, bool)
	Remove(name string)
	CreateEndpoint(uri string) (Endpoint, error)
}

// ComponentRegistry manages component registration and retrieval.
// It implements the Registry interface.
type ComponentRegistry struct {
	components map[string]any
	mu         sync.RWMutex
}

// NewComponentRegistry crée une nouvelle instance de ComponentRegistry
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		components: make(map[string]any),
	}
}

// Bind enregistre un objet avec un nom spécifique
func (r *ComponentRegistry) Bind(name string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components[name] = value
}

// Lookup récupère un objet par son nom
func (r *ComponentRegistry) Lookup(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, exists := r.components[name]
	return value, exists
}

// Remove supprime un objet du registre
func (r *ComponentRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.components, name)
}

// RegisterComponent enregistre un composant avec un nom spécifique (rétrocompatibilité)
func (r *ComponentRegistry) RegisterComponent(name string, component Component) {
	r.Bind(name, component)
}

// GetComponent récupère un composant par son nom (rétrocompatibilité)
func (r *ComponentRegistry) GetComponent(name string) (Component, error) {
	val, exists := r.Lookup(name)
	if !exists {
		return nil, fmt.Errorf("composant non trouvé: %s", name)
	}
	component, ok := val.(Component)
	if !ok {
		return nil, fmt.Errorf("l'objet trouvé n'est pas un composant: %s", name)
	}
	return component, nil
}

// RemoveComponent supprime un composant du registre (rétrocompatibilité)
func (r *ComponentRegistry) RemoveComponent(name string) {
	r.Remove(name)
}

// HasComponent vérifie si un composant existe
func (r *ComponentRegistry) HasComponent(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.components[name]
	return exists
}

// GetComponentNames retourne la liste des noms de composants enregistrés
func (r *ComponentRegistry) GetComponentNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.components))
	for name := range r.components {
		names = append(names, name)
	}
	return names
}

// GetComponentCount retourne le nombre de composants enregistrés
func (r *ComponentRegistry) GetComponentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.components)
}

// Clear supprime tous les objets du registre
func (r *ComponentRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = make(map[string]any)
}

// CreateEndpoint crée un endpoint à partir d'une URI
func (r *ComponentRegistry) CreateEndpoint(uri string) (Endpoint, error) {
	parts := strings.SplitN(uri, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("URI invalide: %s", uri)
	}

	componentName := parts[0]
	component, err := r.GetComponent(componentName)
	if err != nil {
		return nil, fmt.Errorf("erreur lors de la récupération du composant %s: %v", componentName, err)
	}

	return component.CreateEndpoint(uri)
}
