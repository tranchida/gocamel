package sftp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	gocamel "github.com/tranchida/gocamel"
)

type openRecord struct {
	method string
	path   string
	flags  sftp.FileOpenFlags
}

type cmdRecord struct {
	method string
	path   string
	target string
}

type failingCloseReadWriteAt struct {
	sftp.WriterAtReaderAt
	closeErr error
}

func (f *failingCloseReadWriteAt) Close() error {
	return f.closeErr
}

type failingCloseWriteAt struct {
	io.WriterAt
	closeErr error
}

func (f *failingCloseWriteAt) Close() error {
	return f.closeErr
}

type recordingSFTPHandler struct {
	base    sftp.Handlers
	filePut sftp.OpenFileWriter
	fileCmd sftp.PosixRenameFileCmder
	fileLst sftp.LstatFileLister

	mu             sync.Mutex
	opens          []openRecord
	cmds           []cmdRecord
	listedDirs     []string
	closeErrPaths  map[string]error
	readErrPaths   map[string]error
	openErrPaths   map[string]error
	posixRenameErr error
	stallListCh    chan struct{}
}

func newRecordingSFTPHandler() *recordingSFTPHandler {
	base := sftp.InMemHandler()
	return &recordingSFTPHandler{
		base:          base,
		filePut:       base.FilePut.(sftp.OpenFileWriter),
		fileCmd:       base.FileCmd.(sftp.PosixRenameFileCmder),
		fileLst:       base.FileList.(sftp.LstatFileLister),
		closeErrPaths: make(map[string]error),
		readErrPaths:  make(map[string]error),
		openErrPaths:  make(map[string]error),
	}
}

func (h *recordingSFTPHandler) handlers() sftp.Handlers {
	return sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	}
}

func (h *recordingSFTPHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	h.mu.Lock()
	h.opens = append(h.opens, openRecord{method: r.Method, path: r.Filepath, flags: r.Pflags()})
	readErr := h.readErrPaths[r.Filepath]
	h.mu.Unlock()
	if readErr != nil {
		return nil, readErr
	}
	return h.base.FileGet.Fileread(r)
}

func (h *recordingSFTPHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	h.mu.Lock()
	h.opens = append(h.opens, openRecord{method: r.Method, path: r.Filepath, flags: r.Pflags()})
	openErr := h.openErrPaths[r.Filepath]
	closeErr := h.closeErrPaths[r.Filepath]
	h.mu.Unlock()
	if openErr != nil {
		return nil, openErr
	}
	wr, err := h.filePut.Filewrite(r)
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return &failingCloseWriteAt{WriterAt: wr, closeErr: closeErr}, nil
	}
	return wr, nil
}

func (h *recordingSFTPHandler) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	h.mu.Lock()
	h.opens = append(h.opens, openRecord{method: r.Method, path: r.Filepath, flags: r.Pflags()})
	openErr := h.openErrPaths[r.Filepath]
	closeErr := h.closeErrPaths[r.Filepath]
	h.mu.Unlock()
	if openErr != nil {
		return nil, openErr
	}
	rw, err := h.filePut.OpenFile(r)
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return &failingCloseReadWriteAt{WriterAtReaderAt: rw, closeErr: closeErr}, nil
	}
	return rw, nil
}

func (h *recordingSFTPHandler) Filecmd(r *sftp.Request) error {
	h.mu.Lock()
	h.cmds = append(h.cmds, cmdRecord{method: r.Method, path: r.Filepath, target: r.Target})
	h.mu.Unlock()
	return h.fileCmd.Filecmd(r)
}

func (h *recordingSFTPHandler) PosixRename(r *sftp.Request) error {
	h.mu.Lock()
	h.cmds = append(h.cmds, cmdRecord{method: r.Method, path: r.Filepath, target: r.Target})
	posixErr := h.posixRenameErr
	h.mu.Unlock()
	if posixErr != nil {
		return posixErr
	}
	return h.fileCmd.PosixRename(r)
}

