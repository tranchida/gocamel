package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Header constants for the template component.
const (
	CamelTemplatePath     = "CamelTemplatePath"     // Template file path (override)
	CamelTemplateEncoding = "CamelTemplateEncoding" // Template encoding (default: UTF-8)
	CamelTemplatelocal    = "CamelTemplatelocal"    // Locale for the template
)

// TemplateData groups all data available within a template.
type TemplateData struct {
	// Exchange contains the exchange data.
	Exchange struct {
		ID         string
		Created    time.Time
		Properties map[string]any
	}
	// In contains the input message data.
	In struct {
		Body    any
		Headers map[string]any
	}
	// Headers is an alias for In.Headers (Camel compatibility).
	Headers map[string]any
	// Body is an alias for In.Body (Camel compatibility).
	Body any
}

// TemplateComponent represents the template component
type TemplateComponent struct {
	mu    sync.RWMutex
	cache map[string]*template.Template // Cache of parsed templates.
}

// NewTemplateComponent creates a new TemplateComponent
func NewTemplateComponent() *TemplateComponent {
	return &TemplateComponent{
		cache: make(map[string]*template.Template),
	}
}

// CreateEndpoint creates a new template endpoint.
// URI format: template:path/to/file.tmpl[?contentCache=true&allowTemplateFromHeader=true]
func (c *TemplateComponent) CreateEndpoint(uri string) (Endpoint, error) {
	parsedURL, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid template URI: %w", err)
	}

	// The path can be in Opaque (after :) or in Path (absolute path).
	// template:path/to/file → Opaque="path/to/file"
	// template:/absolute/path/to/file → Path="/absolute/path/to/file"
	var path string
	if parsedURL.Opaque != "" {
		path = parsedURL.Opaque
	} else if parsedURL.Path != "" {
		path = parsedURL.Path
	}

	if path == "" {
		return nil, fmt.Errorf("template path missing in URI: %s", uri)
	}

	endpoint := &TemplateEndpoint{
		uri:       uri,
		path:      path,
		component: c,
	}

	query := parsedURL.Query()

	// Option contentCache: cache the template in memory.
	if val := query.Get("contentCache"); val != "" {
		if v, err := parseBool(val); err == nil {
			endpoint.contentCache = v
		}
	}

	// Option allowTemplateFromHeader: allow changing the template via header.
	if val := query.Get("allowTemplateFromHeader"); val != "" {
		if v, err := parseBool(val); err == nil {
			endpoint.allowTemplateFromHeader = v
		}
	}

	// Option encoding: specify the encoding (default UTF-8).
	if val := query.Get("encoding"); val != "" {
		endpoint.encoding = val
	} else {
		endpoint.encoding = "UTF-8"
	}

	// Option startDelimiter/endDelimiter: custom delimiters.
	if val := query.Get("startDelimiter"); val != "" {
		endpoint.startDelimiter = val
	}
	if val := query.Get("endDelimiter"); val != "" {
		endpoint.endDelimiter = val
	}

	return endpoint, nil
}

// parseBool parses a string as a boolean.
func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean value: %s", s)
}

// TemplateEndpoint represents a template endpoint
type TemplateEndpoint struct {
	uri                     string
	path                    string
	component               *TemplateComponent
	contentCache            bool
	allowTemplateFromHeader bool
	encoding                string
	startDelimiter          string
	endDelimiter            string
}

