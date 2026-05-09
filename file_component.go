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

	// URI format: file:///path/to/file?options
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
		return nil, fmt.Errorf("file path missing in URI: %s", uri)
	}

	// Security: validate path for directory traversal
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("path contains traversal sequence: %s", path)
	}

	// Clean the path
	cleanPath := filepath.Clean(path)
	if strings.Contains(cleanPath, "..") {
		return nil, fmt.Errorf("path contains traversal sequence after cleaning: %s", path)
	}

	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return nil, fmt.Errorf("path contains null byte")
	}

	return &FileEndpoint{
		uri:  uri,
		url:  u,
		path: cleanPath,
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

// URI returns the endpoint URI
func (e *FileEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a File producer
func (e *FileEndpoint) CreateProducer() (Producer, error) {
	return &FileProducer{
		path:      e.path,
		fileExist: ParseFileExist(e.url),
	}, nil
}

// CreateConsumer creates a File consumer
func (e *FileEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &FileConsumer{
		path:      e.path,
		url:       e.url,
		processor: processor,
		comp:      e.comp,
	}, nil
}

// FileProducer represents a File producer
type FileProducer struct {
	path      string
	fileExist FileExistBehavior
}

// Start starts the File producer
func (p *FileProducer) Start(ctx context.Context) error {
	return nil
}

// Stop stops the File producer
func (p *FileProducer) Stop() error {
	return nil
}

// Send writes the exchange content to a file according to the fileExist option.
func (p *FileProducer) Send(exchange *Exchange) error {
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return fmt.Errorf("error creating directory: %v", err)
	}

	switch p.fileExist {
	case FileExistFail:
		if _, err := os.Stat(p.path); err == nil {
			return fmt.Errorf("file already exists: %s", p.path)
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
		return fmt.Errorf("error opening file: %v", err)
	}
	defer file.Close()

	var content []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		content = body
	case string:
		content = []byte(body)
	default:
		return fmt.Errorf("unsupported body type: %T", exchange.GetIn().GetBody())
	}

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}
	return nil
}

// FileConsumer represents a File consumer
type FileConsumer struct {
	path      string
	url       *url.URL
	processor Processor
	comp      *FileComponent
	watcher   *fsnotify.Watcher
	stopChan  chan struct{}
}

// Start starts the File consumer
func (c *FileConsumer) Start(ctx context.Context) error {
	info, err := os.Stat(c.path)
	if err != nil {
		return fmt.Errorf("error accessing path: %v", err)
	}
	if info.IsDir() {
		return c.watchDirectory(ctx)
	}
	return c.watchFile(ctx)
}

type fileSynchronization struct {
	path       string
	noop       bool
	delete     bool
	move       string
	moveFailed string
}

func (s *fileSynchronization) OnComplete(exchange *Exchange) {
	if s.noop {
		return
	}
	if s.move != "" {
		moveFilelocal(s.path, s.move)
	} else if s.delete {
		os.Remove(s.path)
	}
}

func (s *fileSynchronization) OnFailure(exchange *Exchange) {
	if errors.Is(exchange.Error, ErrStopRouting) {
		s.OnComplete(exchange)
		return
	}

	if !s.noop && s.moveFailed != "" {
		moveFilelocal(s.path, s.moveFailed)
	}
}

// watchDirectory watches a directory for new files
func (c *FileConsumer) watchDirectory(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("error creating watcher: %v", err)
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
		return fmt.Errorf("error adding path to watcher: %v", err)
	}

	// If recursive, also add all existing subdirectories
	if recursive {
		filepath.Walk(c.path, func(path string, info os.FileInfo, err error) error {
			if err != nil || path == c.path {
				return nil
			}
			if info.IsDir() {
				watcher.Add(path) // error intentionally ignored
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
				// New directory: add to watcher if recursive
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
					logError("error reading file", "file", event.Name, "error", err)
					continue
				}

				exchange := NewExchange(ctx)
				exchange.SetBody(content)
				exchange.SetHeader("FileName", filename)
				exchange.SetHeader("FilePath", event.Name)

				exchange.AddSynchronization(&fileSynchronization{
					path:       event.Name,
					noop:       noop,
					delete:     delete_,
					move:       move,
					moveFailed: moveFailed,
				})

				err = c.processor.Process(exchange)
				exchange.Done(err)

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logError("watcher error", "error", err)
			case <-c.stopChan:
				return
			}
		}
	}()

	return nil
}

// watchFile reads and processes a single file, then applies post-processing options.
func (c *FileConsumer) watchFile(ctx context.Context) error {
	filename := filepath.Base(c.path)
	include := GetConfigValue(c.url, "include")
	exclude := GetConfigValue(c.url, "exclude")
	if !matchFileName(filename, include, exclude) {
		return nil
	}

	content, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("error reading file: %v", err)
	}

	exchange := NewExchange(ctx)
	exchange.SetBody(content)
	exchange.SetHeader("FileName", filename)
	exchange.SetHeader("FilePath", c.path)

	noop := strings.EqualFold(GetConfigValue(c.url, "noop"), "true")
	delete_ := strings.EqualFold(GetConfigValue(c.url, "delete"), "true")
	move := GetConfigValue(c.url, "move")
	moveFailed := GetConfigValue(c.url, "moveFailed")

	exchange.AddSynchronization(&fileSynchronization{
		path:       c.path,
		noop:       noop,
		delete:     delete_,
		move:       move,
		moveFailed: moveFailed,
	})

	procErr := c.processor.Process(exchange)
	exchange.Done(procErr)
	return nil
}

// Stop stops the File consumer
func (c *FileConsumer) Stop() error {
	if c.watcher != nil {
		close(c.stopChan)
		return c.watcher.Close()
	}
	return nil
}

// moveFilelocal moves src to destDir, creating the directory if needed.
func moveFilelocal(src, destDir string) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		logError("error creating directory", "directory", destDir, "error", err)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(src))
	if err := os.Rename(src, dest); err != nil {
		logError("error moving file", "src", src, "dest", dest, "error", err)
	}
}
