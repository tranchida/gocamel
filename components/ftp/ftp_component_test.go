package ftp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

func TestFTPConsumer_StopWaitsForPollingWorker(t *testing.T) {
	consumer := &FTPConsumer{}
	consumer.wg.Add(1)
	stopped := make(chan struct{})
	go func() {
		_ = consumer.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned while polling worker was still running")
	case <-time.After(20 * time.Millisecond):
	}

	consumer.wg.Done()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after polling worker exited")
	}
}

func TestFTPConsumer_ReadLockOptions(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://example.com/incoming?readLock=rename&readLockTimeout=10s&readLockCheckInterval=1s")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}

	consumer, err := ep.CreateConsumer(nil)
	if err != nil {
		t.Fatalf("unexpected error creating consumer: %v", err)
	}

	ftpConsumer, ok := consumer.(*FTPConsumer)
	if !ok {
		t.Fatalf("expected *FTPConsumer, got %T", consumer)
	}

	if ftpConsumer.opts.ReadLock != "rename" {
		t.Errorf("expected readLock=rename, got %v", ftpConsumer.opts.ReadLock)
	}
	if ftpConsumer.opts.ReadLockTimeout != 10*time.Second {
		t.Errorf("expected readLockTimeout=10s, got %v", ftpConsumer.opts.ReadLockTimeout)
	}
	if ftpConsumer.opts.ReadLockCheckInterval != 1*time.Second {
		t.Errorf("expected readLockCheckInterval=1s, got %v", ftpConsumer.opts.ReadLockCheckInterval)
	}
}

func TestFTPEndpoint_RedactsCredentialsInURI(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://admin:topsecret@example.com/incoming?password=querysecret")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}
	got := ep.URI()
	if strings.Contains(got, "topsecret") || strings.Contains(got, "querysecret") {
		t.Errorf("expected credentials to be redacted in URI(), got %q", got)
	}
}

func TestFTPEndpoint_RejectsMoveAndMoveFailedTraversal(t *testing.T) {
	c := NewFTPComponent()
	for _, uri := range []string{
		"ftp://example.com/incoming?move=../outside",
		"ftp://example.com/incoming?move=a/../../outside",
		"ftp://example.com/incoming?moveFailed=../failed",
		"ftp://example.com/incoming?moveFailed=a/b/../../../failed",
	} {
		if _, err := c.CreateEndpoint(uri); err == nil {
			t.Errorf("expected CreateEndpoint(%q) to reject path traversal", uri)
		}
	}

	ep, err := c.CreateEndpoint("ftp://example.com/incoming?move=.done&moveFailed=.error")
	if err != nil {
		t.Fatalf("expected valid move/moveFailed to be accepted: %v", err)
	}
	if _, err := ep.CreateConsumer(nil); err != nil {
		t.Fatalf("expected CreateConsumer with valid move/moveFailed to succeed: %v", err)
	}
}

func TestFTPEndpoint_DefaultMaxMessageSize(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://example.com/incoming")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}
	if ep.(*FTPEndpoint).maxMessageSize != gocamel.DefaultMaxBodySize {
		t.Errorf("expected default maxMessageSize=%d, got %d", gocamel.DefaultMaxBodySize, ep.(*FTPEndpoint).maxMessageSize)
	}
}

func TestFTPProducer_SendAfterStopFailsWithoutDialing(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://127.0.0.1:1/file.txt?connectTimeout=100ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := prod.Stop(); err != nil {
		t.Fatalf("unexpected Stop error: %v", err)
	}
	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("data")
	err = prod.Send(ex)
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("expected stopped producer error, got %v", err)
	}
}

func TestFTPConsumer_ConcurrentStartStop(t *testing.T) {
	c := NewFTPComponent()
	ep, err := c.CreateEndpoint("ftp://127.0.0.1:1/incoming?initialDelay=5s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
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
	_ = consumer.Stop()
}

func TestFTPConsumer_StopUnblocksStalledHandshake(t *testing.T) {
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
		_ = ln.Close()
		select {
		case <-serverDone:
		case <-time.After(time.Second):
		}
	}()

	c := NewFTPComponent()
	ep, err := c.CreateEndpoint(fmt.Sprintf(
		"ftp://%s:%s/inbox?connectTimeout=10000&initialDelay=0s", host, port))
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
		t.Fatal("consumer did not connect to the fake server")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- consumer.Stop() }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on a server that never completed the handshake")
	}
}

