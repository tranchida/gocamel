package telegram

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gocamel "github.com/tranchida/gocamel"
)

func newEndpoint(t *testing.T, uri string) *TelegramEndpoint {
	t.Helper()
	ep, err := NewTelegramComponent().CreateEndpoint(uri)
	if err != nil {
		t.Fatalf("CreateEndpoint(%q): %v", uri, err)
	}
	return ep.(*TelegramEndpoint)
}

func TestCreateEndpoint(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?chatId=42")
	if ep.URI() != "telegram://bots?chatId=42" {
		t.Errorf("URI() = %q, want URI unchanged when no secret is present", ep.URI())
	}
}

func TestEndpointURI_RedactsAuthorizationToken(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?authorizationToken=123456:SECRET-token&chatId=42")
	uri := ep.URI()
	if strings.Contains(uri, "123456:SECRET-token") {
		t.Errorf("URI() leaks the bot token: %q", uri)
	}
	if !strings.Contains(uri, "authorizationToken=xxxxx") {
		t.Errorf("URI() = %q, want authorizationToken masked with xxxxx", uri)
	}
	if !strings.Contains(uri, "chatId=42") {
		t.Errorf("URI() = %q, want non-sensitive parameters preserved", uri)
	}
}

func TestAllowHeaderOverrideParsing(t *testing.T) {
	if newEndpoint(t, "telegram://bots?chatId=42").allowHeaderOverride {
		t.Error("allowHeaderOverride must default to false")
	}
	if !newEndpoint(t, "telegram://bots?chatId=42&allowHeaderOverride=true").allowHeaderOverride {
		t.Error("allowHeaderOverride=true must be parsed from the URI")
	}
}

func TestGetBot_RequiresToken(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?chatId=42")
	if _, err := ep.getBot(); err == nil {
		t.Fatal("expected an error when authorizationToken is missing")
	}
}

func TestResolveChatID(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		header  any
		want    int64
		wantErr bool
	}{
		{name: "from URI", uri: "telegram://bots?chatId=42", want: 42},
		{name: "header ignored without allowHeaderOverride", uri: "telegram://bots?chatId=42", header: int64(7), want: 42},
		{name: "int64 header wins", uri: "telegram://bots?chatId=42&allowHeaderOverride=true", header: int64(7), want: 7},
		{name: "int header", uri: "telegram://bots?chatId=42&allowHeaderOverride=true", header: 7, want: 7},
		{name: "string header", uri: "telegram://bots?chatId=42&allowHeaderOverride=true", header: "7", want: 7},
		{name: "unparseable header", uri: "telegram://bots?chatId=42&allowHeaderOverride=true", header: "abc", wantErr: true},
		{name: "unsupported header type", uri: "telegram://bots?chatId=42&allowHeaderOverride=true", header: 1.5, wantErr: true},
		{name: "missing everywhere", uri: "telegram://bots", wantErr: true},
		{name: "unparseable URI value", uri: "telegram://bots?chatId=abc", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ep := newEndpoint(t, tc.uri)
			prod, err := ep.CreateProducer()
			if err != nil {
				t.Fatal(err)
			}
			ex := gocamel.NewExchange(context.Background())
			if tc.header != nil {
				ex.GetIn().SetHeader(TelegramChatId, tc.header)
			}

			got, err := prod.(*TelegramProducer).resolveChatID(ex)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got chatID %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("chatID = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMessageText(t *testing.T) {
	tests := []struct {
		body any
		want string
	}{
		{"hello", "hello"},
		{[]byte("bytes"), "bytes"},
		{42, "42"},
		{nil, ""},
	}
	for _, tc := range tests {
		if got := messageText(tc.body); got != tc.want {
			t.Errorf("messageText(%#v) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// A misconfigured chatId must be reported without attempting any network call.
func TestSend_ValidatesChatIDBeforeContactingTelegram(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?authorizationToken=invalid-token")
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatal(err)
	}
	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hi")

	err = prod.Send(ex)
	if err == nil {
		t.Fatal("expected an error for a missing chatId")
	}
	if !contains(err.Error(), "chatId missing") {
		t.Errorf("expected a chatId error, got %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newFakeTelegramClient() *http.Client {
	return &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var body string
			switch {
			case strings.HasSuffix(r.URL.Path, "/getMe"):
				body = `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`
			case strings.HasSuffix(r.URL.Path, "/getUpdates"):
				body = `{"ok":true,"result":[{"update_id":1,"message":{"message_id":1,"chat":{"id":42,"type":"private"},"text":"hello"}}]}`
			case strings.HasSuffix(r.URL.Path, "/sendMessage"):
				body = `{"ok":true,"result":{"message_id":2,"chat":{"id":42,"type":"private"},"text":"ok"}}`
			default:
				body = `{"ok":true,"result":true}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}),
	}
}

func TestConsumer_DoubleStopAndContextCancel(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?authorizationToken=123:test&chatId=42")
	ep.httpClient = newFakeTelegramClient()

	received := make(chan string, 16)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		body, _ := ex.GetIn().GetBodyAsString()
		select {
		case received <- body:
		default:
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Duplicate Start must be a no-op
	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	select {
	case msg := <-received:
		if msg != "hello" {
			t.Fatalf("got %q, want %q", msg, "hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	// Concurrent and repeated Stop calls must not panic
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := consumer.Stop(); err != nil {
				t.Errorf("Stop: %v", err)
			}
		}()
	}
	wg.Wait()

	// Restart, then cancel context before Stop
	ctx2, cancel2 := context.WithCancel(context.Background())
	if err := consumer.Start(ctx2); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	cancel2()
	time.Sleep(20 * time.Millisecond)
	if err := consumer.Stop(); err != nil {
		t.Fatalf("Stop after context cancel: %v", err)
	}
}

func TestProducer_SendRejectedAfterStopUntilRestarted(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?authorizationToken=123:test&chatId=42")
	ep.httpClient = newFakeTelegramClient()

	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := prod.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := prod.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	ex := gocamel.NewExchange(ctx)
	ex.SetBody("after stop")
	if err := prod.Send(ex); err == nil {
		t.Fatal("expected error when sending after Stop()")
	}

	if err := prod.Start(ctx); err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	defer prod.Stop()
	if err := prod.Send(ex); err != nil {
		t.Fatalf("Send after restart: %v", err)
	}
}
