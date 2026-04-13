package gocamel

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	// Format de l'URI: file:///chemin/vers/fichier?options
	path := u.Path
	if u.Host != "" && u.Host != "." {
		path = u.Host + path
	}
	if path == "" {
		path = strings.TrimPrefix(uri, "file://")
		if qIdx := strings.Index(path, "?"); qIdx != -1 {
			path = path[:qIdx]
		}
	}
	if path == "" {
		return nil, fmt.Errorf("chemin de fichier manquant dans l'URI: %s", uri)
	}

	return &FileEndpoint{
		uri:  uri,
		url:  u,
		path: path,
		comp: c,
	}, nil
}

// FileEndpoint représente un endpoint File
type FileEndpoint struct {
	uri  string
	url  *url.URL
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
		path:      e.path,
		fileExist: ParseFileExist(e.url),
	}, nil
}

// CreateConsumer crée un consommateur File
func (e *FileEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &FileConsumer{
		path:      e.path,
		url:       e.url,
		processor: processor,
		comp:      e.comp,
	}, nil
}

// FileProducer représente un producteur File
type FileProducer struct {
	path      string
	fileExist FileExistBehavior
}

// Start démarre le producteur File
func (p *FileProducer) Start(ctx context.Context) error {
	return nil
}

// Stop arrête le producteur File
func (p *FileProducer) Stop() error {
	return nil
}

// Send écrit le contenu de l'échange dans un fichier selon l'option fileExist.
func (p *FileProducer) Send(exchange *Exchange) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return fmt.Errorf("erreur lors de la création du répertoire: %v", err)
	}

	switch p.fileExist {
	case FileExistFail:
		if _, err := os.Stat(p.path); err == nil {
			return fmt.Errorf("le fichier existe déjà: %s", p.path)
		}
	case FileExistIgnore:
		if _, err := os.Stat(p.path); err == nil {
			return nil
		}
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if p.fileExist == FileExistAppend {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}

	file, err := os.OpenFile(p.path, flags, 0644)
	if err != nil {
		return fmt.Errorf("erreur lors de l'ouverture du fichier: %v", err)
	}
	defer file.Close()

	var content []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		content = body
	case string:
		content = []byte(body)
	default:
		return fmt.Errorf("type de corps non supporté: %T", exchange.GetIn().GetBody())
	}

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("erreur lors de l'écriture dans le fichier: %v", err)
	}
	return nil
}

// FileConsumer représente un consommateur File
type FileConsumer struct {
	path      string
	url       *url.URL
	processor Processor
	comp      *FileComponent
	watcher   *fsnotify.Watcher
	stopChan  chan struct{}
}

// Start démarre le consommateur File
func (c *FileConsumer) Start(ctx context.Context) error {
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

	include := GetConfigValue(c.url, "include")
	exclude := GetConfigValue(c.url, "exclude")
	noop := strings.EqualFold(GetConfigValue(c.url, "noop"), "true")
	delete_ := strings.EqualFold(GetConfigValue(c.url, "delete"), "true")
	move := GetConfigValue(c.url, "move")
	moveFailed := GetConfigValue(c.url, "moveFailed")
	recursive := strings.EqualFold(GetConfigValue(c.url, "recursive"), "true")

	if err := watcher.Add(c.path); err != nil {
		return fmt.Errorf("erreur lors de l'ajout au watcher: %v", err)
	}

	// Si recursive, ajouter tous les sous-répertoires existants
	if recursive {
		filepath.Walk(c.path, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == c.path {
				return nil
			}
			if info.IsDir() {
				watcher.Add(path) // erreur ignorée volontairement
			}
			return nil
		})
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create != fsnotify.Create {
					continue
				}
				info, statErr := os.Stat(event.Name)
				if statErr != nil {
					continue
				}
				// Nouveau répertoire : l'ajouter au watcher si recursive
				if info.IsDir() {
					if recursive {
						watcher.Add(event.Name)
					}
					continue
				}
				filename := filepath.Base(event.Name)
				if !matchFileName(filename, include, exclude) {
					continue
				}

				content, err := os.ReadFile(event.Name)
				if err != nil {
					fmt.Printf("Erreur lors de la lecture du fichier %s: %v\n", event.Name, err)
					continue
				}

				exchange := NewExchange(ctx)
				exchange.SetBody(content)
				exchange.SetHeader("FileName", filename)
				exchange.SetHeader("FilePath", event.Name)

				if err := c.processor.Process(exchange); err != nil {
					if !errors.Is(err, ErrStopRouting) {
						fmt.Printf("Erreur lors du traitement du fichier %s: %v\n", event.Name, err)
						if !noop && moveFailed != "" {
							moveFileLocal(event.Name, moveFailed)
						}
					}
					continue
				}
				if !noop {
					if move != "" {
						moveFileLocal(event.Name, move)
					} else if delete_ {
						os.Remove(event.Name)
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("Erreur du watcher: %v\n", err)
			case <-c.stopChan:
				return
			}
		}
	}()

	return nil
}

// watchFile lit et traite un fichier unique, puis applique les options post-traitement.
func (c *FileConsumer) watchFile(ctx context.Context) error {
	filename := filepath.Base(c.path)
	include := GetConfigValue(c.url, "include")
	exclude := GetConfigValue(c.url, "exclude")
	if !matchFileName(filename, include, exclude) {
		return nil
	}

	content, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("erreur lors de la lecture du fichier: %v", err)
	}

	exchange := NewExchange(ctx)
	exchange.SetBody(content)
	exchange.SetHeader("FileName", filename)
	exchange.SetHeader("FilePath", c.path)

	noop := strings.EqualFold(GetConfigValue(c.url, "noop"), "true")
	delete_ := strings.EqualFold(GetConfigValue(c.url, "delete"), "true")
	move := GetConfigValue(c.url, "move")
	moveFailed := GetConfigValue(c.url, "moveFailed")

	if err := c.processor.Process(exchange); err != nil {
		if !errors.Is(err, ErrStopRouting) && !noop && moveFailed != "" {
			moveFileLocal(c.path, moveFailed)
		}
		return nil
	}
	if !noop {
		if move != "" {
			moveFileLocal(c.path, move)
		} else if delete_ {
			os.Remove(c.path)
		}
	}
	return nil
}

// Stop arrête le consommateur File
func (c *FileConsumer) Stop() error {
	if c.watcher != nil {
		close(c.stopChan)
		return c.watcher.Close()
	}
	return nil
}

// moveFileLocal déplace src vers destDir en créant le répertoire si nécessaire.
func moveFileLocal(src, destDir string) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		fmt.Printf("Erreur création répertoire %s: %v\n", destDir, err)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(src))
	if err := os.Rename(src, dest); err != nil {
		fmt.Printf("Erreur déplacement %s -> %s: %v\n", src, dest, err)
	}
}