type fakeFTPFile struct {
	content string
	modTime time.Time
}

type fakeFTPServer struct {
	listener     net.Listener
	localIP      string
	mu           sync.Mutex
	files        map[string]*fakeFTPFile
	dirs         map[string]bool
	cmds         []string
	listedDirs   []string
	mkdCalls     []string
	connCount    atomic.Int32
	sizeErrCode  int
	stallList    bool
	stallRetr    bool
	dataStalled  chan struct{}
	releaseStall chan struct{}
	stallOnce    sync.Once
	conns        map[net.Conn]struct{}
	closed       bool
	wg           sync.WaitGroup
}

func newFakeFTPServer(t *testing.T, network, address string) *fakeFTPServer {
	t.Helper()
	ln, err := net.Listen(network, address)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	srv := &fakeFTPServer{
		listener:     ln,
		localIP:      host,
		files:        make(map[string]*fakeFTPFile),
		dirs:         map[string]bool{".": true},
		dataStalled:  make(chan struct{}),
		releaseStall: make(chan struct{}),
		conns:        make(map[net.Conn]struct{}),
	}
	srv.wg.Add(1)
	go srv.acceptLoop()
	t.Cleanup(srv.close)
	return srv
}

func (s *fakeFTPServer) addr() string {
	return s.listener.Addr().String()
}

func (s *fakeFTPServer) addDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureDirsLocked(dir)
}

func (s *fakeFTPServer) ensureDirsLocked(dir string) {
	clean := path.Clean(strings.TrimPrefix(dir, "/"))
	if clean == "." || clean == "" {
		return
	}
	parts := strings.Split(clean, "/")
	cur := ""
	for _, p := range parts {
		if cur == "" {
			cur = p
		} else {
			cur += "/" + p
		}
		s.dirs[cur] = true
	}
}

func (s *fakeFTPServer) addFile(filePath, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clean := path.Clean(strings.TrimPrefix(filePath, "/"))
	if dir := path.Dir(clean); dir != "." {
		s.ensureDirsLocked(dir)
	}
	s.files[clean] = &fakeFTPFile{
		content: content,
		modTime: time.Now().UTC().Add(-time.Minute),
	}
}

func (s *fakeFTPServer) getFile(filePath string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clean := path.Clean(strings.TrimPrefix(filePath, "/"))
	f, ok := s.files[clean]
	if !ok {
		return "", false
	}
	return f.content, true
}

func (s *fakeFTPServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.cmds))
	copy(out, s.cmds)
	return out
}

func (s *fakeFTPServer) listed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.listedDirs))
	copy(out, s.listedDirs)
	return out
}

func (s *fakeFTPServer) mkds() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.mkdCalls))
	copy(out, s.mkdCalls)
	return out
}

func (s *fakeFTPServer) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.releaseStall)
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	_ = s.listener.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
}

func (s *fakeFTPServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.connCount.Add(1)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
				_ = c.Close()
			}()
			s.serveConn(c)
		}(conn)
	}
}

