package file

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
		uri:       uri,
		url:       u,
		path:      filepath.Clean(path),
		directory: strings.HasSuffix(path, string(filepath.Separator)),
	}, nil
}

// FileEndpoint represents a File endpoint
type FileEndpoint struct {
	directory bool // preserves a trailing separator before path normalization
	uri       string
	url       *url.URL
	path      string
}

// URI returns the endpoint URI
func (e *FileEndpoint) URI() string {
	return e.uri
}

// CreateProducer creates a File producer
func (e *FileEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &FileProducer{
		path:      e.path,
		directory: e.directory,
		fileExist: gocamel.ParseFileExist(e.url),
	}, nil
}

// CreateConsumer creates a File consumer
func (e *FileEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	maxSeen := DefaultMaxSeenEntries
	if v := gocamel.GetConfigValue(e.url, "maxSeenEntries"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid maxSeenEntries %q: must be a non-negative integer", v)
		}
		maxSeen = n
	}
	return &FileConsumer{
		path:           e.path,
		url:            e.url,
		processor:      processor,
		maxSeenEntries: maxSeen,
	}, nil
}

// DefaultMaxSeenEntries is the default cap on the consumer's dedup map.
//
// The maxSeenEntries URI option bounds how many ingested files the consumer
// remembers to skip duplicate Create+Write events. When the cap is exceeded the
// oldest-remembered entries are evicted first (FIFO by first ingestion), so an
// evicted file that is modified or touched again may be re-ingested. 0 means
// unlimited (the map grows for the consumer's lifetime).
const DefaultMaxSeenEntries = 100_000

// FileProducer represents a File producer
type FileProducer struct {
	directory bool
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
	if (statErr == nil && info.IsDir()) || (statErr != nil && p.directory) {
		if fileName, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			name := fmt.Sprintf("%v", fileName)
			if err := confinedFileName(name); err != nil {
				return err
			}
			targetPath = filepath.Join(targetPath, name)
		}
	}

	// Confine the write to the endpoint directory: ValidatePath alone accepts
	// "/inbox/../../etc/passwd" (depth never drops below zero on an absolute
	// path) and filepath.Join drops the base when CamelFileName is absolute.
	if err := gocamel.ValidatePathInDir(targetPath, p.path); err != nil {
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

	if p.fileExist == gocamel.FileExistAppend {
		return writeDirect(targetPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, content)
	}

	// Override, Fail and Ignore write a hidden temporary file next to the
	// target and publish it in one step, so a consumer polling the directory
	// never reads a half-written file. Override renames over the target;
	// Fail and Ignore hard-link it, which fails atomically if the target
	// already exists (the O_EXCL semantics without the partial-file window).
	tmpPath, err := writeTemp(targetPath, content)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath) // no-op after a successful rename

	if p.fileExist == gocamel.FileExistOverride || p.fileExist == "" {
		if err := os.Rename(tmpPath, targetPath); err != nil {
			return fmt.Errorf("error publishing file %s: %w", targetPath, err)
		}
		return nil
	}

	err = os.Link(tmpPath, targetPath)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrExist):
		if p.fileExist == gocamel.FileExistIgnore {
			return nil
		}
		return fmt.Errorf("file already exists: %s", targetPath)
	default:
		// Hard links are unsupported on some filesystems (FAT, some network
		// shares): fall back to an exclusive direct write.
		err := writeDirect(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, content)
		if errors.Is(err, os.ErrExist) {
			if p.fileExist == gocamel.FileExistIgnore {
				return nil
			}
			return fmt.Errorf("file already exists: %s", targetPath)
		}
		return err
	}
}

// tempFileSuffix marks the producer's in-progress files; the consumer skips them.
const tempFileSuffix = ".camelTmp"

// producedFileMode is the mode of produced files. 0644 is the deliberate
// default: produced files are meant to be read by other processes.
const producedFileMode = 0644

