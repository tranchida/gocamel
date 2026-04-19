package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Constantes de headers pour le composant template
const (
	CamelTemplatePath     = "CamelTemplatePath"     // Chemin du fichier template (override)
	CamelTemplateEncoding = "CamelTemplateEncoding" // Encodage du template (default: UTF-8)
	CamelTemplateLocale   = "CamelTemplateLocale"   // Locale pour le template
)

// TemplateData regroupe toutes les données disponibles dans un template
type TemplateData struct {
	// Exchange contient les données de l'échange
	Exchange struct {
		ID         string
		Created    time.Time
		Properties map[string]any
	}
	// In contient les données du message d'entrée
	In struct {
		Body    any
		Headers map[string]any
	}
	// Headers est un alias pour In.Headers (compatibilité Camel)
	Headers map[string]any
	// Body est un alias pour In.Body (compatibilité Camel)
	Body any
}

// TemplateComponent représente le composant template
type TemplateComponent struct {
	mu    sync.RWMutex
	cache map[string]*template.Template // Cache des templates parsés
}

// NewTemplateComponent crée une nouvelle instance de TemplateComponent
func NewTemplateComponent() *TemplateComponent {
	return &TemplateComponent{
		cache: make(map[string]*template.Template),
	}
}

// CreateEndpoint crée un nouvel endpoint template
// Format de l'URI: template:chemin/vers/fichier.tmpl[?contentCache=true&allowTemplateFromHeader=true]
func (c *TemplateComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI template invalide: %w", err)
	}

	// Le chemin peut être soit dans Opaque (after :) soit dans Path (chemin absolu)
	// template:path/to/file → Opaque="path/to/file"
	// template:/absolute/path/to/file → Path="/absolute/path/to/file"
	var path string
	if parsedURL.Opaque != "" {
		path = parsedURL.Opaque
	} else if parsedURL.Path != "" {
		path = parsedURL.Path
	}

	if path == "" {
		return nil, fmt.Errorf("chemin de template manquant dans l'URI: %s", uri)
	}

	endpoint := &TemplateEndpoint{
		uri:       uri,
		path:      path,
		component: c,
	}

	query := parsedURL.Query()

	// Option contentCache: cacher le template en mémoire
	if val := query.Get("contentCache"); val != "" {
		if v, err := parseBool(val); err == nil {
			endpoint.contentCache = v
		}
	}

	// Option allowTemplateFromHeader: permettre de changer le template via header
	if val := query.Get("allowTemplateFromHeader"); val != "" {
		if v, err := parseBool(val); err == nil {
			endpoint.allowTemplateFromHeader = v
		}
	}

	// Option encoding: spécifier l'encodage (défaut UTF-8)
	if val := query.Get("encoding"); val != "" {
		endpoint.encoding = val
	} else {
		endpoint.encoding = "UTF-8"
	}

	// Option startDelimiter/endDelimiter: délimiteurs personnalisés
	if val := query.Get("startDelimiter"); val != "" {
		endpoint.startDelimiter = val
	}
	if val := query.Get("endDelimiter"); val != "" {
		endpoint.endDelimiter = val
	}

	return endpoint, nil
}

// parseBool parse une chaîne en booléen
func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("valeur booléenne invalide: %s", s)
}

// TemplateEndpoint représente un endpoint template
type TemplateEndpoint struct {
	uri                       string
	path                      string
	component                 *TemplateComponent
	contentCache              bool
	allowTemplateFromHeader   bool
	encoding                  string
	startDelimiter            string
	endDelimiter              string
}

