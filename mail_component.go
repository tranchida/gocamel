package gocamel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
	pop3 "github.com/knadh/go-pop3"
)

// Protocoles mail supportes
const (
	ProtocolSMTP  = "smtp"
	ProtocolSMTPS = "smtps"
	ProtocolPOP3  = "pop3"
	ProtocolPOP3S = "pop3s"
	ProtocolIMAP  = "imap"
	ProtocolIMAPS = "imaps"
)

// En-tetes standards du composant Mail
const (
	// En-tetes entrants (consommateur)
	MailFrom        = "From"
	MailTo          = "To"
	MailCC          = "Cc"
	MailBCC         = "Bcc"
	MailSubject     = "Subject"
	MailMessageID   = "Message-ID"
	MailDate        = "Date"
	MailContentType = "Content-Type"
	MailSize        = "Size"
	MailUID         = "UID"
	MailReplyTo     = "Reply-To"

	// En-tetes for les pieces jointes
	MailAttachmentPrefix = "CamelMailAttachment"

	// En-tetes de controle
	MailDeleteHeader = "CamelMailDelete"
	MailMoveToHeader = "CamelMailMoveTo"
	MailCopyToHeader = "CamelMailCopyTo"
)

// configuration par defaut
const (
	DefaultSMTPPort          = 25
	DefaultSMTPSPort         = 465
	DefaultPOP3Port          = 110
	DefaultPOP3SPort         = 995
	DefaultIMAPPort          = 143
	DefaultIMAPSPort         = 993
	DefaultFolderName        = "INBOX"
	DefaultContentType       = "text/plain"
	DefaultConnectionTimeout = 30000 // ms
	DefaultPollDelay         = 60000 // ms
	DefaultFetchSize         = -1    // illimite
)

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

// MailComponent implemente le composant Mail for GoCamel.
// Supporte SMTP/SMTPS (sending) et POP3/POP3S/IMAP/IMAPS (reception).
type MailComponent struct {
	mu sync.RWMutex
	// configuration partagee
	defaultFrom       string
	defaultSubject    string
	connectionTimeout time.Duration
	debugMode         bool
}

// NewMailComponent cree un MailComponent with les values par defaut.
func NewMailComponent() *MailComponent {
	return &MailComponent{
		defaultFrom:       "gocamel@localhost",
		defaultSubject:    "",
		connectionTimeout: DefaultConnectionTimeout * time.Millisecond,
		debugMode:         false,
	}
}

// SetDefaultFrom configure l'expediteur par defaut.
func (c *MailComponent) SetDefaultFrom(from string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaultFrom = from
}

// SetDefaultSubject configure le sujet par defaut.
func (c *MailComponent) SetDefaultSubject(subject string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	c.defaultSubject = subject
}

