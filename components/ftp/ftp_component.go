package ftp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"

	gocamel "gitlab.com/tranchida/gocamel"
)

// FTPComponent represents the FTP component
type FTPComponent struct{}

// NewFTPComponent creates a new FTPComponent
func NewFTPComponent() *FTPComponent {
	return &FTPComponent{}
}

// CreateEndpoint creates a new FTP endpoint
func (c *FTPComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return &FTPEndpoint{
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: gocamel.ParseConnectTimeout(u),
		// passiveMode=true (default): the jlaffaye/ftp library uses EPSV/PASV.
		// passiveMode=false: disables EPSV and falls back to basic PASV.
		passiveMode: !strings.EqualFold(gocamel.GetConfigValue(u, "passiveMode"), "false"),
		// disconnect=true: disconnect after each operation.
		// disconnect=false (default): maintain the connection between polls.
		disconnect: strings.EqualFold(gocamel.GetConfigValue(u, "disconnect"), "true"),
		// maxMessageSize bounds how many bytes a single polled file may hold
		// (0 = unlimited). Overriding the default lets a consumer read an
		// oversized remote file without exhausting process memory.
		maxMessageSize: gocamel.ParseInt64Option(gocamel.GetConfigValue(u, "maxMessageSize")),
	}, nil
}

// FTPEndpoint represents an FTP endpoint
type FTPEndpoint struct {
	uri            string
	url            *url.URL
	comp           *FTPComponent
	connectTimeout time.Duration
	passiveMode    bool
	disconnect     bool
	maxMessageSize int64
}

// URI returns the endpoint URI
func (e *FTPEndpoint) URI() string {
	return e.uri
}

// connect establishes an FTP connection
func (e *FTPEndpoint) connect() (*ftp.ServerConn, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host += ":21"
	}

	opts := []ftp.DialOption{ftp.DialWithTimeout(e.connectTimeout)}
	if !e.passiveMode {
		opts = append(opts, ftp.DialWithDisabledEPSV(true))
	}

	conn, err := ftp.Dial(host, opts...)
	if err != nil {
		return nil, fmt.Errorf("FTP connection error: %w", err)
	}

	user := gocamel.GetConfigValue(e.url, "username")
	if user == "" {
		user = "anonymous"
	}
	if err := conn.Login(user, gocamel.GetConfigValue(e.url, "password")); err != nil {
		conn.Quit()
		return nil, fmt.Errorf("FTP authentication error: %w", err)
	}
	return conn, nil
}

// CreateProducer creates an FTP producer
func (e *FTPEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &FTPProducer{endpoint: e, fileExist: gocamel.ParseFileExist(e.url)}, nil
}

// CreateConsumer creates an FTP consumer
func (e *FTPEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return &FTPConsumer{
		endpoint:  e,
		processor: processor,
		opts:      gocamel.ParsePollingOptions(e.url),
	}, nil
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// FTPProducer represents an FTP producer
type FTPProducer struct {
	endpoint  *FTPEndpoint
	fileExist gocamel.FileExistBehavior
	mu        sync.Mutex
	conn      *ftp.ServerConn // persistent connection (disconnect=false)
}

func (p *FTPProducer) Start(ctx context.Context) error { return nil }

func (p *FTPProducer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		p.conn.Quit()
		p.conn = nil
	}
	return nil
}

func (p *FTPProducer) getConn() (*ftp.ServerConn, error) {
	if p.endpoint.disconnect {
		return p.endpoint.connect()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		if err := p.conn.NoOp(); err == nil {
			return p.conn, nil
		}
		p.conn.Quit()
		p.conn = nil
	}
	conn, err := p.endpoint.connect()
	if err != nil {
		return nil, err
	}
	p.conn = conn
	return conn, nil
}

func (p *FTPProducer) releaseConn(conn *ftp.ServerConn) {
	if p.endpoint.disconnect {
		conn.Quit()
	}
}

