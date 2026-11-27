// Package webex provides a GoCamel component for Cisco Webex bots.
//
// The consumer connects outbound over the Webex Mercury WebSocket using
// github.com/3rg0n/webex-message-handler/go (WDM device registration, U2C
// region discovery, KMS/JWE decryption, automatic reconnection, and
// self-message filtering), requiring no public webhook URL. Mercury provides
// at-most-once delivery semantics, and only one active consumer should run per
// bot token because WDM device registrations share a single device name.
//
// The producer posts messages via the Webex REST API (POST /v1/messages) in
// Markdown or plain text, supporting regional room ID resolution (mapping raw
// Mercury UUIDs through paginated GET /rooms with a bounded cache), threaded
// replies (parentId), and direct messages (toPersonEmail / toPersonId).
package webex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	messagehandler "github.com/3rg0n/webex-message-handler/go"

	gocamel "github.com/tranchida/gocamel"
)

const (
	// HeaderRoomID carries the Webex room ID (raw Mercury UUID on consumed
	// exchanges; regional REST ID or raw Mercury UUID on produced exchanges
	// when allowHeaderOverride=true).
	HeaderRoomID = "CamelWebexRoomId"
	// HeaderMessageID carries the inbound Webex message ID (consumer) or the
	// created REST message ID on the Out message (producer).
	HeaderMessageID = "CamelWebexMessageId"
	// HeaderParentID carries the parent message ID for threaded replies.
	HeaderParentID = "CamelWebexParentId"
	// HeaderPersonID carries the sender's Webex person ID on consumed exchanges.
	HeaderPersonID = "CamelWebexPersonId"
	// HeaderPersonEmail carries the sender's email address on consumed exchanges.
	HeaderPersonEmail = "CamelWebexPersonEmail"
	// HeaderRoomType carries the Webex room type ("direct" or "group").
	HeaderRoomType = "CamelWebexRoomType"
	// HeaderHTML carries the decrypted HTML content of an inbound message.
	HeaderHTML = "CamelWebexHtml"
	// HeaderCreated carries the ISO 8601 creation timestamp of an inbound message.
	HeaderCreated = "CamelWebexCreated"
	// HeaderFiles carries file URLs attached to an inbound message ([]string).
	HeaderFiles = "CamelWebexFiles"
	// HeaderMentionedPeople carries person UUIDs mentioned in an inbound message ([]string).
	HeaderMentionedPeople = "CamelWebexMentionedPeople"
	// HeaderMentionedGroups carries group mentions (e.g. "all") in an inbound message ([]string).
	HeaderMentionedGroups = "CamelWebexMentionedGroups"
	// HeaderToPersonEmail overrides the DM recipient email when allowHeaderOverride=true.
	HeaderToPersonEmail = "CamelWebexToPersonEmail"
	// HeaderToPersonID overrides the DM recipient person ID when allowHeaderOverride=true.
	HeaderToPersonID = "CamelWebexToPersonId"
	// HeaderFormat overrides the outbound body format ("markdown" or "text") when allowHeaderOverride=true.
	HeaderFormat = "CamelWebexFormat"

	// FormatMarkdown sends the exchange body in the Webex "markdown" field (default).
	FormatMarkdown = "markdown"
	// FormatText sends the exchange body in the Webex "text" field.
	FormatText = "text"

	defaultMaxConcurrency  = 10
	defaultShutdownTimeout = 10 * time.Second
	defaultPingInterval    = 15 * time.Second
	defaultPongTimeout     = 14 * time.Second
)

// tokenEnvVars lists environment variables checked for the Webex bot access
// token in precedence order before falling back to URI query parameters.
// #nosec G101 -- these are environment variable names, not hardcoded credentials.
var tokenEnvVars = []string{
	"WEBEX_ACCESS_TOKEN",
	"WEBEX_ACCESSTOKEN",
	"WEBEX_AUTHORIZATIONTOKEN",
}

