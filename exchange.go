package gocamel

import (
	"context"
	"maps"
	"time"
)

// Constantes pour les propriétés communes
const (
	// Propriétés de fichier
	CamelFileName         = "CamelFileName"         // Nom du fichier
	CamelFilePath         = "CamelFilePath"         // Chemin complet du fichier
	CamelFileLength       = "CamelFileLength"       // Taille du fichier
	CamelFileLastModified = "CamelFileLastModified" // Date de dernière modification
	CamelFileHost         = "CamelFileHost"         // Hôte du système de fichiers

	// Propriétés HTTP
	CamelHttpMethod       = "CamelHttpMethod"       // Méthode HTTP
	CamelHttpUrl          = "CamelHttpUrl"          // URL HTTP
	CamelHttpPath         = "CamelHttpPath"         // Chemin HTTP
	CamelHttpQuery        = "CamelHttpQuery"        // Paramètres de requête
	CamelHttpResponseCode = "CamelHttpResponseCode" // Code de réponse HTTP
	CamelHttpResponseText = "CamelHttpResponseText" // Texte de réponse HTTP

	// Propriétés de message
	CamelMessageId         = "CamelMessageId"         // ID unique du message
	CamelMessageTimestamp  = "CamelMessageTimestamp"  // Horodatage du message
	CamelMessageExchangeId = "CamelMessageExchangeId" // ID de l'échange

	// Propriétés de route
	CamelRouteId          = "CamelRouteId"          // ID de la route
	CamelRouteGroup       = "CamelRouteGroup"       // Groupe de la route
	CamelRouteDescription = "CamelRouteDescription" // Description de la route

	// Propriétés d'erreur
	CamelExceptionCaught = "CamelExceptionCaught" // Exception capturée
	CamelFailureEndpoint = "CamelFailureEndpoint" // Endpoint en échec
	CamelFailureRouteId  = "CamelFailureRouteId"  // ID de la route en échec

	// Propriétés de performance
	CamelTimerName      = "CamelTimerName"      // Nom du timer
	CamelTimerFiredTime = "CamelTimerFiredTime" // Heure de déclenchement du timer
	CamelTimerPeriod    = "CamelTimerPeriod"    // Période du timer

	// Propriétés de contexte
	CamelContextName    = "CamelContextName"    // Nom du contexte
	CamelContextId      = "CamelContextId"      // ID du contexte
	CamelContextVersion = "CamelContextVersion" // Version du contexte

	// Propriétés de transaction
	CamelTransactionId     = "CamelTransactionId"     // ID de transaction
	CamelTransactionKey    = "CamelTransactionKey"    // Clé de transaction
	CamelTransactionStatus = "CamelTransactionStatus" // Statut de transaction

	// Propriétés de sécurité
	CamelAuthenticationScheme = "CamelAuthenticationScheme" // Schéma d'authentification
	CamelAuthenticationType   = "CamelAuthenticationType"   // Type d'authentification
	CamelAuthorizationPolicy  = "CamelAuthorizationPolicy"  // Politique d'autorisation
)

// Exchange représente le contexte d'échange d'un message dans une route
type Exchange struct {
	Context    context.Context
	In         *Message
	Out        *Message
	Properties map[string]any
	Created    time.Time
	Modified   time.Time
	Error      error
}

// NewExchange crée une nouvelle instance d'Exchange
func NewExchange(ctx context.Context) *Exchange {
	now := time.Now()
	return &Exchange{
		Context:    ctx,
		In:         NewMessage(),
		Out:        NewMessage(),
		Properties: make(map[string]any),
		Created:    now,
		Modified:   now,
	}
}

// GetIn récupère le message d'entrée
func (e *Exchange) GetIn() *Message {
	return e.In
}

// GetOut récupère le message de sortie
func (e *Exchange) GetOut() *Message {
	return e.Out
}


// SetBody définit le corps du message d'entrée
func (e *Exchange) SetBody(body any) {
	e.In.SetBody(body)
	e.Modified = time.Now()
}

// GetBody récupère le corps du message d'entrée
func (e *Exchange) GetBody() any {
	return e.In.GetBody()
}

// SetHeader définit un en-tête du message d'entrée
func (e *Exchange) SetHeader(key string, value any) {
	e.In.SetHeader(key, value)
	e.Modified = time.Now()
}