// Send sends the exchange content to the FTP server.
func (p *FTPProducer) Send(exchange *gocamel.Exchange) error {
	conn, err := p.getConn()
	if err != nil {
		return err
	}
	defer p.releaseConn(conn)

	path := p.endpoint.url.Path
	if path == "" || path == "/" {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			joined, err := gocamel.JoinRemotePath("", fmt.Sprintf("%v", name))
			if err != nil {
				return fmt.Errorf("ftp: invalid path: %w", err)
			}
			path = joined
		} else {
			return fmt.Errorf("no path or CamelFileName specified for FTP")
		}
	} else if strings.HasSuffix(path, "/") {
		if name, ok := exchange.GetIn().GetHeader(gocamel.CamelFileName); ok {
			joined, err := gocamel.JoinRemotePath(path, fmt.Sprintf("%v", name))
			if err != nil {
				return fmt.Errorf("ftp: invalid path: %w", err)
			}
			path = joined
		} else {
			return fmt.Errorf("path is a directory but CamelFileName is not specified")
		}
	}

	if err := gocamel.ValidateRemotePath(path); err != nil {
		return fmt.Errorf("ftp: invalid path: %w", err)
	}

	path = strings.TrimPrefix(path, "/")

	var body []byte
	switch b := exchange.GetIn().GetBody().(type) {
	case []byte:
		body = b
	case string:
		body = []byte(b)
	case io.Reader:
		body, err = io.ReadAll(b)
		if err != nil {
			return fmt.Errorf("error reading body: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body type for FTP: %T", exchange.GetIn().GetBody())
	}

	switch p.fileExist {
	case gocamel.FileExistFail:
		if size, err := conn.FileSize(path); err == nil && size >= 0 {
			return fmt.Errorf("file already exists on FTP: %s", path)
		}
	case gocamel.FileExistIgnore:
		if size, err := conn.FileSize(path); err == nil && size >= 0 {
			return nil
		}
	case gocamel.FileExistAppend:
		// Download the existing file and concatenate. A failed download of a
		// missing file is expected (create-if-absent semantics); a partial
		// read, however, must abort the send: concatenating a truncated copy
		// of the existing content would silently corrupt the stored file.
		if resp, err := conn.Retr(path); err == nil {
			existing, readErr := io.ReadAll(resp)
			resp.Close()
			if readErr != nil {
				return fmt.Errorf("ftp: failed to read existing file %s for append: %w", path, readErr)
			}
			body = append(existing, body...)
		}
	}

	// STOR (like RETR and SIZE above) accepts a full pathname resolved from
	// the login directory, so no ChangeDir is needed. Keeping the session in
	// the login directory matters: with a persistent connection
	// (disconnect=false), a ChangeDir would leak into the next Send and make
	// a relative path resolve against the previous file's directory.
	if err := conn.Stor(path, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("error sending FTP file: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// FTPConsumer represents an FTP consumer
type FTPConsumer struct {
	endpoint  *FTPEndpoint
	processor gocamel.Processor
	opts      gocamel.PollingOptions
	cancel    context.CancelFunc
	conn      *ftp.ServerConn // persistent connection (disconnect=false)
	wg        sync.WaitGroup
}

func (c *FTPConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		select {
		case <-time.After(c.opts.InitialDelay):
		case <-ctx.Done():
			return
		}
		c.poll(ctx)
	}()
	return nil
}

func (c *FTPConsumer) poll(ctx context.Context) {
	ticker := time.NewTicker(c.opts.Delay)
	defer ticker.Stop()
	c.doPoll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.doPoll(ctx)
		}
	}
}

func (c *FTPConsumer) getConn() (*ftp.ServerConn, error) {
	if c.endpoint.disconnect {
		return c.endpoint.connect()
	}
	if c.conn != nil {
		if err := c.conn.NoOp(); err == nil {
			return c.conn, nil
		}
		c.conn.Quit()
		c.conn = nil
	}
	conn, err := c.endpoint.connect()
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

func (c *FTPConsumer) releaseConn(conn *ftp.ServerConn) {
	if c.endpoint.disconnect {
		conn.Quit()
	}
}

type ftpSynchronization struct {
	conn       *ftp.ServerConn
	path       string
	lockPath   string
	markerPath string
	noop       bool
	delete     bool
	move       string
	moveFailed string
}

func (s *ftpSynchronization) cleanupLock() {
	if s.markerPath != "" {
		_ = s.conn.Delete(s.markerPath)
	}
}

func (s *ftpSynchronization) OnComplete(exchange *gocamel.Exchange) {
	defer s.cleanupLock()
	effectivePath := s.path
	if s.lockPath != "" {
		effectivePath = s.lockPath
	}

	if s.noop {
		if s.lockPath != "" {
			_ = s.conn.Rename(s.lockPath, s.path)
		}
		return
	}
	if s.move != "" {
		moveFTPFile(s.conn, effectivePath, s.move)
	} else if s.delete {
		if err := s.conn.Delete(effectivePath); err != nil {
			gocamel.LogError("FTP file deletion error", "file", effectivePath, "error", err)
		}
	} else if s.lockPath != "" {
		_ = s.conn.Rename(s.lockPath, s.path)
	}
}

func (s *ftpSynchronization) OnFailure(exchange *gocamel.Exchange) {
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
		moveFTPFile(s.conn, effectivePath, s.moveFailed)
	} else if s.lockPath != "" {
		_ = s.conn.Rename(s.lockPath, s.path)
	}
}

