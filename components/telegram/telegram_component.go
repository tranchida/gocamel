package telegram

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	gocamel "gitlab.com/tranchida/gocamel"
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
		if b, err := strconv.ParseBool(v); err == nil {
			allowHeaderOverride = b
		}
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
	allowHeaderOverride bool // opt-in: the CamelTelegramChatId header may replace the chatId URI parameter
}

// URI returns the URI de l'endpoint
func (e *TelegramEndpoint) URI() string {
	return e.uri
}

func (e *TelegramEndpoint) getBot() (*tgbotapi.BotAPI, error) {
	token := gocamel.GetConfigValue(e.url, "authorizationToken")
	if token == "" {
		return nil, fmt.Errorf("authorizationToken missing for Telegram")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("error creating the Telegram bot: %w", err)
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
}

func (p *TelegramProducer) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	bot, err := p.endpoint.getBot()
	if err != nil {
		return err
	}
	p.bot = bot
	return nil
}

func (p *TelegramProducer) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bot = nil
	return nil
}

func (p *TelegramProducer) getBot() (*tgbotapi.BotAPI, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bot != nil {
		return p.bot, nil
	}
	bot, err := p.endpoint.getBot()
	if err != nil {
		return nil, err
	}
	p.bot = bot
	return bot, nil
}

func (p *TelegramProducer) Send(exchange *gocamel.Exchange) error {
	// Resolve the chat ID and the text before building the bot: getBot performs
	// a network round-trip to Telegram, so validating the locally-available
	// inputs first turns a misconfiguration into an immediate, precise error
	// instead of a connection failure that hides the real cause.
	chatID, err := p.resolveChatID(exchange)
	if err != nil {
		return err
	}

	text := messageText(exchange.GetIn().GetBody())

	bot, err := p.getBot()
	if err != nil {
		return err
	}

	if _, err := bot.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		return fmt.Errorf("error sending the Telegram message: %w", err)
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

// TelegramConsumer represents a consommateur Telegram
type TelegramConsumer struct {
	endpoint  *TelegramEndpoint
	processor gocamel.Processor
	cancel    context.CancelFunc
	bot       *tgbotapi.BotAPI
	wg        sync.WaitGroup
}

func (c *TelegramConsumer) Start(ctx context.Context) error {
	bot, err := c.endpoint.getBot()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.bot = bot

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.Message == nil { // ignore non-Message updates
					continue
				}

				exchange := gocamel.NewExchange(ctx)
				exchange.SetBody(update.Message.Text)
				exchange.SetHeader(TelegramChatId, update.Message.Chat.ID)
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
	if c.cancel != nil {
		c.cancel()
	}
	// Stop the library's long-polling goroutine and close its updates channel
	// so the consumer goroutine exits (it would otherwise keep polling Telegram
	// and block sending on the now-unread channel after Stop returns).
	if c.bot != nil {
		c.bot.StopReceivingUpdates()
	}
	c.wg.Wait()
	return nil
}
