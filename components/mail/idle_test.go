package mail

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
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