func (c *FTPConsumer) doPoll(ctx context.Context) {
	conn, err := c.getConn()
	if err != nil {
		gocamel.LogError("FTP polling connection error", "error", err)
		return
	}
	defer c.releaseConn(conn)

	rootPath := strings.TrimPrefix(c.endpoint.url.Path, "/")
	if rootPath == "" {
		rootPath = "."
	}

	type ftpFile struct {
		name string
		path string
	}
	var files []ftpFile

	var listDir func(dir string)
	listDir = func(dir string) {
		entries, err := conn.List(dir)
		if err != nil {
			gocamel.LogError("FTP listing error", "directory", dir, "error", err)
			return
		}
		for _, entry := range entries {
			fullPath := dir + "/" + entry.Name
			if dir == "." {
				fullPath = entry.Name
			}
			switch entry.Type {
			case ftp.EntryTypeFolder:
				if c.opts.Recursive && entry.Name != "." && entry.Name != ".." {
					listDir(fullPath)
				}
			case ftp.EntryTypeFile:
				if strings.HasSuffix(entry.Name, ".camelLock") || strings.HasSuffix(entry.Name, ".camelExclusiveReadLock") {
					continue
				}
				if gocamel.MatchFileName(entry.Name, c.opts.Include, c.opts.Exclude) {
					files = append(files, ftpFile{name: entry.Name, path: fullPath})
				}
			}
		}
	}
	listDir(rootPath)

	count := 0
	for _, f := range files {
		if c.opts.MaxMessagesPerPoll > 0 && count >= c.opts.MaxMessagesPerPoll {
			break
		}

		var lockPath string
		var markerPath string

		switch c.opts.ReadLock {
		case gocamel.ReadLockChanged:
			statFn := func() (int64, time.Time, error) {
				size, err := conn.FileSize(f.path)
				if err != nil {
					return 0, time.Time{}, err
				}
				modTime, _ := conn.GetTime(f.path)
				return size, modTime, nil
			}
			if !gocamel.WaitForStable(ctx, statFn, c.opts.ReadLockTimeout, c.opts.ReadLockCheckInterval, c.opts.ReadLockMinAge, c.opts.ReadLockMinLength) {
				gocamel.LogWarn("FTP file did not stabilise before read lock timeout; skipping",
					"file", f.path, "timeout", c.opts.ReadLockTimeout)
				continue
			}

		case gocamel.ReadLockRename:
			targetLockPath := f.path + ".camelExclusiveReadLock"
			if err := conn.Rename(f.path, targetLockPath); err != nil {
				gocamel.LogWarn("could not acquire FTP rename read lock; skipping", "file", f.path, "error", err)
				continue
			}
			lockPath = targetLockPath

		case gocamel.ReadLockMarkerFile:
			targetMarkerPath := f.path + ".camelLock"
			if size, err := conn.FileSize(targetMarkerPath); err == nil && size >= 0 {
				// Marker exists: file is locked
				continue
			}
			if err := conn.Stor(targetMarkerPath, bytes.NewReader([]byte{})); err != nil {
				gocamel.LogWarn("could not create marker file for FTP read lock; skipping", "file", f.path, "error", err)
				continue
			}
			markerPath = targetMarkerPath
		}

		readPath := f.path
		if lockPath != "" {
			readPath = lockPath
		}

		resp, err := conn.Retr(readPath)
		if err != nil {
			gocamel.LogError("FTP file retrieval error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = conn.Delete(markerPath)
			}
			if lockPath != "" {
				_ = conn.Rename(lockPath, f.path)
			}
			continue
		}
		content, err := gocamel.ReadLimited(resp, c.endpoint.maxMessageSize)
		resp.Close()
		if err != nil {
			gocamel.LogError("FTP file read error", "file", readPath, "error", err)
			if markerPath != "" {
				_ = conn.Delete(markerPath)
			}
			if lockPath != "" {
				_ = conn.Rename(lockPath, f.path)
			}
			continue
		}

		exchange := gocamel.NewExchange(ctx)
		exchange.SetBody(content)
		exchange.SetHeader(gocamel.CamelFileName, f.name)
		exchange.SetHeader(gocamel.CamelFilePath, f.path)

		exchange.AddSynchronization(&ftpSynchronization{
			conn:       conn,
			path:       f.path,
			lockPath:   lockPath,
			markerPath: markerPath,
			noop:       c.opts.Noop,
			delete:     c.opts.Delete,
			move:       c.opts.Move,
			moveFailed: c.opts.MoveFailed,
		})

		procErr := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange))

		if procErr == nil {
			count++
		}
	}
}

func (c *FTPConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	if c.conn != nil {
		c.conn.Quit()
		c.conn = nil
	}
	return nil
}

// moveFTPFile renames/moves a file on the FTP server.
func moveFTPFile(conn *ftp.ServerConn, srcPath, destDir string) {
	filename := filepath.Base(srcPath)
	filename = strings.TrimSuffix(filename, ".camelExclusiveReadLock")
	destPath := destDir + "/" + filename
	if err := conn.Rename(srcPath, destPath); err != nil {
		gocamel.LogError("FTP move error", "src", srcPath, "dest", destPath, "error", err)
	}
}
