package ftp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

// Exercise the FTP control/data protocol, including connection reuse, without
// relying on a remote FTP service.
func TestConcurrentTransfersShareConnectionSafely(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	files := make(map[string]string)
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "220 Test FTP ready\r\n")
		reader := bufio.NewReader(conn)
		var data net.Listener
		defer func() {
			if data != nil {
				data.Close()
			}
		}()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverDone <- err
				return
			}
			command, argument, _ := strings.Cut(strings.TrimSpace(line), " ")
			switch command {
			case "USER":
				fmt.Fprint(conn, "331 Password required\r\n")
			case "PASS":
				fmt.Fprint(conn, "230 Logged in\r\n")
			case "FEAT":
				fmt.Fprint(conn, "211 End\r\n")
			case "TYPE", "NOOP", "OPTS":
				fmt.Fprint(conn, "200 OK\r\n")
			case "EPSV":
				data, err = net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					serverDone <- err
					return
				}
				_ = data.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
				fmt.Fprintf(conn, "229 Entering Extended Passive Mode (|||%d|)\r\n", data.Addr().(*net.TCPAddr).Port)
			case "STOR":
				fmt.Fprint(conn, "150 Opening data connection\r\n")
				dc, err := data.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				_ = dc.SetDeadline(time.Now().Add(5 * time.Second))
				body, err := io.ReadAll(dc)
				dc.Close()
				data.Close()
				data = nil
				if err != nil {
					serverDone <- err
					return
				}
				files[argument] = string(body)
				fmt.Fprint(conn, "226 Transfer complete\r\n")
			case "QUIT":
				fmt.Fprint(conn, "221 Goodbye\r\n")
				serverDone <- nil
				return
			default:
				fmt.Fprint(conn, "502 Unsupported command\r\n")
			}
		}
	}()
	ep, err := NewFTPComponent().CreateEndpoint("ftp://" + listener.Addr().String() + "/?connectTimeout=1s")
	require.NoError(t, err)
	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	defer producer.Stop()
	const count = 12
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody(fmt.Sprintf("body-%d", i))
			ex.GetIn().SetHeader(gocamel.CamelFileName, fmt.Sprintf("file-%d", i))
			if err := producer.Send(ex); err != nil {
				t.Errorf("transfer %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	require.NoError(t, producer.Stop())
	require.NoError(t, <-serverDone)
	for i := range count {
		assert.Equal(t, fmt.Sprintf("body-%d", i), files[fmt.Sprintf("file-%d", i)])
	}
}

func TestFTPEndpoint_HandshakeRespectsConnectTimeout(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sendGreeting bool
	}{
		{name: "stalled_before_greeting", sendGreeting: false},
		{name: "stalled_during_login", sendGreeting: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			accepted := make(chan struct{})
			release := make(chan struct{})
			var acceptOnce sync.Once
			var serverConn net.Conn
			var connMu sync.Mutex
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				c, err := ln.Accept()
				if err != nil {
					return
				}
				connMu.Lock()
				serverConn = c
				connMu.Unlock()
				if tc.sendGreeting {
					_, _ = fmt.Fprint(c, "220 Fake FTP\r\n")
				}
				acceptOnce.Do(func() { close(accepted) })
				<-release
				_ = c.Close()
			}()
			t.Cleanup(func() {
				close(release)
				_ = ln.Close()
				connMu.Lock()
				if serverConn != nil {
					_ = serverConn.Close()
				}
				connMu.Unlock()
				select {
				case <-serverDone:
				case <-time.After(time.Second):
				}
			})

			ep, err := NewFTPComponent().CreateEndpoint(fmt.Sprintf("ftp://%s/out/file.txt?connectTimeout=100ms", ln.Addr().String()))
			require.NoError(t, err)
			prod, err := ep.CreateProducer()
			require.NoError(t, err)

			sendErr := make(chan error, 1)
			go func() {
				ex := gocamel.NewExchange(context.Background())
				ex.SetBody("hello")
				sendErr <- prod.Send(ex)
			}()

			select {
			case <-accepted:
			case <-time.After(2 * time.Second):
				t.Fatal("producer did not connect to stalled listener")
			}

			stopDone := make(chan error, 1)
			go func() {
				stopDone <- prod.Stop()
			}()

			select {
			case err := <-sendErr:
				require.Error(t, err, "expected Send to fail on stalled handshake within connectTimeout")
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("Send hung beyond connectTimeout on stalled handshake")
			}

			select {
			case err := <-stopDone:
				require.NoError(t, err)
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("FTPProducer.Stop deadlocked waiting on stalled handshake")
			}
		})
	}
}

