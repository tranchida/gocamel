package gocamel

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	TelegramChatId = "CamelTelegramChatId"
)

// TelegramComponent represents the Telegram component
type TelegramComponent struct{}

// NewTelegramComponent creates a new TelegramComponent
func NewTelegramComponent() *TelegramComponent {
	return &TelegramComponent{}
}

// CreateEndpoint creates a new endpoint Telegram
func (c *TelegramComponent) CreateEndpoint(uri string) (Endpoint, error) {
	u, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}

	return &TelegramEndpoint{
		uri:  uri,
		url:  u,
		comp: c,
	}, nil
}

// TelegramEndpoint represents a Telegram endpoint
type TelegramEndpoint struct {
	uri  string
	url  *url.URL
	comp *TelegramComponent
}

// URI returns the URI de l'endpoint
func (e *TelegramEndpoint) URI() string {
	return e.uri
}

func (e *TelegramEndpoint) getBot() (*tgbotapi.BotAPI, error) {
	token := GetConfigValue(e.url, "authorizationToken")
	if token == "" {
		return nil, fmt.Errorf("authorizationToken missing for Telegram")
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("error during la creation du bot Telegram: %w", err)
	}

	return bot, nil
}

// CreateProducer crée un producteur Telegram
func (e *TelegramEndpoint) CreateProducer() (Producer, error) {
	return &TelegramProducer{
		endpoint: e,
	}, nil
}

// CreateConsumer crée un consommateur Telegram
func (e *TelegramEndpoint) CreateConsumer(processor Processor) (Consumer, error) {
	return &TelegramConsumer{
		endpoint:  e,
		processor: processor,
	}, nil
}

// TelegramProducer represents a producteur Telegram
type TelegramProducer struct {
	endpoint *TelegramEndpoint
}

func (p *TelegramProducer) Start(ctx context.Context) error {
	return nil
}

func (p *TelegramProducer) Stop() error {
	return nil
}

func (p *TelegramProducer) Send(exchange *Exchange) error {
	bot, err := p.endpoint.getBot()
	if err != nil {
		return err
	}

	var chatID int64
	// Essayez de récupérer le chat ID from les headers
	if val, ok := exchange.GetIn().GetHeader(TelegramChatId); ok {
		switch v := val.(type) {
		case int64:
			chatID = v
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("chat ID invalid in le header %s: %w", TelegramChatId, err)
			}
			chatID = parsed
		default:
			return fmt.Errorf("type de %s non supported: %T", TelegramChatId, val)
		}
	} else {
		// Sinon, essayez de le récupérer from l'URI
		chatIdStr := GetConfigValue(p.endpoint.url, "chatId")
		if chatIdStr == "" {
			return fmt.Errorf("chatId missing (required via header %s ou paramètre d'URI)", TelegramChatId)
		}
		parsed, err := strconv.ParseInt(chatIdStr, 10, 64)
		if err != nil {
			return fmt.Errorf("chatId invalid in l'URI: %w", err)
		}
		chatID = parsed
	}

	var text string
	switch body := exchange.GetIn().GetBody().(type) {
	case string:
		text = body
	case []byte:
		text = string(body)
	default:
		text = fmt.Sprintf("%v", body)
	}

	msg := tgbotapi.NewMessage(chatID, text)
	_, err = bot.Send(msg)
	if err != nil {
		return fmt.Errorf("error during l'sending du message Telegram: %w", err)
	}

	return nil
}

// TelegramConsumer represents a consommateur Telegram
type TelegramConsumer struct {
	endpoint  *TelegramEndpoint
	processor Processor
	cancel    context.CancelFunc
}

func (c *TelegramConsumer) Start(ctx context.Context) error {
	bot, err := c.endpoint.getBot()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	go func() {
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

				exchange := NewExchange(ctx)
				exchange.SetBody(update.Message.Text)
				exchange.SetHeader(TelegramChatId, update.Message.Chat.ID)
				if update.Message.From != nil {
					exchange.SetHeader("TelegramUsername", update.Message.From.UserName)
				}

				if err := c.processor.Process(exchange); err != nil {
					fmt.Printf("error during traitement du message Telegram: %v\n", err)
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
	return nil
}