// mercuryHandler is the internal subset of *messagehandler.WebexMessageHandler
// used by WebexConsumer, enabling deterministic in-memory unit testing.
type mercuryHandler interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	OnMessageCreated(fn func(msg messagehandler.DecryptedMessage))
	OnConnected(fn func())
	OnDisconnected(fn func(reason string))
	OnReconnecting(fn func(attempt int))
	OnError(fn func(err error))
}

type handlerFactoryFunc func(cfg messagehandler.Config) (mercuryHandler, error)

func defaultHandlerFactory(cfg messagehandler.Config) (mercuryHandler, error) {
	return messagehandler.New(cfg)
}

// WebexComponent is the GoCamel component for Cisco Webex bots.
type WebexComponent struct{}

// NewWebexComponent creates a new WebexComponent.
func NewWebexComponent() *WebexComponent {
	return &WebexComponent{}
}

// CreateEndpoint parses and validates a Webex endpoint URI.
//
// Canonical format:
//
//	webex:bots[?options...]
//	webex://bots[?options...]
//
// Token resolution is deferred to Start() (environment variables
// WEBEX_ACCESS_TOKEN, WEBEX_ACCESSTOKEN, WEBEX_AUTHORIZATIONTOKEN take
// precedence over accessToken / authorizationToken URI query parameters).
// All non-secret options are read strictly from the URI query string.
func (c *WebexComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid webex URI: %w", err)
	}

	q := u.Query()

	roomID := strings.TrimSpace(q.Get("roomId"))
	toPersonEmail := strings.TrimSpace(q.Get("toPersonEmail"))
	toPersonID := strings.TrimSpace(q.Get("toPersonId"))

	targetCount := 0
	if roomID != "" {
		targetCount++
	}
	if toPersonEmail != "" {
		targetCount++
	}
	if toPersonID != "" {
		targetCount++
	}
	if targetCount > 1 {
		return nil, fmt.Errorf("invalid webex URI: only one of roomId, toPersonEmail, or toPersonId may be specified")
	}

	format := FormatMarkdown
	if v := strings.TrimSpace(q.Get("format")); v != "" {
		parsedFormat, err := parseFormat(v)
		if err != nil {
			return nil, fmt.Errorf("invalid webex URI: %w", err)
		}
		format = parsedFormat
	}

	rawBaseURL := DefaultBaseURL
	if v := strings.TrimSpace(q.Get("baseURL")); v != "" {
		rawBaseURL = v
	}
	baseParsed, baseURL, err := validateBaseURL(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid webex baseURL: %w", err)
	}

	timeout := defaultHTTPTimeout
	if v := strings.TrimSpace(q.Get("timeout")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid webex timeout %q: must be a positive duration", v)
		}
		timeout = d
	}

	allowHeaderOverride, err := parseOptionalBool(q.Get("allowHeaderOverride"), false, "allowHeaderOverride")
	if err != nil {
		return nil, err
	}

	access, err := parseAccessList(q.Get("allowedDomains"), q.Get("allowedEmails"))
	if err != nil {
		return nil, err
	}

	ignoreSelfMessages, err := parseOptionalBool(q.Get("ignoreSelfMessages"), true, "ignoreSelfMessages")
	if err != nil {
		return nil, err
	}

	ignoreEmptyMessages, err := parseOptionalBool(q.Get("ignoreEmptyMessages"), true, "ignoreEmptyMessages")
	if err != nil {
		return nil, err
	}

	maxConcurrency := defaultMaxConcurrency
	if v := strings.TrimSpace(q.Get("maxConcurrency")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid webex maxConcurrency %q: must be a positive integer", v)
		}
		maxConcurrency = n
	}

	shutdownTimeout := defaultShutdownTimeout
	if v := strings.TrimSpace(q.Get("shutdownTimeout")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid webex shutdownTimeout %q: must be a positive duration", v)
		}
		shutdownTimeout = d
	}

	var pingInterval time.Duration
	effectivePing := defaultPingInterval
	if v := strings.TrimSpace(q.Get("pingInterval")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid webex pingInterval %q: must be a positive duration", v)
		}
		pingInterval = d
		effectivePing = d
	}

	var pongTimeout time.Duration
	effectivePong := defaultPongTimeout
	if v := strings.TrimSpace(q.Get("pongTimeout")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid webex pongTimeout %q: must be a positive duration", v)
		}
		pongTimeout = d
		effectivePong = d
	}
	if effectivePong >= effectivePing {
		return nil, fmt.Errorf("invalid webex pongTimeout (%s): must be less than pingInterval (%s)", effectivePong, effectivePing)
	}

	var reconnectBackoffMax time.Duration
	if v := strings.TrimSpace(q.Get("reconnectBackoffMax")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("invalid webex reconnectBackoffMax %q: must be a positive duration", v)
		}
		reconnectBackoffMax = d
	}

	var maxReconnectAttempts int
	if v := strings.TrimSpace(q.Get("maxReconnectAttempts")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid webex maxReconnectAttempts %q: must be a positive integer", v)
		}
		maxReconnectAttempts = n
	}

	return &WebexEndpoint{
		uri:                  uri,
		url:                  u,
		roomID:               roomID,
		toPersonEmail:        toPersonEmail,
		toPersonID:           toPersonID,
		format:               format,
		baseURL:              baseURL,
		baseParsed:           baseParsed,
		timeout:              timeout,
		allowHeaderOverride:  allowHeaderOverride,
		access:               access,
		ignoreSelfMessages:   ignoreSelfMessages,
		ignoreEmptyMessages:  ignoreEmptyMessages,
		maxConcurrency:       maxConcurrency,
		shutdownTimeout:      shutdownTimeout,
		pingInterval:         pingInterval,
		pongTimeout:          pongTimeout,
		reconnectBackoffMax:  reconnectBackoffMax,
		maxReconnectAttempts: maxReconnectAttempts,
		handlerFactory:       defaultHandlerFactory,
	}, nil
}

