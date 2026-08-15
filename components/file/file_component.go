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
	targetPath := p.path
	info, statErr := os.Stat(targetPath)
	if (statErr == nil && info.IsDir()) || (statErr != nil && strings.HasSuffix(targetPath, string(filepath.Separator))) {
		if fileName, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			targetPath = filepath.Join(targetPath, fmt.Sprintf("%v", fileName))
		}
	}

	// Re-validate the target path before any filesystem access.
	if err := gocamel.ValidatePath(targetPath); err != nil {
		return err
	}

	// 0755 is the deliberate default for created parent directories: it keeps
	// produced files reachable by other users/processes (same rationale as the
	// 0644 file mode below) and the effective mode still respects the umask.
	// #nosec G301
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
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

	// targetPath was validated above via ValidatePath. 0644 is the deliberate
	// default: produced files are meant to be read by other processes, and the
	// effective mode still respects the process umask.
	file, err := os.OpenFile(targetPath, flags, 0644) // #nosec G302,G304
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			if p.fileExist == gocamel.FileExistIgnore {
				return nil
			}
			return fmt.Errorf("file already exists: %s", targetPath)
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
	mu        sync.Mutex
	ignored   map[string]struct{}
}

func (c *FileConsumer) ignoreNext(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ignored == nil {
		c.ignored = make(map[string]struct{})
	}
	c.ignored[path] = struct{}{}
}

func (c *FileConsumer) checkAndClearIgnored(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ignored != nil {
		if _, ok := c.ignored[path]; ok {
			delete(c.ignored, path)
			return true
		}
	}
	return false
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
	consumer   *FileConsumer
	path       string
	lockPath   string
	markerPath string
	noop       bool
	delete     bool
	move       string
	moveFailed string
}

func (s *fileSynchronization) cleanupLock() {
	if s.markerPath != "" {
		_ = os.Remove(s.markerPath)
	}
}

func (s *fileSynchronization) OnComplete(exchange *gocamel.Exchange) {
	defer s.cleanupLock()
	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if s.noop {
		if s.lockPath != "" {
			if s.consumer != nil {
				s.consumer.ignoreNext(s.path)
			}
			_ = os.Rename(s.lockPath, s.path)
		}
		return
	}
	if s.move != "" {
		moveFilelocal(effectivePath, s.move)
	} else if s.delete {
		os.Remove(effectivePath)
	} else if s.lockPath != "" {
		if s.consumer != nil {
			s.consumer.ignoreNext(s.path)
		}
		_ = os.Rename(s.lockPath, s.path)
	}
}

