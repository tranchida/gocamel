package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

// TestPop3Consumer_StopUnblocksStalledHandshake verifies that Stop closes the
// transport during connect, so a POP3 server that accepts but never sends its
// greeting cannot block shutdown forever.
func TestPop3Consumer_StopUnblocksStalledHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}

	accepted := make(chan struct{})
	release := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-release
	}()
	defer func() {
		close(release)
		<-serverDone
	}()

	c := NewMailComponent()
	ep, err := c.CreateEndpoint(fmt.Sprintf(
		"pop3://%s:%s/inbox?connectionTimeout=10000", host, port))
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not connect to the fake POP3 server")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- consumer.Stop() }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on a POP3 server that never sent its greeting")
	}
}

func TestPop3Consumer_ParsesMultipartAndHTMLAndPlainWithColonsAndEnforcesMaxSizeAndQuitsBeforeClose(t *testing.T) {
	multipartEmail := "From: alice@example.com\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: Multipart POP3\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"mix1\"\r\n" +
		"\r\n" +
		"--mix1\r\n" +
		"Content-Type: multipart/alternative; boundary=\"alt1\"\r\n" +
		"\r\n" +
		"--alt1\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Plain part\r\n" +
		"--alt1\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<p>HTML part</p>\r\n" +
		"--alt1--\r\n" +
		"--mix1\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"doc.txt\"\r\n" +
		"\r\n" +
		"attach-bytes\r\n" +
		"--mix1--\r\n"

	colonPlainEmail := "From: carol@example.com\r\n" +
		"To: bob@example.com\r\n" +
		"Subject: Colon body\r\n" +
		"Date: Tue, 03 Jan 2006 10:00:00 +0000\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Note: hello\r\n" +
		"second line\r\n"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var mu sync.Mutex
	var cmds []string
	var retr3Called bool
	var quitBeforeClose bool

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprintf(c, "+OK POP3 ready\r\n")
				sawQuit := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						if sawQuit {
							mu.Lock()
							quitBeforeClose = true
							mu.Unlock()
						}
						return
					}
					cmd := strings.TrimSpace(line)
					upper := strings.ToUpper(cmd)
					mu.Lock()
					cmds = append(cmds, upper)
					mu.Unlock()

					switch {
					case strings.HasPrefix(upper, "USER "), strings.HasPrefix(upper, "PASS "), upper == "NOOP":
						fmt.Fprintf(c, "+OK\r\n")
					case upper == "STAT":
						fmt.Fprintf(c, "+OK 3 %d\r\n", len(multipartEmail)+len(colonPlainEmail)+5000)
					case upper == "LIST":
						fmt.Fprintf(c, "+OK 3 messages\r\n1 %d\r\n2 %d\r\n3 5000\r\n.\r\n",
							len(multipartEmail), len(colonPlainEmail))
					case strings.HasPrefix(upper, "RETR "):
						idStr := strings.TrimSpace(strings.TrimPrefix(upper, "RETR "))
						id, _ := strconv.Atoi(idStr)
						switch id {
						case 1:
							fmt.Fprintf(c, "+OK %d octets\r\n%s.\r\n", len(multipartEmail), multipartEmail)
						case 2:
							fmt.Fprintf(c, "+OK %d octets\r\n%s.\r\n", len(colonPlainEmail), colonPlainEmail)
						case 3:
							mu.Lock()
							retr3Called = true
							mu.Unlock()
							fmt.Fprintf(c, "-ERR should not retrieve oversized\r\n")
						default:
							fmt.Fprintf(c, "-ERR no such message\r\n")
						}
					case strings.HasPrefix(upper, "DELE "):
						fmt.Fprintf(c, "+OK deleted\r\n")
					case upper == "QUIT":
						sawQuit = true
						mu.Lock()
						quitBeforeClose = true
						mu.Unlock()
						fmt.Fprintf(c, "+OK bye\r\n")
						return
					default:
						fmt.Fprintf(c, "-ERR unknown\r\n")
					}
				}
			}(conn)
		}
	}()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	comp := NewMailComponent()
	ep, err := comp.CreateEndpoint(fmt.Sprintf(
		"pop3://%s:%s?username=u&password=p&delete=true&maxMessageSize=2048&pollDelay=5000&connectionTimeout=2000",
		host, port))
	require.NoError(t, err)

	type captured struct {
		body     string
		bodyHTML string
		attach   map[string][]byte
		date     string
	}
	var gotMu sync.Mutex
	var got []captured

	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		var bodyStr string
		switch b := ex.GetIn().GetBody().(type) {
		case []byte:
			bodyStr = string(b)
		case string:
			bodyStr = b
		}
		var htmlStr string
		if hv, ok := ex.GetProperty("CamelMailBodyHTML"); ok {
			if hb, ok := hv.([]byte); ok {
				htmlStr = string(hb)
			}
		}
		att := make(map[string][]byte)
		for k, v := range ex.GetIn().GetHeaders() {
			if strings.HasPrefix(k, MailAttachmentPrefix+"_") {
				if vb, ok := v.([]byte); ok {
					att[strings.TrimPrefix(k, MailAttachmentPrefix+"_")] = vb
				}
			}
		}
		dateVal, _ := ex.GetIn().GetHeader(MailDate)
		dateStr, _ := dateVal.(string)

		gotMu.Lock()
		got = append(got, captured{body: bodyStr, bodyHTML: htmlStr, attach: att, date: dateStr})
		gotMu.Unlock()
		return nil
	}))
	require.NoError(t, err)

	require.NoError(t, consumer.Start(context.Background()))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return quitBeforeClose
	}, 3*time.Second, 20*time.Millisecond, "expected DELE + QUIT before socket close")

	require.NoError(t, consumer.Stop())

	gotMu.Lock()
	require.Len(t, got, 2)
	gotMu.Unlock()

	var bySubjectBody = make(map[string]captured)
	for _, g := range got {
		if strings.Contains(g.body, "Plain part") {
			bySubjectBody["multipart"] = g
		} else {
			bySubjectBody["colon"] = g
		}
	}

	mp, ok := bySubjectBody["multipart"]
	require.True(t, ok, "multipart email should be parsed")
	assert.Contains(t, mp.body, "Plain part")
	assert.Contains(t, mp.bodyHTML, "<p>HTML part</p>")
	require.Contains(t, mp.attach, "doc.txt")
	assert.Equal(t, "attach-bytes", strings.TrimSpace(string(mp.attach["doc.txt"])))
	parsedDate, err := time.Parse(time.RFC3339, mp.date)
	require.NoError(t, err)
	assert.Equal(t, 2006, parsedDate.Year())

	cp, ok := bySubjectBody["colon"]
	require.True(t, ok, "colon plain email should be parsed")
	assert.Contains(t, cp.body, "Note: hello")
	assert.Contains(t, cp.body, "second line")

	mu.Lock()
	assert.False(t, retr3Called, "oversized LIST entry must prevent RETR")
	assert.Contains(t, cmds, "DELE 1")
	assert.Contains(t, cmds, "DELE 2")
	// Verify QUIT came after DELE.
	lastDeleIdx := -1
	quitIdx := -1
	for i, c := range cmds {
		if strings.HasPrefix(c, "DELE ") {
			lastDeleIdx = i
		}
		if c == "QUIT" {
			quitIdx = i
		}
	}
	assert.Greater(t, quitIdx, lastDeleIdx, "QUIT must execute after DELE")
	mu.Unlock()

	// Verify command deadlines abort stalled STAT responses.
	stallLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = stallLn.Close() })
	go func() {
		conn, err := stallLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		fmt.Fprintf(conn, "+OK POP3 ready\r\n")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			u := strings.ToUpper(strings.TrimSpace(line))
			if strings.HasPrefix(u, "USER ") || strings.HasPrefix(u, "PASS ") || u == "NOOP" {
				fmt.Fprintf(conn, "+OK\r\n")
			} else if u == "STAT" {
				// Stall without replying so deadline must fire.
				time.Sleep(2 * time.Second)
				return
			}
		}
	}()
	sHost, sPort, err := net.SplitHostPort(stallLn.Addr().String())
	require.NoError(t, err)
	sEp, err := comp.CreateEndpoint(fmt.Sprintf("pop3://%s:%s?username=u&password=p&connectionTimeout=150", sHost, sPort))
	require.NoError(t, err)
	sCons, err := sEp.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	pop3Cons := sCons.(*Pop3Consumer)
	start := time.Now()
	pop3Cons.poll(context.Background())
	assert.Less(t, time.Since(start), 1*time.Second, "command deadline must abort stalled STAT")
	pop3Cons.disconnect()
}

