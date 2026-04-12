package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
)

// FTPComponent représente le composant FTP
type FTPComponent struct{}

// NewFTPComponent crée une nouvelle instance de FTPComponent
func NewFTPComponent() *FTPComponent {
	return &FTPComponent{}
}

// CreateEndpoint crée un nouvel endpoint FTP
func (c *FTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	return &FTPEndpoint{
		uri:  uri,
		url:  u,
		comp: c,
	}, nil
}

// FTPEndpoint représente un endpoint FTP
type FTPEndpoint struct {
	uri  string
	url  *url.URL
	comp *FTPComponent
}

// URI retourne l'URI de l'endpoint
func (e *FTPEndpoint) URI() string {
	return e.uri
}

// connect établit la connexion FTP
func (e *FTPEndpoint) connect() (*ftp.ServerConn, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host = host + ":21"
	}

	conn, err := ftp.Dial(host, ftp.DialWithTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("erreur de connexion FTP: %w", err)
	}

	user := GetConfigValue(e.url, "username")
	if user == "" {
		user = "anonymous"
	}
	pass := GetConfigValue(e.url, "password")

	if err := conn.Login(user, pass); err != nil {
		conn.Quit()
		return nil, fmt.Errorf("erreur d'authentification FTP: %w", err)
	}

	return conn, nil
}

// CreateProducer crée un producteur FTP
func (e *FTPEndpoint) CreateProducer() (Producer, error) {
	return &FTPProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer crée un consommateur FTP
func (e *FTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &FTPConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// FTPProducer représente un producteur FTP
type FTPProducer struct {
	endpoint *FTPEndpoint
}

func (p *FTPProducer) Start(ctx context.Context) error {
	return nil
}

func (p *FTPProducer) Stop() error {
	return nil
}

func (p *FTPProducer) Send(exchange *Exchange) error {
	conn, err := p.endpoint.connect()
	if err != nil {
		return err
	}
	defer conn.Quit()

	path := p.endpoint.url.Path
	if path == "" || path == "/" {
		// Use a header for filename if no path is given
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

	var reader io.Reader
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		reader = bytes.NewReader(body)
	case string:
		reader = strings.NewReader(body)
	case io.Reader:
		reader = body
	default:
		return fmt.Errorf("type de corps non supporté par FTP: %T", exchange.GetIn().GetBody())
	}

	// Change directory if needed
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		// Simple change dir, ignores errors if dir doesn't exist (assumes we might need to create it, but ftp package doesn't have MkdirAll)
		conn.ChangeDir(dir)
	}

	err = conn.Stor(filepath.Base(path), reader)
	if err != nil {
		return fmt.Errorf("erreur lors de l'envoi FTP: %w", err)
	}

	return nil
}

// FTPConsumer représente un consommateur FTP
type FTPConsumer struct {
	endpoint  *FTPEndpoint
	processor Processor
	cancel    context.CancelFunc
}

func (c *FTPConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	delayStr := GetConfigValue(c.endpoint.url, "delay")
	delay := 5 * time.Second
	if delayStr != "" {
		if d, err := time.ParseDuration(delayStr); err == nil {
			delay = d
		}
	}

	go c.poll(ctx, delay)
	return nil
}

func (c *FTPConsumer) poll(ctx context.Context, delay time.Duration) {
	ticker := time.NewTicker(delay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.doPoll(ctx)
		}
	}
}

func (c *FTPConsumer) doPoll(ctx context.Context) {
	conn, err := c.endpoint.connect()
	if err != nil {
		fmt.Printf("Erreur de connexion FTP pendant le polling: %v\n", err)
		return
	}
	defer conn.Quit()

	path := strings.TrimPrefix(c.endpoint.url.Path, "/")
	if path == "" {
		path = "."
	}

	entries, err := conn.List(path)
	if err != nil {
		fmt.Printf("Erreur lors du listage FTP: %v\n", err)
		return
	}

	include := GetConfigValue(c.endpoint.url, "include")
	exclude := GetConfigValue(c.endpoint.url, "exclude")

	for _, entry := range entries {
		if entry.Type == ftp.EntryTypeFile {
			if !matchFileName(entry.Name, include, exclude) {
				continue
			}

			filePath := path + "/" + entry.Name
			if path == "." {
				filePath = entry.Name
			}
			resp, err := conn.Retr(filePath)
			if err != nil {
				fmt.Printf("Erreur lors de la récupération du fichier FTP %s: %v\n", filePath, err)
				continue
			}

			content, err := io.ReadAll(resp)
			resp.Close()

			if err != nil {
				fmt.Printf("Erreur lors de la lecture du fichier FTP %s: %v\n", filePath, err)
				continue
			}

			exchange := NewExchange(ctx)
			exchange.SetBody(content)
			exchange.SetHeader(CamelFileName, entry.Name)
			exchange.SetHeader(CamelFilePath, filePath)

			if err := c.processor.Process(exchange); err != nil {
				fmt.Printf("Erreur lors du traitement du fichier FTP %s: %v\n", filePath, err)
			}

			// Delete after processing if configured
			deleteStr := GetConfigValue(c.endpoint.url, "delete")
			if strings.EqualFold(deleteStr, "true") {
				if err := conn.Delete(filePath); err != nil {
					fmt.Printf("Erreur lors de la suppression du fichier FTP %s: %v\n", filePath, err)
				}
			}
		}
	}
}

func (c *FTPConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
