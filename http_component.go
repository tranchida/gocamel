package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// HTTPComponent représente le composant HTTP
type HTTPComponent struct {
	client *http.Client
}

// NewHTTPComponent crée une nouvelle instance de HTTPComponent
func NewHTTPComponent() *HTTPComponent {
	return &HTTPComponent{
		client: &http.Client{},
	}
}

// CreateEndpoint crée un nouvel endpoint HTTP
func (c *HTTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	if _, err := url.Parse(uri); err != nil {
		return nil, fmt.Errorf("URI HTTP invalide: %v", err)
	}

	return &HTTPEndpoint{
		uri:    uri,
		client: c.client,
	}, nil
}

// HTTPEndpoint représente un endpoint HTTP
type HTTPEndpoint struct {
	uri    string
	client *http.Client
}

// URI retourne l'URI de l'endpoint
func (e *HTTPEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur HTTP
func (e *HTTPEndpoint) CreateProducer() (Producer, error) {
	return &HTTPProducer{
		uri:    e.uri,
		client: e.client,
	}, nil
}

// CreateConsumer crée un consommateur HTTP
func (e *HTTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &HTTPConsumer{
		uri:       e.uri,
		processor: processor,
	}, nil
}

// HTTPProducer représente un producteur HTTP
type HTTPProducer struct {
	uri    string
	client *http.Client
}

// Start démarre le producteur HTTP
func (p *HTTPProducer) Start(ctx context.Context) error {
	return nil // Pas d'initialisation nécessaire pour le producteur HTTP
}

// Stop arrête le producteur HTTP
func (p *HTTPProducer) Stop() error {
	return nil // Pas de nettoyage nécessaire pour le producteur HTTP
}

// Send envoie un message via HTTP
func (p *HTTPProducer) Send(exchange *Exchange) error {
	// Création de la requête
	var bodyReader io.Reader
	if body, ok := exchange.In.Body.([]byte); ok {
		bodyReader = bytes.NewReader(body)
	} else if body, ok := exchange.In.Body.(string); ok {
		bodyReader = bytes.NewReader([]byte(body))
	}

	req, err := http.NewRequest("POST", p.uri, bodyReader)
	if err != nil {
		return fmt.Errorf("erreur lors de la création de la requête HTTP: %v", err)
	}

	// Ajout des en-têtes
	for key, value := range exchange.In.Headers {
		if strValue, ok := value.(string); ok {
			req.Header.Set(key, strValue)
		}
	}

	// Envoi de la requête
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("erreur lors de l'envoi de la requête HTTP: %v", err)
	}
	defer resp.Body.Close()

	// Lecture du corps de la réponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("erreur lors de la lecture de la réponse HTTP: %v", err)
	}

	// Mise à jour de l'échange avec la réponse
	exchange.GetOut().SetBody(body)
	for key, value := range resp.Header {
		exchange.GetOut().SetHeader(key, value[0])
	}
	exchange.GetOut().SetHeader("Status-Code", strconv.Itoa(resp.StatusCode))

	return nil
}

// HTTPConsumer représente un consommateur HTTP
type HTTPConsumer struct {
	uri       string
	processor Processor
	server    *http.Server
}

// Start démarre le consommateur HTTP
func (c *HTTPConsumer) Start(ctx context.Context) error {
	parsedURL, err := url.Parse(c.uri)
	if err != nil {
		return fmt.Errorf("URI HTTP invalide: %v", err)
	}

	port := parsedURL.Port()
	if port == "" {
		port = "8080" // Port par défaut
	}

	mux := http.NewServeMux()
	mux.HandleFunc(parsedURL.Path, func(w http.ResponseWriter, r *http.Request) {
		exchange := NewExchange(ctx)

		// Lecture du corps de la requête
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Configuration de l'échange
		exchange.SetBody(body)
		for key, values := range r.Header {
			if len(values) > 0 {
				exchange.SetHeader(key, values[0])
			}
		}

		// Traitement du message
		if err := c.processor.Process(exchange); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Ajout des en-têtes de réponse
		for key, value := range exchange.Out.Headers {
			if strValue, ok := value.(string); ok {
				w.Header().Set(key, strValue)
			}
		}

		// Envoi de la réponse
		if body, ok := exchange.Out.Body.([]byte); ok {
			w.Write(body)
		} else if body, ok := exchange.Out.Body.(string); ok {
			w.Write([]byte(body))
		}
	})

	c.server = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Démarrage du serveur
	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Erreur du serveur HTTP: %v\n", err)
		}
	}()

	return nil
}

// Stop arrête le consommateur HTTP
func (c *HTTPConsumer) Stop() error {
	if c.server != nil {
		return c.server.Shutdown(context.Background())
	}
	return nil
}