func (s *fakeFTPServer) serveConn(conn net.Conn) {
	_, _ = fmt.Fprint(conn, "220 Fake FTP ready\r\n")
	reader := bufio.NewReader(conn)
	var dataLn net.Listener
	defer func() {
		if dataLn != nil {
			_ = dataLn.Close()
		}
	}()
	var pendingRename string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(trimmed, " ")
		cmdUpper := strings.ToUpper(cmd)

		s.mu.Lock()
		s.cmds = append(s.cmds, trimmed)
		s.mu.Unlock()

		switch cmdUpper {
		case "USER":
			_, _ = fmt.Fprint(conn, "331 Password required\r\n")
		case "PASS":
			_, _ = fmt.Fprint(conn, "230 Logged in\r\n")
		case "FEAT":
			_, _ = fmt.Fprint(conn, "211-Features:\r\n MDTM\r\n SIZE\r\n UTF8\r\n211 End\r\n")
		case "TYPE", "NOOP", "OPTS":
			_, _ = fmt.Fprint(conn, "200 OK\r\n")
		case "EPSV":
			if dataLn != nil {
				_ = dataLn.Close()
			}
			listenAddr := net.JoinHostPort(s.localIP, "0")
			dataLn, err = net.Listen("tcp", listenAddr)
			if err != nil {
				_, _ = fmt.Fprint(conn, "425 Cannot open data connection\r\n")
				return
			}
			port := dataLn.Addr().(*net.TCPAddr).Port
			_, _ = fmt.Fprintf(conn, "229 Entering Extended Passive Mode (|||%d|)\r\n", port)
		case "PASV":
			if dataLn != nil {
				_ = dataLn.Close()
			}
			dataLn, err = net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				_, _ = fmt.Fprint(conn, "425 Cannot open data connection\r\n")
				return
			}
			port := dataLn.Addr().(*net.TCPAddr).Port
			_, _ = fmt.Fprintf(conn, "227 Entering Passive Mode (127,0,0,1,%d,%d)\r\n", port/256, port%256)
		case "SIZE":
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			errCode := s.sizeErrCode
			f, ok := s.files[norm]
			var size int
			if ok {
				size = len(f.content)
			}
			s.mu.Unlock()
			if errCode != 0 {
				_, _ = fmt.Fprintf(conn, "%d Simulated SIZE failure\r\n", errCode)
			} else if !ok {
				_, _ = fmt.Fprint(conn, "550 File not found\r\n")
			} else {
				_, _ = fmt.Fprintf(conn, "213 %d\r\n", size)
			}
		case "MDTM":
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			f, ok := s.files[norm]
			var ts string
			if ok {
				ts = f.modTime.UTC().Format("20060102150405")
			}
			s.mu.Unlock()
			if !ok {
				_, _ = fmt.Fprint(conn, "550 File not found\r\n")
			} else {
				_, _ = fmt.Fprintf(conn, "213 %s\r\n", ts)
			}
		case "LIST":
			if dataLn == nil {
				_, _ = fmt.Fprint(conn, "425 Use PASV or EPSV first\r\n")
				continue
			}
			dir := path.Clean(strings.TrimPrefix(arg, "/"))
			if arg == "" {
				dir = "."
			}
			s.mu.Lock()
			s.listedDirs = append(s.listedDirs, dir)
			dirExists := s.dirs[dir]
			stall := s.stallList
			var lines []string
			if dirExists {
				var names []string
				entries := make(map[string]string)
				for d := range s.dirs {
					if d == "." {
						continue
					}
					if path.Dir(d) == dir {
						name := path.Base(d)
						names = append(names, name)
						entries[name] = fmt.Sprintf("drwxr-xr-x 1 user group 0 %s %s\r\n", time.Now().UTC().Format("Jan 02 15:04"), name)
					}
				}
				for fp, f := range s.files {
					if path.Dir(fp) == dir {
						name := path.Base(fp)
						names = append(names, name)
						entries[name] = fmt.Sprintf("-rw-r--r-- 1 user group %d %s %s\r\n", len(f.content), f.modTime.UTC().Format("Jan 02 15:04"), name)
					}
				}
				sort.Strings(names)
				for _, n := range names {
					lines = append(lines, entries[n])
				}
			}
			s.mu.Unlock()

			if !dirExists {
				_ = dataLn.Close()
				dataLn = nil
				_, _ = fmt.Fprint(conn, "550 Directory not found\r\n")
				continue
			}
			_, _ = fmt.Fprint(conn, "150 Opening data connection for LIST\r\n")
			dc, err := dataLn.Accept()
			_ = dataLn.Close()
			dataLn = nil
			if err != nil {
				_, _ = fmt.Fprint(conn, "425 Data connection failed\r\n")
				continue
			}
			if stall {
				s.stallOnce.Do(func() { close(s.dataStalled) })
				<-s.releaseStall
				_ = dc.Close()
				return
			}
			for _, l := range lines {
				_, _ = io.WriteString(dc, l)
			}
			_ = dc.Close()
			_, _ = fmt.Fprint(conn, "226 Directory send OK\r\n")
		case "RETR":
			if dataLn == nil {
				_, _ = fmt.Fprint(conn, "425 Use PASV or EPSV first\r\n")
				continue
			}
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			f, ok := s.files[norm]
			var content string
			if ok {
				content = f.content
			}
			stall := s.stallRetr
			s.mu.Unlock()
			if !ok {
				_ = dataLn.Close()
				dataLn = nil
				_, _ = fmt.Fprint(conn, "550 File not found\r\n")
				continue
			}
			_, _ = fmt.Fprint(conn, "150 Opening data connection for RETR\r\n")
			dc, err := dataLn.Accept()
			_ = dataLn.Close()
			dataLn = nil
			if err != nil {
				_, _ = fmt.Fprint(conn, "425 Data connection failed\r\n")
				continue
			}
			if stall {
				s.stallOnce.Do(func() { close(s.dataStalled) })
				<-s.releaseStall
				_ = dc.Close()
				return
			}
			_, _ = io.WriteString(dc, content)
			_ = dc.Close()
			_, _ = fmt.Fprint(conn, "226 Transfer complete\r\n")
		case "STOR", "APPE":
			if dataLn == nil {
				_, _ = fmt.Fprint(conn, "425 Use PASV or EPSV first\r\n")
				continue
			}
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			_, _ = fmt.Fprint(conn, "150 Opening data connection\r\n")
			dc, err := dataLn.Accept()
			_ = dataLn.Close()
			dataLn = nil
			if err != nil {
				_, _ = fmt.Fprint(conn, "425 Data connection failed\r\n")
				continue
			}
			body, readErr := io.ReadAll(dc)
			_ = dc.Close()
			if readErr != nil {
				_, _ = fmt.Fprint(conn, "426 Connection closed\r\n")
				continue
			}
			s.mu.Lock()
			if dir := path.Dir(norm); dir != "." {
				s.ensureDirsLocked(dir)
			}
			if cmdUpper == "APPE" {
				if existing, ok := s.files[norm]; ok {
					existing.content += string(body)
					existing.modTime = time.Now().UTC()
				} else {
					s.files[norm] = &fakeFTPFile{content: string(body), modTime: time.Now().UTC()}
				}
			} else {
				s.files[norm] = &fakeFTPFile{content: string(body), modTime: time.Now().UTC()}
			}
			s.mu.Unlock()
			_, _ = fmt.Fprint(conn, "226 Transfer complete\r\n")
		case "MKD":
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			s.mkdCalls = append(s.mkdCalls, arg)
			exists := s.dirs[norm]
			parent := path.Dir(norm)
			parentOK := parent == "." || s.dirs[parent]
			if !exists && parentOK && norm != "." && norm != "" {
				s.dirs[norm] = true
			}
			s.mu.Unlock()
			if exists || !parentOK || norm == "." || norm == "" {
				_, _ = fmt.Fprint(conn, "550 Create directory failed\r\n")
			} else {
				_, _ = fmt.Fprintf(conn, "257 %q created\r\n", arg)
			}
		case "DELE":
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			_, ok := s.files[norm]
			if ok {
				delete(s.files, norm)
			}
			s.mu.Unlock()
			if !ok {
				_, _ = fmt.Fprint(conn, "550 File not found\r\n")
			} else {
				_, _ = fmt.Fprint(conn, "250 Deleted\r\n")
			}
		case "RNFR":
			norm := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			_, ok := s.files[norm]
			s.mu.Unlock()
			if !ok {
				pendingRename = ""
				_, _ = fmt.Fprint(conn, "550 File not found\r\n")
			} else {
				pendingRename = norm
				_, _ = fmt.Fprint(conn, "350 Ready for RNTO\r\n")
			}
		case "RNTO":
			normTo := path.Clean(strings.TrimPrefix(arg, "/"))
			s.mu.Lock()
			f, ok := s.files[pendingRename]
			parent := path.Dir(normTo)
			parentOK := parent == "." || s.dirs[parent]
			if ok && parentOK && pendingRename != "" {
				delete(s.files, pendingRename)
				s.files[normTo] = f
			}
			s.mu.Unlock()
			pendingRename = ""
			if !ok || !parentOK {
				_, _ = fmt.Fprint(conn, "550 Rename failed\r\n")
			} else {
				_, _ = fmt.Fprint(conn, "250 Renamed\r\n")
			}
		case "QUIT":
			_, _ = fmt.Fprint(conn, "221 Goodbye\r\n")
			return
		default:
			_, _ = fmt.Fprint(conn, "502 Command not implemented\r\n")
		}
	}
}