// CreateEndpoint cree un MailEndpoint a partir d'une URI.
//
// Formats supportes :
//
//	smtp://host:port?to=dest@example.com&from=src@example.com
//	smtps://host:port?username=user&password=pass
//	imap://host:port?folderName=INBOX&username=user&password=pass
//	pop3://host:port?delete=true&unseen=true
//
// Options communes :
//
//	username, password : Credentials d'authentification
//	from, to, cc, bcc  : Adresses email (producteur)
//	subject            : Sujet du message (producteur)
//	contentType        : text/plain ou text/html (producteur)
//	folderName         : Dossier a consulter (consommateur, defaut: INBOX)
//	delete             : Supprimer les messages apres traitement (consommateur)
//	unseen             : Ne traiter que les messages non lus (consommateur, defaut: true)
//	moveTo             : Deplacer les messages vers ce dossier apres traitement
//	copyTo             : Copier les messages vers ce dossier apres traitement
//	connectionTimeout  : Timeout de connection en ms (defaut: 30000)
//	pollDelay          : Delai between les polls for les consommateurs (ms, defaut: 60000)
//	disconnect         : Se deconnecter apres chaque poll (consommateur)
//	peek               : Marquer les messages comme SEEN uniquement apres traitement reussi
//	fetchSize          : Nombre max de messages a recuperer par poll (defaut: -1 = illimite)
//	skipFailedMessage  : Ignorer les messages en error (consommateur)
//	handleFailedMessage: Gerer les errors via processor (consommateur)
func (c *MailComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("URI mail invalid: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case ProtocolSMTP, ProtocolSMTPS, ProtocolPOP3, ProtocolPOP3S, ProtocolIMAP, ProtocolIMAPS:
		// OK
	default:
		return nil, fmt.Errorf("Unsupported mail protocol: %s", scheme)
	}

	// Host et port
	host := u.Hostname()
	if host == "" {
		host = u.Host
	}
	if host == "" {
		return nil, fmt.Errorf("host required in mail URI: %s", uri)
	}

	port := u.Port()
	if port == "" {
		port = getDefaultPort(scheme)
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return nil, fmt.Errorf("port invalid in l'URI mail: %s", port)
	}

	// Query parameters
	q := u.Query()

	return &MailEndpoint{
		uri:               uri,
		scheme:            scheme,
		host:              host,
		port:              portNum,
		username:          q.Get("username"),
		password:          q.Get("password"),
		from:              getFirstNotEmpty(q.Get("from"), c.defaultFrom),
		to:                q.Get("to"),
		cc:                q.Get("cc"),
		bcc:               q.Get("bcc"),
		subject:           getFirstNotEmpty(q.Get("subject"), c.defaultSubject),
		contentType:       getFirstNotEmpty(q.Get("contentType"), DefaultContentType),
		folderName:        getFirstNotEmpty(q.Get("folderName"), DefaultFolderName),
		delete:            q.Get("delete") == "true",
		unseen:            q.Get("unseen") != "false", // defaut: true
		moveTo:            q.Get("moveTo"),
		copyTo:            q.Get("copyTo"),
		disconnect:        q.Get("disconnect") == "true",
		peek:              q.Get("peek") != "false", // defaut: true
		closeFolder:       q.Get("closeFolder") != "false", // defaut: true
		useIdle:           q.Get("idle") == "true",
		connectionTimeout: parseDurationMs(q.Get("connectionTimeout"), c.connectionTimeout),
		pollDelay:         parseDurationMs(q.Get("pollDelay"), DefaultPollDelay),
		fetchSize:         parseInt(q.Get("fetchSize"), DefaultFetchSize),
		skipFailedMessage: q.Get("skipFailedMessage") == "true",
		handleFailedMessage: q.Get("handleFailedMessage") == "true",
		debugMode:         q.Get("debugMode") == "true",
		component:         c,
	}, nil
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func getDefaultPort(scheme string) string {
	switch scheme {
	case ProtocolSMTP:
		return strconv.Itoa(DefaultSMTPPort)
	case ProtocolSMTPS:
		return strconv.Itoa(DefaultSMTPSPort)
	case ProtocolPOP3:
		return strconv.Itoa(DefaultPOP3Port)
	case ProtocolPOP3S:
		return strconv.Itoa(DefaultPOP3SPort)
	case ProtocolIMAP:
		return strconv.Itoa(DefaultIMAPPort)
	case ProtocolIMAPS:
		return strconv.Itoa(DefaultIMAPSPort)
	default:
		return strconv.Itoa(DefaultSMTPPort)
	}
}

func getFirstNotEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseInt(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

func parseDurationMs(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultVal
	}
	return time.Duration(ms) * time.Millisecond
}

// ---------------------------------------------------------------------------
// Endpoint
// ---------------------------------------------------------------------------

// MailEndpoint represente un endpoint mail configure.
type MailEndpoint struct {
	uri                 string
	scheme              string
	host                string
	port                int
	username            string
	password            string
	from                string
	to                  string
	cc                  string
	bcc                 string
	subject             string
	contentType         string
	folderName          string
	delete              bool
	unseen              bool
	moveTo              string
	copyTo              string
	disconnect          bool
	peek                bool
	closeFolder         bool
	useIdle             bool
	connectionTimeout   time.Duration
	pollDelay           time.Duration
	fetchSize           int
	skipFailedMessage   bool
	handleFailedMessage bool
	debugMode           bool
	component           *MailComponent
}

func (e *MailEndpoint) URI() string { return e.uri }

// isProducerProtocol retourne true si le protocole est for l'sending (SMTP/SMTPS).
func (e *MailEndpoint) isProducerProtocol() bool {
	return e.scheme == ProtocolSMTP || e.scheme == ProtocolSMTPS
}

// isConsumerProtocol retourne true si le protocole est for la reception (POP3/IMAP).
func (e *MailEndpoint) isConsumerProtocol() bool {
	return e.scheme == ProtocolPOP3 || e.scheme == ProtocolPOP3S ||
		e.scheme == ProtocolIMAP || e.scheme == ProtocolIMAPS
}

// isIMAP retourne true si c'est un protocole IMAP/IMAPS
func (e *MailEndpoint) isIMAP() bool {
	return e.scheme == ProtocolIMAP || e.scheme == ProtocolIMAPS
}

// CreateProducer cree un producteur mail si le protocole le permet.
func (e *MailEndpoint) CreateProducer() (Producer, error) {
	if !e.isProducerProtocol() {
		return nil, fmt.Errorf("le protocole %s ne supporte pas la production", e.scheme)
	}
	return &MailProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer cree un consommateur mail si le protocole le permet.
func (e *MailEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	if !e.isConsumerProtocol() {
		return nil, fmt.Errorf("le protocole %s ne supporte pas la consommation", e.scheme)
	}
	// POP3 utilise un consommateur dedie
	if e.scheme == ProtocolPOP3 || e.scheme == ProtocolPOP3S {
		return &Pop3Consumer{
			endpoint:  e,
			processor: processor,
			stopChan:  make(chan struct{}),
		}, nil
	}
	// IMAP utilise MailConsumer with support IDLE
	return &MailConsumer{
		endpoint:  e,
		processor: processor,
		stopChan:  make(chan struct{}),
	}, nil
}

// address retourne l'adresse complete host:port.
func (e *MailEndpoint) address() string {
	return fmt.Sprintf("%s:%d", e.host, e.port)
}

// isSecure retourne true si le protocole utilise SSL/TLS.
func (e *MailEndpoint) isSecure() bool {
	return e.scheme == ProtocolSMTPS || e.scheme == ProtocolPOP3S || e.scheme == ProtocolIMAPS
}

// ---------------------------------------------------------------------------
// Producer
// ---------------------------------------------------------------------------

// MailProducer implemente l'sending d'emails via SMTP/SMTPS.
type MailProducer struct {
	endpoint *MailEndpoint
}

// Start demarre le producteur mail.
func (p *MailProducer) Start(ctx context.Context) error {
	return nil
}

// Stop arrete le producteur mail.
func (p *MailProducer) Stop() error {
	return nil
}

// Send sends email via SMTP/SMTPS.
func (p *MailProducer) Send(exchange *Exchange) error {
	ep := p.endpoint

	// Construction des en-tetes
	from := getMailHeader(exchange, "From", ep.from)
	to := getMailHeader(exchange, "To", ep.to)
	cc := getMailHeader(exchange, "Cc", ep.cc)
	bcc := getMailHeader(exchange, "Bcc", ep.bcc)
	subject := getMailHeader(exchange, "Subject", ep.subject)
	contentType := getMailHeader(exchange, "Content-Type", ep.contentType)

	// validation des adresses requiredes
	if from == "" {
		return errors.New("l'expediteur (from) est required")
	}
	if to == "" && cc == "" && bcc == "" {
		return errors.New("au moins un destinataire (to, cc, bcc) est required")
	}

	// Extraction des destinataires
	recipients := parseAddresses(to)
	recipients = append(recipients, parseAddresses(cc)...)
	recipients = append(recipients, parseAddresses(bcc)...)

	if len(recipients) == 0 {
		return errors.New("aucun destinataire valid")
	}

	// body du message
	body := extractBody(exchange)

	// Construction du message MIME
	message, err := p.buildMessage(from, to, cc, subject, contentType, body, exchange)
	if err != nil {
		return fmt.Errorf("error construction message: %w", err)
	}

	// connection et sending
	return p.sendMail(from, recipients, message)
}

// buildMessage construit un message MIME.
func (p *MailProducer) buildMessage(from, to, cc, subject, contentType string, body []byte, exchange *Exchange) ([]byte, error) {
	var buf bytes.Buffer

	// En-tetes requireds
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	if to != "" {
		fmt.Fprintf(&buf, "To: %s\r\n", to)
	}
	if cc != "" {
		fmt.Fprintf(&buf, "Cc: %s\r\n", cc)
	}
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(&buf, "Message-Id: <%s@%s>\r\n", generateMessageID(), p.endpoint.host)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")

	// Gestion des pieces jointes
	attachments := extractAttachments(exchange)

	if len(attachments) > 0 {
		// Multipart with pieces jointes
		boundary := generateBoundary()
		fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)

		// Partie texte
		fmt.Fprintf(&buf, "--%s\r\n", boundary)
		fmt.Fprintf(&buf, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.Write(body)
		buf.WriteString("\r\n")

		// Pieces jointes
		for name, data := range attachments {
			fmt.Fprintf(&buf, "--%s\r\n", boundary)
			fmt.Fprintf(&buf, "Content-Type: application/octet-stream\r\n")
			fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s\"\r\n", name)
			fmt.Fprintf(&buf, "Content-Transfer-Encoding: base64\r\n\r\n")

			encoded := base64.StdEncoding.EncodeToString(data)
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				buf.WriteString(encoded[i:end])
				buf.WriteString("\r\n")
			}
		}

		// Fin du message
		fmt.Fprintf(&buf, "--%s--\r\n", boundary)
	} else {
		// Message simple without pieces jointes
		fmt.Fprintf(&buf, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.Write(body)
	}

	return buf.Bytes(), nil
}

// sendMail sendinge le message via SMTP/SMTPS with retry et exponential backoff.
func (p *MailProducer) sendMail(from string, recipients []string, message []byte) error {
	ep := p.endpoint
	addr := ep.address()

	var auth smtp.Auth
	if ep.username != "" && ep.password != "" {
		auth = smtp.PlainAuth("", ep.username, ep.password, ep.host)
	}

	// configuration du retry with exponential backoff
	maxRetries := 5
	baseDelay := time.Second
	maxDelay := 8 * time.Second

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Calcul du delai exponentiel: 1s, 2s, 4s, 8s
			delay := baseDelay * time.Duration(1<<uint(attempt-1))
			if delay > maxDelay {
				delay = maxDelay
			}
			if ep.debugMode {
				fmt.Printf("[Mail] Tentative %d/%d apres error: %v (attente %v)\n", attempt+1, maxRetries, lastErr, delay)
			}
			time.Sleep(delay)
		}

		err := p.trySendMail(addr, auth, from, recipients, message)
		if err == nil {
			// Succès
			if attempt > 0 && ep.debugMode {
				fmt.Printf("[Mail] sending reussi apres %d tentative(s)\n", attempt+1)
			}
			return nil
		}

		lastErr = err
		if ep.debugMode {
			fmt.Printf("[Mail] error tentative %d/%d: %v\n", attempt+1, maxRetries, err)
		}

		// Ne pas retry certaines errors (authentification, adresse invalid...)
		if isNonRetryableError(err) {
			return fmt.Errorf("error non recuperable: %w", err)
		}
	}

	return fmt.Errorf("echec apres %d tentatives: %w", maxRetries, lastErr)
}

