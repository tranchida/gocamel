package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

func TestIdleSearchesAfterMailboxNotification(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	c := &MailConsumer{endpoint: &MailEndpoint{connectionTimeout: time.Second, folderName: "INBOX", unseen: true}, stopChan: make(chan struct{}), changes: make(chan struct{}, 1)}
	client := imapclient.New(clientConn, &imapclient.Options{UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: c.mailboxChanged}})
	c.client = client
	c.processor = gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil })
	defer client.Close()
	defer serverConn.Close()
	searched := make(chan struct{}, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer serverConn.Close()
		fmt.Fprint(serverConn, "* OK [CAPABILITY IMAP4rev1 IDLE] IMAP server ready\r\n")
		reader := bufio.NewReader(serverConn)
		notified := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return
			}
			tag := parts[0]
			switch strings.ToUpper(parts[1]) {
			case "CAPABILITY":
				fmt.Fprintf(serverConn, "* CAPABILITY IMAP4rev1 IDLE\r\n%s OK CAPABILITY done\r\n", tag)
			case "NOOP":
				fmt.Fprintf(serverConn, "%s OK NOOP done\r\n", tag)
			case "SELECT":
				fmt.Fprintf(serverConn, "* 0 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n%s OK [READ-WRITE] SELECT done\r\n", tag)
			case "UID":
				fmt.Fprintf(serverConn, "* SEARCH\r\n%s OK SEARCH done\r\n", tag)
				if notified {
					select {
					case searched <- struct{}{}:
					default:
					}
				}
			case "IDLE":
				fmt.Fprint(serverConn, "+ idling\r\n")
				if !notified {
					fmt.Fprint(serverConn, "* 1 EXISTS\r\n")
					notified = true
				}
				if _, err := reader.ReadString('\n'); err != nil {
					return
				}
				fmt.Fprintf(serverConn, "%s OK IDLE done\r\n", tag)
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- c.runWithIdle(context.Background()) }()
	select {
	case <-searched:
	case <-time.After(2 * time.Second):
		t.Error("mail arrival did not trigger a search")
	}
	close(c.stopChan)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Error("IDLE did not stop")
	}
	client.Close()
	<-serverDone
}

func TestIdleStopUnblocksStalledServerAndDoesNotFallbackToPolling(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	c := &MailConsumer{
		endpoint: &MailEndpoint{
			scheme:            ProtocolIMAP,
			useIdle:           true,
			connectionTimeout: 100 * time.Millisecond,
			pollDelay:         10 * time.Second,
			folderName:        "INBOX",
			unseen:            true,
		},
		stopChan:  make(chan struct{}),
		changes:   make(chan struct{}, 1),
		conn:      clientConn,
		processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
	}
	client := imapclient.New(clientConn, &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: c.mailboxChanged},
	})
	c.client = client
	defer client.Close()
	defer serverConn.Close()

	idling := make(chan struct{}, 1)
	go func() {
		defer serverConn.Close()
		_, _ = fmt.Fprint(serverConn, "* OK [CAPABILITY IMAP4rev1 IDLE] IMAP server ready\r\n")
		reader := bufio.NewReader(serverConn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return
			}
			tag := parts[0]
			switch strings.ToUpper(parts[1]) {
			case "NOOP":
				_, _ = fmt.Fprintf(serverConn, "%s OK NOOP done\r\n", tag)
			case "SELECT":
				_, _ = fmt.Fprintf(serverConn, "* 0 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n%s OK [READ-WRITE] SELECT done\r\n", tag)
			case "UID":
				_, _ = fmt.Fprintf(serverConn, "* SEARCH\r\n%s OK SEARCH done\r\n", tag)
			case "IDLE":
				_, _ = fmt.Fprint(serverConn, "+ idling\r\n")
				select {
				case idling <- struct{}{}:
				default:
				}
				// Drain without ever answering DONE or LOGOUT to simulate a stalled server
				for {
					if _, err := reader.ReadString('\n'); err != nil {
						return
					}
				}
			}
		}
	}()

	require.NoError(t, c.Start(context.Background()))
	select {
	case <-idling:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not enter IDLE")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- c.Stop() }()
	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() deadlocked on stalled IDLE server")
	}
}