func TestPop3Consumer_BoundedConnRejectsLyingLISTAndKeepAliveAndHandleFailed(t *testing.T) {
	// 1. Lying LIST server: claims Size=10 in LIST, then streams 32KB (> maxMessageSize=512).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	var serverBytesWritten int
	var srvMu sync.Mutex
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprintf(c, "+OK POP3 ready\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					u := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(u, "USER "), strings.HasPrefix(u, "PASS "), u == "NOOP":
						fmt.Fprintf(c, "+OK\r\n")
					case u == "STAT":
						fmt.Fprintf(c, "+OK 1 10\r\n")
					case u == "LIST":
						fmt.Fprintf(c, "+OK 1 messages\r\n1 10\r\n.\r\n")
					case strings.HasPrefix(u, "RETR "):
						fmt.Fprintf(c, "+OK 10 octets\r\nFrom: a@b.com\r\n\r\n")
						chunk := strings.Repeat("X", 1024)
						for i := 0; i < 64; i++ {
							n, werr := fmt.Fprint(c, chunk)
							srvMu.Lock()
							serverBytesWritten += n
							srvMu.Unlock()
							if werr != nil {
								return
							}
						}
						fmt.Fprintf(c, "\r\n.\r\n")
					case u == "QUIT":
						fmt.Fprintf(c, "+OK bye\r\n")
						return
					}
				}
			}(conn)
		}
	}()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	comp := NewMailComponent()
	ep, err := comp.CreateEndpoint(fmt.Sprintf(
		"pop3://%s:%s?username=u&password=p&maxMessageSize=512&handleFailedMessage=true&connectionTimeout=1000",
		host, port))
	require.NoError(t, err)

	var failedErr string
	var failedID string
	var capMu sync.Mutex
	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		v, _ := ex.GetIn().GetHeader("CamelMailError")
		idV, _ := ex.GetIn().GetHeader("CamelMailFailedMsgID")
		capMu.Lock()
		failedErr, _ = v.(string)
		failedID, _ = idV.(string)
		capMu.Unlock()
		return nil
	}))
	require.NoError(t, err)

	pop3Cons := cons.(*Pop3Consumer)
	pop3Cons.poll(context.Background())
	pop3Cons.disconnect()

	capMu.Lock()
	assert.Equal(t, "1", failedID)
	assert.Contains(t, failedErr, "maxMessageSize")
	capMu.Unlock()

	// 2. Keep-alive path: disconnect=false with 0 messages reuses connection across polls via NOOP.
	keepLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = keepLn.Close() })

	var acceptCount int
	var noopCount int
	var kMu sync.Mutex
	go func() {
		for {
			conn, err := keepLn.Accept()
			if err != nil {
				return
			}
			kMu.Lock()
			acceptCount++
			kMu.Unlock()
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprintf(c, "+OK POP3 ready\r\n")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					u := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(u, "USER "), strings.HasPrefix(u, "PASS "):
						fmt.Fprintf(c, "+OK\r\n")
					case u == "NOOP":
						kMu.Lock()
						noopCount++
						kMu.Unlock()
						fmt.Fprintf(c, "+OK\r\n")
					case u == "STAT":
						fmt.Fprintf(c, "+OK 0 0\r\n")
					case u == "QUIT":
						fmt.Fprintf(c, "+OK bye\r\n")
						return
					}
				}
			}(conn)
		}
	}()

	kHost, kPort, err := net.SplitHostPort(keepLn.Addr().String())
	require.NoError(t, err)
	kEp, err := comp.CreateEndpoint(fmt.Sprintf(
		"pop3://%s:%s?username=u&password=p&disconnect=false&connectionTimeout=1000", kHost, kPort))
	require.NoError(t, err)
	kCons, err := kEp.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	kPop3 := kCons.(*Pop3Consumer)
	kPop3.poll(context.Background())
	kPop3.poll(context.Background())
	kPop3.disconnect()

	kMu.Lock()
	assert.Equal(t, 1, acceptCount, "keep-alive must reuse the same TCP connection across polls")
	assert.GreaterOrEqual(t, noopCount, 2, "second poll must verify connection via NOOP")
	kMu.Unlock()

	// 3. Concurrent Start/Stop on Pop3Consumer is race-free.
	raceEp, err := comp.CreateEndpoint("pop3://127.0.0.1:1?connectionTimeout=50&pollDelay=20&skipFailedMessage=true")
	require.NoError(t, err)
	raceCons, err := raceEp.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = raceCons.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = raceCons.Stop()
		}()
		wg.Wait()
		_ = raceCons.Stop()
	}
}
