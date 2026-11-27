package webex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	messagehandler "github.com/3rg0n/webex-message-handler/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

func clearTokenEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"WEBEX_ACCESS_TOKEN", "WEBEX_ACCESSTOKEN", "WEBEX_AUTHORIZATIONTOKEN", "accessToken", "authorizationToken"} {
		t.Setenv(k, "")
	}
}

func mustCreateEndpoint(t *testing.T, uri string) *WebexEndpoint {
	t.Helper()
	ep, err := NewWebexComponent().CreateEndpoint(uri)
	require.NoError(t, err, "CreateEndpoint(%q)", uri)
	return ep.(*WebexEndpoint)
}

// fakeMercuryHandler is an in-memory mercuryHandler for deterministic consumer tests.
type fakeMercuryHandler struct {
	mu               sync.Mutex
	connectFn        func(ctx context.Context) error
	disconnectFn     func(ctx context.Context) error
	connectCtx       context.Context
	connectCalls     int
	disconnectCalls  int
	events           []string
	onMessageCreated func(msg messagehandler.DecryptedMessage)
	onConnected      func()
	onDisconnected   func(reason string)
	onReconnecting   func(attempt int)
	onError          func(err error)
}

func (f *fakeMercuryHandler) Connect(ctx context.Context) error {
	f.mu.Lock()
	f.connectCalls++
	f.connectCtx = ctx
	f.events = append(f.events, "connect")
	fn := f.connectFn
	onConn := f.onConnected
	f.mu.Unlock()

	if fn != nil {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	if onConn != nil {
		onConn()
	}
	return nil
}

func (f *fakeMercuryHandler) Disconnect(ctx context.Context) error {
	f.mu.Lock()
	f.disconnectCalls++
	if f.connectCtx != nil && f.connectCtx.Err() != nil {
		f.events = append(f.events, "disconnect-after-cancel")
	} else {
		f.events = append(f.events, "disconnect-before-cancel")
	}
	fn := f.disconnectFn
	onDisc := f.onDisconnected
	f.mu.Unlock()

	if fn != nil {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	if onDisc != nil {
		onDisc("client")
	}
	return nil
}

func (f *fakeMercuryHandler) OnMessageCreated(fn func(msg messagehandler.DecryptedMessage)) {
	f.mu.Lock()
	f.onMessageCreated = fn
	f.mu.Unlock()
}

func (f *fakeMercuryHandler) OnConnected(fn func()) {
	f.mu.Lock()
	f.onConnected = fn
	f.mu.Unlock()
}

func (f *fakeMercuryHandler) OnDisconnected(fn func(reason string)) {
	f.mu.Lock()
	f.onDisconnected = fn
	f.mu.Unlock()
}

func (f *fakeMercuryHandler) OnReconnecting(fn func(attempt int)) {
	f.mu.Lock()
	f.onReconnecting = fn
	f.mu.Unlock()
}

func (f *fakeMercuryHandler) OnError(fn func(err error)) {
	f.mu.Lock()
	f.onError = fn
	f.mu.Unlock()
}

func (f *fakeMercuryHandler) emitMessage(msg messagehandler.DecryptedMessage) {
	f.mu.Lock()
	fn := f.onMessageCreated
	f.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

func TestCreateEndpoint_DefaultsAndRedaction(t *testing.T) {
	ep := mustCreateEndpoint(t, "webex:bots?accessToken=secret-tok-123&roomId=room-1")
	assert.Equal(t, FormatMarkdown, ep.format)
	assert.Equal(t, DefaultBaseURL, ep.baseURL)
	assert.Equal(t, defaultHTTPTimeout, ep.timeout)
	assert.False(t, ep.allowHeaderOverride)
	assert.True(t, ep.ignoreSelfMessages)
	assert.True(t, ep.ignoreEmptyMessages)
	assert.Equal(t, defaultMaxConcurrency, ep.maxConcurrency)
	assert.Equal(t, defaultShutdownTimeout, ep.shutdownTimeout)

	redacted := ep.URI()
	assert.NotContains(t, redacted, "secret-tok-123")
	assert.Contains(t, redacted, "accessToken=xxxxx")
	assert.Contains(t, redacted, "roomId=room-1")

	ep2 := mustCreateEndpoint(t, "webex://bots?authorizationToken=auth-secret-999&format=TEXT&baseURL=https://proxy.example.com/v1/")
	assert.Equal(t, FormatText, ep2.format)
	assert.Equal(t, "https://proxy.example.com/v1", ep2.baseURL)
	assert.NotContains(t, ep2.URI(), "auth-secret-999")
	assert.Contains(t, ep2.URI(), "authorizationToken=xxxxx")
}

func TestCreateEndpoint_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"multiple targets roomId+toPersonEmail", "webex:bots?roomId=r1&toPersonEmail=a@b.com"},
		{"multiple targets roomId+toPersonId", "webex:bots?roomId=r1&toPersonId=p1"},
		{"multiple targets email+personId", "webex:bots?toPersonEmail=a@b.com&toPersonId=p1"},
		{"invalid format", "webex:bots?format=html"},
		{"relative baseURL", "webex:bots?baseURL=/v1"},
		{"non-http baseURL", "webex:bots?baseURL=ftp://webexapis.com/v1"},
		{"hostless baseURL", "webex:bots?baseURL=https:///v1"},
		{"baseURL with userinfo", "webex:bots?baseURL=https://user:pass@webexapis.com/v1"},
		{"baseURL with query", "webex:bots?baseURL=https://webexapis.com/v1?foo=bar"},
		{"baseURL with fragment", "webex:bots?baseURL=" + url.QueryEscape("https://webexapis.com/v1#frag")},
		{"invalid timeout", "webex:bots?timeout=notaduration"},
		{"zero timeout", "webex:bots?timeout=0s"},
		{"negative timeout", "webex:bots?timeout=-5s"},
		{"invalid allowHeaderOverride", "webex:bots?allowHeaderOverride=maybe"},
		{"invalid ignoreSelfMessages", "webex:bots?ignoreSelfMessages=nope"},
		{"invalid ignoreEmptyMessages", "webex:bots?ignoreEmptyMessages=nope"},
		{"invalid maxConcurrency", "webex:bots?maxConcurrency=0"},
		{"non-int maxConcurrency", "webex:bots?maxConcurrency=abc"},
		{"invalid shutdownTimeout", "webex:bots?shutdownTimeout=0s"},
		{"invalid pingInterval", "webex:bots?pingInterval=-1s"},
		{"invalid pongTimeout", "webex:bots?pongTimeout=0s"},
		{"pongTimeout >= default pingInterval", "webex:bots?pongTimeout=15s"},
		{"default pongTimeout >= pingInterval", "webex:bots?pingInterval=10s"},
		{"explicit pongTimeout >= pingInterval", "webex:bots?pingInterval=20s&pongTimeout=20s"},
		{"invalid reconnectBackoffMax", "webex:bots?reconnectBackoffMax=0s"},
		{"invalid maxReconnectAttempts", "webex:bots?maxReconnectAttempts=0"},
		{"comma-only allowedDomains", "webex:bots?allowedDomains=,"},
		{"email in allowedDomains", "webex:bots?allowedDomains=user@example.com"},
		{"comma-only allowedEmails", "webex:bots?allowedEmails=,"},
		{"bad email in allowedEmails", "webex:bots?allowedEmails=@example.com"},
	}

	comp := NewWebexComponent()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := comp.CreateEndpoint(tc.uri)
			assert.Error(t, err)
		})
	}
}

