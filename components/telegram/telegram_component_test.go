package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
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

func TestTelegram_RedactsBotTokenOnHTTPError(t *testing.T) {
	const secretToken = "99887766:SUPER-SECRET-BOT-TOKEN"

	t.Run("rejects invalid allowHeaderOverride and delimiter-injecting tokens", func(t *testing.T) {
		if _, err := NewTelegramComponent().CreateEndpoint("telegram://bots?authorizationToken=" + secretToken + "&allowHeaderOverride=maybe"); err == nil {
			t.Fatal("expected CreateEndpoint to reject invalid allowHeaderOverride boolean")
		}
		for _, badToken := range []string{"123@evil.example/bot", "123/foo", "123?x=1", "123#frag", "123 token"} {
			ep := newEndpoint(t, "telegram://bots?authorizationToken="+url.QueryEscape(badToken)+"&chatId=42")
			if _, err := ep.getBot(); err == nil {
				t.Fatalf("expected getBot to reject malicious token %q", badToken)
			}
		}
	})

	t.Run("redacts bot token in getBot url.Error", func(t *testing.T) {
		ep := newEndpoint(t, "telegram://bots?authorizationToken="+secretToken+"&chatId=42")
		ep.httpClient = &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: connection refused")
			}),
		}

		_, err := ep.getBot()
		if err == nil {
			t.Fatal("expected getBot error when transport fails")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("getBot error leaked bot token: %v", err)
		}
	})

	t.Run("redacts bot token in Send url.Error and uses bounded default timeout", func(t *testing.T) {
		epDefault := newEndpoint(t, "telegram://bots?authorizationToken="+secretToken+"&chatId=42")
		defaultWrapper := epDefault.newHTTPClient(context.Background(), secretToken)
		if defaultWrapper.client.Timeout <= 0 {
			t.Fatalf("expected bounded default HTTP timeout, got %v", defaultWrapper.client.Timeout)
		}

		// Also verify redactedTelegramError preserves context sentinel while redacting inner token strings.
		wrappedCtxErr := redactTelegramError(
			errors.Join(errors.New("https://api.telegram.org/bot"+secretToken+"/getMe"), context.Canceled),
			secretToken,
		)
		if strings.Contains(wrappedCtxErr.Error(), secretToken) {
			t.Fatalf("redactedTelegramError leaked token: %v", wrappedCtxErr)
		}
		if !errors.Is(wrappedCtxErr, context.Canceled) {
			t.Fatalf("expected redactedTelegramError to preserve context.Canceled sentinel")
		}

		epSend := newEndpoint(t, "telegram://bots?authorizationToken="+secretToken+"&chatId=42")
		epSend.httpClient = &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`)),
					}, nil
				}
				return nil, errors.New("send failed")
			}),
		}

		prod, err := epSend.CreateProducer()
		if err != nil {
			t.Fatal(err)
		}
		if err := prod.Send(nil); err == nil {
			t.Fatal("expected Send(nil) to fail")
		}
		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		err = prod.Send(ex)
		if err == nil {
			t.Fatal("expected Send error when transport fails")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("Send error leaked bot token: %v", err)
		}
	})
}

func TestTelegramConsumer_PollErrorsNonMessageAndUsernameHeader(t *testing.T) {
	ep := newEndpoint(t, "telegram://bots?authorizationToken=123:SECRET&chatId=42")
	var pollStep int
	var mu sync.Mutex
	ep.httpClient = &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/getMe") {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`)),
				}, nil
			}
			if strings.HasSuffix(r.URL.Path, "/getUpdates") {
				mu.Lock()
				pollStep++
				step := pollStep
				mu.Unlock()
				switch step {
				case 1:
					// Non-message update + valid message with From username
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":[{"update_id":1},{"update_id":2,"message":{"message_id":2,"from":{"id":9,"is_bot":false,"first_name":"Alice","username":"alice"},"chat":{"id":42,"type":"private"},"text":"hi"}}]}`)),
					}, nil
				default:
					// Simulate transient poll error; Stop cancels backoff timer promptly
					return nil, errors.New("temporary network error")
				}
			}
			return nil, errors.New("unexpected request")
		}),
	}

	gotUser := make(chan string, 1)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		if u, ok := ex.GetIn().GetHeaderAsString("TelegramUsername"); ok {
			select {
			case gotUser <- u:
			default:
			}
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case u := <-gotUser:
		if u != "alice" {
			t.Fatalf("TelegramUsername = %q, want %q", u, "alice")
		}
	case <-time.After(time.Second):
		_ = consumer.Stop()
		t.Fatal("timed out waiting for message with username")
	}

	// Give pollStep 2 a moment to enter backoff, then verify Stop interrupts the 3s retry timer promptly.
	time.Sleep(20 * time.Millisecond)
	startStop := time.Now()
	if err := consumer.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if time.Since(startStop) > 500*time.Millisecond {
		t.Fatalf("Stop waited for retry backoff timer (%v)", time.Since(startStop))
	}
}