func TestFTPProducer_RejectsCRLFCommandInjection(t *testing.T) {
	srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
	srv.addFile("other.txt", "secret")

	c := NewFTPComponent()

	// 1. CamelFileName with CR/LF must fail BEFORE issuing any FTP commands.
	ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/inbox/", srv.addr()))
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = prod.Stop() })

	for _, badName := range []string{
		"a.txt\r\nDELE /other.txt",
		"a.txt\nDELE /other.txt",
		"a.txt\rDELE /other.txt",
		"a\x00.txt",
	} {
		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("payload")
		ex.GetIn().SetHeader(gocamel.CamelFileName, badName)
		err := prod.Send(ex)
		require.Error(t, err, "expected error for CamelFileName %q", badName)
	}
	assert.Empty(t, srv.commands(), "expected zero FTP commands issued for invalid CamelFileName")
	_, stillExists := srv.getFile("other.txt")
	assert.True(t, stillExists)

	// 2. URI path, credentials, move, and moveFailed with percent-encoded CR/LF must fail.
	badURIs := []string{
		fmt.Sprintf("ftp://%s/in%%0d%%0aDELE%%20/other.txt", srv.addr()),
		fmt.Sprintf("ftp://admin%%0d%%0aDELE%%20/other.txt:pass@%s/in", srv.addr()),
		fmt.Sprintf("ftp://admin:pass%%0d%%0aDELE%%20/other.txt@%s/in", srv.addr()),
		fmt.Sprintf("ftp://%s/in?username=admin%%0d%%0aDELE%%20x", srv.addr()),
		fmt.Sprintf("ftp://%s/in?password=secret%%0d%%0aDELE%%20x", srv.addr()),
		fmt.Sprintf("ftp://%s/in?move=.done%%0d%%0aDELE%%20x", srv.addr()),
		fmt.Sprintf("ftp://%s/in?moveFailed=.err%%0d%%0aDELE%%20x", srv.addr()),
	}
	for _, rawURI := range badURIs {
		_, err := c.CreateEndpoint(rawURI)
		require.Error(t, err, "expected CreateEndpoint to reject %q", rawURI)
		assert.NotContains(t, err.Error(), "secret\r\n", "password must not leak in error")
	}

	// 3. Direct connectWithRaw / Send / CreateConsumer defensive checks against CR/LF.
	badCredURL, err := gocamel.ParseURI(fmt.Sprintf("ftp://admin%%0d%%0aDELE%%20x:pass@%s/in", srv.addr()))
	require.NoError(t, err)
	directEP := &FTPEndpoint{url: badCredURL, connectTimeout: time.Second}
	_, err = directEP.connect()
	require.Error(t, err)

	badPassURL, err := gocamel.ParseURI(fmt.Sprintf("ftp://admin:pass%%0d%%0aDELE%%20x@%s/in", srv.addr()))
	require.NoError(t, err)
	directPassEP := &FTPEndpoint{url: badPassURL, connectTimeout: time.Second}
	_, err = directPassEP.connect()
	require.Error(t, err)

	badMoveURL, err := gocamel.ParseURI(fmt.Sprintf("ftp://%s/in?move=.done%%0d%%0aDELE%%20x", srv.addr()))
	require.NoError(t, err)
	badMoveEP := &FTPEndpoint{url: badMoveURL, connectTimeout: time.Second}
	_, err = badMoveEP.CreateConsumer(nil)
	require.Error(t, err)

	assert.Empty(t, srv.commands(), "expected zero FTP commands issued for rejected URIs and credentials")
}