func (h *recordingSFTPHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	h.mu.Lock()
	if r.Method == "List" {
		h.listedDirs = append(h.listedDirs, r.Filepath)
	}
	stallCh := h.stallListCh
	h.mu.Unlock()
	if r.Method == "List" && stallCh != nil {
		select {
		case <-stallCh:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return h.fileLst.Filelist(r)
}

func (h *recordingSFTPHandler) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	return h.fileLst.Lstat(r)
}

type fakeSFTPServer struct {
	listener       net.Listener
	handler        *recordingSFTPHandler
	admin          *sftp.Client
	adminSrv       *sftp.RequestServer
	knownHostsFile string
	privateKeyFile string
	connCount      atomic.Int32

	mu       sync.Mutex
	sshConns map[net.Conn]struct{}
	closed   bool
	wg       sync.WaitGroup
}

func newFakeSFTPServer(t *testing.T, network, addr string) *fakeSFTPServer {
	t.Helper()
	hostPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	require.NoError(t, err)

	clientPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	require.NoError(t, err)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(clientPriv)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "id_ecdsa")
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	ln, err := net.Listen(network, addr)
	require.NoError(t, err)

	knownHostsPath := filepath.Join(tmpDir, "known_hosts")
	line := knownhosts.Line([]string{ln.Addr().String()}, hostSigner.PublicKey())
	require.NoError(t, os.WriteFile(knownHostsPath, []byte(line+"\n"), 0o600))

	rec := newRecordingSFTPHandler()
	serverConn, clientConn := net.Pipe()
	adminSrv := sftp.NewRequestServer(serverConn, rec.base)
	go func() { _ = adminSrv.Serve() }()
	adminClient, err := sftp.NewClientPipe(clientConn, clientConn)
	require.NoError(t, err)

	srv := &fakeSFTPServer{
		listener:       ln,
		handler:        rec,
		admin:          adminClient,
		adminSrv:       adminSrv,
		knownHostsFile: knownHostsPath,
		privateKeyFile: keyPath,
		sshConns:       make(map[net.Conn]struct{}),
	}

	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
		PublicKeyCallback: func(_ ssh.ConnMetadata, pub ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(pub.Marshal(), clientSigner.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unknown public key")
		},
	}
	sshCfg.AddHostKey(hostSigner)

	srv.wg.Add(1)
	go func() {
		defer srv.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			srv.connCount.Add(1)
			srv.mu.Lock()
			if srv.closed {
				srv.mu.Unlock()
				_ = conn.Close()
				return
			}
			srv.sshConns[conn] = struct{}{}
			srv.mu.Unlock()

			srv.wg.Add(1)
			go func(c net.Conn) {
				defer srv.wg.Done()
				defer func() {
					srv.mu.Lock()
					delete(srv.sshConns, c)
					srv.mu.Unlock()
					_ = c.Close()
				}()

				sConn, chans, reqs, err := ssh.NewServerConn(c, sshCfg)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)

				for newCh := range chans {
					if newCh.ChannelType() != "session" {
						_ = newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
						continue
					}
					ch, chReqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					srv.wg.Add(1)
					go func(channel ssh.Channel, requests <-chan *ssh.Request) {
						defer srv.wg.Done()
						defer channel.Close()
						for req := range requests {
							if req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
								_ = req.Reply(true, nil)
								sftpSrv := sftp.NewRequestServer(channel, rec.handlers())
								_ = sftpSrv.Serve()
								_ = sftpSrv.Close()
								return
							}
							if req.WantReply {
								_ = req.Reply(false, nil)
							}
						}
					}(ch, chReqs)
				}
			}(conn)
		}
	}()

	t.Cleanup(func() {
		srv.Close()
	})
	return srv
}

func (s *fakeSFTPServer) addr() string {
	return s.listener.Addr().String()
}

func (s *fakeSFTPServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for c := range s.sshConns {
		_ = c.Close()
	}
	s.mu.Unlock()
	_ = s.listener.Close()
	_ = s.admin.Close()
	_ = s.adminSrv.Close()
	s.wg.Wait()
}

func (s *fakeSFTPServer) dropActiveConns() {
	s.mu.Lock()
	for c := range s.sshConns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

func (s *fakeSFTPServer) addDir(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, s.admin.MkdirAll(dir))
}