// trySendMail effectue une tentative d'sending unique.
func (p *MailProducer) trySendMail(addr string, auth smtp.Auth, from string, recipients []string, message []byte) error {
	ep := p.endpoint

	if ep.scheme == ProtocolSMTPS {
		// connection TLS native (port 465)
		tlsConfig := &tls.Config{
			ServerName: ep.host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("error connection TLS: %w", err)
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, ep.host)
		if err != nil {
			return fmt.Errorf("error creation client SMTP: %w", err)
		}
		defer client.Close()

		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("error authentification: %w", err)
			}
		}

		if err := client.Mail(from); err != nil {
			return fmt.Errorf("error MAIL FROM: %w", err)
		}

		for _, rcpt := range recipients {
			if err := client.Rcpt(rcpt); err != nil {
				return fmt.Errorf("error RCPT TO %s: %w", rcpt, err)
			}
		}

		w, err := client.Data()
		if err != nil {
			return fmt.Errorf("error DATA: %w", err)
		}

		if _, err := w.Write(message); err != nil {
			return fmt.Errorf("error ecriture message: %w", err)
		}

		if err := w.Close(); err != nil {
			return fmt.Errorf("error fermeture DATA: %w", err)
		}

		return client.Quit()
	}

	// SMTP standard with STARTTLS si available
	return smtp.SendMail(addr, auth, from, recipients, message)
}

// isNonRetryableError determine si une error ne devrait pas etre retentee.
func isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	// errors d'authentification
	if strings.Contains(errStr, "auth") || strings.Contains(errStr, "credential") {
		return true
	}
	// errors d'adresse invalid
	if strings.Contains(errStr, "invalid") || strings.Contains(errStr, "bad address") {
		return true
	}
	// errors de syntaxe
	if strings.Contains(errStr, "syntax") {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Consumer IMAP complet with go-imap v2
// ---------------------------------------------------------------------------

// MailConsumer implemente la reception d'emails via IMAP (with go-imap v2).
type MailConsumer struct {
	endpoint  *MailEndpoint
	processor Processor
	stopChan  chan struct{}
	wg        sync.WaitGroup
	client    *imapclient.Client
	mu        sync.Mutex
}

// Start demarre le consommateur mail.
func (c *MailConsumer) Start(ctx context.Context) error {
	c.wg.Add(1)
	go c.run(ctx)
	return nil
}

// run execute la boucle de polling ou IDLE according to la configuration.
func (c *MailConsumer) run(ctx context.Context) {
	defer c.wg.Done()

	ep := c.endpoint

	// Si IDLE est active et c'est IMAP, on utilise IDLE
	if ep.useIdle && ep.isIMAP() {
		if err := c.runWithIdle(ctx); err != nil {
			if ep.debugMode {
				fmt.Printf("[Mail] IDLE echoue (%v), fallback on polling\n", err)
			}
			// Fallback vers polling classique
			c.runPolling(ctx)
		}
	} else {
		c.runPolling(ctx)
	}
}

// runPolling execute la boucle de polling classique.
func (c *MailConsumer) runPolling(ctx context.Context) {
	for {
		select {
		case <-c.stopChan:
			return
		case <-ctx.Done():
			return
		default:
			c.poll(ctx)
			select {
			case <-c.stopChan:
				return
			case <-ctx.Done():
				return
			case <-time.After(c.endpoint.pollDelay):
				// Continue
			}
		}
	}
}

// runWithIdle utilise IMAP IDLE for les notifications push en temps reel.
// returns an error si IDLE n'est pas supporte ou echoue.
func (c *MailConsumer) runWithIdle(parentCtx context.Context) error {
	ep := c.endpoint

	if ep.debugMode {
		fmt.Printf("[Mail] Demarrage IMAP IDLE on %s (folder: %s)\n", ep.address(), ep.folderName)
	}

	// connection au serveur
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if err := c.connect(ctx); err != nil {
		return fmt.Errorf("error connection for IDLE: %w", err)
	}

	// Selection du dossier
	if _, err := c.selectFolder(ep.folderName); err != nil {
		c.disconnect()
		return fmt.Errorf("error selection dossier for IDLE: %w", err)
	}

	for {
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()

		if client == nil {
			c.disconnect()
			return errors.New("client deconnecte during IDLE")
		}

		// Demarrer IDLE
		idleCmd, err := client.Idle()
		if err != nil {
			c.disconnect()
			return fmt.Errorf("error demarrage IDLE: %w", err)
		}

		if ep.debugMode {
			fmt.Printf("[Mail] IDLE actif, attente de notifications...\n")
		}

		// Attendre soit une notification, soit un arret
		idleDone := make(chan error, 1)
		go func() {
			// Wait bloque jusqu'a ce qu'une notification arrive ou IDLE termine
			err := idleCmd.Wait()
			idleDone <- err
		}()

		select {
		case <-c.stopChan:
			// Arret demande
			idleCmd.Close()
			
			return nil

		case <-parentCtx.Done():
			// Contexte annule
			idleCmd.Close()
			
			return nil

		case err := <-idleDone:
			// IDLE s'est termine (notification ou error)
			idleCmd.Close()

			if err != nil {
				if ep.debugMode {
					fmt.Printf("[Mail] IDLE termine with error: %v\n", err)
				}
				c.disconnect()
				return err
			}

			// Notification recue - traiter les messages
			if ep.debugMode {
				fmt.Printf("[Mail] IDLE a detecte une modification, traitement...\n")
			}

			if processErr := c.processNewMessages(parentCtx); processErr != nil {
				if !ep.skipFailedMessage {
					fmt.Printf("[Mail] error traitement messages: %v\n", processErr)
				}
			}

			// Petit delai for eviter boucle trop rapide
			time.Sleep(100 * time.Millisecond)
			// Continue la boucle for redemarrer IDLE
		}
	}
}

// processNewMessages recherche et traite les nouveaux messages non lus.
func (c *MailConsumer) processNewMessages(parentCtx context.Context) error {
	ep := c.endpoint

	// Rechercher les messages non lus
	var uids []imap.UID
	var err error

	if ep.unseen {
		uids, err = c.searchUnseen()
	} else {
		uids, err = c.searchAll()
	}

	if err != nil {
		return fmt.Errorf("error recherche messages: %w", err)
	}

	if len(uids) == 0 {
		return nil
	}

	if ep.debugMode {
		fmt.Printf("[Mail] %d messages trouves via IDLE\n", len(uids))
	}

	// Limitation fetchSize
	if ep.fetchSize > 0 && len(uids) > ep.fetchSize {
		uids = uids[:ep.fetchSize]
	}

	selected, err := c.selectFolder(ep.folderName)
	if err != nil {
		return fmt.Errorf("error selection dossier: %w", err)
	}

	// Traiter chaque message
	for _, uid := range uids {
		select {
		case <-c.stopChan:
			return nil
		case <-parentCtx.Done():
			return nil
		default:
		}

		if err := c.processMessage(parentCtx, uid, selected); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, uid, err); err2 != nil {
					fmt.Printf("[Mail] error handler: %v\n", err2)
				}
			} else if !ep.skipFailedMessage {
				fmt.Printf("[Mail] error traitement message %d: %v\n", uid, err)
			}
		}
	}

	return nil
}