func TestFTPProducer_NativeAppendAndIPv6(t *testing.T) {
	srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
	srv.addFile("append.txt", "hello ")
	srv.addFile("existing.txt", "keep")

	c := NewFTPComponent()

	// 1. fileExist=Append must issue native APPE without calling RETR+STOR.
	epAppend, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/append.txt?fileExist=Append&disconnect=true", srv.addr()))
	require.NoError(t, err)
	prodAppend, err := epAppend.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prodAppend.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody([]byte("world"))
	require.NoError(t, prodAppend.Send(ex))
	require.NoError(t, prodAppend.Stop())

	got, ok := srv.getFile("append.txt")
	require.True(t, ok)
	assert.Equal(t, "hello world", got)
	cmds := srv.commands()
	assert.Contains(t, cmds, "APPE append.txt")
	assert.NotContains(t, cmds, "RETR append.txt")

	// Append to a non-existent file creates it via APPE.
	epAppendNew, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/fresh.txt?fileExist=Append", srv.addr()))
	require.NoError(t, err)
	prodAppendNew, err := epAppendNew.CreateProducer()
	require.NoError(t, err)
	exNew := gocamel.NewExchange(context.Background())
	exNew.SetBody(strings.NewReader("created"))
	require.NoError(t, prodAppendNew.Send(exNew))
	require.NoError(t, prodAppendNew.Stop())
	gotFresh, ok := srv.getFile("fresh.txt")
	require.True(t, ok)
	assert.Equal(t, "created", gotFresh)

	// 2. FileExistFail and FileExistIgnore inspect *textproto.Error on FileSize.
	epFail, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/existing.txt?fileExist=Fail", srv.addr()))
	require.NoError(t, err)
	prodFail, err := epFail.CreateProducer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = prodFail.Stop() })

	exFail := gocamel.NewExchange(context.Background())
	exFail.SetBody("overwrite")
	err = prodFail.Send(exFail)
	require.Error(t, err)

	epIgnore, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/existing.txt?fileExist=Ignore&passiveMode=false", srv.addr()))
	require.NoError(t, err)
	prodIgnore, err := epIgnore.CreateProducer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = prodIgnore.Stop() })

	exIgnore := gocamel.NewExchange(context.Background())
	exIgnore.SetBody("overwrite")
	require.NoError(t, prodIgnore.Send(exIgnore))
	gotExisting, _ := srv.getFile("existing.txt")
	assert.Equal(t, "keep", gotExisting)

	// Non-550 SIZE errors must NOT silently overwrite on Fail or Ignore.
	srv.mu.Lock()
	srv.sizeErrCode = 502
	srv.mu.Unlock()
	exNon550 := gocamel.NewExchange(context.Background())
	exNon550.SetBody("should-fail")
	require.Error(t, prodIgnore.Send(exNon550))
	require.Error(t, prodFail.Send(exNon550))
	gotExisting, _ = srv.getFile("existing.txt")
	assert.Equal(t, "keep", gotExisting)
	srv.mu.Lock()
	srv.sizeErrCode = 0
	srv.mu.Unlock()

	// 3. IPv6 default port formatting and live IPv6 transfer.
	epV6Default, err := c.CreateEndpoint("ftp://[::1]/in?connectTimeout=100ms")
	require.NoError(t, err)
	prodV6Default, err := epV6Default.CreateProducer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = prodV6Default.Stop() })
	exV6Default := gocamel.NewExchange(context.Background())
	exV6Default.SetBody("probe")
	if err := prodV6Default.Send(exV6Default); err != nil {
		assert.NotContains(t, err.Error(), "missing port in address")
	}

	srv6 := newFakeFTPServer(t, "tcp6", "[::1]:0")
	epV6, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/v6/msg.txt", srv6.addr()))
	require.NoError(t, err)
	prodV6, err := epV6.CreateProducer()
	require.NoError(t, err)
	exV6 := gocamel.NewExchange(context.Background())
	exV6.SetBody("ipv6-ok")
	require.NoError(t, prodV6.Send(exV6))
	require.NoError(t, prodV6.Stop())
	gotV6, ok := srv6.getFile("v6/msg.txt")
	require.True(t, ok)
	assert.Equal(t, "ipv6-ok", gotV6)
}