func TestCreateEndpoint_NonSecretOptionsIgnoreEnvVars(t *testing.T) {
	t.Setenv("WEBEX_ALLOWHEADEROVERRIDE", "true")
	t.Setenv("WEBEX_FORMAT", "text")
	t.Setenv("WEBEX_ROOMID", "env-room")
	t.Setenv("format", "text")
	t.Setenv("timeout", "1s")

	ep := mustCreateEndpoint(t, "webex:bots")
	assert.False(t, ep.allowHeaderOverride)
	assert.Equal(t, FormatMarkdown, ep.format)
	assert.Equal(t, "", ep.roomID)
	assert.Equal(t, defaultHTTPTimeout, ep.timeout)
}

func TestResolveToken_PrecedenceAndValidation(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		clearTokenEnv(t)
		ep := mustCreateEndpoint(t, "webex:bots")
		_, err := ep.resolveToken()
		assert.Error(t, err)
	})

	t.Run("WEBEX_ACCESS_TOKEN beats query accessToken", func(t *testing.T) {
		clearTokenEnv(t)
		t.Setenv("WEBEX_ACCESS_TOKEN", "env-primary")
		t.Setenv("WEBEX_ACCESSTOKEN", "env-secondary")
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=query-tok")
		tok, err := ep.resolveToken()
		require.NoError(t, err)
		assert.Equal(t, "env-primary", tok)
	})

	t.Run("WEBEX_AUTHORIZATIONTOKEN beats query accessToken", func(t *testing.T) {
		clearTokenEnv(t)
		t.Setenv("WEBEX_AUTHORIZATIONTOKEN", "env-auth")
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=query-tok")
		tok, err := ep.resolveToken()
		require.NoError(t, err)
		assert.Equal(t, "env-auth", tok)
	})

	t.Run("query accessToken beats query authorizationToken", func(t *testing.T) {
		clearTokenEnv(t)
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=q-access&authorizationToken=q-auth")
		tok, err := ep.resolveToken()
		require.NoError(t, err)
		assert.Equal(t, "q-access", tok)
	})

	t.Run("query authorizationToken fallback", func(t *testing.T) {
		clearTokenEnv(t)
		ep := mustCreateEndpoint(t, "webex:bots?authorizationToken=q-auth")
		tok, err := ep.resolveToken()
		require.NoError(t, err)
		assert.Equal(t, "q-auth", tok)
	})

	t.Run("rejects control, whitespace, and non-ASCII bytes", func(t *testing.T) {
		for _, bad := range []string{"bad token", "bad\r\ntoken", "bad\x80token"} {
			clearTokenEnv(t)
			t.Setenv("WEBEX_ACCESS_TOKEN", bad)
			ep := mustCreateEndpoint(t, "webex:bots")
			_, err := ep.resolveToken()
			assert.Error(t, err, "token %q should be rejected", bad)
		}
		// Null byte cannot be placed in OS env on Unix; verify via query param and direct validation.
		clearTokenEnv(t)
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=bad%00token")
		_, err := ep.resolveToken()
		assert.Error(t, err)
		assert.Error(t, validateAccessToken("bad\x00token"))
	})
}

func TestAccessList(t *testing.T) {
	t.Run("empty allows everyone", func(t *testing.T) {
		list, err := parseAccessList("", "")
		require.NoError(t, err)
		assert.True(t, list.allowed("anyone@anywhere.org"))
		assert.True(t, list.allowed(""))
	})

	t.Run("domains and emails normalized and hardened", func(t *testing.T) {
		list, err := parseAccessList(" @Company.COM , partner.org ", " VIP@External.IO ")
		require.NoError(t, err)

		assert.True(t, list.allowed("Alice@company.com"))
		assert.True(t, list.allowed("bob@partner.org"))
		assert.True(t, list.allowed("vip@external.io"))

		assert.False(t, list.allowed("other@external.io"))
		assert.False(t, list.allowed("@company.com"), "empty local part must be rejected")
		assert.False(t, list.allowed("company.com"))
		assert.False(t, list.allowed("a@b@company.com"))
		assert.False(t, list.allowed(""))
	})
}