// poll se connecte au serveur IMAP et recupere les nouveaux messages.
func (c *MailConsumer) poll(parentCtx context.Context) {
	ep := c.endpoint

	// Contexte with timeout for cette operation de poll
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if ep.debugMode {
		fmt.Printf("[Mail] Polling %s (folder: %s)\n", ep.address(), ep.folderName)
	}

	// connection au serveur IMAP
	if err := c.connect(ctx); err != nil {
		if !ep.skipFailedMessage {
			fmt.Printf("[Mail] error connection: %v\n", err)
		}
		return
	}

	// Deconnection a la fin si demande
	if ep.disconnect {
		defer c.disconnect()
	}

	// Selection du dossier
	selected, err := c.selectFolder(ep.folderName)
	if err != nil {
		if !ep.skipFailedMessage {
			fmt.Printf("[Mail] error selection dossier %s: %v\n", ep.folderName, err)
		}
		return
	}

	// Recherche des messages non lus si option unseen
	var uids []imap.UID
	if ep.unseen {
		uids, err = c.searchUnseen()
		if err != nil {
			if ep.debugMode {
				fmt.Printf("[Mail] error recherche: %v\n", err)
			}
			// Fallback: utiliser Fetch with range
			uids = nil
		}
	} else {
		// Sinon chercher tous les messages
		uids, err = c.searchAll()
		if err != nil {
			if ep.debugMode {
				fmt.Printf("[Mail] error recherche tous: %v\n", err)
			}
			uids = nil
		}
	}

	// Si pas de resultats, quitter
	if len(uids) == 0 {
		if ep.debugMode {
			fmt.Printf("[Mail] Aucun nouveau message\n")
		}
		return
	}

	if ep.debugMode {
		fmt.Printf("[Mail] %d messages trouves\n", len(uids))
	}

	// Limitation fetchSize
	if ep.fetchSize > 0 && len(uids) > ep.fetchSize {
		uids = uids[:ep.fetchSize]
	}

	// Traitement des messages
	for _, uid := range uids {
		select {
		case <-c.stopChan:
			return
		case <-parentCtx.Done():
			return
		default:
		}

		if err := c.processMessage(parentCtx, uid, selected); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, uid, err); err2 != nil {
					fmt.Printf("[Mail] error handler: %v\n", err2)
				}
			} else if !ep.skipFailedMessage {
				fmt.Printf("[Mail] error traitement message %d: %v\n", uid, err)
			}
		}
	}

	if ep.debugMode {
		fmt.Printf("[Mail] Poll termine\n")
	}
}

// connect etablit la connection au serveur IMAP.
func (c *MailConsumer) connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		// Deja connecte, verifier si toujours vivant
		noopCmd := c.client.Noop()
		if err := noopCmd.Wait(); err == nil {
			return nil
		}
		// Reconnection necessaire
		c.client = nil
	}

	ep := c.endpoint

	// Options de connection - TLS config
	tlsConfig := &tls.Config{
		ServerName: ep.host,
	}

	var client *imapclient.Client
	var err error

	if ep.isSecure() {
		// IMAPS on TLS natif (port 993)
		client, err = imapclient.DialTLS(ep.address(), &imapclient.Options{
			TLSConfig: tlsConfig,
		})
	} else {
		// IMAP with STARTTLS possible (port 143)
		client, err = imapclient.DialStartTLS(ep.address(), &imapclient.Options{
			TLSConfig: tlsConfig,
		})
	}

	if err != nil {
		return fmt.Errorf("error connection IMAP: %w", err)
	}

	// Authentification
	if ep.username != "" && ep.password != "" {
		if err := client.Login(ep.username, ep.password).Wait(); err != nil {
			client.Close()
			return fmt.Errorf("error authentification: %w", err)
		}
	}

	c.client = client
	return nil
}

// disconnect ferme la connection.
func (c *MailConsumer) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		c.client.Logout().Wait()
		c.client.Close()
		c.client = nil
	}
}

// selectFolder selectionne un dossier.
func (c *MailConsumer) selectFolder(folderName string) (*imap.SelectData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client non connecte")
	}

	opts := &imap.SelectOptions{}
	return c.client.Select(folderName, opts).Wait()
}

// searchUnseen recherche les messages non lus.
func (c *MailConsumer) searchUnseen() ([]imap.UID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client non connecte")
	}

	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}

	searchData, err := c.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	return searchData.AllUIDs(), nil
}

// searchAll recherche tous les messages.
func (c *MailConsumer) searchAll() ([]imap.UID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client non connecte")
	}

	// Critere vide = tous les messages
	criteria := &imap.SearchCriteria{}

	searchData, err := c.client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, err
	}

	return searchData.AllUIDs(), nil
}

// fetchMessage recupere le contenu d'un message.
func (c *MailConsumer) fetchMessage(uid imap.UID, options *imap.FetchOptions) (*imapclient.FetchMessageBuffer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil, errors.New("client non connecte")
	}

	// Utiliser UID comme sequence set
	uidSet := imap.UIDSetNum(uid)

	cmd := c.client.Fetch(uidSet, options)
	messages, err := cmd.Collect()
	if err != nil {
		return nil, err
	}

	if len(messages) == 0 {
		return nil, fmt.Errorf("message %d non trouve", uid)
	}

	return messages[0], nil
}

// processMessage recupere et traite un message.
func (c *MailConsumer) processMessage(ctx context.Context, uid imap.UID, selected *imap.SelectData) error {
	ep := c.endpoint

	// Recuperation du message with toutes les infos necessaires
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		Flags:       true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}}, // body entier
	}

	msg, err := c.fetchMessage(uid, fetchOptions)
	if err != nil {
		return fmt.Errorf("error recuperation message: %w", err)
	}

	// Parsing du message
	mailMsg, err := c.parseMessage(msg)
	if err != nil {
		return fmt.Errorf("error parsing message: %w", err)
	}

	// Creation de l'exchange
	exchange := NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	// Marquer comme vu si peek=false
	if !ep.peek {
		if err := c.markSeen(uid); err != nil {
			fmt.Printf("[Mail] error marquage SEEN: %v\n", err)
		}
	}

	// Traitement par le processor
	if err := c.processor.Process(exchange); err != nil {
		// Echec du traitement - rollback si peek=true
		if ep.peek {
			if err2 := c.markUnseen(uid); err2 != nil {
				fmt.Printf("[Mail] error rollback SEEN: %v\n", err2)
			}
		}
		return err
	}

	// Post-traitement according to headers modifies par le processor
	return c.postProcess(uid, exchange)
}

// parseMessage extrait les donnees d'un message IMAP.
func (c *MailConsumer) parseMessage(msg *imapclient.FetchMessageBuffer) (*MailMessage, error) {
	mailMsg := &MailMessage{
		UID:         uint32(msg.UID),
		Headers:     make(map[string]string),
		Attachments: make(map[string][]byte),
		Date:        time.Now(),
	}

	// Extraction des infos from l'enveloppe IMAP si available
	if msg.Envelope != nil {
		mailMsg.Subject = msg.Envelope.Subject
		if len(msg.Envelope.From) > 0 {
			mailMsg.From = msg.Envelope.From[0].Addr()
		}
		if len(msg.Envelope.ReplyTo) > 0 {
			mailMsg.ReplyTo = msg.Envelope.ReplyTo[0].Addr()
		}
		for _, to := range msg.Envelope.To {
			mailMsg.To = append(mailMsg.To, to.Addr())
		}
		for _, cc := range msg.Envelope.Cc {
			mailMsg.Cc = append(mailMsg.Cc, cc.Addr())
		}
		if msg.Envelope.MessageID != "" {
			mailMsg.MessageID = msg.Envelope.MessageID
		}
		if !msg.Envelope.Date.IsZero() {
			mailMsg.Date = msg.Envelope.Date
		}
	}

	// Etat des flags
	for _, flag := range msg.Flags {
		if flag == imap.FlagSeen {
			mailMsg.Seen = true
		}
		if flag == imap.FlagDeleted {
			mailMsg.Deleted = true
		}
	}

	// Extraction du body from BodySection
	if len(msg.BodySection) > 0 {
		bodyData := msg.BodySection[0].Bytes

		// Parsing with go-message si c'est un message MIME
		if len(bodyData) > 0 {
			mailMsg.Body, mailMsg.BodyHTML, mailMsg.Attachments = c.parseMimeMessage(bodyData)
			if mailMsg.Body == nil {
				// Fallback: contenu brut
				mailMsg.Body = bodyData
			}
		}
	}

	return mailMsg, nil
}

