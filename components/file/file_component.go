package file

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	gocamel "gitlab.com/tranchida/gocamel"
)

// FileComponent represents the File component
type FileComponent struct{}

// NewFileComponent creates a new FileComponent
func NewFileComponent() *FileComponent {
	return &FileComponent{}
}

// CreateEndpoint creates a new endpoint File
func (c *FileComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
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

	// Delegate to the shared validator instead of a local substring test.
	// A plain strings.Contains(path, "..") check also rejects legitimate
	// filenames such as "report..v2.txt"; gocamel.ValidatePath walks the
	// components and only rejects a '..' that actually escapes the base.
	if err := gocamel.ValidatePath(path); err != nil {
		return nil, err
	}

	return &FileEndpoint{
		uri:  uri,
		url:  u,
		path: filepath.Clean(path),
	}, nil
}

// FileEndpoint represents a File endpoint
type FileEndpoint struct {
	uri  string
	url  *url.URL
	path string
}

// URI returns the endpoint URI
func (e *FileEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a File producer
func (e *FileEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &FileProducer{
		path:      e.path,
		fileExist: gocamel.ParseFileExist(e.url),
	}, nil
}

// CreateConsumer creates a File consumer
func (e *FileEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return &FileConsumer{
		path:      e.path,
		url:       e.url,
		processor: processor,
	}, nil
}

// FileProducer represents a File producer
type FileProducer struct {
	path      string
	fileExist gocamel.FileExistBehavior
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
// FileExistFail and FileExistIgnore use O_EXCL for an atomic existence check,
// avoiding the TOCTOU race that a separate os.Stat + os.OpenFile would introduce.
func (p *FileProducer) Send(exchange *gocamel.Exchange) error {
	// Re-validate the endpoint path before any filesystem access. The path is
	// already checked in CreateEndpoint (null byte + traversal); validating
	// again here keeps this write path self-contained (gosec G304).
	if err := gocamel.ValidatePath(p.path); err != nil {
		return err
	}

	// 0755 is the deliberate default for created parent directories: it keeps
	// produced files reachable by other users/processes (same rationale as the
	// 0644 file mode below) and the effective mode still respects the umask.
	// #nosec G301
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return fmt.Errorf("error creating directory: %v", err)
	}

	var content []byte
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		content = body
	case string:
		content = []byte(body)
	default:
		return fmt.Errorf("unsupported body type: %T", exchange.GetIn().GetBody())
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	switch p.fileExist {
	case gocamel.FileExistFail, gocamel.FileExistIgnore:
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	case gocamel.FileExistAppend:
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}

	// p.path was validated above via ValidatePath. 0644 is the deliberate
	// default: produced files are meant to be read by other processes, and the
	// effective mode still respects the process umask.
	file, err := os.OpenFile(p.path, flags, 0644) // #nosec G302,G304
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if p.fileExist == gocamel.FileExistIgnore {
				return nil
			}
			return fmt.Errorf("file already exists: %s", p.path)
		}
		return fmt.Errorf("error opening file: %v", err)
	}
	defer file.Close()

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("error writing to file: %v", err)
	}
	return nil
}