func TestRestIDAndUUIDHelpers(t *testing.T) {
	const rawUUID = "11111111-2222-3333-4444-555555555555"
	assert.True(t, isUUID(rawUUID))
	assert.True(t, isUUID(strings.ToUpper(rawUUID)))
	assert.False(t, isUUID("not-a-uuid"))
	assert.False(t, isUUID("11111111-2222-3333-4444-55555555555g"))

	rawURI := "ciscospark://urn:TEAM:eu-central-1_k/ROOM/" + strings.ToUpper(rawUUID)
	variants := []string{
		base64.StdEncoding.EncodeToString([]byte(rawURI)),
		base64.RawStdEncoding.EncodeToString([]byte(rawURI)),
		base64.URLEncoding.EncodeToString([]byte(rawURI)),
		base64.RawURLEncoding.EncodeToString([]byte(rawURI)),
	}
	for _, v := range variants {
		parts, err := decodeRestID(v)
		require.NoError(t, err, "variant %q", v)
		assert.Equal(t, "urn:TEAM:eu-central-1_k", parts.cluster)
		assert.Equal(t, "ROOM", parts.resourceType)
		assert.Equal(t, rawUUID, parts.uuid)
	}

	_, err := decodeRestID("not-base64!!!")
	assert.Error(t, err)
	_, err = decodeRestID(base64.StdEncoding.EncodeToString([]byte("https://example.com/ROOM/" + rawUUID)))
	assert.Error(t, err)

	encoded := encodeRestID("eu", "MESSAGE", rawUUID)
	parts, err := decodeRestID(encoded)
	require.NoError(t, err)
	assert.Equal(t, "eu", parts.cluster)
	assert.Equal(t, "MESSAGE", parts.resourceType)
	assert.Equal(t, rawUUID, parts.uuid)
}

func TestParseNextLink(t *testing.T) {
	assert.Equal(t, "https://webexapis.com/v1/rooms?cursor=abc",
		parseNextLink([]string{`<https://webexapis.com/v1/rooms?cursor=abc>; rel="next"`}))
	assert.Equal(t, "https://webexapis.com/v1/rooms?cursor=def",
		parseNextLink([]string{
			`<https://webexapis.com/v1/rooms?cursor=prev>; rel="previous"`,
			`<https://webexapis.com/v1/rooms?cursor=def>; title="page2"; REL=next`,
		}))
	assert.Equal(t, "https://webexapis.com/v1/rooms?a=1,2",
		parseNextLink([]string{`<https://webexapis.com/v1/rooms?a=1,2>; rel="next prev"`}))
	assert.Equal(t, "",
		parseNextLink([]string{`<https://webexapis.com/v1/rooms?cursor=prev>; rel="previous"`}))
}

