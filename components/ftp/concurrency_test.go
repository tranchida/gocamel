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
	gocamel "gitlab.com/tranchida/gocamel"
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