// FileConsumer represents a File consumer
type FileConsumer struct {
	path      string
	url       *url.URL
	processor gocamel.Processor
	watcher   *fsnotify.Watcher
	stopChan  chan struct{}
	wg        sync.WaitGroup
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

func (s *fileSynchronization) OnComplete(exchange *gocamel.Exchange) {
	if s.noop {
		return
	}
	if s.move != "" {
		moveFilelocal(s.path, s.move)
	} else if s.delete {
		os.Remove(s.path)
	}
}

func (s *fileSynchronization) OnFailure(exchange *gocamel.Exchange) {
	if errors.Is(exchange.Error, gocamel.ErrStopRouting) {
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

	include := gocamel.GetConfigValue(c.url, "include")
	exclude := gocamel.GetConfigValue(c.url, "exclude")
	noop := strings.EqualFold(gocamel.GetConfigValue(c.url, "noop"), "true")
	delete_ := strings.EqualFold(gocamel.GetConfigValue(c.url, "delete"), "true")
	move := gocamel.GetConfigValue(c.url, "move")
	moveFailed := gocamel.GetConfigValue(c.url, "moveFailed")
	recursive := strings.EqualFold(gocamel.GetConfigValue(c.url, "recursive"), "true")

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

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
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
				if !gocamel.MatchFileName(filename, include, exclude) {
					continue
				}

				// fsnotify reports Create as soon as the inode exists, long
				// before the writer has finished. Reading immediately yields a
				// truncated (often empty) body for anything not written
				// atomically — a shell redirect, a copy, an upload. Wait for
				// the size to stop changing first; this is the equivalent of
				// Camel's readLock=changed.
				if !waitForStableFile(ctx, event.Name, readLockTimeout, readLockInterval) {
					gocamel.LogWarn("file did not stabilise before the read lock timeout; skipping",
						"file", event.Name, "timeout", readLockTimeout)
					continue
				}

				content, err := os.ReadFile(event.Name)
				if err != nil {
					gocamel.LogError("error reading file", "file", event.Name, "error", err)
					continue
				}

				exchange := gocamel.NewExchange(ctx)
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

				_ = gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				gocamel.LogError("watcher error", "error", err)
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
	include := gocamel.GetConfigValue(c.url, "include")
	exclude := gocamel.GetConfigValue(c.url, "exclude")
	if !gocamel.MatchFileName(filename, include, exclude) {
		return nil
	}

	content, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("error reading file: %v", err)
	}

	exchange := gocamel.NewExchange(ctx)
	exchange.SetBody(content)
	exchange.SetHeader("FileName", filename)
	exchange.SetHeader("FilePath", c.path)

	noop := strings.EqualFold(gocamel.GetConfigValue(c.url, "noop"), "true")
	delete_ := strings.EqualFold(gocamel.GetConfigValue(c.url, "delete"), "true")
	move := gocamel.GetConfigValue(c.url, "move")
	moveFailed := gocamel.GetConfigValue(c.url, "moveFailed")

	exchange.AddSynchronization(&fileSynchronization{
		path:       c.path,
		noop:       noop,
		delete:     delete_,
		move:       move,
		moveFailed: moveFailed,
	})

	_ = gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
	return nil
}

// Stop stops the File consumer and waits for the watcher goroutine to exit.
func (c *FileConsumer) Stop() error {
	if c.watcher != nil {
		close(c.stopChan)
		err := c.watcher.Close()
		c.wg.Wait()
		return err
	}
	return nil
}

// Read-lock defaults: how long to wait for a file to stop growing before
// giving up, and how often to sample its size.
const (
	readLockTimeout  = 10 * time.Second
	readLockInterval = 100 * time.Millisecond
)

// waitForStableFile blocks until path's size and modification time stop
// changing between two consecutive samples, and reports whether the file
// settled before timeout elapsed (or the context was cancelled).
//
// This is a heuristic, not a lock: a writer that pauses longer than interval
// mid-write can still be observed as stable. It removes the common truncated
// read without requiring cooperation from the producer. For strict handoff,
// have the writer create the file under a temporary name and rename it into
// the watched directory — rename is atomic.
func waitForStableFile(ctx context.Context, path string, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)

	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	lastSize, lastMod := info.Size(), info.ModTime()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}

		info, err := os.Stat(path)
		if err != nil {
			return false // removed or replaced while we waited
		}
		if info.Size() == lastSize && info.ModTime().Equal(lastMod) {
			return true
		}
		lastSize, lastMod = info.Size(), info.ModTime()
	}
	return false
}

// moveFilelocal moves src to destDir, creating the directory if needed.
func moveFilelocal(src, destDir string) {
	// 0755 is the deliberate default for move directories (see Send).
	// #nosec G301
	if err := os.MkdirAll(destDir, 0755); err != nil {
		gocamel.LogError("error creating directory", "directory", destDir, "error", err)
		return
	}
	dest := filepath.Join(destDir, filepath.Base(src))
	if err := os.Rename(src, dest); err != nil {
		gocamel.LogError("error moving file", "src", src, "dest", dest, "error", err)
	}
}
