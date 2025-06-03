package gocamel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// FileComponent représente le composant File
type FileComponent struct {
	watchers map[string]*fsnotify.Watcher
}

// NewFileComponent crée une nouvelle instance de FileComponent
func NewFileComponent() *FileComponent {
	return &FileComponent{
		watchers: make(map[string]*fsnotify.Watcher),
	}
}

// CreateEndpoint crée un nouvel endpoint File
func (c *FileComponent) CreateEndpoint(uri string) (Endpoint, error) {
	// Format de l'URI: file:///chemin/vers/fichier?options
	path := strings.TrimPrefix(uri, "file://")
	if path == "" {
		return nil, fmt.Errorf("chemin de fichier manquant dans l'URI: %s", uri)
	}

	return &FileEndpoint{
		uri:  uri,
		path: path,
		comp: c,
	}, nil
}

// FileEndpoint représente un endpoint File
type FileEndpoint struct {
	uri  string
	path string
	comp *FileComponent
}

// URI retourne l'URI de l'endpoint
func (e *FileEndpoint) URI() string {
	return e.uri
}

// CreateProducer crée un producteur File
func (e *FileEndpoint) CreateProducer() (Producer, error) {
	return &FileProducer{
		path: e.path,
	}, nil
}

// CreateConsumer crée un consommateur File
func (e *FileEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &FileConsumer{
		path:      e.path,
		processor: processor,
		comp:      e.comp,
	}, nil
}

// FileProducer représente un producteur File
type FileProducer struct {
	path string
}

// Start démarre le producteur File
func (p *FileProducer) Start(ctx context.Context) error {
	return nil
}

// Stop arrête le producteur File
func (p *FileProducer) Stop() error {
	return nil
}

// Send écrit le contenu de l'échange dans un fichier
func (p *FileProducer) Send(exchange *Exchange) error {
	// Création du répertoire parent si nécessaire
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("erreur lors de la création du répertoire: %v", err)
	}

	// Ouverture du fichier en mode écriture
	file, err := os.OpenFile(p.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("erreur lors de l'ouverture du fichier: %v", err)
	}
	defer file.Close()

	// Écriture du contenu
	var content []byte
	switch body := exchange.In.Body.(type) {
	case []byte:
		content = body
	case string:
		content = []byte(body)
	default:
		return fmt.Errorf("type de corps non supporté: %T", exchange.In.Body)
	}

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("erreur lors de l'écriture dans le fichier: %v", err)
	}

	return nil
}

// FileConsumer représente un consommateur File
type FileConsumer struct {
	path      string
	processor Processor
	comp      *FileComponent
	watcher   *fsnotify.Watcher
	stopChan  chan struct{}
}

// Start démarre le consommateur File
func (c *FileConsumer) Start(ctx context.Context) error {
	// Vérification si le chemin est un répertoire
	info, err := os.Stat(c.path)
	if err != nil {
		return fmt.Errorf("erreur lors de l'accès au chemin: %v", err)
	}

	if info.IsDir() {
		return c.watchDirectory(ctx)
	}

	return c.watchFile(ctx)
}

// watchDirectory surveille un répertoire pour les nouveaux fichiers
func (c *FileConsumer) watchDirectory(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("erreur lors de la création du watcher: %v", err)
	}

	c.watcher = watcher
	c.stopChan = make(chan struct{})

	// Surveillance du répertoire
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsnotify.Create == fsnotify.Create {
					// Traitement du nouveau fichier
					exchange := NewExchange(ctx)
					content, err := os.ReadFile(event.Name)
					if err != nil {
						fmt.Printf("Erreur lors de la lecture du fichier %s: %v\n", event.Name, err)
						continue
					}

					exchange.SetBody(content)
					exchange.SetHeader("FileName", filepath.Base(event.Name))
					exchange.SetHeader("FilePath", event.Name)

					if err := c.processor.Process(exchange); err != nil {
						fmt.Printf("Erreur lors du traitement du fichier %s: %v\n", event.Name, err)
					}
				}
			case err := <-watcher.Errors:
				fmt.Printf("Erreur du watcher: %v\n", err)
			case <-c.stopChan:
				return
			}
		}
	}()

	return watcher.Add(c.path)
}

// watchFile surveille un fichier spécifique
func (c *FileConsumer) watchFile(ctx context.Context) error {
	// Lecture initiale du fichier
	content, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("erreur lors de la lecture du fichier: %v", err)
	}

	exchange := NewExchange(ctx)
	exchange.SetBody(content)
	exchange.SetHeader("FileName", filepath.Base(c.path))
	exchange.SetHeader("FilePath", c.path)

	return c.processor.Process(exchange)
}

// Stop arrête le consommateur File
func (c *FileConsumer) Stop() error {
	if c.watcher != nil {
		close(c.stopChan)
		return c.watcher.Close()
	}
	return nil
}