func TestProducer_LifecycleAndBasicSend(t *testing.T) {
	clearTokenEnv(t)

	t.Run("Start fails fast without target when allowHeaderOverride is false", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok-1")
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		assert.Error(t, prod.Start(context.Background()))
	})

	t.Run("Send fails when producer not started or exchange/body invalid", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok-1&roomId=room-1&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		// Not started
		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		assert.Error(t, prod.Send(ex))

		require.NoError(t, prod.Start(context.Background()))
		// Idempotent Start
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		assert.Error(t, prod.Send(nil))

		nilBody := gocamel.NewExchange(context.Background())
		assert.Error(t, prod.Send(nilBody))

		wsBody := gocamel.NewExchange(context.Background())
		wsBody.SetBody("   \n\t ")
		assert.Error(t, prod.Send(wsBody))
	})

	t.Run("Markdown default, Text format, and Out message ID preservation", func(t *testing.T) {
		var received []outboundMessage
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/messages", r.URL.Path)
			assert.Equal(t, "Bearer test-secret", r.Header.Get("Authorization"))
			var msg outboundMessage
			require.NoError(t, json.NewDecoder(r.Body).Decode(&msg))
			received = append(received, msg)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"msg-created-42"}`)
		}))
		defer server.Close()

		ep := mustCreateEndpoint(t, "webex:bots?accessToken=test-secret&roomId=rest-room-1&allowHeaderOverride=true&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		// Default markdown
		ex1 := gocamel.NewExchange(context.Background())
		ex1.SetBody("**bold**")
		require.NoError(t, prod.Send(ex1))
		assert.Equal(t, "**bold**", ex1.GetIn().GetBody())
		idVal, ok := ex1.GetOut().GetHeader(HeaderMessageID)
		require.True(t, ok)
		assert.Equal(t, "msg-created-42", idVal)

		// Override format to text + []byte body
		ex2 := gocamel.NewExchange(context.Background())
		ex2.SetBody([]byte("plain text"))
		ex2.SetHeader(HeaderFormat, "text")
		require.NoError(t, prod.Send(ex2))

		require.Len(t, received, 2)
		assert.Equal(t, outboundMessage{RoomID: "rest-room-1", Markdown: "**bold**"}, received[0])
		assert.Equal(t, outboundMessage{RoomID: "rest-room-1", Text: "plain text"}, received[1])
	})

	t.Run("2xx empty and non-JSON responses do not fail", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "OK")
		}))
		defer server.Close()

		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&roomId=r1&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		for i := 0; i < 2; i++ {
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody("hi")
			require.NoError(t, prod.Send(ex))
			_, hasID := ex.GetOut().GetHeader(HeaderMessageID)
			assert.False(t, hasID)
		}
	})
}

func TestProducer_HeaderOverrideAndDestinations(t *testing.T) {
	clearTokenEnv(t)

	var lastMsg outboundMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg outboundMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&msg))
		lastMsg = msg
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("allowHeaderOverride=false ignores inbound headers", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&roomId=static-room&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		ex.SetHeader(HeaderRoomID, "attacker-room")
		ex.SetHeader(HeaderFormat, "text")
		ex.SetHeader(HeaderParentID, "parent-1")
		require.NoError(t, prod.Send(ex))
		assert.Equal(t, outboundMessage{RoomID: "static-room", Markdown: "hello"}, lastMsg)
	})

	t.Run("allowHeaderOverride=true precedence and validation", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&allowHeaderOverride=true&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		// roomId wins over toPersonEmail
		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("msg1")
		ex.SetHeader(HeaderRoomID, "room-a")
		ex.SetHeader(HeaderToPersonEmail, "alice@company.com")
		require.NoError(t, prod.Send(ex))
		assert.Equal(t, outboundMessage{RoomID: "room-a", Markdown: "msg1"}, lastMsg)

		// toPersonEmail when roomId empty/whitespace
		ex2 := gocamel.NewExchange(context.Background())
		ex2.SetBody("msg2")
		ex2.SetHeader(HeaderRoomID, "   ")
		ex2.SetHeader(HeaderToPersonEmail, []byte("alice@company.com"))
		require.NoError(t, prod.Send(ex2))
		assert.Equal(t, outboundMessage{ToPersonEmail: "alice@company.com", Markdown: "msg2"}, lastMsg)

		// toPersonId fallback
		ex3 := gocamel.NewExchange(context.Background())
		ex3.SetBody("msg3")
		ex3.SetHeader(HeaderToPersonID, "person-id-9")
		require.NoError(t, prod.Send(ex3))
		assert.Equal(t, outboundMessage{ToPersonID: "person-id-9", Markdown: "msg3"}, lastMsg)

		// parentId with toPersonEmail rejected
		ex4 := gocamel.NewExchange(context.Background())
		ex4.SetBody("msg4")
		ex4.SetHeader(HeaderToPersonEmail, "alice@company.com")
		ex4.SetHeader(HeaderParentID, "parent-1")
		assert.Error(t, prod.Send(ex4))

		// invalid header type rejected
		ex5 := gocamel.NewExchange(context.Background())
		ex5.SetBody("msg5")
		ex5.SetHeader(HeaderRoomID, 12345)
		assert.Error(t, prod.Send(ex5))

		// invalid format header rejected
		ex6 := gocamel.NewExchange(context.Background())
		ex6.SetBody("msg6")
		ex6.SetHeader(HeaderRoomID, "room-a")
		ex6.SetHeader(HeaderFormat, "html")
		assert.Error(t, prod.Send(ex6))

		// missing target everywhere rejected
		ex7 := gocamel.NewExchange(context.Background())
		ex7.SetBody("msg7")
		assert.Error(t, prod.Send(ex7))
	})
}

func TestProducer_RegionalRoomResolutionPaginationCacheAndParentID(t *testing.T) {
	clearTokenEnv(t)

	const (
		rawRoomUUID   = "11111111-2222-3333-4444-555555555555"
		rawParentUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		cluster       = "urn:TEAM:eu-central-1_k"
	)
	// Use unpadded base64 as Webex REST API sometimes returns.
	regionalRoomID := base64.RawStdEncoding.EncodeToString([]byte("ciscospark://" + cluster + "/ROOM/" + rawRoomUUID))
	wantParentRestID := encodeRestID(cluster, "MESSAGE", rawParentUUID)

	var roomListCalls atomic.Int32
	var sentMessages []outboundMessage
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			roomListCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Query().Get("page") != "2" {
				w.Header().Add("Link", fmt.Sprintf(`<%s/rooms?page=2>; rel="next"`, server.URL))
				_, _ = fmt.Fprint(w, `{"items":[]}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"items":[{"id":%q}]}`, regionalRoomID)
		case "/messages":
			var msg outboundMessage
			require.NoError(t, json.NewDecoder(r.Body).Decode(&msg))
			sentMessages = append(sentMessages, msg)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"sent-1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&allowHeaderOverride=true&baseURL="+url.QueryEscape(server.URL))
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	// First send resolves across 2 pages and encodes parentId using the EU cluster.
	for i := 0; i < 2; i++ {
		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("reply")
		ex.SetHeader(HeaderRoomID, rawRoomUUID)
		ex.SetHeader(HeaderParentID, rawParentUUID)
		require.NoError(t, prod.Send(ex))
	}

	assert.Equal(t, int32(2), roomListCalls.Load(), "expected 2 paginated GET /rooms calls followed by cache hit")
	require.Len(t, sentMessages, 2)
	assert.Equal(t, regionalRoomID, sentMessages[0].RoomID)
	assert.Equal(t, wantParentRestID, sentMessages[0].ParentID)
	assert.Equal(t, regionalRoomID, sentMessages[1].RoomID)
	assert.Equal(t, wantParentRestID, sentMessages[1].ParentID)
}

func TestWebexClient_PaginationSecurityAndBounds(t *testing.T) {
	const rawUUID = "11111111-2222-3333-4444-555555555555"

	t.Run("rejects cross-origin Link next URL", func(t *testing.T) {
		var evilHit atomic.Bool
		evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			evilHit.Store(true)
			w.WriteHeader(http.StatusOK)
		}))
		defer evil.Close()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", fmt.Sprintf(`<%s/rooms?page=2>; rel="next"`, evil.URL))
			_, _ = fmt.Fprint(w, `{"items":[]}`)
		}))
		defer server.Close()

		parsed, base, err := validateBaseURL(server.URL)
		require.NoError(t, err)
		c := newWebexClient("secret-token", base, parsed, newSafeProducerHTTPClient(5*time.Second))

		_, err = c.resolveRoomID(context.Background(), rawUUID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cross-origin")
		assert.False(t, evilHit.Load(), "Bearer token must never be sent to cross-origin Link target")
	})

	t.Run("rejects Link next URL with userinfo and relative URL", func(t *testing.T) {
		for _, link := range []string{
			`<https://user:pass@127.0.0.1/rooms?page=2>; rel="next"`,
			`</rooms?page=2>; rel="next"`,
		} {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Link", link)
				_, _ = fmt.Fprint(w, `{"items":[]}`)
			}))
			parsed, base, err := validateBaseURL(server.URL)
			require.NoError(t, err)
			c := newWebexClient("tok", base, parsed, newSafeProducerHTTPClient(5*time.Second))
			_, err = c.resolveRoomID(context.Background(), rawUUID)
			assert.Error(t, err)
			server.Close()
		}
	})

	t.Run("detects pagination cycle", func(t *testing.T) {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", fmt.Sprintf(`<%s/rooms?max=1000>; rel="next"`, server.URL))
			_, _ = fmt.Fprint(w, `{"items":[]}`)
		}))
		defer server.Close()

		parsed, base, err := validateBaseURL(server.URL)
		require.NoError(t, err)
		c := newWebexClient("tok", base, parsed, newSafeProducerHTTPClient(5*time.Second))
		_, err = c.resolveRoomID(context.Background(), rawUUID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cycle")
	})

	t.Run("enforces maxPages limit", func(t *testing.T) {
		var page atomic.Int32
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := page.Add(1)
			w.Header().Set("Link", fmt.Sprintf(`<%s/rooms?p=%d>; rel="next"`, server.URL, n))
			_, _ = fmt.Fprint(w, `{"items":[]}`)
		}))
		defer server.Close()

		parsed, base, err := validateBaseURL(server.URL)
		require.NoError(t, err)
		c := newWebexClient("tok", base, parsed, newSafeProducerHTTPClient(5*time.Second))
		c.maxPages = 3
		_, err = c.resolveRoomID(context.Background(), rawUUID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum room pagination pages")
		assert.Equal(t, int32(3), page.Load())
	})

	t.Run("cache eviction still caches target room and concurrent hits do not block on miss", func(t *testing.T) {
		const (
			cachedUUID = "00000000-0000-0000-0000-000000000001"
			otherUUID1 = "00000000-0000-0000-0000-000000000002"
			otherUUID2 = "00000000-0000-0000-0000-000000000003"
			targetUUID = "00000000-0000-0000-0000-000000000004"
		)
		cachedRest := encodeRestID("eu", "ROOM", cachedUUID)
		targetRest := encodeRestID("eu", "ROOM", targetUUID)

		missEntered := make(chan struct{})
		releaseMiss := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(missEntered)
			<-releaseMiss
			_, _ = fmt.Fprintf(w, `{"items":[{"id":%q},{"id":%q},{"id":%q}]}`,
				encodeRestID("eu", "ROOM", otherUUID1),
				encodeRestID("eu", "ROOM", otherUUID2),
				targetRest,
			)
		}))
		defer server.Close()

		parsed, base, err := validateBaseURL(server.URL)
		require.NoError(t, err)
		c := newWebexClient("tok", base, parsed, newSafeProducerHTTPClient(5*time.Second))
		c.maxCacheEntries = 2
		c.cacheRooms(nil, cachedUUID, cachedRest)

		missDone := make(chan error, 1)
		go func() {
			got, err := c.resolveRoomID(context.Background(), targetUUID)
			if err == nil && got != targetRest {
				err = fmt.Errorf("got %q want %q", got, targetRest)
			}
			missDone <- err
		}()

		<-missEntered
		// While miss is blocked in HTTP I/O, a cached room lookup succeeds immediately!
		gotCached, err := c.resolveRoomID(context.Background(), cachedUUID)
		require.NoError(t, err)
		assert.Equal(t, cachedRest, gotCached)

		close(releaseMiss)
		require.NoError(t, <-missDone)

		// Target must be present in cache despite capacity=2 and 3 returned items.
		gotTarget, ok := c.lookupCachedRoom(targetUUID)
		require.True(t, ok)
		assert.Equal(t, targetRest, gotTarget)
		c.cacheMu.RLock()
		cacheLen := len(c.roomIDs)
		c.cacheMu.RUnlock()
		assert.LessOrEqual(t, cacheLen, 2)
	})
}