// parseMimeMessageResult contient le resultat du parsing MIME.
type parseMimeMessageResult struct {
	bodyText    []byte
	bodyHTML    []byte
	attachments map[string][]byte
}

// parseMimeMessage analyse un message MIME et extrait body + pieces jointes.
// Version amelioree gerant multipart/alternative et les encodings.
// Delegue a la fonction commune partagee with POP3.
func (c *MailConsumer) parseMimeMessage(data []byte) ([]byte, []byte, map[string][]byte) {
	return parseMimeMessageCommon(data)
}

// parseEntity analyse recursivement une entite MIME.
func (c *MailConsumer) parseEntity(entity *mail.Reader, result *parseMimeMessageResult, parentContentType string) {
	// Headers du message
	header := entity.Header
	contentType, _, _ := header.ContentType()

	switch {
	case strings.HasPrefix(contentType, "multipart/alternative"):
		// Choisir la meilleure partie available (HTML prefere au texte)
		c.parseMultipartAlternative(entity, result)

	case strings.HasPrefix(contentType, "multipart/"): // multipart/mixed, multipart/related, etc.
		// Scanner toutes les parties
		for {
			part, err := entity.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			// Traiter directement la partie
			c.handlePart(part, result)
		}

	case strings.HasPrefix(contentType, "text/"):
		// Contenu texte simple
		c.handleTextEntity(entity, result, contentType)

	default:
		// Autre contenu (binaire, piece jointe...)
		c.handleAttachmentEntity(entity, result)
	}
}

// parseMultipartAlternative gère multipart/alternative en choisissant le meilleur format.
func (c *MailConsumer) parseMultipartAlternative(entity *mail.Reader, result *parseMimeMessageResult) {
	var textPart []byte
	var htmlPart []byte

	for {
		part, err := entity.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
		break
	}

		// Utiliser Get for obtenir le Content-Type du header PartHeader
		contentType := part.Header.Get("Content-Type")
		partData, err := c.decodePart(part)
		if err != nil {
			continue
		}

		switch {
		case strings.HasPrefix(contentType, "text/plain"):
			textPart = partData
		case strings.HasPrefix(contentType, "text/html"):
			htmlPart = partData
		default:
			// Autre alternative, la garder si on n'a rien de mieux
			if textPart == nil {
				textPart = partData
			}
		}
	}

	result.bodyText = textPart
	result.bodyHTML = htmlPart
}

// handlePart traite une partie multipart (simplifie).
func (c *MailConsumer) handlePart(part *mail.Part, result *parseMimeMessageResult) {
	// PartHeader n'a pas de methode ContentType(), utiliser Get()
	contentType := part.Header.Get("Content-Type")
	contentDisp := part.Header.Get("Content-Disposition")
	
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	// Verifier si c'est une piece jointe par Content-Disposition
	isAttachment := strings.Contains(contentDisp, "attachment")
	
	// Si c'est du texte et pas une piece jointe
	if strings.HasPrefix(contentType, "text/") && !isAttachment {
		if strings.HasPrefix(contentType, "text/html") {
			result.bodyHTML = data
		} else {
			result.bodyText = data
		}
	} else {
		// Piece jointe ou autre contenu
		filename := c.extractFilenameFromDisposition(contentDisp, contentType)
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleSimplePart traite une partie simple non-MIME.
func (c *MailConsumer) handleSimplePart(part *mail.Part, result *parseMimeMessageResult) {
	// PartHeader n'a pas de methode ContentType(), utiliser Get()
	contentType := part.Header.Get("Content-Type")
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/") {
		if result.bodyText == nil {
			result.bodyText = data
		}
	} else {
		// Probablement une piece jointe
		// for PartHeader, extraire manuellement le filename
		contentDisp := part.Header.Get("Content-Disposition")
		filename := c.extractFilenameFromDisposition(contentDisp, part.Header.Get("Content-Type"))
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleTextEntity traite une entite texte.
func (c *MailConsumer) handleTextEntity(entity *mail.Reader, result *parseMimeMessageResult, contentType string) {
	// Lire le body from l'entite - entity.Reader n'est pas un io.Reader
	// On doit utiliser NextPart for lire les parties
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/html") {
		result.bodyHTML = data
	} else {
		result.bodyText = data
	}
}

// handleAttachmentEntity traite une entite comme piece jointe.
func (c *MailConsumer) handleAttachmentEntity(entity *mail.Reader, result *parseMimeMessageResult) {
	filename := c.extractFilename(entity.Header)
	if filename == "" {
		filename = "attachment.bin"
	}
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := c.decodePart(part)
	if err != nil {
		return
	}

	if len(data) > 0 {
		result.attachments[filename] = data
	}
}

// decodePart decode une partie with encodage (base64, quoted-printable).
func (c *MailConsumer) decodePart(part *mail.Part) ([]byte, error) {
	// go-message gere deja le decoding via le body du part
	return io.ReadAll(part.Body)
}

// extractFilenameFromDisposition extrait le filename from Content-Disposition et Content-Type.
func (c *MailConsumer) extractFilenameFromDisposition(contentDisposition, contentType string) string {
	if contentDisposition != "" {
		if strings.Contains(contentDisposition, "filename=") {
			parts := strings.Split(contentDisposition, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], ` "'`)
				if idx := strings.Index(filename, ";"); idx > 0 {
					filename = filename[:idx]
					filename = strings.Trim(filename, ` "'`)
				}
				return filename
			}
		}
	}
	// Essayer Content-Type name parameter
	if strings.Contains(contentType, "name=") {
		parts := strings.Split(contentType, "name=")
		if len(parts) > 1 {
			return strings.Trim(parts[1], ` "'`)
		}
	}
	return ""
}

// extractFilename extrait le nom de file des headers.
func (c *MailConsumer) extractFilename(header mail.Header) string {
	// Content-Disposition
	disp, _, _ := header.ContentDisposition()
	if disp != "" {
		if strings.Contains(disp, "filename=") {
			parts := strings.Split(disp, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], ` "'`)
				// Nettoyer les parametres supplementaires
				if idx := strings.Index(filename, ";"); idx > 0 {
					filename = filename[:idx]
					filename = strings.Trim(filename, ` "'`)
				}
				return filename
			}
		}
	}

	// Content-Type name parameter
	_, params, _ := header.ContentType()
	if name, ok := params["name"]; ok && name != "" {
		return name
	}

	return ""
}

// populateExchange remplit l'exchange with les donnees du message.
func (c *MailConsumer) populateExchange(exchange *Exchange, msg *MailMessage) {
	exchange.SetBody(msg.Body)
	exchange.SetHeader(MailFrom, msg.From)
	exchange.SetHeader(MailReplyTo, msg.ReplyTo)
	exchange.SetHeader(MailTo, strings.Join(msg.To, ", "))
	exchange.SetHeader(MailCC, strings.Join(msg.Cc, ", "))
	exchange.SetHeader(MailSubject, msg.Subject)
	exchange.SetHeader(MailMessageID, msg.MessageID)
	exchange.SetHeader(MailDate, msg.Date.Format(time.RFC3339))
	exchange.SetHeader(MailContentType, msg.ContentType)
	exchange.SetHeader(MailSize, strconv.Itoa(len(msg.Body)))
	exchange.SetHeader(MailUID, strconv.FormatUint(uint64(msg.UID), 10))

	// Si version HTML existe, l'addinger comme propriete
	if msg.BodyHTML != nil {
		exchange.SetProperty("CamelMailBodyHTML", msg.BodyHTML)
	}

	for k, v := range msg.Headers {
		if k != MailFrom && k != MailTo && k != MailSubject && k != MailDate && k != MailContentType {
			exchange.SetHeader(k, v)
		}
	}

	// Pieces jointes comme headers speciaux
	for name, data := range msg.Attachments {
		exchange.SetHeader(MailAttachmentPrefix+"_"+name, data)
	}
}