// writeTemp writes content to a new hidden temporary file in the directory of
// targetPath and returns its path. The file is fully written and closed (with
// the close error checked) before it is published.
func writeTemp(targetPath string, content []byte) (string, error) {
	dir, base := filepath.Split(targetPath)
	if dir == "" {
		dir = "."
	}
	// CreateTemp opens with O_EXCL, so it never follows a planted symlink.
	tmp, err := os.CreateTemp(dir, "."+base+".*"+tempFileSuffix)
	if err != nil {
		return "", fmt.Errorf("error creating temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	fail := func(err error) (string, error) {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if _, err := tmp.Write(content); err != nil {
		return fail(fmt.Errorf("error writing to file: %w", err))
	}
	if err := tmp.Chmod(producedFileMode); err != nil {
		return fail(fmt.Errorf("error setting file mode: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("error closing file: %w", err)
	}
	return tmpPath, nil
}

// writeDirect writes content to path in place (append mode, or the fallback
// when hard links are unavailable). The close error is returned: on network
// filesystems it is where a failed write surfaces.
func writeDirect(path string, flags int, content []byte) error {
	// path was confined by the caller via ValidatePathInDir.
	file, err := confinedOpen(path, flags, producedFileMode) // #nosec G302,G304
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return err
		}
		return fmt.Errorf("error opening file: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("error writing to file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("error closing file: %w", err)
	}
	return nil
}

// FileConsumer represents a File consumer
type fileStamp struct {
	size int64
	mod  time.Time
}

type FileConsumer struct {
	path      string
	url       *url.URL
	processor gocamel.Processor
	watcher   *fsnotify.Watcher
	stopChan  chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	ignored   map[string]struct{}
	seen      map[string]fileStamp // last ingested size+mtime; skips Create+Write duplicates
	seenOrder []string             // insertion order of seen keys, oldest first; guarded by mu

	maxSeenEntries int // cap on len(seen); 0 = unlimited
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

	// Snapshot files already in the directory so a consumer started against a
	// non-empty inbox does not wait for a later Create event.
	filepath.Walk(c.path, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !opts.Recursive && filepath.Dir(path) != c.path {
			return nil
		}
		c.ingest(ctx, path, opts, false)
		return nil
	})

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
					continue
				}
				c.ingest(ctx, event.Name, opts, true)
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

func (c *FileConsumer) ingest(ctx context.Context, path string, opts gocamel.PollingOptions, waitForLock bool) {
	if c.checkAndClearIgnored(path) {
		return
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		gocamel.LogWarn("file consumer: skipping symlink", "file", path)
		return
	}
	if info.IsDir() {
		if opts.Recursive && c.watcher != nil {
			_ = c.watcher.Add(path)
		}
		return
	}
	if info.Size() > gocamel.DefaultMaxBodySize {
		gocamel.LogWarn("file consumer: skipping oversized file", "file", path, "size", info.Size(), "limit", gocamel.DefaultMaxBodySize)
		return
	}
	filename := filepath.Base(path)
	if strings.HasSuffix(filename, ".camelLock") || strings.HasSuffix(filename, ".camelExclusiveReadLock") ||
		strings.HasSuffix(filename, tempFileSuffix) {
		return
	}
	if !gocamel.MatchFileName(filename, opts.Include, opts.Exclude) {
		return
	}
	if c.alreadySeen(path, info.Size(), info.ModTime()) {
		return
	}

	var lockPath string
	var markerPath string

	switch opts.ReadLock {
	case gocamel.ReadLockChanged:
		if waitForLock {
			statFn := func() (int64, time.Time, error) {
				fi, err := os.Stat(path)
				if err != nil {
					return 0, time.Time{}, err
				}
				return fi.Size(), fi.ModTime(), nil
			}
			if !gocamel.WaitForStable(ctx, statFn, opts.ReadLockTimeout, opts.ReadLockCheckInterval, opts.ReadLockMinAge, opts.ReadLockMinLength) {
				gocamel.LogWarn("file did not stabilise before the read lock timeout; skipping",
					"file", path, "timeout", opts.ReadLockTimeout)
				return
			}
		}
	case gocamel.ReadLockRename:
		targetLockPath := path + ".camelExclusiveReadLock"
		if err := os.Rename(path, targetLockPath); err != nil {
			gocamel.LogWarn("could not acquire file rename read lock; skipping", "file", path, "error", err)
			return
		}
		lockPath = targetLockPath
	case gocamel.ReadLockMarkerFile:
		targetMarkerPath := path + ".camelLock"
		if _, err := os.Stat(targetMarkerPath); err == nil {
			return
		}
		mf, err := os.OpenFile(targetMarkerPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G302,G304
		if err != nil {
			gocamel.LogWarn("could not create marker file for file read lock; skipping", "file", path, "error", err)
			return
		}
		_ = mf.Close()
		markerPath = targetMarkerPath
	}

	readPath := path
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
			_ = os.Rename(lockPath, path)
		}
		return
	}

	if restat, err := os.Stat(readPath); err == nil {
		info = restat
	}
	c.remember(path, info.Size(), info.ModTime())

	exchange := gocamel.NewExchange(ctx)
	exchange.SetBody(content)
	exchange.SetHeader(gocamel.CamelFileName, filename)
	exchange.SetHeader(gocamel.CamelFilePath, path)
	exchange.SetHeader(gocamel.CamelFileLength, info.Size())
	exchange.SetHeader(gocamel.CamelFileLastModified, info.ModTime())
	exchange.SetHeader("FileName", filename)
	exchange.SetHeader("FilePath", path)

	exchange.AddSynchronization(&fileSynchronization{
		consumer:   c,
		path:       path,
		lockPath:   lockPath,
		markerPath: markerPath,
		noop:       opts.Noop,
		delete:     opts.Delete,
		move:       opts.Move,
		moveFailed: opts.MoveFailed,
	})

	_ = gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))
}

func (c *FileConsumer) alreadySeen(path string, size int64, mod time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.seen[path]
	return ok && st.size == size && st.mod.Equal(mod)
}

func (c *FileConsumer) remember(path string, size int64, mod time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[string]fileStamp)
	}
	if _, ok := c.seen[path]; !ok {
		c.seenOrder = append(c.seenOrder, path)
	}
	c.seen[path] = fileStamp{size: size, mod: mod}
	for c.maxSeenEntries > 0 && len(c.seen) > c.maxSeenEntries {
		delete(c.seen, c.seenOrder[0])
		c.seenOrder[0] = ""
		c.seenOrder = c.seenOrder[1:]
	}
}

// watchFile reads and processes a single file, then applies post-processing options.
func (c *FileConsumer) watchFile(ctx context.Context) error {
	opts := gocamel.ParsePollingOptions(c.url)
	filename := filepath.Base(c.path)
	if !gocamel.MatchFileName(filename, opts.Include, opts.Exclude) {
		return nil
	}

	info, statErr := os.Lstat(c.path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("file consumer: refusing to read symlink %s", c.path)
	}
	if statErr == nil && info.Size() > gocamel.DefaultMaxBodySize {
		return fmt.Errorf("file consumer: file %s exceeds %d bytes", c.path, gocamel.DefaultMaxBodySize)
	}
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

// confinedFileName rejects a CamelFileName that would escape the endpoint
// directory via filepath.Join: an empty name, an absolute path (Join discards
// the base), or a Windows volume name.
func confinedFileName(name string) error {
	if err := gocamel.ConfineFileName(name); err != nil {
		return fmt.Errorf("CamelFileName: %w", err)
	}
	return nil
}