func (s *fakeSFTPServer) addFile(t *testing.T, remotePath, content string) {
	t.Helper()
	if dir := filepath.Dir(remotePath); dir != "." && dir != "/" {
		require.NoError(t, s.admin.MkdirAll(dir))
	}
	f, err := s.admin.Create(remotePath)
	require.NoError(t, err)
	_, err = f.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func (s *fakeSFTPServer) getFile(remotePath string) (string, bool) {
	f, err := s.admin.Open(remotePath)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func (s *fakeSFTPServer) exists(remotePath string) bool {
	_, err := s.admin.Lstat(remotePath)
	return err == nil
}

func (s *fakeSFTPServer) openRecords() []openRecord {
	s.handler.mu.Lock()
	defer s.handler.mu.Unlock()
	out := make([]openRecord, len(s.handler.opens))
	copy(out, s.handler.opens)
	return out
}

func (s *fakeSFTPServer) clearRecords() {
	s.handler.mu.Lock()
	s.handler.opens = nil
	s.handler.cmds = nil
	s.handler.listedDirs = nil
	s.handler.mu.Unlock()
}

func (s *fakeSFTPServer) cmdCalls() []cmdRecord {
	s.handler.mu.Lock()
	defer s.handler.mu.Unlock()
	out := make([]cmdRecord, len(s.handler.cmds))
	copy(out, s.handler.cmds)
	return out
}

func (s *fakeSFTPServer) listedDirs() []string {
	s.handler.mu.Lock()
	defer s.handler.mu.Unlock()
	out := make([]string, len(s.handler.listedDirs))
	copy(out, s.handler.listedDirs)
	return out
}

func TestGetHostKeyCallback(t *testing.T) {
	e := &SFTPEndpoint{}

	// Case 1: strictHostKeyChecking=false
	u1, _ := url.Parse("sftp://example.com?strictHostKeyChecking=false")
	cb1, err := e.getHostKeyCallback(u1)
	if err != nil {
		t.Errorf("Unexpected error for strictHostKeyChecking=false: %v", err)
	}
	if cb1 == nil {
		t.Error("Expected callback to be non-nil for strictHostKeyChecking=false")
	}

	// Case 2: strictHostKeyChecking=true (default) but no knownHostsFile
	u2, _ := url.Parse("sftp://example.com")
	_, err = e.getHostKeyCallback(u2)
	if err == nil {
		t.Error("Expected error for strictHostKeyChecking=true without knownHostsFile")
	}

	// Case 3: strictHostKeyChecking=true with valid knownHostsFile
	tmpFile, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	u3, _ := url.Parse("sftp://example.com?knownHostsFile=" + tmpFile.Name())
	cb3, err := e.getHostKeyCallback(u3)
	if err != nil {
		t.Errorf("Unexpected error for strictHostKeyChecking=true with knownHostsFile: %v", err)
	}
	if cb3 == nil {
		t.Error("Expected callback to be non-nil for strictHostKeyChecking=true with knownHostsFile")
	}
}

func TestSFTPConsumer_ReadLockOptions(t *testing.T) {
	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint("sftp://example.com/incoming?readLock=changed&readLockTimeout=20s&readLockCheckInterval=500ms&readLockMinLength=512&readLockMinAge=3s")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}

	consumer, err := ep.CreateConsumer(nil)
	if err != nil {
		t.Fatalf("unexpected error creating consumer: %v", err)
	}

	sftpConsumer, ok := consumer.(*SFTPConsumer)
	if !ok {
		t.Fatalf("expected *SFTPConsumer, got %T", consumer)
	}

	if sftpConsumer.opts.ReadLock != "changed" {
		t.Errorf("expected readLock=changed, got %v", sftpConsumer.opts.ReadLock)
	}
	if sftpConsumer.opts.ReadLockTimeout != 20*1000*1000*1000 { // 20s
		t.Errorf("expected readLockTimeout=20s, got %v", sftpConsumer.opts.ReadLockTimeout)
	}
	if sftpConsumer.opts.ReadLockCheckInterval != 500*1000*1000 { // 500ms
		t.Errorf("expected readLockCheckInterval=500ms, got %v", sftpConsumer.opts.ReadLockCheckInterval)
	}
	if sftpConsumer.opts.ReadLockMinLength != 512 {
		t.Errorf("expected readLockMinLength=512, got %v", sftpConsumer.opts.ReadLockMinLength)
	}
	if sftpConsumer.opts.ReadLockMinAge != 3*1000*1000*1000 { // 3s
		t.Errorf("expected readLockMinAge=3s, got %v", sftpConsumer.opts.ReadLockMinAge)
	}
}

func TestSFTPProducer_ReturnsFileCloseErrorAndUsesOAppendOExcl(t *testing.T) {
	c := NewSFTPComponent()

	t.Run("propagates_file_close_error", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.handler.mu.Lock()
		srv.handler.closeErrPaths["/close-fail.txt"] = errors.New("flush failed on close")
		srv.handler.mu.Unlock()

		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/close-fail.txt?strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("payload")
		err = prod.Send(ex)
		require.Error(t, err, "Send must propagate remote file.Close() error")
	})

	t.Run("append_uses_o_append_and_preserves_existing_on_read_error", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/append.txt", "hello")
		srv.addFile(t, "/append-noread.txt", "keep")
		srv.handler.mu.Lock()
		srv.handler.readErrPaths["/append-noread.txt"] = os.ErrPermission
		srv.handler.mu.Unlock()
		srv.clearRecords()

		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/?fileExist=Append&strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prod.Stop() })

		// Existing file appends natively via O_APPEND without truncating.
		ex1 := gocamel.NewExchange(context.Background())
		ex1.GetIn().SetHeader(gocamel.CamelFileName, "append.txt")
		ex1.SetBody(" world")
		require.NoError(t, prod.Send(ex1))
		got1, ok := srv.getFile("/append.txt")
		require.True(t, ok)
		assert.Equal(t, "hello world", got1)

		// When read permission is denied, O_APPEND must still append instead of falling back to Create/O_TRUNC.
		ex2 := gocamel.NewExchange(context.Background())
		ex2.GetIn().SetHeader(gocamel.CamelFileName, "append-noread.txt")
		ex2.SetBody("-more")
		require.NoError(t, prod.Send(ex2))
		got2, ok := srv.getFile("/append-noread.txt")
		require.True(t, ok)
		assert.Equal(t, "keep-more", got2, "existing file must not be clobbered when read is denied")

		// Creating a fresh file with fileExist=Append also works.
		ex3 := gocamel.NewExchange(context.Background())
		ex3.GetIn().SetHeader(gocamel.CamelFileName, "append-new.txt")
		ex3.SetBody("created")
		require.NoError(t, prod.Send(ex3))
		got3, ok := srv.getFile("/append-new.txt")
		require.True(t, ok)
		assert.Equal(t, "created", got3)

		var sawAppendOpen bool
		for _, rec := range srv.openRecords() {
			if rec.path == "/append.txt" && rec.flags.Write && rec.flags.Creat && rec.flags.Append && !rec.flags.Trunc {
				sawAppendOpen = true
			}
			assert.False(t, rec.flags.Trunc, "fileExist=Append must never open with O_TRUNC (%+v)", rec)
		}
		assert.True(t, sawAppendOpen, "expected FileExistAppend to open with O_WRONLY|O_CREATE|O_APPEND")
	})

	t.Run("fail_and_ignore_use_o_excl", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.clearRecords()

		epFail, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/excl-fail.txt?fileExist=Fail&strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prodFail, err := epFail.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodFail.Stop() })

		ex1 := gocamel.NewExchange(context.Background())
		ex1.SetBody("first")
		require.NoError(t, prodFail.Send(ex1))

		ex2 := gocamel.NewExchange(context.Background())
		ex2.SetBody("overwrite")
		err = prodFail.Send(ex2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		gotFail, ok := srv.getFile("/excl-fail.txt")
		require.True(t, ok)
		assert.Equal(t, "first", gotFail)

		epIgn, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/excl-ignore.txt?fileExist=Ignore&strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prodIgn, err := epIgn.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodIgn.Stop() })

		ex3 := gocamel.NewExchange(context.Background())
		ex3.SetBody("keep")
		require.NoError(t, prodIgn.Send(ex3))

		ex4 := gocamel.NewExchange(context.Background())
		ex4.SetBody("overwrite")
		require.NoError(t, prodIgn.Send(ex4))
		gotIgn, ok := srv.getFile("/excl-ignore.txt")
		require.True(t, ok)
		assert.Equal(t, "keep", gotIgn)

		// Non-existence errors (e.g. permission denied) must fail even with fileExist=Ignore.
		srv.handler.mu.Lock()
		srv.handler.openErrPaths["/perm-denied.txt"] = os.ErrPermission
		srv.handler.mu.Unlock()
		epPerm, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/perm-denied.txt?fileExist=Ignore&strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prodPerm, err := epPerm.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodPerm.Stop() })
		exPerm := gocamel.NewExchange(context.Background())
		exPerm.SetBody("denied")
		require.Error(t, prodPerm.Send(exPerm))

		var sawFailExcl, sawIgnExcl bool
		for _, rec := range srv.openRecords() {
			if rec.path == "/excl-fail.txt" && rec.flags.Write && rec.flags.Creat && rec.flags.Excl {
				sawFailExcl = true
			}
			if rec.path == "/excl-ignore.txt" && rec.flags.Write && rec.flags.Creat && rec.flags.Excl {
				sawIgnExcl = true
			}
		}
		assert.True(t, sawFailExcl, "expected FileExistFail to open with O_EXCL")
		assert.True(t, sawIgnExcl, "expected FileExistIgnore to open with O_EXCL")
	})
}