func TestFTPConsumer_SerializedStartStopAndNoPostStopReconnect(t *testing.T) {
	c := NewFTPComponent()

	// 1. Concurrent Start/Stop against a live fake server under -race.
	t.Run("concurrent_start_stop_race_free", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/in?initialDelay=0s&delay=10ms", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)

		var wg sync.WaitGroup
		for range 8 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = consumer.Start(context.Background())
			}()
			go func() {
				defer wg.Done()
				_ = consumer.Stop()
			}()
		}
		wg.Wait()
		require.NoError(t, consumer.Stop())

		fc := consumer.(*FTPConsumer)
		fc.mu.Lock()
		assert.False(t, fc.started)
		assert.False(t, fc.stopping)
		assert.Empty(t, fc.conns)
		fc.mu.Unlock()

		// Consumer can still be cleanly restarted and stopped after concurrent churn.
		require.NoError(t, consumer.Start(context.Background()))
		require.NoError(t, consumer.Stop())
	})

	// 1b. When a consumer is stopping, stopped, or has a canceled context,
	// getConn must not dial a replacement connection.
	t.Run("no_reconnect_when_stopping_or_canceled", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addDir("in")
		ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/in?initialDelay=0s&delay=5s", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))

		require.Eventually(t, func() bool {
			return len(srv.listed()) >= 1
		}, 2*time.Second, 15*time.Millisecond)

		require.NoError(t, consumer.Stop())
		connsAfterStop := srv.connCount.Load()

		fc := consumer.(*FTPConsumer)

		// Stopped consumer must refuse to dial.
		_, connErr := fc.getConn(context.Background())
		require.Error(t, connErr)
		assert.Contains(t, connErr.Error(), "stopped")

		// Stopping consumer must refuse to dial even if started=true.
		fc.mu.Lock()
		fc.started = true
		fc.stopping = true
		fc.mu.Unlock()
		_, stoppingErr := fc.getConn(context.Background())
		require.Error(t, stoppingErr)
		assert.Contains(t, stoppingErr.Error(), "stopped")

		// Canceled context must refuse to dial even if started=true.
		fc.mu.Lock()
		fc.stopping = false
		fc.mu.Unlock()
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		_, cancelErr := fc.getConn(canceledCtx)
		require.Error(t, cancelErr)

		fc.mu.Lock()
		fc.started = false
		fc.mu.Unlock()

		assert.Equal(t, connsAfterStop, srv.connCount.Load(), "must not dial after stopping/cancel")
	})

	// 2. Stop aborts stalled LIST data connection promptly and does not reconnect.
	t.Run("stop_aborts_stalled_list_data_socket", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addDir("in")
		srv.mu.Lock()
		srv.stallList = true
		srv.mu.Unlock()

		ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/in?initialDelay=0s&delay=20ms&connectTimeout=5s", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))

		select {
		case <-srv.dataStalled:
		case <-time.After(2 * time.Second):
			t.Fatal("consumer never reached stalled LIST data connection")
		}

		connsBeforeStop := srv.connCount.Load()
		stopDone := make(chan error, 1)
		go func() {
			stopDone <- consumer.Stop()
		}()

		select {
		case err := <-stopDone:
			require.NoError(t, err)
		case <-time.After(1500 * time.Millisecond):
			t.Fatal("Stop deadlocked on stalled LIST data socket")
		}

		assert.Never(t, func() bool {
			return srv.connCount.Load() > connsBeforeStop
		}, 120*time.Millisecond, 15*time.Millisecond, "must not reconnect after Stop")
	})

	// 3. Stop aborts stalled RETR data connection promptly and does not reconnect.
	t.Run("stop_aborts_stalled_retr_data_socket", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("in/slow.txt", "payload")
		srv.mu.Lock()
		srv.stallRetr = true
		srv.mu.Unlock()

		ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/in?initialDelay=0s&delay=20ms&connectTimeout=5s", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))

		select {
		case <-srv.dataStalled:
		case <-time.After(2 * time.Second):
			t.Fatal("consumer never reached stalled RETR data connection")
		}

		connsBeforeStop := srv.connCount.Load()
		stopDone := make(chan error, 1)
		go func() {
			stopDone <- consumer.Stop()
		}()

		select {
		case err := <-stopDone:
			require.NoError(t, err)
		case <-time.After(1500 * time.Millisecond):
			t.Fatal("Stop deadlocked on stalled RETR data socket")
		}

		assert.Never(t, func() bool {
			return srv.connCount.Load() > connsBeforeStop
		}, 120*time.Millisecond, 15*time.Millisecond, "must not reconnect after Stop")
	})

	// 4. Zero/negative Delay guard avoids time.NewTicker panic even with v0.20.0 options.
	t.Run("non_positive_delay_does_not_panic", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/in?initialDelay=0s", srv.addr()))
		require.NoError(t, err)
		consumer := &FTPConsumer{
			endpoint:  ep.(*FTPEndpoint),
			processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
			opts:      gocamel.PollingOptions{Delay: 0, InitialDelay: -time.Second},
		}
		require.NoError(t, consumer.Start(context.Background()))
		assert.Eventually(t, func() bool {
			return len(srv.listed()) > 0
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())
	})
}