func TestFTPConsumer_SkipsMoveDirRecursiveAndMkdirAndMaxMessagesPerPoll(t *testing.T) {
	c := NewFTPComponent()

	// Subtest 1: recursive=true excludes existing move/moveFailed directories and subtrees.
	t.Run("recursive_excludes_move_dirs", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("in/a.txt", "ok-a")
		srv.addFile("in/sub/b.txt", "fail-b")
		srv.addFile("in/.done/already.txt", "old-done")
		srv.addFile("in/.done/deep/older.txt", "old-deep-done")
		srv.addFile("in/.error/failed.txt", "old-err")

		var processed sync.Mutex
		seen := make(map[string]int)
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/in?recursive=true&move=in/.done&moveFailed=/in/.error&initialDelay=0s&delay=30ms",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			processed.Lock()
			seen[name]++
			processed.Unlock()
			if name == "b.txt" {
				return errors.New("boom")
			}
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			_, okDone := srv.getFile("in/.done/a.txt")
			_, okErr := srv.getFile("in/.error/b.txt")
			return okDone && okErr
		}, 3*time.Second, 20*time.Millisecond)

		require.Eventually(t, func() bool {
			return len(srv.listed()) >= 4
		}, 2*time.Second, 20*time.Millisecond)

		require.NoError(t, consumer.Stop())

		processed.Lock()
		assert.Equal(t, 1, seen["a.txt"])
		assert.Equal(t, 1, seen["b.txt"])
		assert.Zero(t, seen["already.txt"])
		assert.Zero(t, seen["older.txt"])
		assert.Zero(t, seen["failed.txt"])
		processed.Unlock()

		for _, listedDir := range srv.listed() {
			assert.False(t, strings.HasPrefix(listedDir, "in/.done"), "must not list move dir %q", listedDir)
			assert.False(t, strings.HasPrefix(listedDir, "in/.error"), "must not list moveFailed dir %q", listedDir)
		}
	})

	// Subtest 2: moveFTPFile creates missing multi-segment destDir via MKD before RNFR/RNTO.
	t.Run("move_creates_nested_dirs_via_mkd", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("in/a.txt", "ok-a")
		srv.addFile("in/sub/b.txt", "fail-b")

		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/in?recursive=true&move=in/.archive/done&moveFailed=in/.archive/err&initialDelay=0s&delay=30ms",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			if name == "b.txt" {
				return errors.New("boom")
			}
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			_, okDone := srv.getFile("in/.archive/done/a.txt")
			_, okErr := srv.getFile("in/.archive/err/b.txt")
			return okDone && okErr
		}, 3*time.Second, 20*time.Millisecond)

		require.NoError(t, consumer.Stop())

		mkds := srv.mkds()
		assert.Contains(t, mkds, "in")
		assert.Contains(t, mkds, "in/.archive")
		assert.Contains(t, mkds, "in/.archive/done")
		assert.Contains(t, mkds, "in/.archive/err")
	})

	// Subtest 3: MaxMessagesPerPoll counts failed exchanges too.
	t.Run("max_messages_per_poll_counts_failures", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		for i := 1; i <= 4; i++ {
			srv.addFile(fmt.Sprintf("q/%d.txt", i), fmt.Sprintf("body-%d", i))
		}

		var calls atomic.Int32
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/q?maxMessagesPerPoll=2&delete=true&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			calls.Add(1)
			return errors.New("always fail")
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			return calls.Load() == 2
		}, 2*time.Second, 10*time.Millisecond)
		// Ensure a brief window does not exceed MaxMessagesPerPoll=2 in the single poll.
		assert.Never(t, func() bool {
			return calls.Load() > 2
		}, 80*time.Millisecond, 10*time.Millisecond)
		require.NoError(t, consumer.Stop())
	})
}