func TestSFTPEndpoint_SecurityPrivateKeyKnownHostsAndProducerEdges(t *testing.T) {
	c := NewSFTPComponent()

	t.Run("endpoint_validation_and_uri_redaction", func(t *testing.T) {
		for _, badURI := range []string{
			"://bad-uri",
			"sftp://example.com/in/../../etc/passwd",
			"sftp://example.com/in?move=../outside",
			"sftp://example.com/in?moveFailed=../outside",
			"sftp://example.com/in?knownHostsFile=../hosts",
		} {
			ep, err := c.CreateEndpoint(badURI)
			if err == nil {
				_, cErr := ep.CreateConsumer(nil)
				if cErr == nil && strings.Contains(badURI, "knownHostsFile") {
					_, pErr := ep.CreateProducer()
					require.NoError(t, pErr)
					_, hErr := ep.(*SFTPEndpoint).getHostKeyCallback(ep.(*SFTPEndpoint).url)
					require.Error(t, hErr)
					continue
				}
				t.Errorf("expected CreateEndpoint(%q) to fail", badURI)
			}
		}

		ep, err := c.CreateEndpoint("sftp://alice:topsecret@example.com/inbox?password=querysecret&strictHostKeyChecking=false")
		require.NoError(t, err)
		redacted := ep.URI()
		assert.NotContains(t, redacted, "topsecret")
		assert.NotContains(t, redacted, "querysecret")
	})

	t.Run("privateKeyFile_and_knownHostsFile_end_to_end", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")

		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"sftp://%s/auth/key.txt?username=alice&privateKeyFile=%s&knownHostsFile=%s",
			srv.addr(), url.QueryEscape(srv.privateKeyFile), url.QueryEscape(srv.knownHostsFile),
		))
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prod.Stop() })

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody([]byte("key-auth-ok"))
		require.NoError(t, prod.Send(ex))
		got, ok := srv.getFile("/auth/key.txt")
		require.True(t, ok)
		assert.Equal(t, "key-auth-ok", got)

		// Invalid private key cases.
		badKeyFile := filepath.Join(t.TempDir(), "bad_key")
		require.NoError(t, os.WriteFile(badKeyFile, []byte("not-a-pem"), 0o600))
		for _, keyParam := range []string{
			"../escape",
			filepath.Join(t.TempDir(), "missing"),
			badKeyFile,
		} {
			badEp, err := c.CreateEndpoint(fmt.Sprintf(
				"sftp://%s/x.txt?strictHostKeyChecking=false&privateKeyFile=%s",
				srv.addr(), url.QueryEscape(keyParam),
			))
			require.NoError(t, err)
			badProd, err := badEp.CreateProducer()
			require.NoError(t, err)
			exBad := gocamel.NewExchange(context.Background())
			exBad.SetBody("x")
			require.Error(t, badProd.Send(exBad))
			_ = badProd.Stop()
		}
	})

	t.Run("producer_paths_bodies_reconnect_and_disconnect", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")

		// Path validation and body type validation.
		epRoot, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/?maxMessageSize=8&strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prodRoot, err := epRoot.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodRoot.Stop() })

		require.Error(t, prodRoot.Send(nil))

		exNoName := gocamel.NewExchange(context.Background())
		exNoName.SetBody("hi")
		require.Error(t, prodRoot.Send(exNoName))

		exBadName := gocamel.NewExchange(context.Background())
		exBadName.GetIn().SetHeader(gocamel.CamelFileName, "../../evil.txt")
		exBadName.SetBody("hi")
		require.Error(t, prodRoot.Send(exBadName))

		exBadBody := gocamel.NewExchange(context.Background())
		exBadBody.GetIn().SetHeader(gocamel.CamelFileName, "ok.txt")
		exBadBody.SetBody(12345)
		require.Error(t, prodRoot.Send(exBadBody))

		exBigReader := gocamel.NewExchange(context.Background())
		exBigReader.GetIn().SetHeader(gocamel.CamelFileName, "ok.txt")
		exBigReader.SetBody(strings.NewReader("0123456789"))
		require.Error(t, prodRoot.Send(exBigReader))

		// Directory trailing slash without CamelFileName and with CamelFileName.
		epDir, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/dir/?strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prodDir, err := epDir.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodDir.Stop() })
		require.Error(t, prodDir.Send(exNoName))

		exReader := gocamel.NewExchange(context.Background())
		exReader.GetIn().SetHeader(gocamel.CamelFileName, "sub/r.txt")
		exReader.SetBody(strings.NewReader("reader"))
		require.NoError(t, prodDir.Send(exReader))
		gotR, ok := srv.getFile("/dir/sub/r.txt")
		require.True(t, ok)
		assert.Equal(t, "reader", gotR)

		// Persistent connection reconnects automatically after server drops connections,
		// and Start() re-enables a stopped producer.
		exRoot := gocamel.NewExchange(context.Background())
		exRoot.GetIn().SetHeader(gocamel.CamelFileName, "p1.txt")
		exRoot.SetBody("one")
		require.NoError(t, prodRoot.Send(exRoot))
		srv.dropActiveConns()
		exRoot2 := gocamel.NewExchange(context.Background())
		exRoot2.GetIn().SetHeader(gocamel.CamelFileName, "p2.txt")
		exRoot2.SetBody("two")
		require.NoError(t, prodRoot.Send(exRoot2))
		gotP2, ok := srv.getFile("/p2.txt")
		require.True(t, ok)
		assert.Equal(t, "two", gotP2)

		require.NoError(t, prodRoot.Stop())
		require.Error(t, prodRoot.Send(exRoot2))
		require.NoError(t, prodRoot.Start(context.Background()))
		require.NoError(t, prodRoot.Send(exRoot2))

		// disconnect=true dials a fresh connection per Send.
		srv.connCount.Store(0)
		epDisc, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/disc.txt?disconnect=true&strictHostKeyChecking=false", srv.addr()))
		require.NoError(t, err)
		prodDisc, err := epDisc.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodDisc.Stop() })
		exDisc := gocamel.NewExchange(context.Background())
		exDisc.SetBody("d1")
		require.NoError(t, prodDisc.Send(exDisc))
		require.NoError(t, prodDisc.Send(exDisc))
		assert.Equal(t, int32(2), srv.connCount.Load())
	})
}

