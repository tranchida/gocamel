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

// FileComponent represents the File component
type FileComponent struct {
	watchers map[string]*fsnotify.Watcher
}

// NewFileComponent creates a new FileComponent
func NewFileComponent() *FileComponent {
	return &FileComponent{
		watchers: make(map[string]*fsnotify.Watcher),
	}
}

// CreateEndpoint creates a new endpoint File
func (c *FileComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	// Format de l'URI: file:///path/vers/file?options
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
		return nil, fmt.Errorf("path de file missing in l'URI: %s", uri)
	}

	return &FileEndpoint{
		uri:  uri,
		url:  u,
		path: path,
		comp: c,
	}, nil
}

// FileEndpoint represents a File endpoint
type FileEndpoint struct {
	uri  string
	url  *url.URL
	path string
	comp *FileComponent
}

// URI returns the URI de l'endpoint
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

// FileProducer represents a producteur File
type FileProducer struct {
	path      string
	fileExist FileExistBehavior
}

// Start starts the producteur File
func (p *FileProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the producteur File
func (p *FileProducer) Stop() error {
	return nil
}

// Send écrit le contenu de l'échange in un file according to l'option fileExist.
func (p *FileProducer) Send(exchange *Exchange) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return fmt.Errorf("error during la creation du directory: %v", err)
	}

	switch p.fileExist {
	case FileExistFail:
		if _, err := os.Stat(p.path); err == nil {
			return fmt.Errorf("le file existe déjà: %s", p.path)
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
		return fmt.Errorf("error during l'ouverture du file: %v", err)
	}
	defer file.Close()

	var content []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		content = body
	case string:
		content = []byte(body)
	default:
		return fmt.Errorf("type de body non supported: %T", exchange.GetIn().GetBody())
	}

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("error during l'writing in le file: %v", err)
	}
	return nil
}

// FileConsumer represents a consommateur File
type FileConsumer struct {
	path      string
	url       *url.URL
	processor Processor
	comp      *FileComponent
	watcher   *fsnotify.Watcher
	stopChan  chan struct{}
}

// Start starts the consommateur File
func (c *FileConsumer) Start(ctx context.Context) error {
	info, err := os.Stat(c.path)
	if err != nil {
		return fmt.Errorf("error during l'accès au path: %v", err)
	}
	if info.IsDir() {
		return c.watchDirectory(ctx)
	}
	return c.watchFile(ctx)
}

// watchDirectory onveille un directory for les nouveaux files
func (c *FileConsumer) watchDirectory(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("error during la creation du watcher: %v", err)
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
		return fmt.Errorf("error during l'adding au watcher: %v", err)
	}

	// Si recursive, addinger tous les sous-directorys existants
	if recursive {
		filepath.Walk(c.path, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == c.path {
				return nil
			}
			if info.IsDir() {
				watcher.Add(path) // error ignorée volontairement
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
				// Nouveau directory : l'addinger au watcher si recursive
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
					fmt.Printf("error during la reading du file %s: %v\n", event.Name, err)
					continue
				}

				exchange := NewExchange(ctx)
				exchange.SetBody(content)
				exchange.SetHeader("FileName", filename)
				exchange.SetHeader("FilePath", event.Name)

				if err := c.processor.Process(exchange); err != nil {
					if !errors.Is(err, ErrStopRouting) {
						fmt.Printf("error during traitement du file %s: %v\n", event.Name, err)
						if !noop && moveFailed != "" {
							moveFilelocal(event.Name, moveFailed)
						}
					}
					continue
				}
				if !noop {
					if move != "" {
						moveFilelocal(event.Name, move)
					} else if delete_ {
						os.Remove(event.Name)
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Printf("error du watcher: %v\n", err)
			case <-c.stopChan:
				return
			}
		}
	}()

	return nil
}

// watchFile lit et traite un file unique, puis applique les options post-traitement.
func (c *FileConsumer) watchFile(ctx context.Context) error {
	filename := filepath.Base(c.path)
	include := GetConfigValue(c.url, "include")
	exclude := GetConfigValue(c.url, "exclude")
	if !matchFileName(filename, include, exclude) {
		return nil
	}

	content, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("error during la reading du file: %v", err)
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
			moveFilelocal(c.path, moveFailed)
		}
		return nil
	}
	if !noop {
		if move != "" {
			moveFilelocal(c.path, move)
		} else if delete_ {
			os.Remove(c.path)
		}
	}
	return nil
}

// Stop stops the consommateur File
func (c *FileConsumer) Stop() error {
	if c.watcher != nil {
		close(c.stopChan)
		return c.watcher.Close()
	}
	return nil
}

// moveFilelocal déplace src vers destDir en créant le directory si nécessaire.
func moveFilelocal(src, destDir string) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		fmt.Printf("error creation directory %s: %v\n", destDir, err)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(src))
	if err := os.Rename(src, dest); err != nil {
		fmt.Printf("error déplacement %s -> %s: %v\n", src, dest, err)
	}
}
