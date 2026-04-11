package gocamel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/hirochachacha/go-smb2"
)

// SMBComponent représente le composant SMB
type SMBComponent struct{}

// NewSMBComponent crée une nouvelle instance de SMBComponent
func NewSMBComponent() *SMBComponent {
	return &SMBComponent{}
}

// CreateEndpoint crée un nouvel endpoint SMB
func (c *SMBComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	return &SMBEndpoint{
		uri:  uri,
		url:  u,
		comp: c,
	}, nil
}

// SMBEndpoint représente un endpoint SMB
type SMBEndpoint struct {
	uri  string
	url  *url.URL
	comp *SMBComponent
}

// URI retourne l'URI de l'endpoint
func (e *SMBEndpoint) URI() string {
	return e.uri
}

// connect établit la connexion SMB
func (e *SMBEndpoint) connect() (net.Conn, *smb2.Session, *smb2.Share, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host = host + ":445"
	}

	conn, err := net.Dial("tcp", host)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("erreur de connexion TCP (SMB): %w", err)
	}

	user := GetConfigValue(e.url, "username")
	pass := GetConfigValue(e.url, "password")
	domain := GetConfigValue(e.url, "domain")

	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     user,
			Password: pass,
			Domain:   domain,
		},
	}

	session, err := d.Dial(conn)
	if err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("erreur de session SMB: %w", err)
	}

	// Le share est le premier élément du path
	pathParts := strings.SplitN(strings.TrimPrefix(e.url.Path, "/"), "/", 2)
	shareName := ""
	if len(pathParts) > 0 {
		shareName = pathParts[0]
	}

	if shareName == "" {
		session.Logoff()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("aucun nom de partage spécifié dans l'URI SMB")
	}

	share, err := session.Mount(shareName)
	if err != nil {
		session.Logoff()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("erreur de montage du partage SMB %s: %w", shareName, err)
	}

	return conn, session, share, nil
}

// getFilePath extrait le chemin relatif au partage
func (e *SMBEndpoint) getFilePath() string {
	pathParts := strings.SplitN(strings.TrimPrefix(e.url.Path, "/"), "/", 2)
	if len(pathParts) > 1 {
		return pathParts[1]
	}
	return ""
}

// CreateProducer crée un producteur SMB
func (e *SMBEndpoint) CreateProducer() (Producer, error) {
	return &SMBProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer crée un consommateur SMB
func (e *SMBEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &SMBConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// SMBProducer représente un producteur SMB
type SMBProducer struct {
	endpoint *SMBEndpoint
}

func (p *SMBProducer) Start(ctx context.Context) error {
	return nil
}

func (p *SMBProducer) Stop() error {
	return nil
}

func (p *SMBProducer) Send(exchange *Exchange) error {
	conn, session, share, err := p.endpoint.connect()
	if err != nil {
		return err
	}
	defer share.Umount()
	defer session.Logoff()
	defer conn.Close()

	path := p.endpoint.getFilePath()
	if path == "" || strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		if name, ok := exchange.GetIn().GetHeader(CamelFileName); ok {
			path = filepath.Join(path, fmt.Sprintf("%v", name))
		} else {
			return fmt.Errorf("aucun nom de fichier (CamelFileName) spécifié pour SMB")
		}
	}
	path = strings.ReplaceAll(path, "/", "\\")

	var reader io.Reader
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		reader = bytes.NewReader(body)
	case string:
		reader = strings.NewReader(body)
	case io.Reader:
		reader = body
	default:
		return fmt.Errorf("type de corps non supporté par SMB: %T", exchange.GetIn().GetBody())
	}

	// Create directories if needed
	dir := filepath.Dir(path)
	if dir != "." && dir != "\\" && dir != "" {
		parts := strings.Split(dir, "\\")
		current := ""
		for _, part := range parts {
			if part == "" {
				continue
			}
			if current == "" {
				current = part
			} else {
				current = current + "\\" + part
			}
			share.Mkdir(current, 0755) // ignore error, dir might exist
		}
	}

	file, err := share.Create(path)
	if err != nil {
		return fmt.Errorf("erreur lors de la création du fichier SMB: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, reader)
	if err != nil {
		return fmt.Errorf("erreur lors de l'écriture SMB: %w", err)
	}

	return nil
}

// SMBConsumer représente un consommateur SMB
type SMBConsumer struct {
	endpoint  *SMBEndpoint
	processor Processor
	cancel    context.CancelFunc
}

func (c *SMBConsumer) Start(ctx context.Context) error {
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

func (c *SMBConsumer) poll(ctx context.Context, delay time.Duration) {
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

func (c *SMBConsumer) doPoll(ctx context.Context) {
	conn, session, share, err := c.endpoint.connect()
	if err != nil {
		fmt.Printf("Erreur de connexion SMB pendant le polling: %v\n", err)
		return
	}
	defer share.Umount()
	defer session.Logoff()
	defer conn.Close()

	path := c.endpoint.getFilePath()
	path = strings.ReplaceAll(path, "/", "\\")
	if path == "" {
		path = "."
	}

	entries, err := share.ReadDir(path)
	if err != nil {
		fmt.Printf("Erreur lors du listage SMB: %v\n", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			filePath := filepath.Join(path, entry.Name())
			filePath = strings.ReplaceAll(filePath, "/", "\\")

			file, err := share.Open(filePath)
			if err != nil {
				fmt.Printf("Erreur lors de l'ouverture du fichier SMB %s: %v\n", filePath, err)
				continue
			}

			content, err := io.ReadAll(file)
			file.Close()

			if err != nil {
				fmt.Printf("Erreur lors de la lecture du fichier SMB %s: %v\n", filePath, err)
				continue
			}

			exchange := NewExchange(ctx)
			exchange.SetBody(content)
			exchange.SetHeader(CamelFileName, entry.Name())
			exchange.SetHeader(CamelFilePath, filePath)

			if err := c.processor.Process(exchange); err != nil {
				fmt.Printf("Erreur lors du traitement du fichier SMB %s: %v\n", filePath, err)
			}

			// Delete after processing if configured
			deleteStr := GetConfigValue(c.endpoint.url, "delete")
			if strings.EqualFold(deleteStr, "true") {
				if err := share.Remove(filePath); err != nil {
					fmt.Printf("Erreur lors de la suppression du fichier SMB %s: %v\n", filePath, err)
				}
			}
		}
	}
}

func (c *SMBConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