// markSeen marque un message comme lu.
func (c *MailConsumer) markSeen(uid imap.UID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client non connecte")
	}

	uidSet := imap.UIDSetNum(uid)
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}

	cmd := c.client.Store(uidSet, store, nil)
	// Attendre que la commande termine en consommant les data
	_, err := cmd.Collect()
	cmd.Close()
	return err
}

// markUnseen enleve le flag SEEN.
func (c *MailConsumer) markUnseen(uid imap.UID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client non connecte")
	}

	uidSet := imap.UIDSetNum(uid)
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsDel,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagSeen},
	}

	cmd := c.client.Store(uidSet, store, nil)
	_, err := cmd.Collect()
	cmd.Close()
	return err
}

// postProcess applique les actions post-traitement (delete, move, copy).
func (c *MailConsumer) postProcess(uid imap.UID, exchange *Exchange) error {
	ep := c.endpoint

	// Delete
	if ep.delete || c.getBoolHeader(exchange, MailDeleteHeader) {
		if err := c.deleteMessage(uid); err != nil {
			return fmt.Errorf("error deletion: %w", err)
		}
		return nil
	}

	// MoveTo
	if moveTo := c.getStringHeader(exchange, MailMoveToHeader); moveTo != "" {
		if err := c.moveMessage(uid, moveTo); err != nil {
			return fmt.Errorf("error deplacement: %w", err)
		}
		return nil
	}

	// MoveTo from config
	if ep.moveTo != "" {
		if err := c.moveMessage(uid, ep.moveTo); err != nil {
			return fmt.Errorf("error deplacement: %w", err)
		}
		return nil
	}

	// CopyTo
	if copyTo := c.getStringHeader(exchange, MailCopyToHeader); copyTo != "" {
		if err := c.copyMessage(uid, copyTo); err != nil {
			return fmt.Errorf("error copie: %w", err)
		}
	}

	// CopyTo from config
	if ep.copyTo != "" {
		if err := c.copyMessage(uid, ep.copyTo); err != nil {
			return fmt.Errorf("error copie: %w", err)
		}
	}

	return nil
}

// deleteMessage supprime un message.
func (c *MailConsumer) deleteMessage(uid imap.UID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client non connecte")
	}

	uidSet := imap.UIDSetNum(uid)

	// addinger le flag \Deleted
	store := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Silent: true,
		Flags:  []imap.Flag{imap.FlagDeleted},
	}

	cmd := c.client.Store(uidSet, store, nil)
	_, err := cmd.Collect()
	cmd.Close()
	if err != nil {
		return err
	}

	// Expunger (deletion definitive)
	expungeCmd := c.client.Expunge()
	_, err = expungeCmd.Collect()
	expungeCmd.Close()
	return err
}

// moveMessage deplace un message vers un autre dossier.
func (c *MailConsumer) moveMessage(uid imap.UID, folder string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client non connecte")
	}

	uidSet := imap.UIDSetNum(uid)

	// Utiliser MOVE si supporte par le serveur, sinon COPY + DELETE
	// for go-imap v2, on utilise Move qui retourne MoveData
	_, err := c.client.Move(uidSet, folder).Wait()
	if err != nil {
		// MOVE non supporte, fallback on COPY + DELETE
		_, err := c.client.Copy(uidSet, folder).Wait()
		if err != nil {
			return err
		}
		// Marquer comme supprime in le dossier source
		store := &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Silent: true,
			Flags:  []imap.Flag{imap.FlagDeleted},
		}
		cmd := c.client.Store(uidSet, store, nil)
		_, err = cmd.Collect()
		cmd.Close()
		if err != nil {
			return err
		}
		expungeCmd := c.client.Expunge()
		_, err = expungeCmd.Collect()
		expungeCmd.Close()
		return err
	}

	return nil
}

// copyMessage copie un message vers un autre dossier.
func (c *MailConsumer) copyMessage(uid imap.UID, folder string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return errors.New("client non connecte")
	}

	uidSet := imap.UIDSetNum(uid)
	_, err := c.client.Copy(uidSet, folder).Wait()
	return err
}

// handleFailedMessage permet de traiter une error via le processor.
func (c *MailConsumer) handleFailedMessage(ctx context.Context, uid imap.UID, msgErr error) error {
	exchange := NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedUID", strconv.FormatUint(uint64(uid), 10))

	return c.processor.Process(exchange)
}

// getStringHeader recupere une value string d'un header.
func (c *MailConsumer) getStringHeader(exchange *Exchange, header string) string {
	if v, exists := exchange.GetOut().GetHeader(header); exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	if v, exists := exchange.GetIn().GetHeader(header); exists {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// getBoolHeader recupere une value bool d'un header.
func (c *MailConsumer) getBoolHeader(exchange *Exchange, header string) bool {
	if v, exists := exchange.GetOut().GetHeader(header); exists {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return strings.EqualFold(s, "true")
		}
	}
	return false
}

// Stop arrete le consommateur mail.
func (c *MailConsumer) Stop() error {
	close(c.stopChan)
	c.wg.Wait()
	c.disconnect()
	return nil
}

// ---------------------------------------------------------------------------
// Types de donnees
// ---------------------------------------------------------------------------

// MailMessage represente un message email recu.
type MailMessage struct {
	MessageID   string
	From        string
	ReplyTo     string
	To          []string
	Cc          []string
	Bcc         []string
	Subject     string
	Date        time.Time
	Body        []byte
	BodyHTML    []byte
	ContentType string
	Headers     map[string]string
	Attachments map[string][]byte
	Seen        bool
	Deleted     bool
	UID         uint32
}

// ---------------------------------------------------------------------------
// Helper functions for le producteur
// ---------------------------------------------------------------------------

func getMailHeader(exchange *Exchange, header, defaultVal string) string {
	if v, exists := exchange.GetIn().GetHeader(header); exists {
		switch s := v.(type) {
		case string:
			if s != "" {
				return s
			}
		}
	}
	return defaultVal
}

func parseAddresses(s string) []string {
	if s == "" {
		return nil
	}
	var addrs []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			addrs = append(addrs, part)
		}
	}
	return addrs
}

func extractBody(exchange *Exchange) []byte {
	switch v := exchange.GetIn().GetBody().(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		if v != nil {
			return []byte(fmt.Sprintf("%v", v))
		}
		return nil
	}
}

func extractAttachments(exchange *Exchange) map[string][]byte {
	attachments := make(map[string][]byte)

	for key, value := range exchange.GetIn().GetHeaders() {
		if strings.HasPrefix(key, MailAttachmentPrefix+"_") {
			name := strings.TrimPrefix(key, MailAttachmentPrefix+"_")
			switch data := value.(type) {
			case []byte:
				attachments[name] = data
			case string:
				attachments[name] = []byte(data)
			}
		}
	}

	return attachments
}

func generateMessageID() string {
	return fmt.Sprintf("%d.%d.%d", time.Now().UnixNano(), time.Now().Unix(), time.Now().Nanosecond())
}

func generateBoundary() string {
	return fmt.Sprintf("----=_Part_%d_%d", time.Now().UnixNano(), time.Now().Unix())
}

// ---------------------------------------------------------------------------
// Consumer POP3
// ---------------------------------------------------------------------------