// WebexEndpoint represents a configured Webex endpoint. Its exported contract
// fields are immutable after creation.
type WebexEndpoint struct {
	uri                  string
	url                  *url.URL
	roomID               string
	toPersonEmail        string
	toPersonID           string
	format               string
	baseURL              string
	baseParsed           *url.URL
	timeout              time.Duration
	allowHeaderOverride  bool
	access               accessList
	ignoreSelfMessages   bool
	ignoreEmptyMessages  bool
	maxConcurrency       int
	shutdownTimeout      time.Duration
	pingInterval         time.Duration
	pongTimeout          time.Duration
	reconnectBackoffMax  time.Duration
	maxReconnectAttempts int

	// Unexported test seams.
	httpClient     *http.Client
	handlerFactory handlerFactoryFunc
}

// URI returns the endpoint URI with credentials masked for safe logging.
func (e *WebexEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

// CreateProducer creates a new WebexProducer for this endpoint.
func (e *WebexEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &WebexProducer{endpoint: e}, nil
}

// CreateConsumer creates a new WebexConsumer for this endpoint.
func (e *WebexEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return &WebexConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

func (e *WebexEndpoint) hasStaticTarget() bool {
	return e.roomID != "" || e.toPersonEmail != "" || e.toPersonID != ""
}

func (e *WebexEndpoint) newHandler(cfg messagehandler.Config) (mercuryHandler, error) {
	factory := e.handlerFactory
	if factory == nil {
		factory = defaultHandlerFactory
	}
	return factory(cfg)
}

func (e *WebexEndpoint) resolveToken() (string, error) {
	for _, envName := range tokenEnvVars {
		if v := os.Getenv(envName); v != "" {
			if err := validateAccessToken(v); err != nil {
				return "", err
			}
			return v, nil
		}
	}

	var token string
	if e.url != nil {
		q := e.url.Query()
		if q.Get("accessToken") != "" {
			token = gocamel.GetConfigValue(e.url, "accessToken")
		} else if q.Get("authorizationToken") != "" {
			token = gocamel.GetConfigValue(e.url, "authorizationToken")
		}
	}
	if err := validateAccessToken(token); err != nil {
		return "", err
	}
	return token, nil
}

// WebexProducer sends outbound messages via the Webex REST API.
type WebexProducer struct {
	endpoint *WebexEndpoint

	mu      sync.RWMutex // protects started and client
	started bool
	client  *webexClient
}

// Start resolves credentials, validates configuration, and initializes the
// REST client. Start is idempotent when already started.
func (p *WebexProducer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return nil
	}

	token, err := p.endpoint.resolveToken()
	if err != nil {
		return err
	}

	if !p.endpoint.allowHeaderOverride && !p.endpoint.hasStaticTarget() {
		return fmt.Errorf("webex: destination missing (configure roomId, toPersonEmail, or toPersonId in URI, or set allowHeaderOverride=true)")
	}

	httpClient := p.endpoint.httpClient
	if httpClient == nil {
		httpClient = newSafeProducerHTTPClient(p.endpoint.timeout)
	}

	p.client = newWebexClient(token, p.endpoint.baseURL, p.endpoint.baseParsed, httpClient)
	p.started = true
	return nil
}

// Stop stops the producer and releases idle HTTP connections.
func (p *WebexProducer) Stop() error {
	p.mu.Lock()
	client := p.client
	p.client = nil
	p.started = false
	p.mu.Unlock()

	if client != nil && client.httpClient != nil {
		client.httpClient.CloseIdleConnections()
	}
	return nil
}

// Send posts the exchange body to Webex. When Webex returns a message ID in
// its JSON response, HeaderMessageID is written onto the Out message while
// preserving In.Body for downstream processors.
func (p *WebexProducer) Send(exchange *gocamel.Exchange) error {
	if exchange == nil {
		return fmt.Errorf("webex: exchange cannot be nil")
	}

	p.mu.RLock()
	started := p.started
	client := p.client
	p.mu.RUnlock()

	if !started || client == nil {
		return fmt.Errorf("webex: producer has not been started")
	}

	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	text, err := renderMessageBody(exchange.GetIn().GetBody())
	if err != nil {
		return err
	}

	format, err := p.resolveFormat(exchange)
	if err != nil {
		return err
	}

	roomID, toPersonEmail, toPersonID, parentID, err := p.resolveDestination(exchange)
	if err != nil {
		return err
	}

	if roomID != "" {
		if isUUID(roomID) {
			resolved, err := client.resolveRoomID(ctx, roomID)
			if err != nil {
				return err
			}
			roomID = resolved
		}
		if parentID != "" && isUUID(parentID) {
			if parts, err := decodeRestID(roomID); err == nil {
				parentID = encodeRestID(parts.cluster, "MESSAGE", strings.ToLower(parentID))
			}
		}
	}

	msg := outboundMessage{
		RoomID:        roomID,
		ToPersonEmail: toPersonEmail,
		ToPersonID:    toPersonID,
		ParentID:      parentID,
	}
	if format == FormatText {
		msg.Text = text
	} else {
		msg.Markdown = text
	}

	createdID, err := client.sendMessage(ctx, msg)
	if err != nil {
		return err
	}
	if createdID != "" {
		exchange.GetOut().SetHeader(HeaderMessageID, createdID)
	}
	return nil
}

func (p *WebexProducer) resolveFormat(exchange *gocamel.Exchange) (string, error) {
	if p.endpoint.allowHeaderOverride {
		raw, present, err := optionalStringHeader(exchange, HeaderFormat)
		if err != nil {
			return "", err
		}
		if present && raw != "" {
			return parseFormat(raw)
		}
	}
	return p.endpoint.format, nil
}

func (p *WebexProducer) resolveDestination(exchange *gocamel.Exchange) (roomID, toPersonEmail, toPersonID, parentID string, err error) {
	if p.endpoint.allowHeaderOverride {
		roomID, _, err = optionalStringHeader(exchange, HeaderRoomID)
		if err != nil {
			return "", "", "", "", err
		}
		toPersonEmail, _, err = optionalStringHeader(exchange, HeaderToPersonEmail)
		if err != nil {
			return "", "", "", "", err
		}
		toPersonID, _, err = optionalStringHeader(exchange, HeaderToPersonID)
		if err != nil {
			return "", "", "", "", err
		}
		parentID, _, err = optionalStringHeader(exchange, HeaderParentID)
		if err != nil {
			return "", "", "", "", err
		}

		// Deterministic header precedence: roomId > toPersonEmail > toPersonId.
		switch {
		case roomID != "":
			toPersonEmail = ""
			toPersonID = ""
		case toPersonEmail != "":
			toPersonID = ""
		case toPersonID != "":
			// keep toPersonID
		default:
			roomID = p.endpoint.roomID
			toPersonEmail = p.endpoint.toPersonEmail
			toPersonID = p.endpoint.toPersonID
		}
	} else {
		roomID = p.endpoint.roomID
		toPersonEmail = p.endpoint.toPersonEmail
		toPersonID = p.endpoint.toPersonID
	}

	if roomID == "" && toPersonEmail == "" && toPersonID == "" {
		return "", "", "", "", fmt.Errorf("webex: destination missing (provide roomId, toPersonEmail, or toPersonId via URI or headers with allowHeaderOverride=true)")
	}
	if parentID != "" && roomID == "" {
		return "", "", "", "", fmt.Errorf("webex: parentId cannot be used with toPersonEmail or toPersonId")
	}
	return roomID, toPersonEmail, toPersonID, parentID, nil
}

// WebexConsumer receives inbound Webex messages over the Mercury WebSocket.
type WebexConsumer struct {
	endpoint  *WebexEndpoint
	processor gocamel.Processor

	mu sync.Mutex // protects transition and all lifecycle fields below
	// transition is non-nil while Start or Stop is running, serializing
	// concurrent lifecycle transitions without holding mu across blocking I/O.
	transition         chan struct{}
	started            bool
	stopping           bool
	gen                uint64
	token              string
	handler            mercuryHandler
	consumerHTTPClient *http.Client
	connectCancel      context.CancelFunc
	workerCtx          context.Context
	workerCancel       context.CancelFunc
	sem                chan struct{}
	stopCh             chan struct{}
	watcherDone        chan struct{}

	wg sync.WaitGroup // tracks in-flight message processing workers
}

func (c *WebexConsumer) acquireTransition() {
	for {
		c.mu.Lock()
		if c.transition != nil {
			done := c.transition
			c.mu.Unlock()
			<-done
			continue
		}
		c.transition = make(chan struct{})
		c.mu.Unlock()
		return
	}
}

func (c *WebexConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

// Start connects to Webex Mercury and begins delivering inbound messages to
// the route processor. Start is idempotent and supports restart after Stop.
func (c *WebexConsumer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if c.processor == nil {
		return fmt.Errorf("webex: consumer processor cannot be nil")
	}

	token, err := c.endpoint.resolveToken()
	if err != nil {
		return err
	}

	consumerHTTPClient := newSafeConsumerHTTPClient()
	ignoreSelf := c.endpoint.ignoreSelfMessages
	cfg := messagehandler.Config{
		Token:              token,
		Logger:             &webexLogger{token: token},
		HTTPClient:         consumerHTTPClient,
		IgnoreSelfMessages: &ignoreSelf,
	}
	if c.endpoint.pingInterval > 0 {
		cfg.PingInterval = c.endpoint.pingInterval.Seconds()
	}
	if c.endpoint.pongTimeout > 0 {
		cfg.PongTimeout = c.endpoint.pongTimeout.Seconds()
	}
	if c.endpoint.reconnectBackoffMax > 0 {
		cfg.ReconnectBackoffMax = c.endpoint.reconnectBackoffMax.Seconds()
	}
	if c.endpoint.maxReconnectAttempts > 0 {
		cfg.MaxReconnectAttempts = c.endpoint.maxReconnectAttempts
	}

	handler, err := c.endpoint.newHandler(cfg)
	if err != nil {
		consumerHTTPClient.CloseIdleConnections()
		return fmt.Errorf("webex: failed to create message handler: %w", redactWebexError(err, token))
	}

	connectCtx, connectCancel := context.WithCancel(context.Background())
	workerCtx, workerCancel := context.WithCancel(context.Background())
	sem := make(chan struct{}, c.endpoint.maxConcurrency)
	stopCh := make(chan struct{})
	watcherDone := make(chan struct{})

	c.mu.Lock()
	c.stopping = false
	c.token = token
	c.workerCtx = workerCtx
	c.workerCancel = workerCancel
	c.connectCancel = connectCancel
	c.sem = sem
	c.mu.Unlock()

	handler.OnConnected(func() {
		gocamel.LogInfo("webex: connected to Mercury", "uri", c.endpoint.URI())
	})
	handler.OnReconnecting(func(attempt int) {
		gocamel.LogWarn("webex: Mercury reconnecting", "uri", c.endpoint.URI(), "attempt", attempt)
	})
	handler.OnDisconnected(func(reason string) {
		switch reason {
		case "auth-failed", "permanent-failure", "max-attempts-exceeded":
			gocamel.LogError("webex: Mercury disconnected permanently", "uri", c.endpoint.URI(), "reason", reason)
		default:
			gocamel.LogInfo("webex: Mercury disconnected", "uri", c.endpoint.URI(), "reason", reason)
		}
	})
	handler.OnError(func(err error) {
		gocamel.LogError("webex: message handler error", "uri", c.endpoint.URI(), "error", redactWebexError(err, token))
	})
	handler.OnMessageCreated(func(msg messagehandler.DecryptedMessage) {
		c.handleMessage(msg)
	})

	// Couple parent ctx ONLY during Connect(). Once connected, parent
	// cancellation is handled by the watcher goroutine which calls Disconnect()
	// before cancelling connectCtx so Mercury disables auto-reconnect cleanly.
	connectDone := make(chan struct{})
	var bridgeDone chan struct{}
	if ctx.Done() != nil {
		bridgeDone = make(chan struct{})
		go func() {
			defer close(bridgeDone)
			select {
			case <-connectDone:
			case <-ctx.Done():
				connectCancel()
			}
		}()
	}

	connectErr := handler.Connect(connectCtx)
	close(connectDone)
	if bridgeDone != nil {
		<-bridgeDone
	}

	if connectErr != nil || ctx.Err() != nil {
		c.mu.Lock()
		c.stopping = true
		c.mu.Unlock()

		disconnectCtx, dCancel := context.WithTimeout(context.Background(), c.endpoint.shutdownTimeout)
		_ = handler.Disconnect(disconnectCtx)
		dCancel()

		connectCancel()
		workerCancel()
		c.wg.Wait()
		consumerHTTPClient.CloseIdleConnections()

		c.mu.Lock()
		c.stopping = false
		c.token = ""
		c.workerCtx = nil
		c.workerCancel = nil
		c.connectCancel = nil
		c.sem = nil
		c.mu.Unlock()

		if connectErr != nil {
			return fmt.Errorf("webex: failed to connect to Mercury: %w", redactWebexError(connectErr, token))
		}
		return ctx.Err()
	}

	c.mu.Lock()
	c.gen++
	myGen := c.gen
	c.started = true
	c.handler = handler
	c.consumerHTTPClient = consumerHTTPClient
	c.stopCh = stopCh
	c.watcherDone = watcherDone
	c.mu.Unlock()

	if ctx.Done() != nil {
		go func() {
			defer close(watcherDone)
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				_ = c.stop(true, myGen)
			}
		}()
	} else {
		close(watcherDone)
	}

	return nil
}

// Stop disconnects from Webex Mercury, cancels worker contexts, and waits for
// all in-flight exchanges to finish. Stop is idempotent.
func (c *WebexConsumer) Stop() error {
	return c.stop(false, 0)
}

func (c *WebexConsumer) stop(fromWatcher bool, expectedGen uint64) error {
	watcherDone, err := c.stopCore(fromWatcher, expectedGen)
	// Join the parent-context watcher AFTER releasing the transition lock so
	// a simultaneous parent cancellation racing into stop(true, ...) cannot
	// deadlock waiting on acquireTransition().
	if !fromWatcher && watcherDone != nil {
		<-watcherDone
	}
	return err
}

func (c *WebexConsumer) stopCore(fromWatcher bool, expectedGen uint64) (chan struct{}, error) {
	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if !c.started || (fromWatcher && c.gen != expectedGen) {
		c.mu.Unlock()
		return nil, nil
	}
	c.stopping = true
	handler := c.handler
	consumerHTTPClient := c.consumerHTTPClient
	connectCancel := c.connectCancel
	workerCancel := c.workerCancel
	stopCh := c.stopCh
	watcherDone := c.watcherDone
	token := c.token
	c.mu.Unlock()

	close(stopCh)

	// Disconnect Mercury FIRST so shouldReconnect=false is set before
	// connectCancel closes the underlying context.
	var disconnectErr error
	if handler != nil {
		disconnectCtx, dCancel := context.WithTimeout(context.Background(), c.endpoint.shutdownTimeout)
		disconnectErr = handler.Disconnect(disconnectCtx)
		dCancel()
	}

	if connectCancel != nil {
		connectCancel()
	}
	if workerCancel != nil {
		workerCancel()
	}

	c.wg.Wait()

	if consumerHTTPClient != nil {
		consumerHTTPClient.CloseIdleConnections()
	}

	c.mu.Lock()
	c.started = false
	c.stopping = false
	c.token = ""
	c.handler = nil
	c.consumerHTTPClient = nil
	c.connectCancel = nil
	c.workerCtx = nil
	c.workerCancel = nil
	c.sem = nil
	c.stopCh = nil
	c.watcherDone = nil
	c.mu.Unlock()

	if disconnectErr != nil {
		return watcherDone, fmt.Errorf("webex: failed to disconnect from Mercury: %w", redactWebexError(disconnectErr, token))
	}
	return watcherDone, nil
}

func (c *WebexConsumer) handleMessage(msg messagehandler.DecryptedMessage) {
	if c.endpoint.ignoreEmptyMessages && strings.TrimSpace(msg.Text) == "" {
		return
	}
	if !c.endpoint.access.allowed(msg.PersonEmail) {
		gocamel.LogDebug("webex: blocked message from unauthorized sender", "personEmail", msg.PersonEmail)
		return
	}

	c.mu.Lock()
	if c.stopping || c.workerCtx == nil || c.sem == nil {
		c.mu.Unlock()
		return
	}
	workerCtx := c.workerCtx
	sem := c.sem
	token := c.token
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()

		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-workerCtx.Done():
			return
		}
		if workerCtx.Err() != nil {
			return
		}

		exchange := gocamel.NewExchange(workerCtx)
		exchange.SetBody(msg.Text)
		populateMessageHeaders(exchange, msg)

		if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
			gocamel.LogError("webex: error processing message", "error", redactWebexError(err, token))
		}
	}()
}