func TestIMAPConsumer_UsesBodyPeekAndPopulatesHeadersAndUIDExpungeAndGetBoolHeaderIn(t *testing.T) {
	htmlRaw := "From: alice@example.com\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: HTML Only\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"X-Custom: abc\r\n" +
		"\r\n" +
		"<html><body><h1>Only HTML</h1></body></html>\r\n"

	plainRaw := "From: carol@example.com\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: Delete Me\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"X-Trace: xyz\r\n" +
		"\r\n" +
		"Hello plain\r\n"

	runMockIMAP := func(t *testing.T, searchUIDs string, bodyByUID map[string]string, flagsByUID map[string]string) (*MailConsumer, *[]string, *sync.Mutex, func()) {
		t.Helper()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })

		var mu sync.Mutex
		var cmds []string
		serverDone := make(chan struct{})

		go func() {
			defer close(serverDone)
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			fmt.Fprint(conn, "* OK [CAPABILITY IMAP4rev1 UIDPLUS MOVE] IMAP ready\r\n")
			r := bufio.NewReader(conn)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				trimmed := strings.TrimRight(line, "\r\n")
				parts := strings.Fields(trimmed)
				if len(parts) < 2 {
					return
				}
				tag := parts[0]
				upper := strings.ToUpper(trimmed)
				mu.Lock()
				cmds = append(cmds, upper)
				mu.Unlock()

				cmdName := strings.ToUpper(parts[1])
				switch cmdName {
				case "NOOP":
					fmt.Fprintf(conn, "%s OK NOOP done\r\n", tag)
				case "CAPABILITY":
					fmt.Fprintf(conn, "* CAPABILITY IMAP4rev1 UIDPLUS MOVE\r\n%s OK CAPABILITY done\r\n", tag)
				case "SELECT":
					fmt.Fprintf(conn, "* 2 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n%s OK [READ-WRITE] SELECT done\r\n", tag)
				case "UID":
					if len(parts) < 3 {
						fmt.Fprintf(conn, "%s BAD missing UID subcmd\r\n", tag)
						continue
					}
					sub := strings.ToUpper(parts[2])
					switch sub {
					case "SEARCH":
						fmt.Fprintf(conn, "* SEARCH %s\r\n%s OK UID SEARCH done\r\n", searchUIDs, tag)
					case "FETCH":
						uid := parts[3]
						raw := bodyByUID[uid]
						flags := flagsByUID[uid]
						if strings.Contains(upper, "RFC822.SIZE") && !strings.Contains(upper, "BODY") {
							fmt.Fprintf(conn, "* 1 FETCH (UID %s FLAGS (%s) ENVELOPE (\"Mon, 02 Jan 2006 15:04:05 +0000\" \"Subject\" ((\"Alice\" NIL \"alice\" \"example.com\")) NIL NIL ((\"Bob\" NIL \"bob\" \"example.com\")) NIL NIL NIL \"<1@ex>\") RFC822.SIZE %d)\r\n%s OK UID FETCH done\r\n",
								uid, flags, len(raw), tag)
						} else {
							fmt.Fprintf(conn, "* 1 FETCH (UID %s BODY[] {%d}\r\n%s)\r\n%s OK UID FETCH done\r\n",
								uid, len(raw), raw, tag)
						}
					case "STORE":
						fmt.Fprintf(conn, "%s OK UID STORE done\r\n", tag)
					case "EXPUNGE":
						fmt.Fprintf(conn, "* 1 EXPUNGE\r\n%s OK UID EXPUNGE done\r\n", tag)
					default:
						fmt.Fprintf(conn, "%s OK UID %s done\r\n", tag, sub)
					}
				case "EXPUNGE":
					fmt.Fprintf(conn, "%s OK EXPUNGE done\r\n", tag)
				case "LOGOUT":
					fmt.Fprintf(conn, "* BYE logging out\r\n%s OK LOGOUT done\r\n", tag)
					return
				default:
					fmt.Fprintf(conn, "%s OK done\r\n", tag)
				}
			}
		}()

		clientConn, err := net.Dial("tcp", ln.Addr().String())
		require.NoError(t, err)
		c := &MailConsumer{
			endpoint: &MailEndpoint{
				scheme:            ProtocolIMAP,
				connectionTimeout: 2 * time.Second,
				pollDelay:         5 * time.Second,
				folderName:        "INBOX",
				peek:              true,
				maxMessageSize:    gocamel.DefaultMaxBodySize,
			},
			stopChan: make(chan struct{}),
			changes:  make(chan struct{}, 1),
			conn:     clientConn,
		}
		client := imapclient.New(clientConn, &imapclient.Options{
			UnilateralDataHandler: &imapclient.UnilateralDataHandler{Mailbox: c.mailboxChanged},
		})
		c.client = client
		cleanup := func() {
			c.disconnect()
			<-serverDone
		}
		return c, &cmds, &mu, cleanup
	}

	// Part 1: peek=true, unseen=false, HTML-only email, processor fails -> must NOT markUnseen or markSeen,
	// must send BODY.PEEK[], must populate Content-Type & CamelMailHeader_*, must NOT dump raw RFC822 into Body.
	t.Run("peek and HTML-only and unseen=false failure does not rollback Seen", func(t *testing.T) {
		c, cmds, mu, cleanup := runMockIMAP(t, "10", map[string]string{"10": htmlRaw}, map[string]string{"10": `\Seen`})
		defer cleanup()
		c.endpoint.unseen = false

		var capturedEx *gocamel.Exchange
		c.processor = gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			capturedEx = ex
			return fmt.Errorf("downstream failure")
		})

		c.poll(context.Background())

		require.NotNil(t, capturedEx)
		assert.Nil(t, capturedEx.GetIn().GetBody(), "HTML-only email must not dump raw RFC822 into Body")
		htmlVal, ok := capturedEx.GetProperty("CamelMailBodyHTML")
		require.True(t, ok)
		assert.Contains(t, string(htmlVal.([]byte)), "<h1>Only HTML</h1>")
		ct, _ := capturedEx.GetIn().GetHeader(MailContentType)
		assert.Contains(t, ct, "text/html")
		custom, ok := capturedEx.GetIn().GetHeader(MailHeaderPrefix + "X-Custom")
		require.True(t, ok)
		assert.Equal(t, "abc", custom)

		mu.Lock()
		joined := strings.Join(*cmds, "\n")
		mu.Unlock()
		assert.Contains(t, joined, "BODY.PEEK[]")
		assert.NotContains(t, joined, "STORE", "must not mark Seen or Unseen on failure when peek=true and unseen=false")
	})

	// Part 2: peek=true, delete=true, processor succeeds -> marks \Seen AFTER success and uses UID EXPUNGE.
	t.Run("marks Seen after success and uses UID EXPUNGE", func(t *testing.T) {
		c, cmds, mu, cleanup := runMockIMAP(t, "11", map[string]string{"11": plainRaw}, map[string]string{"11": ""})
		defer cleanup()
		c.endpoint.unseen = true
		c.endpoint.delete = true

		var seenBeforeProc bool
		c.processor = gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			mu.Lock()
			for _, cmd := range *cmds {
				if strings.Contains(cmd, "STORE") && strings.Contains(cmd, `\SEEN`) {
					seenBeforeProc = true
				}
			}
			mu.Unlock()
			return nil
		})

		c.poll(context.Background())
		assert.False(t, seenBeforeProc, "\\Seen must NOT be marked before CompleteExchange succeeds")

		mu.Lock()
		joined := strings.Join(*cmds, "\n")
		mu.Unlock()
		assert.Contains(t, joined, "BODY.PEEK[]")
		assert.Contains(t, joined, "+FLAGS.SILENT (\\SEEN)")
		assert.Contains(t, joined, "UID EXPUNGE 11")
		// Check there is no standalone non-UID EXPUNGE.
		for _, cmd := range *cmds {
			assert.False(t, strings.HasSuffix(cmd, " EXPUNGE"), "unexpected non-UID EXPUNGE: %s", cmd)
		}
	})

	// Part 3: getBoolHeader checks Out then In.
	t.Run("getBoolHeader checks Out then In", func(t *testing.T) {
		c := &MailConsumer{}
		exInOnly := gocamel.NewExchange(context.Background())
		exInOnly.GetIn().SetHeader(MailDeleteHeader, true)
		assert.True(t, c.getBoolHeader(exInOnly, MailDeleteHeader))

		exInStr := gocamel.NewExchange(context.Background())
		exInStr.GetIn().SetHeader(MailDeleteHeader, "TRUE")
		assert.True(t, c.getBoolHeader(exInStr, MailDeleteHeader))

		exOutOverrides := gocamel.NewExchange(context.Background())
		exOutOverrides.GetIn().SetHeader(MailDeleteHeader, true)
		exOutOverrides.GetOut().SetHeader(MailDeleteHeader, false)
		assert.False(t, c.getBoolHeader(exOutOverrides, MailDeleteHeader))

		// Pop3Consumer shares the same Out-then-In behavior.
		pc := &Pop3Consumer{}
		assert.True(t, pc.getBoolHeader(exInOnly, MailDeleteHeader))
		assert.False(t, pc.getBoolHeader(exOutOverrides, MailDeleteHeader))
	})

	// Part 4: Concurrent Start/Stop under -race must be race-free.
	t.Run("concurrent Start and Stop is race-free", func(t *testing.T) {
		ep := &MailEndpoint{
			scheme:            ProtocolIMAP,
			host:              "127.0.0.1",
			port:              1, // closed/fast-fail
			connectionTimeout: 50 * time.Millisecond,
			pollDelay:         20 * time.Millisecond,
			folderName:        "INBOX",
			skipFailedMessage: true,
		}
		c := &MailConsumer{
			endpoint:  ep,
			processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
			stopChan:  make(chan struct{}),
			changes:   make(chan struct{}, 1),
		}
		for i := 0; i < 5; i++ {
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = c.Start(context.Background())
			}()
			go func() {
				defer wg.Done()
				_ = c.Stop()
			}()
			wg.Wait()
			_ = c.Stop()
		}
	})

	_ = imap.FlagSeen
}