// Pop3Consumer implemente la reception d'emails via POP3/POP3S.
type Pop3Consumer struct {
	endpoint  *MailEndpoint
	processor Processor
	stopChan  chan struct{}
	wg        sync.WaitGroup
	client    *pop3.Conn
	mu        sync.Mutex
}

// Start demarre le consommateur POP3.
func (c *Pop3Consumer) Start(ctx context.Context) error {
	c.wg.Add(1)
	go c.run(ctx)
	return nil
}

// run execute la boucle de polling POP3.
func (c *Pop3Consumer) run(ctx context.Context) {
	defer c.wg.Done()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ctx.Done():
			return
		default:
			c.poll(ctx)
			select {
			case <-c.stopChan:
				return
			case <-ctx.Done():
				return
			case <-time.After(c.endpoint.pollDelay):
				// Continue
			}
		}
	}
}

// pop3MessageID represente un message in la liste POP3.
type pop3MessageID struct {
	ID   int
	Size int
	UID  string
}

// poll se connecte au serveur POP3 et recupere les nouveaux messages.
func (c *Pop3Consumer) poll(parentCtx context.Context) {
	ep := c.endpoint

	// Contexte with timeout for cette operation
	ctx, cancel := context.WithTimeout(parentCtx, ep.connectionTimeout)
	defer cancel()

	if ep.debugMode {
		fmt.Printf("[POP3] Polling %s\n", ep.address())
	}

	// connection au serveur POP3
	if err := c.connect(ctx); err != nil {
		if !ep.skipFailedMessage {
			fmt.Printf("[POP3] error connection: %v\n", err)
		}
		return
	}
	defer c.disconnect()

	// Liste des messages
	count, size, err := c.client.Stat()
	if err != nil {
		if !ep.skipFailedMessage {
			fmt.Printf("[POP3] error stat messages: %v\n", err)
		}
		return
	}

	if count == 0 {
		if ep.debugMode {
			fmt.Printf("[POP3] Aucun nouveau message\n")
		}
		return
	}

	if ep.debugMode {
		fmt.Printf("[POP3] %d messages trouves (size: %d)\n", count, size)
	}

	// Construire la liste des IDs de messages
	var msgIDs []int
	for i := 1; i <= count; i++ {
		msgIDs = append(msgIDs, i)
	}

	// Limitation fetchSize
	if ep.fetchSize > 0 && len(msgIDs) > ep.fetchSize {
		msgIDs = msgIDs[:ep.fetchSize]
	}

	// Traiter les messages en ordre inverse (plus anciens d'abord)
	for i := len(msgIDs) - 1; i >= 0; i-- {
		select {
		case <-c.stopChan:
			return
		case <-parentCtx.Done():
			return
		default:
		}

		msgID := msgIDs[i]
		if err := c.processMessage(parentCtx, msgID); err != nil {
			if ep.handleFailedMessage {
				if err2 := c.handleFailedMessage(parentCtx, msgID, err); err2 != nil {
					fmt.Printf("[POP3] error handler: %v\n", err2)
				}
			} else if !ep.skipFailedMessage {
				fmt.Printf("[POP3] error traitement message %d: %v\n", msgID, err)
			}
		}
	}

	if ep.debugMode {
		fmt.Printf("[POP3] Poll termine\n")
	}
}

// connect etablit la connection au serveur POP3.
func (c *Pop3Consumer) connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		// Deja connecte
		return nil
	}

	ep := c.endpoint

	// Creer le client POP3
	clientOpt := pop3.Opt{
		Host: ep.host,
		Port: ep.port,
	}

	// TLS for POP3S
	if ep.isSecure() {
		clientOpt.TLSEnabled = true
	}

	// Creer le client et obtenir une connection
	p3 := pop3.New(clientOpt)
	conn, err := p3.NewConn()
	if err != nil {
		return fmt.Errorf("error connection POP3: %w", err)
	}

	// Authentification
	if ep.username != "" && ep.password != "" {
		if err := conn.Auth(ep.username, ep.password); err != nil {
			conn.Quit()
			return fmt.Errorf("error authentification POP3: %w", err)
		}
	}

	c.client = conn
	return nil
}

// disconnect ferme la connection POP3.
func (c *Pop3Consumer) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		c.client.Quit()
		c.client = nil
	}
}

// processMessage recupere et traite un message POP3.
func (c *Pop3Consumer) processMessage(ctx context.Context, msgID int) error {
	// Telecharger le message complet
	msgData, err := c.client.Retr(msgID)
	if err != nil {
		return fmt.Errorf("error recuperation message: %w", err)
	}

	// Parser le message
	mailMsg, err := c.parsePop3Message(msgData)
	if err != nil {
		return fmt.Errorf("error parsing message: %w", err)
	}
	mailMsg.UID = uint32(msgID)

	// Creation de l'exchange
	exchange := NewExchange(ctx)
	c.populateExchange(exchange, mailMsg)

	// Traitement par le processor
	if err := c.processor.Process(exchange); err != nil {
		return err
	}

	// Post-traitement according to les options
	return c.postProcess(msgID, exchange)
}

// parsePop3Message analyse un message POP3 extrait.
func (c *Pop3Consumer) parsePop3Message(msgData *message.Entity) (*MailMessage, error) {
	mailMsg := &MailMessage{
		Headers:     make(map[string]string),
		Attachments: make(map[string][]byte),
		Date:        time.Now(),
	}

	// Parsing MIME with go-message
	// Lire le body complet from msgData.Body
	bodyData, err := io.ReadAll(msgData.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading body: %w", err)
	}

	body, bodyHTML, attachments := c.parseMimeMessage(bodyData)
	mailMsg.Body = body
	mailMsg.BodyHTML = bodyHTML
	mailMsg.Attachments = attachments

	// Extraction des headers from l'entite message.Header
	// Utiliser Fields() et iterer manuellement
	mailMsg.Headers = make(map[string]string)
	fields := msgData.Header.Fields()
	for fields.Next() {
		key := fields.Key()
		text, err := fields.Text()
		if err != nil {
			text = fields.Value() // fallback on value brute
		}
		mailMsg.Headers[key] = text

		// Extraire les champs importants
		switch key {
		case "From":
			mailMsg.From = text
		case "Reply-To":
			mailMsg.ReplyTo = text
		case "To":
			mailMsg.To = parseAddresses(text)
		case "Cc":
			mailMsg.Cc = parseAddresses(text)
		case "Subject":
			mailMsg.Subject = text
		case "Message-Id":
			mailMsg.MessageID = text
		case "Content-Type":
			mailMsg.ContentType = text
		case "Date":
			if date, err := time.Parse(time.RFC1123, text); err == nil {
				mailMsg.Date = date
			}
		}
	}

	return mailMsg, nil
}

// parseMimeMessage analyse un message MIME for POP3.
func (c *Pop3Consumer) parseMimeMessage(data []byte) ([]byte, []byte, map[string][]byte) {
	return parseMimeMessageCommon(data)
}

// populateExchange remplit l'exchange with les donnees du message POP3.
func (c *Pop3Consumer) populateExchange(exchange *Exchange, msg *MailMessage) {
	exchange.SetBody(msg.Body)
	exchange.SetHeader(MailFrom, msg.From)
	exchange.SetHeader(MailReplyTo, msg.ReplyTo)
	exchange.SetHeader(MailTo, strings.Join(msg.To, ", "))
	exchange.SetHeader(MailCC, strings.Join(msg.Cc, ", "))
	exchange.SetHeader(MailSubject, msg.Subject)
	exchange.SetHeader(MailMessageID, msg.MessageID)
	if !msg.Date.IsZero() {
		exchange.SetHeader(MailDate, msg.Date.Format(time.RFC3339))
	}
	exchange.SetHeader(MailContentType, msg.ContentType)
	exchange.SetHeader(MailSize, strconv.Itoa(len(msg.Body)))
	exchange.SetHeader(MailUID, strconv.FormatUint(uint64(msg.UID), 10))

	// Si version HTML existe, l'addinger comme propriete
	if msg.BodyHTML != nil {
		exchange.SetProperty("CamelMailBodyHTML", msg.BodyHTML)
	}

	for k, v := range msg.Headers {
		if k != MailFrom && k != MailTo && k != MailSubject && k != MailDate && k != MailContentType {
			exchange.SetHeader(k, v)
		}
	}

	// Pieces jointes
	for name, data := range msg.Attachments {
		exchange.SetHeader(MailAttachmentPrefix+"_"+name, data)
	}
}