func TestSFTPConsumer_ReadLocksPostProcessingAndEdgeCases(t *testing.T) {
	c := NewSFTPComponent()

	t.Run("disconnect_true_filtering_and_symlinks", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/keep.txt", "kept")
		srv.addFile(t, "/in/ignore.tmp", "ignored")
		srv.addFile(t, "/in/x.camelLock", "lock")
		srv.addFile(t, "/in/y.camelExclusiveReadLock", "lock2")
		require.NoError(t, srv.admin.Symlink("/in/keep.txt", "/in/link.txt"))

		var got []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"sftp://%s/in?delete=true&disconnect=true&exclude=\\.tmp$&strictHostKeyChecking=false&initialDelay=0s&delay=5s",
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
			return !srv.exists("/in/keep.txt")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"kept"}, got)
		mu.Unlock()
		assert.True(t, srv.exists("/in/ignore.tmp"))
		assert.True(t, srv.exists("/in/link.txt"))
	})

	t.Run("readLock_rename_errStopRouting_and_noop_restore", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/r1.txt", "one")
		srv.addFile(t, "/in/r2.txt", "two")

		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"sftp://%s/in?readLock=rename&delete=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s",
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
		// r2.txt fails -> lock restored back to /in/r2.txt.
		require.Eventually(t, func() bool {
			return !srv.exists("/in/r1.txt") && srv.exists("/in/r2.txt") && !srv.exists("/in/r2.txt.camelExclusiveReadLock")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		// Test noop=true with readLock=rename restores lock file via Rename.
		epNoop, err := c.CreateEndpoint(fmt.Sprintf(
			"sftp://%s/in?readLock=rename&noop=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		var noopSeen atomic.Int32
		cNoop, err := epNoop.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			noopSeen.Add(1)
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, cNoop.Start(context.Background()))
		t.Cleanup(func() { _ = cNoop.Stop() })
		require.Eventually(t, func() bool {
			return noopSeen.Load() >= 1 && srv.exists("/in/r2.txt") && !srv.exists("/in/r2.txt.camelExclusiveReadLock")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, cNoop.Stop())
	})

	t.Run("readLock_changed_maxMessageSize_and_persistent_reconnect", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/small.txt", "ok")
		srv.addFile(t, "/big.txt", "0123456789abcdef")

		var got []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"sftp://%s/?readLock=changed&readLockCheckInterval=10ms&readLockTimeout=500ms&maxMessageSize=5&delete=true&strictHostKeyChecking=false&initialDelay=0s&delay=25ms",
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
			return !srv.exists("/small.txt")
		}, 2*time.Second, 15*time.Millisecond)

		// Drop active connections and add a second small file; persistent consumer reconnects cleanly.
		srv.dropActiveConns()
		srv.addFile(t, "/second.txt", "hi")
		require.Eventually(t, func() bool {
			return !srv.exists("/second.txt")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"small.txt", "second.txt"}, got)
		mu.Unlock()
		assert.True(t, srv.exists("/big.txt"), "oversized file must be rejected without deleting")
	})

	t.Run("read_error_releases_locks_and_parent_ctx_cancel_exits", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/unreadable.txt", "secret")
		srv.handler.mu.Lock()
		srv.handler.readErrPaths["/in/unreadable.txt.camelExclusiveReadLock"] = os.ErrPermission
		srv.handler.mu.Unlock()

		ep, err := c.CreateEndpoint(fmt.Sprintf(
			"sftp://%s/in?readLock=rename&strictHostKeyChecking=false&initialDelay=0s&delay=5s",
			srv.addr(),
		))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			t.Fatal("processor must not be called when Open fails")
			return nil
		}))
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		require.NoError(t, consumer.Start(ctx))
		require.Eventually(t, func() bool {
			for _, cmd := range srv.cmdCalls() {
				if cmd.method == "Rename" && cmd.path == "/in/unreadable.txt.camelExclusiveReadLock" && cmd.target == "/in/unreadable.txt" {
					return true
				}
			}
			return false
		}, 2*time.Second, 15*time.Millisecond)
		cancel()
		require.NoError(t, consumer.Stop())
	})
}