// URI returns the endpoint URI.
func (e *TemplateEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a template producer.
func (e *TemplateEndpoint) CreateProducer() (Producer, error) {
	return &TemplateProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer returns an error: the template component does not support consumers.
func (e *TemplateEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return nil, fmt.Errorf("the template component does not support consumers")
}

// TemplateProducer represents a template producer.
type TemplateProducer struct {
	endpoint     *TemplateEndpoint
	templateText string // Template loaded into memory when contentCache=true.
}

// Start starts the template producer.
func (p *TemplateProducer) Start(ctx context.Context) error {
	// If contentCache is enabled, load the template into memory.
	if p.endpoint.contentCache {
		content, err := os.ReadFile(p.endpoint.path)
		if err != nil {
			return fmt.Errorf("error reading template %s: %w", p.endpoint.path, err)
		}
		p.templateText = string(content)
	}
	return nil
}

// Stop stops the template producer.
func (p *TemplateProducer) Stop() error {
	// Clear the in-memory cache if needed.
	p.templateText = ""
	return nil
}

// Send performs the template transformation.
func (p *TemplateProducer) Send(exchange *Exchange) error {
	// Determine which template path to use.
	templatePath := p.endpoint.path
	if p.endpoint.allowTemplateFromHeader {
		if v, ok := exchange.GetHeader(CamelTemplatePath); ok && v != "" {
			if s, isString := v.(string); isString && s != "" {
				// Security: validate template path before using from header
				if strings.Contains(s, "..") {
					return fmt.Errorf("template: path contains traversal sequence: %s", s)
				}
				if strings.Contains(s, "\x00") {
					return fmt.Errorf("template: path contains null byte")
				}
				cleanPath := filepath.Clean(s)
				if strings.Contains(cleanPath, "..") {
					return fmt.Errorf("template: path contains traversal after cleaning: %s", s)
				}
				templatePath = cleanPath
			}
		}
	}

	// Retrieve the template content.
	var templateContent string
	var err error

	if p.endpoint.contentCache && !p.endpoint.allowTemplateFromHeader {
		// Use the cached template.
		templateContent = p.templateText
	} else {
		// Load from file.
		content, err := os.ReadFile(templatePath)
		if err != nil {
			return fmt.Errorf("error reading template %s: %w", templatePath, err)
		}
		templateContent = string(content)
	}

	// Prepare the template data.
	data := prepareTemplateData(exchange)

	// Parse and execute the template.
	result, err := p.executeTemplate(templateContent, data, templatePath)
	if err != nil {
		return fmt.Errorf("error executing template: %w", err)
	}

	// Set the result as the message body.
	exchange.GetIn().SetBody(result)
	return nil
}

// executeTemplate parses and executes a template.
func (p *TemplateProducer) executeTemplate(content string, data any, name string) (string, error) {
	// Check the cache if contentCache is enabled.
	if p.endpoint.contentCache {
		if tmpl := p.endpoint.component.getCachedTemplate(name); tmpl != nil {
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				return "", err
			}
			return buf.String(), nil
		}
	}

	// Create the parsing options.
	var tmpl *template.Template
	var err error

	// Use the base filename as the template name.
	templateName := filepath.Base(name)
	if templateName == "" {
		templateName = "template"
	}

	// If custom delimiters are specified, use them.
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
		return "", fmt.Errorf("error parsing template: %w", err)
	}

	// Cache the template if enabled.
	if p.endpoint.contentCache {
		p.endpoint.component.cacheTemplate(name, tmpl)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("error executing template: %w", err)
	}

	return buf.String(), nil
}

// getCachedTemplate retrieves a template from the cache.
func (c *TemplateComponent) getCachedTemplate(name string) *template.Template {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache[name]
}

// cacheTemplate stores a template in the cache.
func (c *TemplateComponent) cacheTemplate(name string, tmpl *template.Template) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[name] = tmpl
}

// prepareTemplateData prepares the data available to the template.
func prepareTemplateData(exchange *Exchange) *TemplateData {
	data := &TemplateData{}

	// Exchange information.
	data.Exchange.ID = fmt.Sprintf("%p", exchange) // Unique ID based on memory address.
	data.Exchange.Created = exchange.Created
	data.Exchange.Properties = exchange.Properties

	// Message body — convert []byte to string if necessary.
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
		// Convert []byte to string for headers as well.
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

// templateFuncs returns the functions available in templates.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// Utility functions.
		"upper":     strings.ToUpper,
		"lower":     strings.ToLower,
		"title":     strings.Title,
		"trim":      strings.TrimSpace,
		"join":      strings.Join,
		"split":     strings.Split,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"replace": func(old, new, s string) string {
			return strings.ReplaceAll(s, old, new)
		},
		"replaceN": strings.Replace,
		// Function for JSON-escaping values.
		"json": func(v any) template.HTML {
			switch val := v.(type) {
			case string:
				// Escape special JSON characters.
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
		// Function for marking content as safe (no HTML escaping).
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
		// Date functions.
		"now": time.Now,
		"formatDate": func(layout string, t time.Time) string {
			return t.Format(layout)
		},
		"formatTime": func(layout string, t time.Time) string {
			return t.Format(layout)
		},
		// Type conversion functions.
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

// toString converts any value to a string.
func toString(v any) string {
	return fmt.Sprintf("%v", v)
}