// postProcess applique les actions post-traitement (delete).
func (c *Pop3Consumer) postProcess(msgID int, exchange *Exchange) error {
	ep := c.endpoint

	// Delete
	if ep.delete || c.getBoolHeader(exchange, MailDeleteHeader) {
		if err := c.client.Dele(msgID); err != nil {
			return fmt.Errorf("error deletion message %d: %w", msgID, err)
		}
		return nil
	}

	return nil
}

// handleFailedMessage permet de traiter une error via le processor.
func (c *Pop3Consumer) handleFailedMessage(ctx context.Context, msgID int, msgErr error) error {
	exchange := NewExchange(ctx)
	exchange.SetHeader("CamelMailError", msgErr.Error())
	exchange.SetHeader("CamelMailFailedMsgID", strconv.Itoa(msgID))

	return c.processor.Process(exchange)
}

// getBoolHeader recupere une value bool d'un header.
func (c *Pop3Consumer) getBoolHeader(exchange *Exchange, header string) bool {
	if v, exists := exchange.GetOut().GetHeader(header); exists {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return strings.EqualFold(s, "true")
		}
	}
	return false
}

// Stop arrete le consommateur POP3.
func (c *Pop3Consumer) Stop() error {
	close(c.stopChan)
	c.wg.Wait()
	c.disconnect()
	return nil
}

// parseMimeMessageCommon fonction utilitaire partagee for le parsing MIME.
func parseMimeMessageCommon(data []byte) ([]byte, []byte, map[string][]byte) {
	result := &parseMimeMessageResult{
		attachments: make(map[string][]byte),
	}

	// Essai de parsing comme entite mail
	entity, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		// Pas un MIME valid, retourner comme texte brut
		return data, nil, result.attachments
	}

	// Analyse recursive des parties
	parseEntityCommon(entity, result, "")

	return result.bodyText, result.bodyHTML, result.attachments
}

// parseEntityCommon fonction utilitaire for parser une entite MIME.
func parseEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, parentContentType string) {
	// Headers du message
	header := entity.Header
	contentType, _, _ := header.ContentType()

	switch {
	case strings.HasPrefix(contentType, "multipart/alternative"):
		// Choisir la meilleure partie available (HTML prefere au texte)
		parseMultipartAlternativeCommon(entity, result)

	case strings.HasPrefix(contentType, "multipart/"): // multipart/mixed, multipart/related, etc.
		// Scanner toutes les parties
		for {
			part, err := entity.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			// Traiter directement la partie
			handlePartCommon(part, result)
		}

	case strings.HasPrefix(contentType, "text/"):
		// Contenu texte simple
		handleTextEntityCommon(entity, result, contentType)

	default:
		// Autre contenu (binaire, piece jointe...)
		handleAttachmentEntityCommon(entity, result)
	}
}

// parseMultipartAlternativeCommon gere multipart/alternative.
func parseMultipartAlternativeCommon(entity *mail.Reader, result *parseMimeMessageResult) {
	var textPart []byte
	var htmlPart []byte

	for {
		part, err := entity.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		// Utiliser Get for obtenir le Content-Type du header PartHeader
		contentType := part.Header.Get("Content-Type")
		partData, err := decodePartCommon(part)
		if err != nil {
			continue
		}

		switch {
		case strings.HasPrefix(contentType, "text/plain"):
			textPart = partData
		case strings.HasPrefix(contentType, "text/html"):
			htmlPart = partData
		default:
			// Autre alternative, la garder si on n'a rien de mieux
			if textPart == nil {
				textPart = partData
			}
		}
	}

	result.bodyText = textPart
	result.bodyHTML = htmlPart
}

// handlePartCommon traite une partie multipart.
func handlePartCommon(part *mail.Part, result *parseMimeMessageResult) {
	// PartHeader n'a pas de methode ContentType(), utiliser Get()
	contentType := part.Header.Get("Content-Type")
	contentDisp := part.Header.Get("Content-Disposition")

	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	// Verifier si c'est une piece jointe par Content-Disposition
	isAttachment := strings.Contains(contentDisp, "attachment")

	// Si c'est du texte et pas une piece jointe
	if strings.HasPrefix(contentType, "text/") && !isAttachment {
		if strings.HasPrefix(contentType, "text/html") {
			result.bodyHTML = data
		} else {
			result.bodyText = data
		}
	} else {
		// Piece jointe ou autre contenu
		filename := extractFilenameFromDispositionCommon(contentDisp, contentType)
		if filename == "" {
			filename = "attachment.bin"
		}
		if len(data) > 0 {
			result.attachments[filename] = data
		}
	}
}

// handleTextEntityCommon traite une entite texte.
func handleTextEntityCommon(entity *mail.Reader, result *parseMimeMessageResult, contentType string) {
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	if strings.HasPrefix(contentType, "text/html") {
		result.bodyHTML = data
	} else {
		result.bodyText = data
	}
}

// handleAttachmentEntityCommon traite une entite comme piece jointe.
func handleAttachmentEntityCommon(entity *mail.Reader, result *parseMimeMessageResult) {
	filename := extractFilenameCommon(entity.Header)
	if filename == "" {
		filename = "attachment.bin"
	}
	part, err := entity.NextPart()
	if err != nil {
		return
	}
	data, err := decodePartCommon(part)
	if err != nil {
		return
	}

	if len(data) > 0 {
		result.attachments[filename] = data
	}
}

// decodePartCommon decode une partie.
func decodePartCommon(part *mail.Part) ([]byte, error) {
	return io.ReadAll(part.Body)
}

// extractFilenameFromDispositionCommon extrait le filename from Content-Disposition et Content-Type.
func extractFilenameFromDispositionCommon(contentDisposition, contentType string) string {
	if contentDisposition != "" {
		if strings.Contains(contentDisposition, "filename=") {
			parts := strings.Split(contentDisposition, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], ` "'`)
				if idx := strings.Index(filename, ";"); idx > 0 {
					filename = filename[:idx]
					filename = strings.Trim(filename, ` "'`)
				}
				return filename
			}
		}
	}
	// Essayer Content-Type name parameter
	if strings.Contains(contentType, "name=") {
		parts := strings.Split(contentType, "name=")
		if len(parts) > 1 {
			return strings.Trim(parts[1], ` "'`)
		}
	}
	return ""
}

// extractFilenameCommon extrait le nom de file des headers.
func extractFilenameCommon(header mail.Header) string {
	// Content-Disposition
	disp, _, _ := header.ContentDisposition()
	if disp != "" {
		if strings.Contains(disp, "filename=") {
			parts := strings.Split(disp, "filename=")
			if len(parts) > 1 {
				filename := strings.Trim(parts[1], ` "'`)
				// Nettoyer les parametres supplementaires
				if idx := strings.Index(filename, ";"); idx > 0 {
					filename = filename[:idx]
					filename = strings.Trim(filename, ` "'`)
				}
				return filename
			}
		}
	}

	// Content-Type name parameter
	_, params, _ := header.ContentType()
	if name, ok := params["name"]; ok && name != "" {
		return name
	}

	return ""
}