func TestIMAPConsumer_MoveCopyExpungeStreamLimitAndTLSConnect(t *testing.T) {
	srvTLS, cliTLS := newTestLocalhostTLS(t)

	// 1. Test imaps:// connect + MOVE + COPY + non-UIDPLUS EXPUNGE + streaming ReadLimited limit.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var cmds []string
	var mode string // "move", "copy", "delete_plain", "oversized_stream"

	plainBody := "From: s@example.com\r\nSubject: Hi\r\nContent-Type: text/plain\r\n\r\nhello\r\n"
	oversizedBody := "From: s@example.com\r\nSubject: Big\r\nContent-Type: text/plain\r\n\r\n" + strings.Repeat("Z", 1024)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Deliberately advertise only IMAP4rev1 + MOVE (no UIDPLUS) to exercise plain EXPUNGE fallback.
				fmt.Fprint(c, "* OK [CAPABILITY IMAP4rev1 MOVE] IMAPS ready\r\n")
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					trimmed := strings.TrimRight(line, "\r\n")
					parts := strings.Fields(trimmed)
					if len(parts) < 2 {
						return
					}
					tag := parts[0]
					upper := strings.ToUpper(trimmed)
					mu.Lock()
					cmds = append(cmds, upper)
					curMode := mode
					mu.Unlock()

					switch strings.ToUpper(parts[1]) {
					case "LOGIN", "NOOP", "CAPABILITY":
						if strings.ToUpper(parts[1]) == "CAPABILITY" {
							fmt.Fprintf(c, "* CAPABILITY IMAP4rev1 MOVE\r\n")
						}
						fmt.Fprintf(c, "%s OK done\r\n", tag)
					case "SELECT":
						fmt.Fprintf(c, "* 1 EXISTS\r\n* OK [UIDVALIDITY 1] valid\r\n%s OK [READ-WRITE] SELECT done\r\n", tag)
					case "UID":
						sub := strings.ToUpper(parts[2])
						switch sub {
						case "SEARCH":
							fmt.Fprintf(c, "* SEARCH 21\r\n%s OK SEARCH done\r\n", tag)
						case "FETCH":
							uid := parts[3]
							if strings.Contains(upper, "RFC822.SIZE") && !strings.Contains(upper, "BODY") {
								// Report tiny RFC822.SIZE even in oversized_stream mode so streaming ReadLimited is exercised!
								fmt.Fprintf(c, "* 1 FETCH (UID %s FLAGS () ENVELOPE (\"Mon, 02 Jan 2006 15:04:05 +0000\" \"Subj\" NIL NIL NIL NIL NIL NIL NIL \"<21@ex>\") RFC822.SIZE 10)\r\n%s OK FETCH done\r\n", uid, tag)
							} else {
								payload := plainBody
								if curMode == "oversized_stream" {
									payload = oversizedBody
								}
								fmt.Fprintf(c, "* 1 FETCH (UID %s BODY[] {%d}\r\n%s)\r\n%s OK FETCH done\r\n", uid, len(payload), payload, tag)
							}
						case "STORE":
							fmt.Fprintf(c, "%s OK STORE done\r\n", tag)
						case "MOVE":
							fmt.Fprintf(c, "%s OK [COPYUID 1 21 1] MOVE done\r\n", tag)
						case "COPY":
							fmt.Fprintf(c, "%s OK [COPYUID 1 21 1] COPY done\r\n", tag)
						default:
							fmt.Fprintf(c, "%s OK done\r\n", tag)
						}
					case "EXPUNGE":
						fmt.Fprintf(c, "* 1 EXPUNGE\r\n%s OK EXPUNGE done\r\n", tag)
					case "LOGOUT":
						fmt.Fprintf(c, "* BYE\r\n%s OK LOGOUT done\r\n", tag)
						return
					default:
						fmt.Fprintf(c, "%s OK done\r\n", tag)
					}
				}
			}(conn)
		}
	}()

	comp := NewMailComponent()
	host, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	// Subcase A: Move via CamelMailMoveTo header + disconnect=true.
	epMove, err := comp.CreateEndpoint(fmt.Sprintf("imaps://%s:%s?username=u&password=p&disconnect=true&connectionTimeout=2000", host, port))
	require.NoError(t, err)
	cMove := &MailConsumer{
		endpoint:  epMove.(*MailEndpoint),
		tlsConfig: cliTLS,
		stopChan:  make(chan struct{}),
		changes:   make(chan struct{}, 1),
		processor: gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			ex.GetOut().SetHeader(MailMoveToHeader, "Archive")
			return nil
		}),
	}
	mu.Lock()
	mode = "move"
	cmds = nil
	mu.Unlock()
	cMove.poll(context.Background())
	mu.Lock()
	joinedMove := strings.Join(cmds, "\n")
	mu.Unlock()
	assert.Contains(t, joinedMove, `UID MOVE 21 "ARCHIVE"`)

	// Subcase B: Copy via CamelMailCopyTo header.
	epCopy, err := comp.CreateEndpoint(fmt.Sprintf("imaps://%s:%s?username=u&password=p&disconnect=true&connectionTimeout=2000", host, port))
	require.NoError(t, err)
	cCopy := &MailConsumer{
		endpoint:  epCopy.(*MailEndpoint),
		tlsConfig: cliTLS,
		stopChan:  make(chan struct{}),
		changes:   make(chan struct{}, 1),
		processor: gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			ex.GetIn().SetHeader(MailCopyToHeader, "Backup")
			return nil
		}),
	}
	mu.Lock()
	mode = "copy"
	cmds = nil
	mu.Unlock()
	cCopy.poll(context.Background())
	mu.Lock()
	joinedCopy := strings.Join(cmds, "\n")
	mu.Unlock()
	assert.Contains(t, joinedCopy, `UID COPY 21 "BACKUP"`)

	// Subcase C: Delete without UIDPLUS falls back to EXPUNGE.
	epDel, err := comp.CreateEndpoint(fmt.Sprintf("imaps://%s:%s?username=u&password=p&delete=true&disconnect=true&connectionTimeout=2000", host, port))
	require.NoError(t, err)
	cDel := &MailConsumer{
		endpoint:  epDel.(*MailEndpoint),
		tlsConfig: cliTLS,
		stopChan:  make(chan struct{}),
		changes:   make(chan struct{}, 1),
		processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
	}
	mu.Lock()
	mode = "delete_plain"
	cmds = nil
	mu.Unlock()
	cDel.poll(context.Background())
	mu.Lock()
	joinedDel := strings.Join(cmds, "\n")
	mu.Unlock()
	assert.Contains(t, joinedDel, "EXPUNGE")

	// Subcase D: Oversized streaming literal (server lies in RFC822.SIZE=10) triggers ReadLimited error -> handleFailedMessage.
	epLimit, err := comp.CreateEndpoint(fmt.Sprintf("imaps://%s:%s?username=u&password=p&maxMessageSize=200&handleFailedMessage=true&disconnect=true&connectionTimeout=2000", host, port))
	require.NoError(t, err)
	var failedUID, failedErr string
	cLimit := &MailConsumer{
		endpoint:  epLimit.(*MailEndpoint),
		tlsConfig: cliTLS,
		stopChan:  make(chan struct{}),
		changes:   make(chan struct{}, 1),
		processor: gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			u, _ := ex.GetIn().GetHeader("CamelMailFailedUID")
			e, _ := ex.GetIn().GetHeader("CamelMailError")
			failedUID, _ = u.(string)
			failedErr, _ = e.(string)
			return nil
		}),
	}
	mu.Lock()
	mode = "oversized_stream"
	mu.Unlock()
	cLimit.poll(context.Background())
	assert.Equal(t, "21", failedUID)
	assert.Contains(t, failedErr, "body read error")
}
