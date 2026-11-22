package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	gocamel "github.com/tranchida/gocamel"
)

const (
	// TelegramChatId overrides the chatId URI parameter. Honoured ONLY when
	// the endpoint sets allowHeaderOverride=true — see TelegramProducer.resolveChatID.
	TelegramChatId = "CamelTelegramChatId"
)

// TelegramComponent represents the Telegram component
type TelegramComponent struct{}

// NewTelegramComponent creates a new TelegramComponent
func NewTelegramComponent() *TelegramComponent {
	return &TelegramComponent{}
}

// CreateEndpoint creates a new endpoint Telegram
//
// Format:
//
//	telegram://bots?authorizationToken=TOKEN&chatId=42
//
// Options:
//
//	authorizationToken  Telegram bot token (required)
//	chatId              Destination chat ID
//	allowHeaderOverride true to let the CamelTelegramChatId header replace the
//	                    configured chatId (default: false — see TelegramProducer.resolveChatID)
func (c *TelegramComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	u, err := gocamel.ParseURI(uri)
	if err != nil {
		return nil, err
	}

	allowHeaderOverride := false
	if v := gocamel.GetConfigValue(u, "allowHeaderOverride"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid allowHeaderOverride value %q: %w", v, err)
		}
		allowHeaderOverride = b
	}

	return &TelegramEndpoint{
		uri:                 uri,
		url:                 u,
		comp:                c,
		allowHeaderOverride: allowHeaderOverride,
	}, nil
}

// TelegramEndpoint represents a Telegram endpoint
type TelegramEndpoint struct {
	uri                 string
	url                 *url.URL
	comp                *TelegramComponent
	allowHeaderOverride bool         // opt-in: the CamelTelegramChatId header may replace the chatId URI parameter
	httpClient          *http.Client // optional custom HTTP client for tests
}

// URI returns the endpoint URI, with the authorizationToken masked for safe
// logging.
func (e *TelegramEndpoint) URI() string {
	return gocamel.RedactURI(e.uri)
}

// defaultTelegramHTTPTimeout bounds Telegram Bot API HTTP calls while remaining
// comfortably above the 60s long-polling timeout used by GetUpdates.
const defaultTelegramHTTPTimeout = 75 * time.Second

type telegramHTTPClient struct {
	client *http.Client
	token  string
	ctx    context.Context
}

func (c *telegramHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != "https" || req.URL.Host != "api.telegram.org" {
		return nil, fmt.Errorf("unexpected Telegram API target")
	}
	if c.ctx != nil {
		req = req.Clone(c.ctx)
	}
	resp, err := c.client.Do(req) // #nosec G704 -- URL scheme/host locked to https://api.telegram.org and token validated against URL delimiter injection
	if err != nil {
		return resp, redactTelegramError(err, c.token)
	}
	return resp, nil
}

func validateBotToken(token string) error {
	if token == "" {
		return fmt.Errorf("authorizationToken missing for Telegram")
	}
	for i := 0; i < len(token); i++ {
		b := token[i]
		if b <= 0x20 || b == 0x7f {
			return fmt.Errorf("invalid authorizationToken: whitespace and control characters are not allowed")
		}
		switch b {
		case '/', '\\', '?', '#', '@':
			return fmt.Errorf("invalid authorizationToken: URL delimiter characters are not allowed")
		}
	}
	return nil
}

type redactedTelegramError struct {
	msg string
	err error
}

func (e *redactedTelegramError) Error() string { return e.msg }
func (e *redactedTelegramError) Unwrap() error { return e.err }

func redactTelegramToken(s, token string) string {
	if token == "" {
		return s
	}
	s = strings.ReplaceAll(s, token, "xxxxx")
	if escaped := url.PathEscape(token); escaped != "" && escaped != token {
		s = strings.ReplaceAll(s, escaped, "xxxxx")
	}
	if queryEscaped := url.QueryEscape(token); queryEscaped != "" && queryEscaped != token {
		s = strings.ReplaceAll(s, queryEscaped, "xxxxx")
	}
	return s
}

