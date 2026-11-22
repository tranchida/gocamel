package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
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

func TestMailProducer_RejectsESMTPInjectionAndNoDuplicateOnQuitFailAndCancelsOnCtx(t *testing.T) {
	// 1. ESMTP injection attempts in To/From/Cc/Bcc must be rejected before dialing.
	comp := NewMailComponent()
	for _, badTo := range []string{
		"a@b.com>\r\nDATA",
		"a@b.com> NOTIFY=SUCCESS",
		"not-an-email",
	} {
		ep, err := comp.CreateEndpoint("smtp://127.0.0.1:25?from=src@example.com&allowHeaderOverride=true")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetHeader("To", badTo)
		ex.SetBody("hi")
		err = prod.Send(ex)
		require.Error(t, err, "expected rejection for To=%q", badTo)
	}

	// 2. Server drops connection right after 250 OK on DATA (QUIT fails) -> must NOT retry!
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var deliveries atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprintf(c, "220 stub.example.com ESMTP\r\n")
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" {
							inData = false
							deliveries.Add(1)
							fmt.Fprintf(c, "250 OK queued\r\n")
							// Drop connection immediately before QUIT can succeed.
							return
						}
						continue
					}
					cmd := strings.ToUpper(strings.TrimRight(line, "\r\n"))
					switch {
					case strings.HasPrefix(cmd, "EHLO"):
						fmt.Fprintf(c, "250-stub.example.com\r\n250 8BITMIME\r\n")
					case cmd == "DATA":
						inData = true
						fmt.Fprintf(c, "354 Go ahead\r\n")
					default:
						fmt.Fprintf(c, "250 OK\r\n")
					}
				}
			}(conn)
		}
	}()

	uri := fmt.Sprintf("smtp://%s?from=src@example.com&to=dest@example.com&connectionTimeout=200", ln.Addr().String())
	err = sendWithin(t, context.Background(), uri, 5*time.Second)
	require.NoError(t, err, "Send must treat post-250 QUIT failure as best-effort")
	assert.Equal(t, int32(1), deliveries.Load(), "must deliver exactly once without retrying after 250 OK")

	// 3. Cancelling ctx while server is stalled mid-protocol must close conn immediately without retrying.
	stalledLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = stalledLn.Close() })

	accepted := make(chan struct{})
	connClosed := make(chan struct{})
	go func() {
		conn, err := stalledLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		// Wait for client to close conn (never send greeting).
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		close(connClosed)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	epCancel, err := comp.CreateEndpoint(fmt.Sprintf("smtp://%s?from=src@example.com&to=dest@example.com&connectionTimeout=10000", stalledLn.Addr().String()))
	require.NoError(t, err)
	prodCancel, err := epCancel.CreateProducer()
	require.NoError(t, err)

	exCancel := gocamel.NewExchange(ctx)
	exCancel.SetBody("hello")
	sendDone := make(chan error, 1)
	start := time.Now()
	go func() { sendDone <- prodCancel.Send(exCancel) }()

	<-accepted
	cancel()

	select {
	case err := <-sendDone:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), 1*time.Second, "cancelling ctx must abort Send immediately without retrying")
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not abort promptly when ctx was cancelled")
	}

	select {
	case <-connClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling ctx did not close the underlying SMTP conn")
	}
}

func newTestLocalhostTLS(t *testing.T) (serverTLS *tls.Config, clientTLS *tls.Config) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	tlsCert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}, &tls.Config{
		RootCAs:    pool,
		ServerName: "127.0.0.1",
		MinVersion: tls.VersionTLS12,
	}
}