func populateMessageHeaders(exchange *gocamel.Exchange, msg messagehandler.DecryptedMessage) {
	in := exchange.GetIn()
	if msg.RoomID != "" {
		in.SetHeader(HeaderRoomID, msg.RoomID)
	}
	if msg.ID != "" {
		in.SetHeader(HeaderMessageID, msg.ID)
	}
	if msg.ParentID != "" {
		in.SetHeader(HeaderParentID, msg.ParentID)
	}
	if msg.PersonID != "" {
		in.SetHeader(HeaderPersonID, msg.PersonID)
	}
	if msg.PersonEmail != "" {
		in.SetHeader(HeaderPersonEmail, msg.PersonEmail)
	}
	if msg.RoomType != "" {
		in.SetHeader(HeaderRoomType, msg.RoomType)
	}
	if msg.HTML != "" {
		in.SetHeader(HeaderHTML, msg.HTML)
	}
	if msg.Created != "" {
		in.SetHeader(HeaderCreated, msg.Created)
	}
	if len(msg.Files) > 0 {
		in.SetHeader(HeaderFiles, slices.Clone(msg.Files))
	}
	if len(msg.MentionedPeople) > 0 {
		in.SetHeader(HeaderMentionedPeople, slices.Clone(msg.MentionedPeople))
	}
	if len(msg.MentionedGroups) > 0 {
		in.SetHeader(HeaderMentionedGroups, slices.Clone(msg.MentionedGroups))
	}
}