// URI retourne l'URI de l'endpoint
func (e *TemplateEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur template
func (e *TemplateEndpoint) CreateProducer() (Producer, error) {
	return &TemplateProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer n'est pas supporté pour le composant template
func (e *TemplateEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("le composant template ne supporte pas les consommateurs")
}

// TemplateProducer représente un producteur template
type TemplateProducer struct {
	endpoint     *TemplateEndpoint
	templateText string // Template chargé en mémoire si contentCache=true
}

// Start démarre le producteur template
func (p *TemplateProducer) Start(ctx context.Context) error {
	// Si contentCache est activé, charger le template en mémoire
	if p.endpoint.contentCache {
		content, err := os.ReadFile(p.endpoint.path)
		if err != nil {
			return fmt.Errorf("erreur lors de la lecture du template %s: %w", p.endpoint.path, err)
		}
		p.templateText = string(content)
	}
	return nil
}

// Stop arrête le producteur template
func (p *TemplateProducer) Stop() error {
	// Nettoyer le cache en mémoire si nécessaire
	p.templateText = ""
	return nil
}

// Send effectue la transformation du template
func (p *TemplateProducer) Send(exchange *Exchange) error {
	// Déterminer le chemin du template à utiliser
	templatePath := p.endpoint.path
	if p.endpoint.allowTemplateFromHeader {
		if v, ok := exchange.GetHeader(CamelTemplatePath); ok && v != "" {
			if s, isString := v.(string); isString && s != "" {
				templatePath = s
			}
		}
	}

	// Récupérer le contenu du template
	var templateContent string
	var err error

	if p.endpoint.contentCache && !p.endpoint.allowTemplateFromHeader {
		// Utiliser le template en cache
		templateContent = p.templateText
	} else {
		// Charger depuis le fichier
		content, err := os.ReadFile(templatePath)
		if err != nil {
			return fmt.Errorf("erreur lors de la lecture du template %s: %w", templatePath, err)
		}
		templateContent = string(content)
	}

	// Préparer les données pour le template
	data := prepareTemplateData(exchange)

	// Parser et exécuter le template
	result, err := p.executeTemplate(templateContent, data, templatePath)
	if err != nil {
		return fmt.Errorf("erreur lors de l'exécution du template: %w", err)
	}

	// Définir le résultat dans le corps du message
	exchange.GetIn().SetBody(result)
	return nil
}

// executeTemplate parse et exécute un template
func (p *TemplateProducer) executeTemplate(content string, data any, name string) (string, error) {
	// Vérifier le cache si contentCache est activé
	if p.endpoint.contentCache {
		if tmpl := p.endpoint.component.getCachedTemplate(name); tmpl != nil {
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return "", err
			}
			return buf.String(), nil
		}
	}

	// Créer les options de parsing
	var tmpl *template.Template
	var err error

	// Utiliser le nom de base du fichier comme nom de template
	templateName := filepath.Base(name)
	if templateName == "" {
		templateName = "template"
	}

	// Si des délimiteurs personnalisés sont spécifiés, les utiliser
	if p.endpoint.startDelimiter != "" || p.endpoint.endDelimiter != "" {
		startDelim := p.endpoint.startDelimiter
		if startDelim == "" {
			startDelim = "{{"
		}
		endDelim := p.endpoint.endDelimiter
		if endDelim == "" {
			endDelim = "}}"
		}
		tmpl = template.New(templateName).Delims(startDelim, endDelim).Funcs(templateFuncs())
		tmpl, err = tmpl.Parse(content)
	} else {
		tmpl, err = template.New(templateName).Funcs(templateFuncs()).Parse(content)
	}

	if err != nil {
		return "", fmt.Errorf("erreur lors du parsing du template: %w", err)
	}

	// Mettre en cache si activé
	if p.endpoint.contentCache {
		p.endpoint.component.cacheTemplate(name, tmpl)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("erreur lors de l'exécution du template: %w", err)
	}

	return buf.String(), nil
}

// getCachedTemplate récupère un template du cache
func (c *TemplateComponent) getCachedTemplate(name string) *template.Template {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache[name]
}

// cacheTemplate stocke un template dans le cache
func (c *TemplateComponent) cacheTemplate(name string, tmpl *template.Template) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[name] = tmpl
}

// prepareTemplateData prépare les données pour le template
func prepareTemplateData(exchange *Exchange) *TemplateData {
	data := &TemplateData{}

	// Informations sur l'échange
	data.Exchange.ID = fmt.Sprintf("%p", exchange) // ID unique basé sur l'adresse mémoire
	data.Exchange.Created = exchange.Created
	data.Exchange.Properties = exchange.Properties

	// Corps du message - convertir []byte en string si nécessaire
	body := exchange.GetIn().GetBody()
	switch v := body.(type) {
	case []byte:
		data.In.Body = string(v)
	default:
		data.In.Body = v
	}
	data.Body = data.In.Body // Alias

	// Headers
	data.In.Headers = make(map[string]any)
	for k, v := range exchange.GetIn().Headers {
		// Convertir les []byte en string pour les headers aussi
		switch val := v.(type) {
		case []byte:
			data.In.Headers[k] = string(val)
		default:
			data.In.Headers[k] = val
		}
	}
	data.Headers = data.In.Headers // Alias

	return data
}

// templateFuncs retourne les fonctions disponibles dans les templates
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// Fonctions utilitaires
		"upper":    strings.ToUpper,
		"lower":    strings.ToLower,
		"title":    strings.Title,
		"trim":     strings.TrimSpace,
		"join":     strings.Join,
		"split":    strings.Split,
		"contains": strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"replace": func(old, new, s string) string {
			return strings.ReplaceAll(s, old, new)
		},
		"replaceN": strings.Replace,
		// Fonction pour échapper JSON (requise pour l'exemple)
		"json": func(v any) template.HTML {
			switch val := v.(type) {
			case string:
				// Échapper les caractères spéciaux JSON
				result := strings.ReplaceAll(val, "\\", "\\\\")
				result = strings.ReplaceAll(result, "\"", "\\\"")
				result = strings.ReplaceAll(result, "\n", "\\n")
				result = strings.ReplaceAll(result, "\r", "\\r")
				result = strings.ReplaceAll(result, "\t", "\\t")
				return template.HTML(fmt.Sprintf("\"%s\"", result))
			default:
				return template.HTML(toString(val))
			}
		},
		// Fonction pour marquer du contenu comme sûr (pas d'échappement HTML)
		"safeHTML": func(v any) template.HTML {
			switch val := v.(type) {
			case string:
				return template.HTML(val)
			case template.HTML:
				return val
			default:
				return template.HTML(fmt.Sprintf("%v", val))
			}
		},
		// Fonctions de date
		"now": time.Now,
		"formatDate": func(layout string, t time.Time) string {
			return t.Format(layout)
		},
		"formatTime": func(layout string, t time.Time) string {
			return t.Format(layout)
		},
		// Fonctions de type
		"toString": toString,
		"toInt": func(v any) int {
			switch val := v.(type) {
			case int:
				return val
			case int64:
				return int(val)
			case float64:
				return int(val)
			case string:
				var i int
				fmt.Sscanf(val, "%d", &i)
				return i
			}
			return 0
		},
		"toFloat": func(v any) float64 {
			switch val := v.(type) {
			case float64:
				return val
			case int:
				return float64(val)
			case int64:
				return float64(val)
			case string:
				var f float64
				fmt.Sscanf(val, "%f", &f)
				return f
			}
			return 0
		},
	}
}

// toString convertit n'importe quelle valeur en chaîne
func toString(v any) string {
	return fmt.Sprintf("%v", v)
}