func TestTelegramConsumer_StopAbortsInFlightPollPromptlyAndNilChatSafe(t *testing.T) {
	t.Run("Stop aborts in-flight getUpdates promptly", func(t *testing.T) {
		ep := newEndpoint(t, "telegram://bots?authorizationToken=123:test&chatId=42")
		pollingStarted := make(chan struct{}, 1)
		pollExited := make(chan error, 1)
		releasePoll := make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-releasePoll:
			default:
				close(releasePoll)
			}
		})

		ep.httpClient = &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`)),
					}, nil
				}
				if strings.HasSuffix(r.URL.Path, "/getUpdates") {
					select {
					case pollingStarted <- struct{}{}:
					default:
					}
					select {
					case <-r.Context().Done():
						pollExited <- r.Context().Err()
						return nil, r.Context().Err()
					case <-releasePoll:
						pollExited <- nil
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": []string{"application/json"}},
							Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`)),
						}, nil
					}
				}
				return nil, errors.New("unexpected request")
			}),
		}

		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}

		if err := consumer.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}

		select {
		case <-pollingStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for getUpdates poll to start")
		}

		stopDone := make(chan error, 1)
		go func() {
			stopDone <- consumer.Stop()
		}()

		select {
		case err := <-stopDone:
			if err != nil {
				t.Fatalf("Stop returned error: %v", err)
			}
		case <-time.After(400 * time.Millisecond):
			close(releasePoll)
			t.Fatal("Stop did not abort in-flight getUpdates request promptly")
		}

		select {
		case pollErr := <-pollExited:
			if !errors.Is(pollErr, context.Canceled) {
				t.Fatalf("expected in-flight getUpdates to exit with context.Canceled, got %v", pollErr)
			}
		case <-time.After(200 * time.Millisecond):
			close(releasePoll)
			t.Fatal("Stop returned while getUpdates goroutine was still leaked in flight")
		}
	})

	t.Run("nil Message.Chat does not panic", func(t *testing.T) {
		ep := newEndpoint(t, "telegram://bots?authorizationToken=123:test&chatId=42")
		var once sync.Once
		ep.httpClient = &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`)),
					}, nil
				}
				if strings.HasSuffix(r.URL.Path, "/getUpdates") {
					body := `{"ok":true,"result":[]}`
					once.Do(func() {
						body = `{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"text":"no-chat"}}]}`
					})
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(body)),
					}, nil
				}
				return nil, errors.New("unexpected request")
			}),
		}

		gotBody := make(chan string, 1)
		gotHasChat := make(chan bool, 1)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			s, _ := ex.GetIn().GetBodyAsString()
			_, hasChat := ex.GetIn().GetHeader(TelegramChatId)
			select {
			case gotBody <- s:
				gotHasChat <- hasChat
			default:
			}
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		if err := consumer.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = consumer.Stop() }()

		select {
		case msg := <-gotBody:
			if msg != "no-chat" {
				t.Fatalf("got body %q, want %q", msg, "no-chat")
			}
			if <-gotHasChat {
				t.Fatal("did not expect TelegramChatId header when Message.Chat is nil")
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for nil-chat message")
		}
	})
}

func TestTelegramProducer_NoLockDuringGetMeAndRespectsCtx(t *testing.T) {
	t.Run("Stop does not block on p.mu while getMe is in flight", func(t *testing.T) {
		ep := newEndpoint(t, "telegram://bots?authorizationToken=123:test&chatId=42")
		getMeEntered := make(chan struct{}, 1)
		releaseGetMe := make(chan struct{})
		t.Cleanup(func() {
			select {
			case <-releaseGetMe:
			default:
				close(releaseGetMe)
			}
		})

		ep.httpClient = &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					select {
					case getMeEntered <- struct{}{}:
					default:
					}
					select {
					case <-releaseGetMe:
						return &http.Response{
							StatusCode: http.StatusOK,
							Header:     http.Header{"Content-Type": []string{"application/json"}},
							Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`)),
						}, nil
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				}
				return nil, errors.New("unexpected call")
			}),
		}

		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatal(err)
		}

		startDone := make(chan error, 1)
		go func() {
			startDone <- prod.Start(context.Background())
		}()

		select {
		case <-getMeEntered:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for getMe")
		}

		stopDone := make(chan error, 1)
		go func() {
			stopDone <- prod.Stop()
		}()

		select {
		case err := <-stopDone:
			if err != nil {
				t.Fatalf("Stop error: %v", err)
			}
		case <-time.After(300 * time.Millisecond):
			close(releaseGetMe)
			t.Fatal("Stop blocked while getMe was in flight (p.mu held across network call)")
		}

		close(releaseGetMe)
		<-startDone

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("after concurrent stop")
		if err := prod.Send(ex); err == nil {
			t.Fatal("expected Send to fail when producer was stopped during Start")
		}
	})

	t.Run("Start and Send respect context cancellation", func(t *testing.T) {
		ep := newEndpoint(t, "telegram://bots?authorizationToken=123:test&chatId=42")
		ep.httpClient = &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					if r.Context().Err() != nil {
						return nil, r.Context().Err()
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"TestBot","username":"test_bot"}}`)),
					}, nil
				}
				if strings.HasSuffix(r.URL.Path, "/sendMessage") {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return nil, errors.New("unexpected call")
			}),
		}

		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatal(err)
		}

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := prod.Start(cancelledCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected Start with cancelled context to fail with context.Canceled, got %v", err)
		}

		if err := prod.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = prod.Stop() }()

		sendCtx, sendCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer sendCancel()
		ex := gocamel.NewExchange(sendCtx)
		ex.SetBody("hello")
		err = prod.Send(ex)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected Send to abort with context.DeadlineExceeded, got %v", err)
		}
	})
}
