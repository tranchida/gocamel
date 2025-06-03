package gocamel

import (
	"fmt"
	"strings"
	"sync"
)

// ComponentRegistry gère l'enregistrement et la récupération des composants
type ComponentRegistry struct {
	components map[string]Component
	mu         sync.RWMutex
}

// NewComponentRegistry crée une nouvelle instance de ComponentRegistry
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		components: make(map[string]Component),
	}
}

// RegisterComponent enregistre un composant avec un nom spécifique
func (r *ComponentRegistry) RegisterComponent(name string, component Component) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components[name] = component
}

// GetComponent récupère un composant par son nom
func (r *ComponentRegistry) GetComponent(name string) (Component, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	component, exists := r.components[name]
	if !exists {
		return nil, fmt.Errorf("composant non trouvé: %s", name)
	}
	return component, nil
}

// RemoveComponent supprime un composant du registre
func (r *ComponentRegistry) RemoveComponent(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.components, name)
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

// Clear supprime tous les composants du registre
func (r *ComponentRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.components = make(map[string]Component)
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
