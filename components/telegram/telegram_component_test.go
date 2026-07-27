package telegram

import (
	"context"
	"testing"

	gocamel "gitlab.com/tranchida/gocamel"
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
	uri := "telegram://bots?authorizationToken=abc&chatId=42"
	ep := newEndpoint(t, uri)
	if ep.URI() != uri {
		t.Errorf("URI() = %q, want %q", ep.URI(), uri)
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
		{name: "int64 header wins", uri: "telegram://bots?chatId=42", header: int64(7), want: 7},
		{name: "int header", uri: "telegram://bots?chatId=42", header: 7, want: 7},
		{name: "string header", uri: "telegram://bots?chatId=42", header: "7", want: 7},
		{name: "unparseable header", uri: "telegram://bots?chatId=42", header: "abc", wantErr: true},
		{name: "unsupported header type", uri: "telegram://bots?chatId=42", header: 1.5, wantErr: true},
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