func redactTelegramError(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		cp := *urlErr
		cp.URL = redactTelegramToken(cp.URL, token)
		if cp.Err != nil {
			cp.Err = redactTelegramError(cp.Err, token)
		}
		return &cp
	}
	msg := redactTelegramToken(err.Error(), token)
	if msg == err.Error() {
		return err
	}
	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	}
	return &redactedTelegramError{msg: msg, err: cause}
}

func (e *TelegramEndpoint) newHTTPClient(ctx context.Context, token string) *telegramHTTPClient {
	base := e.httpClient
	if base == nil {
		base = &http.Client{Timeout: defaultTelegramHTTPTimeout}
	}
	return &telegramHTTPClient{
		client: base,
		token:  token,
		ctx:    ctx,
	}
}

func (e *TelegramEndpoint) getBot() (*tgbotapi.BotAPI, error) {
	return e.getBotWithContext(context.Background())
}

func (e *TelegramEndpoint) getBotWithContext(ctx context.Context) (*tgbotapi.BotAPI, error) {
	token := gocamel.GetConfigValue(e.url, "authorizationToken")
	if err := validateBotToken(token); err != nil {
		return nil, err
	}

	bot, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, e.newHTTPClient(ctx, token))
	if err != nil {
		return nil, fmt.Errorf("error creating the Telegram bot: %w", redactTelegramError(err, token))
	}

	return bot, nil
}

// CreateProducer creates a Telegram producer.
func (e *TelegramEndpoint) CreateProducer() (gocamel.Producer, error) {
	return &TelegramProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer creates a Telegram consumer.
func (e *TelegramEndpoint) CreateConsumer(processor gocamel.Processor) (gocamel.Consumer, error) {
	return &TelegramConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// TelegramProducer represents a Telegram producer.
type TelegramProducer struct {
	endpoint *TelegramEndpoint
	bot      *tgbotapi.BotAPI
	mu       sync.Mutex
	stopped  bool
	gen      uint64
}

func (p *TelegramProducer) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	if p.bot != nil && !p.stopped {
		p.mu.Unlock()
		return nil
	}
	p.stopped = false
	p.gen++
	startGen := p.gen
	p.mu.Unlock()

	bot, err := p.endpoint.getBotWithContext(ctx)
	if err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.gen != startGen {
		return fmt.Errorf("telegram producer stopped during start")
	}
	bot.Client = nil
	p.bot = bot
	return nil
}

func (p *TelegramProducer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bot = nil
	p.stopped = true
	p.gen++
	return nil
}

func (p *TelegramProducer) getBot(ctx context.Context) (*tgbotapi.BotAPI, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return nil, fmt.Errorf("telegram producer is stopped")
	}
	if p.bot != nil {
		bot := p.bot
		p.mu.Unlock()
		return bot, nil
	}
	startGen := p.gen
	p.mu.Unlock()

	bot, err := p.endpoint.getBotWithContext(ctx)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return nil, fmt.Errorf("telegram producer is stopped")
	}
	if p.gen != startGen {
		if p.bot != nil {
			return p.bot, nil
		}
		return nil, fmt.Errorf("telegram producer is stopped")
	}
	if p.bot == nil {
		bot.Client = nil
		p.bot = bot
	}
	return p.bot, nil
}

func (p *TelegramProducer) Send(exchange *gocamel.Exchange) error {
	if exchange == nil {
		return fmt.Errorf("telegram: exchange cannot be nil")
	}
	ctx := exchange.Context
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	stopped := p.stopped
	p.mu.Unlock()
	if stopped {
		return fmt.Errorf("telegram producer is stopped")
	}

	// Resolve the chat ID and the text before building the bot: getBot performs
	// a network round-trip to Telegram, so validating the locally-available
	// inputs first turns a misconfiguration into an immediate, precise error
	// instead of a connection failure that hides the real cause.
	chatID, err := p.resolveChatID(exchange)
	if err != nil {
		return err
	}

	text := messageText(exchange.GetIn().GetBody())

	bot, err := p.getBot(ctx)
	if err != nil {
		return err
	}

	// Copy BotAPI value per call so each Send uses its own exchange.Context
	// without mutating shared bot.Client state across concurrent senders.
	callBot := *bot
	callBot.Client = p.endpoint.newHTTPClient(ctx, bot.Token)

	if _, err := callBot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		return fmt.Errorf("error sending the Telegram message: %w", redactTelegramError(err, bot.Token))
	}

	return nil
}