func TestMailProducer_BuildMessageAndValidationAndTLS(t *testing.T) {
	comp := NewMailComponent()

	// 1. Validation errors before dialing.
	epMissingFrom, err := comp.CreateEndpoint("smtp://127.0.0.1:25?to=dest@example.com")
	require.NoError(t, err)
	epMissingFrom.(*MailEndpoint).from = ""
	prodMissingFrom, err := epMissingFrom.CreateProducer()
	require.NoError(t, err)
	require.Error(t, prodMissingFrom.Send(gocamel.NewExchange(context.Background())))

	epNoRcpt, err := comp.CreateEndpoint("smtp://127.0.0.1:25?from=src@example.com")
	require.NoError(t, err)
	prodNoRcpt, err := epNoRcpt.CreateProducer()
	require.NoError(t, err)
	require.Error(t, prodNoRcpt.Send(gocamel.NewExchange(context.Background())))

	// 2. buildMessage: RFC1123Z Date, entropy in Message-Id/boundary, no duplicate charset,
	// sanitized Subject, mime.FormatMediaType Content-Disposition with sanitized filename.
	epBuild, err := comp.CreateEndpoint("smtp://127.0.0.1:25?from=src@example.com&to=dest@example.com&cc=cc@example.com")
	require.NoError(t, err)
	prodBuild := &MailProducer{endpoint: epBuild.(*MailEndpoint)}

	ex := gocamel.NewExchange(context.Background())
	ex.SetHeader(MailAttachmentPrefix+"_../escape.txt", []byte("secret"))
	msg1, err := prodBuild.buildMessage(
		"src@example.com",
		"dest@example.com",
		"cc@example.com",
		"Hello\r\nBcc: evil@example.com",
		"text/plain; charset=UTF-8",
		[]byte("Body text"),
		ex,
	)
	require.NoError(t, err)
	s1 := string(msg1)
	assert.Contains(t, s1, "Cc: cc@example.com\r\n")
	assert.Contains(t, s1, "Subject: HelloBcc: evil@example.com\r\n")
	assert.NotContains(t, s1, "charset=UTF-8; charset=UTF-8")
	assert.Contains(t, s1, "Content-Type: text/plain; charset=UTF-8\r\n")
	assert.Contains(t, s1, "Content-Disposition: attachment; filename=escape.txt\r\n")
	assert.NotContains(t, s1, "../escape.txt")

	// Extract Date header and verify RFC1123Z numeric offset format.
	for _, line := range strings.Split(s1, "\r\n") {
		if strings.HasPrefix(line, "Date: ") {
			_, parseErr := time.Parse(time.RFC1123Z, strings.TrimPrefix(line, "Date: "))
			assert.NoError(t, parseErr, "Date must be formatted as RFC1123Z: %s", line)
		}
	}

	// Verify crypto/rand entropy produces distinct Message-Ids and boundaries.
	msg2, err := prodBuild.buildMessage("src@example.com", "dest@example.com", "", "Subj", "text/plain", []byte("b"), ex)
	require.NoError(t, err)
	assert.NotEqual(t, string(msg1), string(msg2))

	// 3. SMTPS + AUTH + STARTTLS with ephemeral localhost TLS cert.
	srvTLS, cliTLS := newTestLocalhostTLS(t)

	serveSMTP := func(t *testing.T, useImplicitTLS bool) string {
		t.Helper()
		var ln net.Listener
		var err error
		if useImplicitTLS {
			ln, err = tls.Listen("tcp", "127.0.0.1:0", srvTLS)
		} else {
			ln, err = net.Listen("tcp", "127.0.0.1:0")
		}
		require.NoError(t, err)
		t.Cleanup(func() { _ = ln.Close() })

		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			r := bufio.NewReader(conn)
			fmt.Fprintf(conn, "220 127.0.0.1 ESMTP\r\n")
			upgraded := useImplicitTLS
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				cmd := strings.ToUpper(strings.TrimRight(line, "\r\n"))
				switch {
				case strings.HasPrefix(cmd, "EHLO"):
					if upgraded {
						fmt.Fprintf(conn, "250-127.0.0.1\r\n250 AUTH PLAIN\r\n")
					} else {
						fmt.Fprintf(conn, "250-127.0.0.1\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
					}
				case cmd == "STARTTLS":
					fmt.Fprintf(conn, "220 Ready to start TLS\r\n")
					tc := tls.Server(conn, srvTLS)
					if err := tc.Handshake(); err != nil {
						return
					}
					conn = tc
					r = bufio.NewReader(conn)
					upgraded = true
				case strings.HasPrefix(cmd, "AUTH "):
					fmt.Fprintf(conn, "235 Authentication succeeded\r\n")
				case cmd == "DATA":
					fmt.Fprintf(conn, "354 Go ahead\r\n")
					for {
						dline, derr := r.ReadString('\n')
						if derr != nil {
							return
						}
						if dline == ".\r\n" {
							fmt.Fprintf(conn, "250 OK\r\n")
							break
						}
					}
				case cmd == "QUIT":
					fmt.Fprintf(conn, "221 Bye\r\n")
					return
				default:
					fmt.Fprintf(conn, "250 OK\r\n")
				}
			}
		}()
		return ln.Addr().String()
	}

	smtpsAddr := serveSMTP(t, true)
	epSMTPS, err := comp.CreateEndpoint(fmt.Sprintf("smtps://%s?from=src@example.com&to=dest@example.com&username=u&password=p&connectionTimeout=2000", smtpsAddr))
	require.NoError(t, err)
	prodSMTPS := &MailProducer{endpoint: epSMTPS.(*MailEndpoint), tlsConfig: cliTLS}
	exSMTPS := gocamel.NewExchange(context.Background())
	exSMTPS.SetBody("over smtps")
	require.NoError(t, prodSMTPS.Send(exSMTPS))

	startTLSAddr := serveSMTP(t, false)
	epStartTLS, err := comp.CreateEndpoint(fmt.Sprintf("smtp://%s?from=src@example.com&to=dest@example.com&username=u&password=p&requireStartTLS=true&connectionTimeout=2000", startTLSAddr))
	require.NoError(t, err)
	prodStartTLS := &MailProducer{endpoint: epStartTLS.(*MailEndpoint), tlsConfig: cliTLS}
	exStartTLS := gocamel.NewExchange(context.Background())
	exStartTLS.SetBody("over starttls")
	require.NoError(t, prodStartTLS.Send(exStartTLS))
}