// GetHeader récupère un en-tête du message d'entrée
func (e *Exchange) GetHeader(key string) (any, bool) {
	return e.In.GetHeader(key)
}

// SetProperty définit une propriété de l'échange
func (e *Exchange) SetProperty(key string, value any) {
	e.Properties[key] = value
	e.Modified = time.Now()
}

// GetProperty récupère une propriété de l'échange
func (e *Exchange) GetProperty(key string) (any, bool) {
	value, exists := e.Properties[key]
	return value, exists
}

// GetPropertyOrDefault récupère une propriété ou une valeur par défaut
func (e *Exchange) GetPropertyOrDefault(key string, defaultValue any) any {
	if value, exists := e.Properties[key]; exists {
		return value
	}
	return defaultValue
}

// RemoveProperty supprime une propriété de l'échange
func (e *Exchange) RemoveProperty(key string) {
	delete(e.Properties, key)
	e.Modified = time.Now()
}

// HasProperty vérifie si une propriété existe
func (e *Exchange) HasProperty(key string) bool {
	_, exists := e.Properties[key]
	return exists
}

// GetProperties récupère toutes les propriétés
func (e *Exchange) GetProperties() map[string]any {
	return e.Properties
}

// SetProperties définit plusieurs propriétés
func (e *Exchange) SetProperties(properties map[string]any) {
	for key, value := range properties {
		e.Properties[key] = value
	}
	e.Modified = time.Now()
}

// ClearProperties supprime toutes les propriétés
func (e *Exchange) ClearProperties() {
	e.Properties = make(map[string]any)
	e.Modified = time.Now()
}

// GetPropertyAsString récupère une propriété sous forme de chaîne
func (e *Exchange) GetPropertyAsString(key string) (string, bool) {
	if value, exists := e.Properties[key]; exists {
		if str, ok := value.(string); ok {
			return str, true
		}
	}
	return "", false
}

// GetPropertyAsInt récupère une propriété sous forme d'entier
func (e *Exchange) GetPropertyAsInt(key string) (int, bool) {
	if value, exists := e.Properties[key]; exists {
		if i, ok := value.(int); ok {
			return i, true
		}
	}
	return 0, false
}

// GetPropertyAsBool récupère une propriété sous forme de booléen
func (e *Exchange) GetPropertyAsBool(key string) (bool, bool) {
	if value, exists := e.Properties[key]; exists {
		if b, ok := value.(bool); ok {
			return b, true
		}
	}
	return false, false
}

// GetPropertyAsFloat récupère une propriété sous forme de nombre à virgule flottante
func (e *Exchange) GetPropertyAsFloat(key string) (float64, bool) {
	if value, exists := e.Properties[key]; exists {
		if f, ok := value.(float64); ok {
			return f, true
		}
	}
	return 0, false
}

// GetPropertyAsTime récupère une propriété sous forme de time.Time
func (e *Exchange) GetPropertyAsTime(key string) (time.Time, bool) {
	if value, exists := e.Properties[key]; exists {
		if t, ok := value.(time.Time); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// GetPropertyAsDuration récupère une propriété sous forme de time.Duration
func (e *Exchange) GetPropertyAsDuration(key string) (time.Duration, bool) {
	if value, exists := e.Properties[key]; exists {
		if d, ok := value.(time.Duration); ok {
			return d, true
		}
	}
	return 0, false
}

// GetPropertyAsMap récupère une propriété sous forme de map
func (e *Exchange) GetPropertyAsMap(key string) (map[string]any, bool) {
	if value, exists := e.Properties[key]; exists {
		if m, ok := value.(map[string]any); ok {
			return m, true
		}
	}
	return nil, false
}

// GetPropertyAsSlice récupère une propriété sous forme de slice
func (e *Exchange) GetPropertyAsSlice(key string) ([]any, bool) {
	if value, exists := e.Properties[key]; exists {
		if s, ok := value.([]any); ok {
			return s, true
		}
	}
	return nil, false
}

// Copy crée une copie de l'échange
func (e *Exchange) Copy() *Exchange {
	copy := NewExchange(e.Context)
	copy.In = e.In.Copy()
	copy.Out = e.Out.Copy()

	// Copie des propriétés
	maps.Copy(copy.Properties, e.Properties)

	copy.Created = e.Created
	copy.Modified = time.Now()
	copy.Error = e.Error

	return copy
}