func TestFTPConsumer_ReadLocksPostProcessingAndEdgeCases(t *testing.T) {
	c := NewFTPComponent()

	t.Run("delete_and_disconnect_true", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("in/del.txt", "bye")
		srv.addFile("in/ignore.tmp", "skip")

		var got []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/in?delete=true&disconnect=true&exclude=\\.tmp$&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			b, ok := ex.GetIn().GetBody().([]byte)
			require.True(t, ok)
			mu.Lock()
			got = append(got, string(b))
			mu.Unlock()
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			_, stillThere := srv.getFile("in/del.txt")
			return !stillThere
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"bye"}, got)
		mu.Unlock()
		_, tmpExists := srv.getFile("in/ignore.tmp")
		assert.True(t, tmpExists)
	})

	t.Run("readLock_rename_and_err_stop_routing", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("in/r1.txt", "one")
		srv.addFile("in/r2.txt", "two")

		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/in?readLock=rename&delete=true&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			if name == "r1.txt" {
				return gocamel.ErrStopRouting
			}
			return errors.New("fail-r2")
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		// r1.txt triggers ErrStopRouting -> OnFailure delegates to OnComplete -> deleted.
		// r2.txt fails -> lock file restored back to r2.txt.
		require.Eventually(t, func() bool {
			_, r1Exists := srv.getFile("in/r1.txt")
			_, r2Exists := srv.getFile("in/r2.txt")
			return !r1Exists && r2Exists
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())
	})

	t.Run("readLock_markerFile_and_noop", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("in/m.txt", "data")
		srv.addFile("in/locked.txt", "locked-data")
		srv.addFile("in/locked.txt.camelLock", "")

		var seen []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/in?readLock=markerFile&noop=true&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			if len(seen) == 0 {
				return false
			}
			_, lockStillExists := srv.getFile("in/m.txt.camelLock")
			return !lockStillExists
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"m.txt"}, seen)
		mu.Unlock()
		_, mExists := srv.getFile("in/m.txt")
		assert.True(t, mExists, "noop=true must keep original file")
	})

	t.Run("readLock_changed_and_maxMessageSize", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile("small.txt", "ok")
		srv.addFile("big.txt", "0123456789abcdef")

		var got []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"ftp://%s/?readLock=changed&readLockCheckInterval=10ms&readLockTimeout=500ms&maxMessageSize=5&delete=true&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			mu.Lock()
			got = append(got, name)
			mu.Unlock()
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			_, smallExists := srv.getFile("small.txt")
			return !smallExists
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"small.txt"}, got)
		mu.Unlock()
		_, bigExists := srv.getFile("big.txt")
		assert.True(t, bigExists, "oversized file must be rejected without deleting")
	})

	t.Run("producer_validation_and_reconnect", func(t *testing.T) {
		srv := newFakeFTPServer(t, "tcp", "127.0.0.1:0")
		ep, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/?maxMessageSize=4", srv.addr()))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prod.Stop() })

		// Missing CamelFileName on root.
		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("ok")
		require.Error(t, prod.Send(ex))

		// Directory path without CamelFileName.
		epDir, err := c.CreateEndpoint(fmt.Sprintf("ftp://%s/dir/", srv.addr()))
		require.NoError(t, err)
		prodDir, err := epDir.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodDir.Stop() })
		require.Error(t, prodDir.Send(ex))

		// Unsupported body type.
		exBadBody := gocamel.NewExchange(context.Background())
		exBadBody.GetIn().SetHeader(gocamel.CamelFileName, "a.txt")
		exBadBody.SetBody(12345)
		require.Error(t, prod.Send(exBadBody))

		// Reader body exceeding maxMessageSize.
		exBig := gocamel.NewExchange(context.Background())
		exBig.GetIn().SetHeader(gocamel.CamelFileName, "a.txt")
		exBig.SetBody(strings.NewReader("toolong"))
		require.Error(t, prod.Send(exBig))

		// Valid send establishes persistent connection; closing server conns forces NoOp reconnect.
		exOK := gocamel.NewExchange(context.Background())
		exOK.GetIn().SetHeader(gocamel.CamelFileName, "ok.txt")
		exOK.SetBody("ok")
		require.NoError(t, prod.Send(exOK))

		srv.mu.Lock()
		for conn := range srv.conns {
			_ = conn.Close()
		}
		srv.mu.Unlock()

		exOK2 := gocamel.NewExchange(context.Background())
		exOK2.GetIn().SetHeader(gocamel.CamelFileName, "ok2.txt")
		exOK2.SetBody("ok2")
		require.NoError(t, prod.Send(exOK2))
		got2, ok := srv.getFile("ok2.txt")
		require.True(t, ok)
		assert.Equal(t, "ok2", got2)
	})
}