func parseOptionalBool(raw string, defaultVal bool, name string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultVal, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid webex %s value %q: %w", name, raw, err)
	}
	return b, nil
}

func parseFormat(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case FormatMarkdown:
		return FormatMarkdown, nil
	case FormatText:
		return FormatText, nil
	default:
		return "", fmt.Errorf("invalid webex format %q: must be %q or %q", raw, FormatMarkdown, FormatText)
	}
}

func renderMessageBody(body any) (string, error) {
	if body == nil {
		return "", fmt.Errorf("webex: message body cannot be nil")
	}
	var text string
	switch v := body.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		text = fmt.Sprintf("%v", v)
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("webex: message body cannot be empty")
	}
	return text, nil
}

func optionalStringHeader(exchange *gocamel.Exchange, key string) (string, bool, error) {
	val, ok := exchange.GetIn().GetHeader(key)
	if !ok || val == nil {
		return "", false, nil
	}
	switch v := val.(type) {
	case string:
		return strings.TrimSpace(v), true, nil
	case []byte:
		return strings.TrimSpace(string(v)), true, nil
	default:
		return "", true, fmt.Errorf("webex: unsupported type for header %s: %T", key, val)
	}
}

// webexLogger adapts webex-message-handler's Logger interface to GoCamel's
// structured logger while redacting the bot access token from messages and args.
type webexLogger struct {
	token string
}

func (l *webexLogger) Debug(msg string, args ...any) {
	gocamel.LogDebug(redactWebexToken(msg, l.token), l.redactArgs(args)...)
}

func (l *webexLogger) Info(msg string, args ...any) {
	gocamel.LogInfo(redactWebexToken(msg, l.token), l.redactArgs(args)...)
}

func (l *webexLogger) Warn(msg string, args ...any) {
	gocamel.LogWarn(redactWebexToken(msg, l.token), l.redactArgs(args)...)
}

func (l *webexLogger) Error(msg string, args ...any) {
	gocamel.LogError(redactWebexToken(msg, l.token), l.redactArgs(args)...)
}

func (l *webexLogger) redactArgs(args []any) []any {
	if len(args) == 0 || l.token == "" {
		return args
	}
	out := make([]any, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case string:
			out[i] = redactWebexToken(v, l.token)
		case error:
			out[i] = redactWebexError(v, l.token)
		case fmt.Stringer:
			out[i] = redactWebexToken(v.String(), l.token)
		default:
			out[i] = arg
		}
	}
	return out
}
