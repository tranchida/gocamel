package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
)

// startStalledServer accepts TCP connections and never answers, like an SMTP
// server stuck before its greeting or a blackholed TLS handshake.
func startStalledServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return ln.Addr().String()
}

// sendWithin sends one message through uri and fails the test if Send does not
// return within the deadline.
func sendWithin(t *testing.T, ctx context.Context, uri string, deadline time.Duration) error {
	t.Helper()
	endpoint, err := NewMailComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	producer, err := endpoint.CreateProducer()
	require.NoError(t, err)

	ex := gocamel.NewExchange(ctx)
	ex.SetBody("hello")

	result := make(chan error, 1)
	go func() { result <- producer.Send(ex) }()
	select {
	case err := <-result:
		return err
	case <-time.After(deadline):
		t.Fatalf("Send did not return within %s", deadline)
		return nil
	}
}

func TestMailProducer_StalledServerHonoursConnectionTimeout(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scheme string
		params string
	}{
		{"smtp", "smtp", ""},
		{"smtps", "smtps", ""},
		{"requireStartTLS", "smtp", "&requireStartTLS=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr := startStalledServer(t)
			// 5 attempts bounded by 100ms each, plus backoffs capped at 100ms.
			uri := fmt.Sprintf("%s://%s?from=src@example.com&to=dest@example.com&connectionTimeout=100%s", tc.scheme, addr, tc.params)
			err := sendWithin(t, context.Background(), uri, 5*time.Second)
			assert.Error(t, err)
		})
	}
}

// The default SMTP path (no STARTTLS advertised) still delivers the message.
func TestMailProducer_SendsOverPlainSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	commands := make(chan string, 32)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		fmt.Fprintf(conn, "220 stub.example.com ESMTP\r\n")
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprintf(conn, "250 OK queued\r\n")
				} else {
					commands <- "body:" + strings.TrimRight(line, "\r\n")
				}
				continue
			}
			cmd := strings.ToUpper(strings.TrimRight(line, "\r\n"))
			commands <- cmd
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				fmt.Fprintf(conn, "250-stub.example.com\r\n250 8BITMIME\r\n")
			case cmd == "DATA":
				inData = true
				fmt.Fprintf(conn, "354 Go ahead\r\n")
			case cmd == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()

	uri := fmt.Sprintf("smtp://%s?from=src@example.com&to=dest@example.com&connectionTimeout=2000", ln.Addr().String())
	require.NoError(t, sendWithin(t, context.Background(), uri, 5*time.Second))

	close(commands)
	var received []string
	for c := range commands {
		received = append(received, c)
	}
	assert.Contains(t, received, "MAIL FROM:<SRC@EXAMPLE.COM> BODY=8BITMIME")
	assert.Contains(t, received, "RCPT TO:<DEST@EXAMPLE.COM>")
	assert.Contains(t, received, "body:hello")
	assert.Contains(t, received, "QUIT")
}

func TestMailProducer_RetryBackoffIsCancellable(t *testing.T) {
	// A closed port: every attempt fails fast with a retryable error, and the
	// default connectionTimeout (30s) leaves the 1s, 2s, 4s, 8s backoffs intact.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	err = sendWithin(t, ctx, fmt.Sprintf("smtp://%s?from=src@example.com&to=dest@example.com", addr), 20*time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 900*time.Millisecond, "the backoff ignored the exchange context")
}
