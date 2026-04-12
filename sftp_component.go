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

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SFTPComponent représente le composant SFTP
type SFTPComponent struct{}

// NewSFTPComponent crée une nouvelle instance de SFTPComponent
func NewSFTPComponent() *SFTPComponent {
	return &SFTPComponent{}
}

// CreateEndpoint crée un nouvel endpoint SFTP
func (c *SFTPComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	return &SFTPEndpoint{
		uri:  uri,
		url:  u,
		comp: c,
	}, nil
}

// SFTPEndpoint représente un endpoint SFTP
type SFTPEndpoint struct {
	uri  string
	url  *url.URL
	comp *SFTPComponent
}

// URI retourne l'URI de l'endpoint
func (e *SFTPEndpoint) URI() string {
	return e.uri
}

func (e *SFTPEndpoint) getHostKeyCallback(u *url.URL) (ssh.HostKeyCallback, error) {
	strictStr := GetConfigValue(u, "strictHostKeyChecking")
	if strings.EqualFold(strictStr, "false") {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	knownHostsFile := GetConfigValue(u, "knownHostsFile")
	if knownHostsFile == "" {
		return nil, fmt.Errorf("strictHostKeyChecking is true but knownHostsFile is not specified")
	}

	return knownhosts.New(knownHostsFile)
}

// connect établit la connexion SFTP
func (e *SFTPEndpoint) connect() (*ssh.Client, *sftp.Client, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host = host + ":22"
	}

	user := GetConfigValue(e.url, "username")
	if user == "" {
		user = "root"
	}
	pass := GetConfigValue(e.url, "password")

	hostKeyCallback, err := e.getHostKeyCallback(e.url)
	if err != nil {
		return nil, nil, fmt.Errorf("erreur de configuration de la validation de clé d'hôte: %w", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
	}

	sshClient, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return nil, nil, fmt.Errorf("erreur de connexion SSH: %w", err)
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, nil, fmt.Errorf("erreur de création du client SFTP: %w", err)
	}

	return sshClient, sftpClient, nil
}

// CreateProducer crée un producteur SFTP
func (e *SFTPEndpoint) CreateProducer() (Producer, error) {
	return &SFTPProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer crée un consommateur SFTP
func (e *SFTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &SFTPConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// SFTPProducer représente un producteur SFTP
type SFTPProducer struct {
	endpoint *SFTPEndpoint
}

func (p *SFTPProducer) Start(ctx context.Context) error {
	return nil
}

func (p *SFTPProducer) Stop() error {
	return nil
}

func (p *SFTPProducer) Send(exchange *Exchange) error {
	sshClient, sftpClient, err := p.endpoint.connect()
	if err != nil {
		return err
	}
	defer sftpClient.Close()
	defer sshClient.Close()

	path := p.endpoint.url.Path
	if path == "" || path == "/" {
		if name, ok := exchange.GetIn().GetHeader(CamelFileName); ok {
			path = fmt.Sprintf("/%v", name)
		} else {
			return fmt.Errorf("aucun chemin ou CamelFileName spécifié pour SFTP")
		}
	} else if strings.HasSuffix(path, "/") {
		if name, ok := exchange.GetIn().GetHeader(CamelFileName); ok {
			path = path + fmt.Sprintf("%v", name)
		} else {
			return fmt.Errorf("le chemin est un répertoire mais CamelFileName n'est pas spécifié")
		}
	}

	var reader io.Reader
	switch body := exchange.GetIn().GetBody().(type) {
	case []byte:
		reader = bytes.NewReader(body)
	case string:
		reader = strings.NewReader(body)
	case io.Reader:
		reader = body
	default:
		return fmt.Errorf("type de corps non supporté par SFTP: %T", exchange.GetIn().GetBody())
	}

	// Create directory if needed
	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		sftpClient.MkdirAll(dir) // ignores errors
	}

	file, err := sftpClient.Create(path)
	if err != nil {
		return fmt.Errorf("erreur lors de la création du fichier SFTP: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, reader)
	if err != nil {
		return fmt.Errorf("erreur lors de l'écriture SFTP: %w", err)
	}

	return nil
}

// SFTPConsumer représente un consommateur SFTP
type SFTPConsumer struct {
	endpoint  *SFTPEndpoint
	processor Processor
	cancel    context.CancelFunc
}

func (c *SFTPConsumer) Start(ctx context.Context) error {
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

func (c *SFTPConsumer) poll(ctx context.Context, delay time.Duration) {
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

func (c *SFTPConsumer) doPoll(ctx context.Context) {
	sshClient, sftpClient, err := c.endpoint.connect()
	if err != nil {
		fmt.Printf("Erreur de connexion SFTP pendant le polling: %v\n", err)
		return
	}
	defer sftpClient.Close()
	defer sshClient.Close()

	path := c.endpoint.url.Path
	if path == "" {
		path = "."
	}

	entries, err := sftpClient.ReadDir(path)
	if err != nil {
		fmt.Printf("Erreur lors du listage SFTP: %v\n", err)
		return
	}

	include := GetConfigValue(c.endpoint.url, "include")
	exclude := GetConfigValue(c.endpoint.url, "exclude")

	for _, entry := range entries {
		if !entry.IsDir() {
			if !matchFileName(entry.Name(), include, exclude) {
				continue
			}

			filePath := path + "/" + entry.Name()
			if path == "." || path == "/" {
				filePath = path + entry.Name()
			}

			file, err := sftpClient.Open(filePath)
			if err != nil {
				fmt.Printf("Erreur lors de l'ouverture du fichier SFTP %s: %v\n", filePath, err)
				continue
			}

			content, err := io.ReadAll(file)
			file.Close()

			if err != nil {
				fmt.Printf("Erreur lors de la lecture du fichier SFTP %s: %v\n", filePath, err)
				continue
			}

			exchange := NewExchange(ctx)
			exchange.SetBody(content)
			exchange.SetHeader(CamelFileName, entry.Name())
			exchange.SetHeader(CamelFilePath, filePath)

			if err := c.processor.Process(exchange); err != nil {
				fmt.Printf("Erreur lors du traitement du fichier SFTP %s: %v\n", filePath, err)
			}

			// Delete after processing if configured
			deleteStr := GetConfigValue(c.endpoint.url, "delete")
			if strings.EqualFold(deleteStr, "true") {
				if err := sftpClient.Remove(filePath); err != nil {
					fmt.Printf("Erreur lors de la suppression du fichier SFTP %s: %v\n", filePath, err)
				}
			}
		}
	}
}

func (c *SFTPConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
