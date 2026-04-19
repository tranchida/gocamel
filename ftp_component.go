package gocamel

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
)

// FTPComponent represents the FTP component
type FTPComponent struct{}

// NewFTPComponent creates a new FTPComponent
func NewFTPComponent() *FTPComponent {
	return &FTPComponent{}
}

// CreateEndpoint creates a new FTP endpoint
func (c *FTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	return &FTPEndpoint{
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: parseConnectTimeout(u),
		// passiveMode=true (défaut) : le library jlaffaye/ftp utilise EPSV/PASV.
		// passiveMode=false : désactive EPSV et tombe sur PASV basique.
		passiveMode: !strings.EqualFold(GetConfigValue(u, "passiveMode"), "false"),
		// disconnect=true : se déconnecter après chaque opération.
		// disconnect=false (default): maintain the connection between polls.
		disconnect: strings.EqualFold(GetConfigValue(u, "disconnect"), "true"),
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
		return nil, fmt.Errorf("erreur de connexion FTP: %w", err)
	}

	user := GetConfigValue(e.url, "username")
	if user == "" {
		user = "anonymous"
	}
	if err := conn.Login(user, GetConfigValue(e.url, "password")); err != nil {
		conn.Quit()
		return nil, fmt.Errorf("erreur d'authentification FTP: %w", err)
	}
	return conn, nil
}

// CreateProducer creates an FTP producer
func (e *FTPEndpoint) CreateProducer() (Producer, error) {
	return &FTPProducer{endpoint: e, fileExist: ParseFileExist(e.url)}, nil
}

// CreateConsumer creates an FTP consumer
func (e *FTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &FTPConsumer{
		endpoint:  e,
		processor: processor,
		opts:      ParsePollingOptions(e.url),
	}, nil
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// FTPProducer represents an FTP producer
type FTPProducer struct {
	endpoint   *FTPEndpoint
	fileExist  FileExistBehavior
	mu         sync.Mutex
	conn       *ftp.ServerConn // persistent connection (disconnect=false)
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
func (p *FTPProducer) Send(exchange *Exchange) error {
	conn, err := p.getConn()
	if err != nil {
		return err
	}
	defer p.releaseConn(conn)

	path := p.endpoint.url.Path
	if path == "" || path == "/" {
		if name, ok := exchange.GetIn().GetHeader(CamelFileName); ok {
			path = fmt.Sprintf("%v", name)
		} else {
			return fmt.Errorf("aucun chemin ou CamelFileName spécifié pour FTP")
		}
	} else if strings.HasSuffix(path, "/") {
		if name, ok := exchange.GetIn().GetHeader(CamelFileName); ok {
			path = path + fmt.Sprintf("%v", name)
		} else {
			return fmt.Errorf("le chemin est un répertoire mais CamelFileName n'est pas spécifié")
		}
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
			return fmt.Errorf("erreur lecture du corps: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body type for FTP: %T", exchange.GetIn().GetBody())
	}

	switch p.fileExist {
	case FileExistFail:
		if size, err := conn.FileSize(path); err == nil && size >= 0 {
			return fmt.Errorf("le fichier existe déjà sur FTP: %s", path)
		}
	case FileExistIgnore:
		if size, err := conn.FileSize(path); err == nil && size >= 0 {
			return nil
		}
	case FileExistAppend:
		// Télécharger l'existant et concaténer
		if resp, err := conn.Retr(path); err == nil {
			existing, _ := io.ReadAll(resp)
			resp.Close()
			body = append(existing, body...)
		}
	}

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		conn.ChangeDir(dir) // ignore the error if the directory already exists
	}
	if err := conn.Stor(filepath.Base(path), bytes.NewReader(body)); err != nil {
		return fmt.Errorf("erreur lors de l'envoi FTP: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// FTPConsumer represents an FTP consumer
type FTPConsumer struct {
	endpoint  *FTPEndpoint
	processor Processor
	opts      PollingOptions
	cancel    context.CancelFunc
	conn      *ftp.ServerConn // persistent connection (disconnect=false)
}

func (c *FTPConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	go func() {
		select {
		case <-time.After(c.opts.InitialDelay):
		case <-ctx.Done():
			return
		}
		c.poll(ctx)
	}()
	return nil
}

func (c *FTPConsumer) poll(ctx context.Context, ) {
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

func (c *FTPConsumer) doPoll(ctx context.Context) {
	conn, err := c.getConn()
	if err != nil {
		fmt.Printf("Erreur de connexion FTP pendant le polling: %v\n", err)
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
			fmt.Printf("Erreur lors du listage FTP %s: %v\n", dir, err)
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
				if matchFileName(entry.Name, c.opts.Include, c.opts.Exclude) {
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

		resp, err := conn.Retr(f.path)
		if err != nil {
			fmt.Printf("Erreur lors de la récupération du fichier FTP %s: %v\n", f.path, err)
			continue
		}
		content, err := io.ReadAll(resp)
		resp.Close()
		if err != nil {
			fmt.Printf("Erreur lors de la lecture du fichier FTP %s: %v\n", f.path, err)
			continue
		}

		exchange := NewExchange(ctx)
		exchange.SetBody(content)
		exchange.SetHeader(CamelFileName, f.name)
		exchange.SetHeader(CamelFilePath, f.path)

		if err := c.processor.Process(exchange); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				fmt.Printf("Erreur lors du traitement du fichier FTP %s: %v\n", f.path, err)
				if !c.opts.Noop && c.opts.MoveFailed != "" {
					moveFTPFile(conn, f.path, c.opts.MoveFailed)
				}
			}
			continue
		}

		count++
		if !c.opts.Noop {
			if c.opts.Move != "" {
				moveFTPFile(conn, f.path, c.opts.Move)
			} else if c.opts.Delete {
				if err := conn.Delete(f.path); err != nil {
					fmt.Printf("Erreur lors de la suppression du fichier FTP %s: %v\n", f.path, err)
				}
			}
		}
	}
}

func (c *FTPConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.conn != nil {
		c.conn.Quit()
		c.conn = nil
	}
	return nil
}

// moveFTPFile renames/moves a file on the FTP server.
func moveFTPFile(conn *ftp.ServerConn, srcPath, destDir string) {
	destPath := destDir + "/" + filepath.Base(srcPath)
	if err := conn.Rename(srcPath, destPath); err != nil {
		fmt.Printf("Erreur déplacement FTP %s -> %s: %v\n", srcPath, destPath, err)
	}
}
