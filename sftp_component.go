package gocamel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		uri:            uri,
		url:            u,
		comp:           c,
		connectTimeout: parseConnectTimeout(u),
		disconnect:     strings.EqualFold(GetConfigValue(u, "disconnect"), "true"),
	}, nil
}

// SFTPEndpoint représente un endpoint SFTP
type SFTPEndpoint struct {
	uri            string
	url            *url.URL
	comp           *SFTPComponent
	connectTimeout time.Duration
	disconnect     bool
}

// URI retourne l'URI de l'endpoint
func (e *SFTPEndpoint) URI() string {
	return e.uri
}

func (e *SFTPEndpoint) getHostKeyCallback(u *url.URL) (ssh.HostKeyCallback, error) {
	if strings.EqualFold(GetConfigValue(u, "strictHostKeyChecking"), "false") {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	knownHostsFile := GetConfigValue(u, "knownHostsFile")
	if knownHostsFile == "" {
		return nil, fmt.Errorf("strictHostKeyChecking is true but knownHostsFile is not specified")
	}
	return knownhosts.New(knownHostsFile)
}

// connect établit une connexion SSH + SFTP
func (e *SFTPEndpoint) connect() (*ssh.Client, *sftp.Client, error) {
	host := e.url.Host
	if !strings.Contains(host, ":") {
		host += ":22"
	}

	user := GetConfigValue(e.url, "username")
	if user == "" {
		user = "root"
	}

	hostKeyCallback, err := e.getHostKeyCallback(e.url)
	if err != nil {
		return nil, nil, fmt.Errorf("erreur de configuration de la validation de clé d'hôte: %w", err)
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(GetConfigValue(e.url, "password"))},
		HostKeyCallback: hostKeyCallback,
		Timeout:         e.connectTimeout,
	}

	// Authentification par clé privée (prioritaire sur le mot de passe si présent)
	if privateKeyFile := GetConfigValue(e.url, "privateKeyFile"); privateKeyFile != "" {
		key, err := os.ReadFile(privateKeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("impossible de lire la clé privée %s: %w", privateKeyFile, err)
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, nil, fmt.Errorf("impossible de parser la clé privée: %w", err)
		}
		config.Auth = append([]ssh.AuthMethod{ssh.PublicKeys(signer)}, config.Auth...)
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
	return &SFTPProducer{endpoint: e, fileExist: ParseFileExist(e.url)}, nil
}

// CreateConsumer crée un consommateur SFTP
func (e *SFTPEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &SFTPConsumer{
		endpoint:  e,
		processor: processor,
		opts:      ParsePollingOptions(e.url),
	}, nil
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// SFTPProducer représente un producteur SFTP
type SFTPProducer struct {
	endpoint   *SFTPEndpoint
	fileExist  FileExistBehavior
	mu         sync.Mutex
	sshClient  *ssh.Client
	sftpClient *sftp.Client
}

func (p *SFTPProducer) Start(ctx context.Context) error { return nil }

func (p *SFTPProducer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sftpClient != nil {
		p.sftpClient.Close()
		p.sftpClient = nil
	}
	if p.sshClient != nil {
		p.sshClient.Close()
		p.sshClient = nil
	}
	return nil
}

func (p *SFTPProducer) getClients() (*ssh.Client, *sftp.Client, error) {
	if p.endpoint.disconnect {
		return p.endpoint.connect()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sftpClient != nil {
		if _, err := p.sftpClient.Getwd(); err == nil {
			return p.sshClient, p.sftpClient, nil
		}
		p.sftpClient.Close()
		p.sshClient.Close()
		p.sftpClient = nil
		p.sshClient = nil
	}
	ssh, sftp, err := p.endpoint.connect()
	if err != nil {
		return nil, nil, err
	}
	p.sshClient = ssh
	p.sftpClient = sftp
	return ssh, sftp, nil
}

func (p *SFTPProducer) releaseClients(sshClient *ssh.Client, sftpClient *sftp.Client) {
	if p.endpoint.disconnect {
		sftpClient.Close()
		sshClient.Close()
	}
}

// Send envoie le contenu de l'échange vers le serveur SFTP.
func (p *SFTPProducer) Send(exchange *Exchange) error {
	sshClient, sftpClient, err := p.getClients()
	if err != nil {
		return err
	}
	defer p.releaseClients(sshClient, sftpClient)

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

	switch p.fileExist {
	case FileExistFail:
		if _, err := sftpClient.Stat(path); err == nil {
			return fmt.Errorf("le fichier existe déjà sur SFTP: %s", path)
		}
	case FileExistIgnore:
		if _, err := sftpClient.Stat(path); err == nil {
			return nil
		}
	}

	var body []byte
	switch b := exchange.GetIn().GetBody().(type) {
	case []byte:
		body = b
	case string:
		body = []byte(b)
	case io.Reader:
		if body, err = io.ReadAll(b); err != nil {
			return fmt.Errorf("erreur lecture du corps: %w", err)
		}
	default:
		return fmt.Errorf("type de corps non supporté par SFTP: %T", exchange.GetIn().GetBody())
	}

	if p.fileExist == FileExistAppend {
		if f, err := sftpClient.Open(path); err == nil {
			existing, _ := io.ReadAll(f)
			f.Close()
			body = append(existing, body...)
		}
	}

	if dir := filepath.Dir(path); dir != "." && dir != "/" {
		sftpClient.MkdirAll(dir) // ignore l'erreur si le répertoire existe
	}

	file, err := sftpClient.Create(path)
	if err != nil {
		return fmt.Errorf("erreur lors de la création du fichier SFTP: %w", err)
	}
	defer file.Close()

	if _, err = io.Copy(file, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("erreur lors de l'écriture SFTP: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Consumer
// ---------------------------------------------------------------------------

// SFTPConsumer représente un consommateur SFTP
type SFTPConsumer struct {
	endpoint   *SFTPEndpoint
	processor  Processor
	opts       PollingOptions
	cancel     context.CancelFunc
	sshClient  *ssh.Client  // connexion persistante (disconnect=false)
	sftpClient *sftp.Client // connexion persistante (disconnect=false)
}

func (c *SFTPConsumer) Start(ctx context.Context) error {
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

func (c *SFTPConsumer) poll(ctx context.Context) {
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

func (c *SFTPConsumer) getClients() (*ssh.Client, *sftp.Client, error) {
	if c.endpoint.disconnect {
		return c.endpoint.connect()
	}
	if c.sftpClient != nil {
		if _, err := c.sftpClient.Getwd(); err == nil {
			return c.sshClient, c.sftpClient, nil
		}
		c.sftpClient.Close()
		c.sshClient.Close()
		c.sftpClient = nil
		c.sshClient = nil
	}
	ssh, sftp, err := c.endpoint.connect()
	if err != nil {
		return nil, nil, err
	}
	c.sshClient = ssh
	c.sftpClient = sftp
	return ssh, sftp, nil
}

func (c *SFTPConsumer) releaseClients(sshClient *ssh.Client, sftpClient *sftp.Client) {
	if c.endpoint.disconnect {
		sftpClient.Close()
		sshClient.Close()
	}
}

func (c *SFTPConsumer) doPoll(ctx context.Context) {
	sshClient, sftpClient, err := c.getClients()
	if err != nil {
		fmt.Printf("Erreur de connexion SFTP pendant le polling: %v\n", err)
		return
	}
	defer c.releaseClients(sshClient, sftpClient)

	rootPath := c.endpoint.url.Path
	if rootPath == "" {
		rootPath = "."
	}

	type sftpFile struct {
		name string
		path string
	}
	var files []sftpFile

	if c.opts.Recursive {
		walker := sftpClient.Walk(rootPath)
		for walker.Step() {
			if walker.Err() != nil {
				continue
			}
			if walker.Stat().IsDir() {
				continue
			}
			name := filepath.Base(walker.Path())
			if matchFileName(name, c.opts.Include, c.opts.Exclude) {
				files = append(files, sftpFile{name: name, path: walker.Path()})
			}
		}
	} else {
		entries, err := sftpClient.ReadDir(rootPath)
		if err != nil {
			fmt.Printf("Erreur lors du listage SFTP: %v\n", err)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || !matchFileName(entry.Name(), c.opts.Include, c.opts.Exclude) {
				continue
			}
			sep := "/"
			if rootPath == "/" {
				sep = ""
			}
			files = append(files, sftpFile{
				name: entry.Name(),
				path: rootPath + sep + entry.Name(),
			})
		}
	}

	count := 0
	for _, f := range files {
		if c.opts.MaxMessagesPerPoll > 0 && count >= c.opts.MaxMessagesPerPoll {
			break
		}

		file, err := sftpClient.Open(f.path)
		if err != nil {
			fmt.Printf("Erreur lors de l'ouverture du fichier SFTP %s: %v\n", f.path, err)
			continue
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			fmt.Printf("Erreur lors de la lecture du fichier SFTP %s: %v\n", f.path, err)
			continue
		}

		exchange := NewExchange(ctx)
		exchange.SetBody(content)
		exchange.SetHeader(CamelFileName, f.name)
		exchange.SetHeader(CamelFilePath, f.path)

		if err := c.processor.Process(exchange); err != nil {
			if !errors.Is(err, ErrStopRouting) {
				fmt.Printf("Erreur lors du traitement du fichier SFTP %s: %v\n", f.path, err)
				if !c.opts.Noop && c.opts.MoveFailed != "" {
					moveSFTPFile(sftpClient, f.path, c.opts.MoveFailed)
				}
			}
			continue
		}

		count++
		if !c.opts.Noop {
			if c.opts.Move != "" {
				moveSFTPFile(sftpClient, f.path, c.opts.Move)
			} else if c.opts.Delete {
				if err := sftpClient.Remove(f.path); err != nil {
					fmt.Printf("Erreur lors de la suppression du fichier SFTP %s: %v\n", f.path, err)
				}
			}
		}
	}
}

func (c *SFTPConsumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	if c.sftpClient != nil {
		c.sftpClient.Close()
		c.sftpClient = nil
	}
	if c.sshClient != nil {
		c.sshClient.Close()
		c.sshClient = nil
	}
	return nil
}

// moveSFTPFile renomme/déplace un fichier sur le serveur SFTP.
func moveSFTPFile(client *sftp.Client, srcPath, destDir string) {
	destPath := destDir + "/" + filepath.Base(srcPath)
	if err := client.MkdirAll(destDir); err == nil || strings.Contains(err.Error(), "exist") {
		if err := client.PosixRename(srcPath, destPath); err != nil {
			fmt.Printf("Erreur déplacement SFTP %s -> %s: %v\n", srcPath, destPath, err)
		}
	} else {
		fmt.Printf("Erreur création répertoire SFTP %s: %v\n", destDir, err)
	}
}