func TestProducer_SecurityRedirectResponseLimitAndTokenRedaction(t *testing.T) {
	clearTokenEnv(t)

	t.Run("blocks 3xx redirects without leaking Bearer token", func(t *testing.T) {
		var redirectTargetHit atomic.Bool
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectTargetHit.Store(true)
			w.WriteHeader(http.StatusOK)
		}))
		defer target.Close()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/messages", http.StatusFound)
		}))
		defer server.Close()

		ep := mustCreateEndpoint(t, "webex:bots?accessToken=top-secret&roomId=r1&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		err = prod.Send(ex)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 302")
		assert.False(t, redirectTargetHit.Load())
	})

	t.Run("redacts token in API error body and URL errors", func(t *testing.T) {
		const secret = "super-secret-token-xyz"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "invalid token "+secret, http.StatusUnauthorized)
		}))
		defer server.Close()

		ep := mustCreateEndpoint(t, "webex:bots?accessToken="+secret+"&roomId=r1&baseURL="+url.QueryEscape(server.URL))
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prod.Start(context.Background()))
		defer func() { _ = prod.Stop() }()

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("hello")
		err = prod.Send(ex)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secret)
		assert.Contains(t, err.Error(), "xxxxx")

		urlErr := &url.Error{Op: "Post", URL: "https://webexapis.com/v1/" + secret, Err: context.DeadlineExceeded}
		redacted := redactWebexError(urlErr, secret)
		assert.NotContains(t, redacted.Error(), secret)
		assert.ErrorIs(t, redacted, context.DeadlineExceeded)
	})

	t.Run("bounded response transport rejects oversized body and skips HTTP 101", func(t *testing.T) {
		tr := &limitedResponseTransport{
			base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/upgrade" {
					return &http.Response{
						StatusCode: http.StatusSwitchingProtocols,
						Body:       io.NopCloser(strings.NewReader("1234567890")),
					}, nil
				}
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: -1,
					Body:          io.NopCloser(strings.NewReader("1234567890")),
				}, nil
			}),
			maxBytes:               5,
			skipSwitchingProtocols: true,
		}

		req101, _ := http.NewRequest(http.MethodGet, "https://example.com/upgrade", nil)
		resp101, err := tr.RoundTrip(req101)
		require.NoError(t, err)
		b101, err := io.ReadAll(resp101.Body)
		require.NoError(t, err)
		assert.Equal(t, "1234567890", string(b101))

		req200, _ := http.NewRequest(http.MethodGet, "https://example.com/data", nil)
		resp200, err := tr.RoundTrip(req200)
		require.NoError(t, err)
		_, err = io.ReadAll(resp200.Body)
		assert.Error(t, err)
	})

	t.Run("consumer HTTP client has ResponseHeaderTimeout and blocks cross-origin redirects", func(t *testing.T) {
		client := newSafeConsumerHTTPClient()
		assert.Zero(t, client.Timeout, "consumer HTTP client must not set global Timeout")

		prev, _ := url.Parse("https://webexapis.com/v1/people/me")
		crossOrigin, _ := url.Parse("https://evil.example/steal")
		downgrade, _ := url.Parse("http://webexapis.com/v1/people/me")
		sameOrig, _ := url.Parse("https://webexapis.com/v1/people/me2")

		assert.Error(t, client.CheckRedirect(&http.Request{URL: crossOrigin}, []*http.Request{{URL: prev}}))
		assert.Error(t, client.CheckRedirect(&http.Request{URL: downgrade}, []*http.Request{{URL: prev}}))
		assert.NoError(t, client.CheckRedirect(&http.Request{URL: sameOrig}, []*http.Request{{URL: prev}}))
		tenHops := make([]*http.Request, maxConsumerRedirects)
		for i := range tenHops {
			tenHops[i] = &http.Request{URL: prev}
		}
		assert.Error(t, client.CheckRedirect(&http.Request{URL: sameOrig}, tenHops))
	})
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestProducer_ConcurrentSendRace(t *testing.T) {
	clearTokenEnv(t)

	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"m1"}`)
	}))
	defer server.Close()

	ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&roomId=r1&baseURL="+url.QueryEscape(server.URL))
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody(fmt.Sprintf("msg-%d", i))
			assert.NoError(t, prod.Send(ex))
		}(i)
	}
	wg.Wait()
	assert.Equal(t, int32(16), count.Load())
}

func TestConsumer_RealHandlerConfigWiring(t *testing.T) {
	clearTokenEnv(t)
	ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok-wiring&ignoreSelfMessages=false&pingInterval=20s&pongTimeout=10s&reconnectBackoffMax=60s&maxReconnectAttempts=5")
	var capturedCfg messagehandler.Config
	ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) {
		capturedCfg = cfg
		// Verify real messagehandler.New accepts this exact Config.
		realHandler, err := messagehandler.New(cfg)
		require.NoError(t, err)
		require.NotNil(t, realHandler)
		return &fakeMercuryHandler{}, nil
	}

	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	require.NoError(t, cons.Start(context.Background()))
	defer func() { _ = cons.Stop() }()

	assert.Equal(t, "tok-wiring", capturedCfg.Token)
	require.NotNil(t, capturedCfg.IgnoreSelfMessages)
	assert.False(t, *capturedCfg.IgnoreSelfMessages)
	assert.Equal(t, 20.0, capturedCfg.PingInterval)
	assert.Equal(t, 10.0, capturedCfg.PongTimeout)
	assert.Equal(t, 60.0, capturedCfg.ReconnectBackoffMax)
	assert.Equal(t, 5, capturedCfg.MaxReconnectAttempts)
	assert.NotNil(t, capturedCfg.HTTPClient)
	assert.NotNil(t, capturedCfg.Logger)
}

func TestConsumer_MessageDeliveryFilteringAndHeaders(t *testing.T) {
	clearTokenEnv(t)

	ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&allowedDomains=company.com&allowedEmails=guest@partner.org")
	fake := &fakeMercuryHandler{}
	ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }

	received := make(chan *gocamel.Exchange, 8)
	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		received <- e
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, cons.Start(context.Background()))
	defer func() { _ = cons.Stop() }()

	// Ignored: empty/whitespace text
	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:          "m-empty",
		RoomID:      "r1",
		PersonEmail: "alice@company.com",
		Text:        "   \n ",
	})
	// Ignored: unauthorized sender
	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:          "m-unauth",
		RoomID:      "r1",
		PersonEmail: "intruder@evil.org",
		Text:        "hello",
	})
	// Accepted: allowed domain with full metadata
	files := []string{"https://webexapis.com/v1/contents/1"}
	mentions := []string{"person-uuid-1"}
	groupMentions := []string{"all"}
	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:              "msg-1",
		ParentID:        "parent-1",
		RoomID:          "room-uuid-1",
		PersonID:        "person-1",
		PersonEmail:     "alice@company.com",
		RoomType:        "group",
		Text:            "Hello bot",
		HTML:            "<p>Hello bot</p>",
		Created:         "2026-11-27T04:00:00Z",
		Files:           files,
		MentionedPeople: mentions,
		MentionedGroups: groupMentions,
	})
	// Accepted: allowed email with minimal metadata (empty optional fields omitted)
	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:          "msg-2",
		RoomID:      "room-uuid-2",
		PersonEmail: "guest@partner.org",
		Text:        "Guest ping",
	})

	got := make(map[string]*gocamel.Exchange)
	for i := 0; i < 2; i++ {
		select {
		case ex := <-received:
			id, _ := ex.GetIn().GetHeader(HeaderMessageID)
			got[id.(string)] = ex
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for message %d", i+1)
		}
	}

	ex1 := got["msg-1"]
	require.NotNil(t, ex1)
	assert.Equal(t, "Hello bot", ex1.GetIn().GetBody())
	mustHeaderStr := func(ex *gocamel.Exchange, key string) string {
		t.Helper()
		v, ok := ex.GetIn().GetHeaderAsString(key)
		require.True(t, ok, "missing string header %s", key)
		return v
	}
	assert.Equal(t, "room-uuid-1", mustHeaderStr(ex1, HeaderRoomID))
	assert.Equal(t, "parent-1", mustHeaderStr(ex1, HeaderParentID))
	assert.Equal(t, "person-1", mustHeaderStr(ex1, HeaderPersonID))
	assert.Equal(t, "alice@company.com", mustHeaderStr(ex1, HeaderPersonEmail))
	assert.Equal(t, "group", mustHeaderStr(ex1, HeaderRoomType))
	assert.Equal(t, "<p>Hello bot</p>", mustHeaderStr(ex1, HeaderHTML))
	assert.Equal(t, "2026-11-27T04:00:00Z", mustHeaderStr(ex1, HeaderCreated))

	gotFiles, _ := ex1.GetIn().GetHeader(HeaderFiles)
	assert.Equal(t, []string{"https://webexapis.com/v1/contents/1"}, gotFiles)
	gotMentions, _ := ex1.GetIn().GetHeader(HeaderMentionedPeople)
	assert.Equal(t, []string{"person-uuid-1"}, gotMentions)
	gotGroups, _ := ex1.GetIn().GetHeader(HeaderMentionedGroups)
	assert.Equal(t, []string{"all"}, gotGroups)

	ex2 := got["msg-2"]
	require.NotNil(t, ex2)
	assert.Equal(t, "Guest ping", ex2.GetIn().GetBody())
	_, hasParent := ex2.GetIn().GetHeader(HeaderParentID)
	_, hasRoomType := ex2.GetIn().GetHeader(HeaderRoomType)
	_, hasHTML := ex2.GetIn().GetHeader(HeaderHTML)
	assert.False(t, hasParent)
	assert.False(t, hasRoomType)
	assert.False(t, hasHTML)
}

func TestConsumer_IgnoreEmptyMessagesFalse(t *testing.T) {
	clearTokenEnv(t)

	ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&ignoreEmptyMessages=false")
	fake := &fakeMercuryHandler{}
	ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }

	received := make(chan *gocamel.Exchange, 1)
	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		received <- e
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, cons.Start(context.Background()))
	defer func() { _ = cons.Stop() }()

	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:     "file-only",
		RoomID: "r1",
		Text:   "",
		Files:  []string{"https://webexapis.com/v1/contents/doc.pdf"},
	})

	select {
	case ex := <-received:
		assert.Equal(t, "", ex.GetIn().GetBody())
		f, _ := ex.GetIn().GetHeader(HeaderFiles)
		assert.Equal(t, []string{"https://webexapis.com/v1/contents/doc.pdf"}, f)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for file-only message")
	}
}

type syncRecorder struct {
	completed atomic.Int32
	failed    atomic.Int32
}

func (s *syncRecorder) OnComplete(*gocamel.Exchange) { s.completed.Add(1) }
func (s *syncRecorder) OnFailure(*gocamel.Exchange)  { s.failed.Add(1) }

func TestConsumer_PanicContainmentAndMaxConcurrency(t *testing.T) {
	clearTokenEnv(t)

	ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok&maxConcurrency=2")
	fake := &fakeMercuryHandler{}
	ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }

	rec := &syncRecorder{}
	var active, maxSeen, done atomic.Int32
	gate := make(chan struct{})

	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
		e.AddSynchronization(rec)
		cur := active.Add(1)
		for {
			prev := maxSeen.Load()
			if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer func() {
			active.Add(-1)
			done.Add(1)
		}()

		if e.GetIn().GetBody() == "panic-me" {
			panic("boom")
		}
		<-gate
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, cons.Start(context.Background()))

	// Panic message must be recovered and fire OnFailure.
	fake.emitMessage(messagehandler.DecryptedMessage{ID: "p1", RoomID: "r1", Text: "panic-me"})
	require.Eventually(t, func() bool { return rec.failed.Load() == 1 }, 2*time.Second, 10*time.Millisecond)

	// Burst of 6 messages with maxConcurrency=2.
	for i := 0; i < 6; i++ {
		fake.emitMessage(messagehandler.DecryptedMessage{ID: fmt.Sprintf("m%d", i), RoomID: "r1", Text: "work"})
	}
	require.Eventually(t, func() bool { return active.Load() == 2 }, 2*time.Second, 10*time.Millisecond)
	assert.LessOrEqual(t, maxSeen.Load(), int32(2))

	close(gate)
	require.Eventually(t, func() bool { return rec.completed.Load() == 6 && done.Load() == 7 }, 2*time.Second, 10*time.Millisecond)
	assert.LessOrEqual(t, maxSeen.Load(), int32(2))
	require.NoError(t, cons.Stop())
}

func TestConsumer_ShutdownOrderRestartAndParentCancellation(t *testing.T) {
	clearTokenEnv(t)

	t.Run("Disconnect runs before connectCtx cancels and Stop waits for workers", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok")
		fake := &fakeMercuryHandler{}
		ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }

		workerStarted := make(chan struct{})
		workerCancelled := make(chan struct{})
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
			close(workerStarted)
			<-e.Context.Done()
			close(workerCancelled)
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, cons.Start(context.Background()))

		fake.emitMessage(messagehandler.DecryptedMessage{ID: "w1", RoomID: "r1", Text: "hold"})
		<-workerStarted

		require.NoError(t, cons.Stop())
		select {
		case <-workerCancelled:
		default:
			t.Fatal("Stop returned before worker finished")
		}

		fake.mu.Lock()
		events := append([]string(nil), fake.events...)
		fake.mu.Unlock()
		assert.Equal(t, []string{"connect", "disconnect-before-cancel"}, events)
	})

	t.Run("Connect failure triggers cleanup Disconnect", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=secret-tok")
		fake := &fakeMercuryHandler{
			connectFn: func(ctx context.Context) error {
				return errors.New("kms failed with secret-tok")
			},
		}
		ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }

		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		err = cons.Start(context.Background())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret-tok")
		assert.Contains(t, err.Error(), "xxxxx")

		fake.mu.Lock()
		disconnects := fake.disconnectCalls
		fake.mu.Unlock()
		assert.Equal(t, 1, disconnects)
	})

	t.Run("Start -> Stop -> Start creates fresh handler and delivers messages", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok")
		var created []*fakeMercuryHandler
		ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) {
			f := &fakeMercuryHandler{}
			created = append(created, f)
			return f, nil
		}

		got := make(chan string, 2)
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error {
			body, _ := e.GetIn().GetBodyAsString()
			got <- body
			return nil
		}))
		require.NoError(t, err)

		require.NoError(t, cons.Start(context.Background()))
		require.NoError(t, cons.Start(context.Background())) // idempotent
		created[0].emitMessage(messagehandler.DecryptedMessage{ID: "1", RoomID: "r1", Text: "first"})
		assert.Equal(t, "first", <-got)
		require.NoError(t, cons.Stop())
		require.NoError(t, cons.Stop()) // idempotent

		require.NoError(t, cons.Start(context.Background()))
		require.Len(t, created, 2)
		created[1].emitMessage(messagehandler.DecryptedMessage{ID: "2", RoomID: "r1", Text: "second"})
		assert.Equal(t, "second", <-got)
		require.NoError(t, cons.Stop())
	})

	t.Run("Parent context cancellation disconnects once without deadlock against Stop", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok")
			fake := &fakeMercuryHandler{}
			ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }

			cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { return nil }))
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			require.NoError(t, cons.Start(ctx))

			// Race parent cancel with explicit Stop.
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				cancel()
			}()
			go func() {
				defer wg.Done()
				_ = cons.Stop()
			}()
			wg.Wait()

			fake.mu.Lock()
			disconnects := fake.disconnectCalls
			events := append([]string(nil), fake.events...)
			fake.mu.Unlock()
			assert.Equal(t, 1, disconnects)
			assert.Equal(t, []string{"connect", "disconnect-before-cancel"}, events)
		}
	})

	t.Run("Logger redacts tokens in message and args", func(t *testing.T) {
		const secret = "tok-logger-secret"
		l := &webexLogger{token: secret}
		redacted := l.redactArgs([]any{"key", "val=" + secret, errors.New("err " + secret), 42})
		assert.Equal(t, "val=xxxxx", redacted[1])
		assert.Equal(t, "err xxxxx", redacted[2].(error).Error())
		assert.Equal(t, 42, redacted[3])
		// Smoke-test forwarding methods do not panic.
		l.Debug("dbg " + secret)
		l.Info("inf " + secret)
		l.Warn("wrn " + secret)
		l.Error("err " + secret)
	})

	t.Run("Consumer Start validation and lifecycle callbacks", func(t *testing.T) {
		ep := mustCreateEndpoint(t, "webex:bots?accessToken=tok-cb")
		nilProcCons, err := ep.CreateConsumer(nil)
		require.NoError(t, err)
		assert.Error(t, nilProcCons.Start(context.Background()))

		clearTokenEnv(t)
		noTokEp := mustCreateEndpoint(t, "webex:bots")
		noTokCons, err := noTokEp.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		assert.Error(t, noTokCons.Start(context.Background()))

		fake := &fakeMercuryHandler{}
		ep.handlerFactory = func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil }
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(e *gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		// Passing nil context defaults to context.Background().
		var nilCtx context.Context
		require.NoError(t, cons.Start(nilCtx))
		fake.mu.Lock()
		reconn := fake.onReconnecting
		disc := fake.onDisconnected
		onErr := fake.onError
		fake.mu.Unlock()
		if reconn != nil {
			reconn(2)
		}
		if disc != nil {
			disc("auth-failed")
		}
		if onErr != nil {
			onErr(errors.New("mercury error tok-cb"))
		}
		require.NoError(t, cons.Stop())
	})
}

func TestEndToEndBotRoute_InMemory(t *testing.T) {
	clearTokenEnv(t)
	t.Setenv("WEBEX_ACCESS_TOKEN", "bot-e2e-token")

	const (
		rawRoomUUID   = "22222222-3333-4444-5555-666666666666"
		rawParentUUID = "77777777-8888-9999-aaaa-bbbbbbbbbbbb"
		cluster       = "eu"
	)
	regionalRoomID := encodeRestID(cluster, "ROOM", rawRoomUUID)
	wantParentID := encodeRestID(cluster, "MESSAGE", rawParentUUID)

	sentCh := make(chan outboundMessage, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rooms":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"items":[{"id":%q}]}`, regionalRoomID)
		case "/messages":
			var msg outboundMessage
			require.NoError(t, json.NewDecoder(r.Body).Decode(&msg))
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":"reply-id-1"}`)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			sentCh <- msg
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// Custom wrapper component injecting the fake Mercury handler for the consumer endpoint.
	fake := &fakeMercuryHandler{}
	comp := &testWebexComponent{
		inner:          NewWebexComponent(),
		handlerFactory: func(cfg messagehandler.Config) (mercuryHandler, error) { return fake, nil },
	}

	camelCtx := gocamel.NewCamelContext()
	camelCtx.AddComponent("webex", comp)

	routeDone := make(chan string, 1)
	route, err := camelCtx.CreateRouteBuilder().
		From("webex:bots?allowedDomains=company.com").
		ProcessFunc(func(ex *gocamel.Exchange) error {
			// Simulate an LLM step writing to Out (GoCamel propagates Out.Body to In.Body
			// while preserving inbound CamelWebex* headers).
			body, _ := ex.GetIn().GetBodyAsString()
			ex.GetOut().SetBody("**Reply:** " + body)
			return nil
		}).
		To("webex:bots?allowHeaderOverride=true&baseURL=" + url.QueryEscape(server.URL)).
		ProcessFunc(func(ex *gocamel.Exchange) error {
			id, _ := ex.GetIn().GetHeaderAsString(HeaderMessageID)
			routeDone <- id
			return nil
		}).
		Build()
	require.NoError(t, err)
	camelCtx.AddRoute(route)

	require.NoError(t, camelCtx.Start())
	defer func() { _ = camelCtx.Stop() }()

	// Unauthorized user is dropped.
	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:          "m-blocked",
		RoomID:      rawRoomUUID,
		PersonEmail: "stranger@other.org",
		Text:        "secret?",
	})

	// Authorized user in a thread triggers regional room + parent resolution and Markdown reply.
	fake.emitMessage(messagehandler.DecryptedMessage{
		ID:          "m-ok",
		ParentID:    rawParentUUID,
		RoomID:      rawRoomUUID,
		PersonEmail: "alice@company.com",
		RoomType:    "group",
		Text:        "Hello GoCamel",
	})

	select {
	case got := <-sentCh:
		assert.Equal(t, regionalRoomID, got.RoomID)
		assert.Equal(t, wantParentID, got.ParentID)
		assert.Equal(t, "**Reply:** Hello GoCamel", got.Markdown)
		assert.Empty(t, got.Text)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for outbound bot reply")
	}

	select {
	case createdID := <-routeDone:
		assert.Equal(t, "reply-id-1", createdID)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for route completion")
	}
}

type testWebexComponent struct {
	inner          *WebexComponent
	handlerFactory handlerFactoryFunc
}

func (c *testWebexComponent) CreateEndpoint(uri string) (gocamel.Endpoint, error) {
	ep, err := c.inner.CreateEndpoint(uri)
	if err != nil {
		return nil, err
	}
	ep.(*WebexEndpoint).handlerFactory = c.handlerFactory
	return ep, nil
}