func (s *fileSynchronization) OnFailure(exchange *gocamel.Exchange) {
	if errors.Is(exchange.Error, gocamel.ErrStopRouting) {
		s.OnComplete(exchange)
		return
	}
	defer s.cleanupLock()

	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if !s.noop && s.moveFailed != "" {
		moveFilelocal(effectivePath, s.moveFailed)
	} else if s.lockPath != "" {
		if s.consumer != nil {
			s.consumer.ignoreNext(s.path)
		}
		_ = os.Rename(s.lockPath, s.path)
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

	opts := gocamel.ParsePollingOptions(c.url)
	// For local file watching, default interval is 100ms if not explicitly overridden
	if gocamel.GetConfigValue(c.url, "readLockCheckInterval") == "" && gocamel.GetConfigValue(c.url, "readLockInterval") == "" {
		opts.ReadLockCheckInterval = 100 * time.Millisecond
	}
	// For file component watcher, default strategy is "changed" unless explicitly set to "none"
	readLockParam := gocamel.GetConfigValue(c.url, "readLock")
	if readLockParam == "" {
		opts.ReadLock = gocamel.ReadLockChanged
	}

	if err := watcher.Add(c.path); err != nil {
		return fmt.Errorf("error adding path to watcher: %v", err)
	}

	// If recursive, also add all existing subdirectories
	if opts.Recursive {
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
				if c.checkAndClearIgnored(event.Name) {
					continue
				}
				info, statErr := os.Stat(event.Name)
				if statErr != nil {
					continue
				}
				// New directory: add to watcher if recursive
				if info.IsDir() {
					if opts.Recursive {
						watcher.Add(event.Name)
					}
					continue
				}
				filename := filepath.Base(event.Name)
				if strings.HasSuffix(filename, ".camelLock") || strings.HasSuffix(filename, ".camelExclusiveReadLock") {
					continue
				}
				if !gocamel.MatchFileName(filename, opts.Include, opts.Exclude) {
					continue
				}

				var lockPath string
				var markerPath string

				switch opts.ReadLock {
				case gocamel.ReadLockChanged:
					statFn := func() (int64, time.Time, error) {
						fi, err := os.Stat(event.Name)
						if err != nil {
							return 0, time.Time{}, err
						}
						return fi.Size(), fi.ModTime(), nil
					}
					if !gocamel.WaitForStable(ctx, statFn, opts.ReadLockTimeout, opts.ReadLockCheckInterval, opts.ReadLockMinAge, opts.ReadLockMinLength) {
						gocamel.LogWarn("file did not stabilise before the read lock timeout; skipping",
							"file", event.Name, "timeout", opts.ReadLockTimeout)
						continue
					}

				case gocamel.ReadLockRename:
					targetLockPath := event.Name + ".camelExclusiveReadLock"
					if err := os.Rename(event.Name, targetLockPath); err != nil {
						gocamel.LogWarn("could not acquire file rename read lock; skipping", "file", event.Name, "error", err)
						continue
					}
					lockPath = targetLockPath

				case gocamel.ReadLockMarkerFile:
					targetMarkerPath := event.Name + ".camelLock"
					if _, err := os.Stat(targetMarkerPath); err == nil {
						continue
					}
					mf, err := os.OpenFile(targetMarkerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G302,G304
					if err != nil {
						gocamel.LogWarn("could not create marker file for file read lock; skipping", "file", event.Name, "error", err)
						continue
					}
					_ = mf.Close()
					markerPath = targetMarkerPath
				}

				readPath := event.Name
				if lockPath != "" {
					readPath = lockPath
				}

				content, err := os.ReadFile(readPath) // #nosec G304
				if err != nil {
					gocamel.LogError("error reading file", "file", readPath, "error", err)
					if markerPath != "" {
						_ = os.Remove(markerPath)
					}
					if lockPath != "" {
						_ = os.Rename(lockPath, event.Name)
					}
					continue
				}

				exchange := gocamel.NewExchange(ctx)
				exchange.SetBody(content)
				exchange.SetHeader(gocamel.CamelFileName, filename)
				exchange.SetHeader(gocamel.CamelFilePath, event.Name)
				exchange.SetHeader(gocamel.CamelFileLength, info.Size())
				exchange.SetHeader(gocamel.CamelFileLastModified, info.ModTime())
				// Compatibility alias
				exchange.SetHeader("FileName", filename)
				exchange.SetHeader("FilePath", event.Name)

				exchange.AddSynchronization(&fileSynchronization{
					consumer:   c,
					path:       event.Name,
					lockPath:   lockPath,
					markerPath: markerPath,
					noop:       opts.Noop,
					delete:     opts.Delete,
					move:       opts.Move,
					moveFailed: opts.MoveFailed,
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
	opts := gocamel.ParsePollingOptions(c.url)
	filename := filepath.Base(c.path)
	if !gocamel.MatchFileName(filename, opts.Include, opts.Exclude) {
		return nil
	}

	info, statErr := os.Stat(c.path)
	content, err := os.ReadFile(c.path)
	if err != nil {
		return fmt.Errorf("error reading file: %v", err)
	}

	exchange := gocamel.NewExchange(ctx)
	exchange.SetBody(content)
	exchange.SetHeader(gocamel.CamelFileName, filename)
	exchange.SetHeader(gocamel.CamelFilePath, c.path)
	if statErr == nil {
		exchange.SetHeader(gocamel.CamelFileLength, info.Size())
		exchange.SetHeader(gocamel.CamelFileLastModified, info.ModTime())
	}
	// Compatibility alias
	exchange.SetHeader("FileName", filename)
	exchange.SetHeader("FilePath", c.path)

	exchange.AddSynchronization(&fileSynchronization{
		path:       c.path,
		noop:       opts.Noop,
		delete:     opts.Delete,
		move:       opts.Move,
		moveFailed: opts.MoveFailed,
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

// waitForStableFile blocks until path's size and modification time stop
// changing between two consecutive samples, and reports whether the file
// settled before timeout elapsed (or the context was cancelled).
func waitForStableFile(ctx context.Context, path string, timeout, interval time.Duration) bool {
	return gocamel.WaitForStable(ctx, func() (int64, time.Time, error) {
		info, err := os.Stat(path)
		if err != nil {
			return 0, time.Time{}, err
		}
		return info.Size(), info.ModTime(), nil
	}, timeout, interval, 0, 0)
}

// moveFilelocal moves src to destDir, creating the directory if needed.
func moveFilelocal(src, destDir string) {
	// 0755 is the deliberate default for move directories (see Send).
	// #nosec G301
	if err := os.MkdirAll(destDir, 0755); err != nil {
		gocamel.LogError("error creating directory", "directory", destDir, "error", err)
		return
	}
	filename := filepath.Base(src)
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	dest := filepath.Join(destDir, filename)
	if err := os.Rename(src, dest); err != nil {
		gocamel.LogError("error moving file", "src", src, "dest", dest, "error", err)
	}
}