// resolveChatID reads the destination chat from the chatId URI parameter.
// The TelegramChatId header takes precedence ONLY when the endpoint opts in
// with allowHeaderOverride=true: the consumer sets that header on inbound
// exchanges, so an attacker who can trigger a route towards telegram: could
// otherwise redirect messages to an arbitrary chat.
func (p *TelegramProducer) resolveChatID(exchange *gocamel.Exchange) (int64, error) {
	if val, ok := exchange.GetIn().GetHeader(TelegramChatId); ok && p.endpoint.allowHeaderOverride {
		switch v := val.(type) {
		case int64:
			return v, nil
		case int:
			return int64(v), nil
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid chat ID in header %s: %w", TelegramChatId, err)
			}
			return parsed, nil
		default:
			return 0, fmt.Errorf("unsupported type for %s: %T", TelegramChatId, val)
		}
	}

	chatIdStr := gocamel.GetConfigValue(p.endpoint.url, "chatId")
	if chatIdStr == "" {
		return 0, fmt.Errorf("chatId missing (required via header %s or URI parameter)", TelegramChatId)
	}
	parsed, err := strconv.ParseInt(chatIdStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid chatId in URI: %w", err)
	}
	return parsed, nil
}

// messageText renders a message body as the text of a Telegram message.
func messageText(body any) string {
	switch v := body.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// TelegramConsumer represents a Telegram consumer
type TelegramConsumer struct {
	endpoint  *TelegramEndpoint
	processor gocamel.Processor
	mu        sync.Mutex
	// transition is non-nil while a Start or Stop is in progress. It serializes
	// the two operations so a Stop racing a Start cannot observe started=false
	// and return before the bot polling loop is up. Guarded by mu.
	transition chan struct{}
	started    bool
	cancel     context.CancelFunc
	bot        *tgbotapi.BotAPI
	wg         sync.WaitGroup
}

// acquireTransition waits for an in-progress Start/Stop and claims the next
// transition. It never holds c.mu while waiting.
func (c *TelegramConsumer) acquireTransition() {
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

// releaseTransition publishes the final state and wakes Start/Stop waiters.
func (c *TelegramConsumer) releaseTransition() {
	c.mu.Lock()
	close(c.transition)
	c.transition = nil
	c.mu.Unlock()
}

func (c *TelegramConsumer) Start(ctx context.Context) error {
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

	pollCtx, cancel := context.WithCancel(ctx)
	bot, err := c.endpoint.getBotWithContext(pollCtx)
	if err != nil {
		cancel()
		return err
	}

	c.mu.Lock()
	c.cancel = cancel
	c.bot = bot
	c.started = true
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		u := tgbotapi.NewUpdate(0)
		u.Timeout = 60

		for {
			if pollCtx.Err() != nil {
				return
			}

			updates, err := bot.GetUpdates(u)
			if err != nil {
				if pollCtx.Err() != nil {
					return
				}
				gocamel.LogError("Error polling Telegram updates", "error", redactTelegramError(err, bot.Token))
				timer := time.NewTimer(3 * time.Second)
				select {
				case <-pollCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				continue
			}

			for _, update := range updates {
				if pollCtx.Err() != nil {
					return
				}
				if update.UpdateID < u.Offset {
					continue
				}
				u.Offset = update.UpdateID + 1
				if update.Message == nil { // ignore non-Message updates
					continue
				}

				exchange := gocamel.NewExchange(pollCtx)
				exchange.SetBody(update.Message.Text)
				if update.Message.Chat != nil {
					exchange.SetHeader(TelegramChatId, update.Message.Chat.ID)
				}
				if update.Message.From != nil {
					exchange.SetHeader("TelegramUsername", update.Message.From.UserName)
				}

				if err := gocamel.CompleteExchange(exchange, gocamel.ProcessSafely(c.processor, exchange)); err != nil {
					gocamel.LogError("Error processing Telegram message", "error", err)
				}
			}
		}
	}()

	return nil
}

func (c *TelegramConsumer) Stop() error {
	c.acquireTransition()
	defer c.releaseTransition()

	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()

	c.mu.Lock()
	c.started = false
	c.cancel = nil
	c.bot = nil
	c.mu.Unlock()
	return nil
}
