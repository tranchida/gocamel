package smb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hirochachacha/go-smb2"

	"github.com/tranchida/gocamel"
)

func TestSMBConsumer_StopWaitsForPollingWorker(t *testing.T) {
	consumer := &SMBConsumer{}
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

func TestSMBConsumer_ReadLockOptions(t *testing.T) {
	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://example.com/share/folder?readLock=markerFile&readLockTimeout=30s&readLockCheckInterval=2s&readLockMinLength=100")
	if err != nil {
		t.Fatalf("unexpected error creating endpoint: %v", err)
	}

	consumer, err := ep.CreateConsumer(nil)
	if err != nil {
		t.Fatalf("unexpected error creating consumer: %v", err)
	}

	smbConsumer, ok := consumer.(*SMBConsumer)
	if !ok {
		t.Fatalf("expected *SMBConsumer, got %T", consumer)
	}

	if smbConsumer.opts.ReadLock != "markerFile" {
		t.Errorf("expected readLock=markerFile, got %v", smbConsumer.opts.ReadLock)
	}
	if smbConsumer.opts.ReadLockTimeout != 30*time.Second {
		t.Errorf("expected readLockTimeout=30s, got %v", smbConsumer.opts.ReadLockTimeout)
	}
	if smbConsumer.opts.ReadLockCheckInterval != 2*time.Second {
		t.Errorf("expected readLockCheckInterval=2s, got %v", smbConsumer.opts.ReadLockCheckInterval)
	}
	if smbConsumer.opts.ReadLockMinLength != 100 {
		t.Errorf("expected readLockMinLength=100, got %v", smbConsumer.opts.ReadLockMinLength)
	}
}

func TestSMBEndpoint_ConnectHandshakeDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	// Accept connection and hold it open without responding so SMB handshake stalls
	done := make(chan struct{})
	defer close(done)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-done
	}()

	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://" + ln.Addr().String() + "/share/folder?username=guest&connectTimeout=100ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	smbEp := ep.(*SMBEndpoint)

	start := time.Now()
	_, err = smbEp.connect(context.Background())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected connect to fail on stalled SMB handshake")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("connect took too long (%v), deadline was not applied", elapsed)
	}
}

func TestSMBProducer_SendAfterStopFailsWithoutDialing(t *testing.T) {
	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://127.0.0.1:1/share/out?disconnect=false&connectTimeout=50ms")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := prod.Start(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := prod.Stop(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hello")
	ex.SetHeader(gocamel.CamelFileName, "test.txt")
	err = prod.Send(ex)
	if err == nil || !strings.Contains(err.Error(), "smb producer is stopped") {
		t.Fatalf("expected 'smb producer is stopped' error, got: %v", err)
	}
}

func TestSMBConsumer_ConcurrentStartStop(t *testing.T) {
	c := NewSMBComponent()
	ep, err := c.CreateEndpoint("smb://127.0.0.1:1/share/in?initialDelay=1h&delay=1h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		return nil
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = cons.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = cons.Stop()
		}()
	}
	wg.Wait()
	_ = cons.Stop()
}

func TestSMBConsumer_StopUnblocksStalledHandshake(t *testing.T) {
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

	c := NewSMBComponent()
	ep, err := c.CreateEndpoint(fmt.Sprintf(
		"smb://%s:%s/inbox?connectTimeout=10000&initialDelay=0s", host, port))
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

type fakeSMBFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (fi fakeSMBFileInfo) Name() string       { return fi.name }
func (fi fakeSMBFileInfo) Size() int64        { return fi.size }
func (fi fakeSMBFileInfo) Mode() os.FileMode  { return fi.mode }
func (fi fakeSMBFileInfo) ModTime() time.Time { return fi.modTime }
func (fi fakeSMBFileInfo) IsDir() bool        { return fi.mode.IsDir() }
func (fi fakeSMBFileInfo) Sys() any           { return nil }

type fakeSMBEntry struct {
	data    []byte
	mode    os.FileMode
	modTime time.Time
}

type smbOpenRecord struct {
	path string
	flag int
	perm os.FileMode
}

type smbRenameRecord struct {
	oldpath string
	newpath string
}

type fakeSMBShare struct {
	mu             sync.Mutex
	dirs           map[string]time.Time
	files          map[string]*fakeSMBEntry
	mkdirCalls     []string
	openCalls      []string
	openFileCalls  []smbOpenRecord
	renameCalls    []smbRenameRecord
	removeCalls    []string
	readDirCalls   []string
	statErr        error
	openErrPaths   map[string]error
	readErrPaths   map[string]error
	writeErrPaths  map[string]error
	closeErrPaths  map[string]error
	mkdirErrPaths  map[string]error
	umountCount    atomic.Int32
	umountOnce     sync.Once
	umountCh       chan struct{}
	stallReadDir   chan struct{}
	readDirEntered chan struct{}
	readDirOnce    sync.Once
}

func newFakeSMBShare() *fakeSMBShare {
	now := time.Now()
	return &fakeSMBShare{
		dirs: map[string]time.Time{
			"":  now,
			".": now,
		},
		files:         make(map[string]*fakeSMBEntry),
		openErrPaths:  make(map[string]error),
		readErrPaths:  make(map[string]error),
		writeErrPaths: make(map[string]error),
		closeErrPaths: make(map[string]error),
		mkdirErrPaths: make(map[string]error),
		umountCh:      make(chan struct{}),
	}
}

func normalizeFakeSMBPath(op, p string) (string, error) {
	norm := strings.ReplaceAll(p, "/", "\\")
	for strings.HasPrefix(norm, ".\\") {
		norm = norm[2:]
	}
	if strings.HasPrefix(norm, "\\") {
		return "", &os.PathError{Op: op, Path: p, Err: errors.New("leading '\\' is not allowed in this operation")}
	}
	if norm == "." {
		return "", nil
	}
	return norm, nil
}

func smbParentPath(norm string) string {
	idx := strings.LastIndex(norm, "\\")
	if idx < 0 {
		return ""
	}
	return norm[:idx]
}

func smbBaseName(norm string) string {
	idx := strings.LastIndex(norm, "\\")
	if idx < 0 {
		return norm
	}
	return norm[idx+1:]
}

func (s *fakeSMBShare) seedDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	norm, err := normalizeFakeSMBPath("mkdir", dir)
	if err != nil || norm == "" {
		return
	}
	parts := strings.Split(norm, "\\")
	cur := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if cur == "" {
			cur = part
		} else {
			cur += "\\" + part
		}
		if _, ok := s.dirs[cur]; !ok {
			s.dirs[cur] = time.Now()
		}
	}
}

func (s *fakeSMBShare) seedFile(p, content string) {
	s.seedEntry(p, []byte(content), 0o666)
}

func (s *fakeSMBShare) seedEntry(p string, content []byte, mode os.FileMode) {
	norm, err := normalizeFakeSMBPath("seed", p)
	if err != nil || norm == "" {
		return
	}
	if parent := smbParentPath(norm); parent != "" {
		s.seedDir(parent)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if mode&os.ModeDir != 0 {
		s.dirs[norm] = time.Now()
	}
	copied := append([]byte(nil), content...)
	s.files[norm] = &fakeSMBEntry{
		data:    copied,
		mode:    mode,
		modTime: time.Now(),
	}
}

func (s *fakeSMBShare) getFile(p string) (string, bool) {
	norm, err := normalizeFakeSMBPath("get", p)
	if err != nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.files[norm]
	if !ok || entry.mode.IsDir() {
		return "", false
	}
	return string(entry.data), true
}

func (s *fakeSMBShare) exists(p string) bool {
	norm, err := normalizeFakeSMBPath("exists", p)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[norm]; ok {
		return true
	}
	_, ok := s.dirs[norm]
	return ok
}

func (s *fakeSMBShare) Stat(name string) (os.FileInfo, error) {
	s.mu.Lock()
	statErr := s.statErr
	s.mu.Unlock()
	if statErr != nil && name == "." {
		return nil, statErr
	}
	norm, err := normalizeFakeSMBPath("stat", name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.files[norm]; ok {
		return fakeSMBFileInfo{
			name:    smbBaseName(norm),
			size:    int64(len(entry.data)),
			mode:    entry.mode,
			modTime: entry.modTime,
		}, nil
	}
	if mt, ok := s.dirs[norm]; ok {
		return fakeSMBFileInfo{
			name:    smbBaseName(norm),
			mode:    os.ModeDir | 0o755,
			modTime: mt,
		}, nil
	}
	return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
}

func (s *fakeSMBShare) Open(name string) (smbFileHandle, error) {
	s.mu.Lock()
	s.openCalls = append(s.openCalls, name)
	s.mu.Unlock()
	return s.OpenFile(name, os.O_RDONLY, 0)
}

func (s *fakeSMBShare) OpenFile(name string, flag int, perm os.FileMode) (smbFileHandle, error) {
	s.mu.Lock()
	s.openFileCalls = append(s.openFileCalls, smbOpenRecord{path: name, flag: flag, perm: perm})
	openErr := s.openErrPaths[name]
	readErr := s.readErrPaths[name]
	writeErr := s.writeErrPaths[name]
	closeErr := s.closeErrPaths[name]
	s.mu.Unlock()

	if openErr != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: openErr}
	}

	norm, err := normalizeFakeSMBPath("open", name)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if parent := smbParentPath(norm); parent != "" {
		if _, ok := s.dirs[parent]; !ok {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
		}
	}

	existing, exists := s.files[norm]
	if flag&(os.O_CREATE|os.O_EXCL) == (os.O_CREATE | os.O_EXCL) {
		if exists {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrExist}
		}
	}

	if !exists {
		if flag&os.O_CREATE == 0 {
			return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
		}
		existing = &fakeSMBEntry{
			mode:    0o666,
			modTime: time.Now(),
		}
		s.files[norm] = existing
	} else if flag&os.O_TRUNC != 0 {
		existing.data = nil
		existing.modTime = time.Now()
	}

	snapshot := append([]byte(nil), existing.data...)
	return &fakeSMBFileHandle{
		share:    s,
		norm:     norm,
		flag:     flag,
		readBuf:  bytes.NewReader(snapshot),
		readErr:  readErr,
		writeErr: writeErr,
		closeErr: closeErr,
	}, nil
}

func (s *fakeSMBShare) ReadDir(dirname string) ([]os.FileInfo, error) {
	s.mu.Lock()
	s.readDirCalls = append(s.readDirCalls, dirname)
	stall := s.stallReadDir
	entered := s.readDirEntered
	umountCh := s.umountCh
	s.mu.Unlock()

	if entered != nil {
		s.readDirOnce.Do(func() { close(entered) })
	}
	if stall != nil {
		select {
		case <-stall:
		case <-umountCh:
			return nil, errors.New("share unmounted")
		}
	}

	norm, err := normalizeFakeSMBPath("readdir", dirname)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dirs[norm]; !ok {
		return nil, &os.PathError{Op: "readdir", Path: dirname, Err: os.ErrNotExist}
	}

	seen := make(map[string]os.FileInfo)
	prefix := ""
	if norm != "" {
		prefix = norm + "\\"
	}
	for dirPath, mt := range s.dirs {
		if dirPath == "" || dirPath == "." || dirPath == norm {
			continue
		}
		if !strings.HasPrefix(dirPath, prefix) {
			continue
		}
		rest := strings.TrimPrefix(dirPath, prefix)
		if rest == "" || strings.Contains(rest, "\\") {
			continue
		}
		mode := os.ModeDir | 0o755
		if entry, ok := s.files[dirPath]; ok {
			mode = entry.mode
		}
		seen[rest] = fakeSMBFileInfo{
			name:    rest,
			mode:    mode,
			modTime: mt,
		}
	}
	for filePath, entry := range s.files {
		if !strings.HasPrefix(filePath, prefix) {
			continue
		}
		rest := strings.TrimPrefix(filePath, prefix)
		if rest == "" || strings.Contains(rest, "\\") {
			continue
		}
		seen[rest] = fakeSMBFileInfo{
			name:    rest,
			size:    int64(len(entry.data)),
			mode:    entry.mode,
			modTime: entry.modTime,
		}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]os.FileInfo, 0, len(names))
	for _, n := range names {
		out = append(out, seen[n])
	}
	return out, nil
}

func (s *fakeSMBShare) Mkdir(name string, _ os.FileMode) error {
	s.mu.Lock()
	s.mkdirCalls = append(s.mkdirCalls, name)
	mkdirErr := s.mkdirErrPaths[name]
	s.mu.Unlock()
	if mkdirErr != nil {
		return &os.PathError{Op: "mkdir", Path: name, Err: mkdirErr}
	}
	norm, err := normalizeFakeSMBPath("mkdir", name)
	if err != nil {
		return err
	}
	if norm == "" {
		return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrExist}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if parent := smbParentPath(norm); parent != "" {
		if _, ok := s.dirs[parent]; !ok {
			return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrNotExist}
		}
	}
	if _, ok := s.dirs[norm]; ok {
		return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrExist}
	}
	if _, ok := s.files[norm]; ok {
		return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrExist}
	}
	s.dirs[norm] = time.Now()
	return nil
}

func (s *fakeSMBShare) Rename(oldpath, newpath string) error {
	s.mu.Lock()
	s.renameCalls = append(s.renameCalls, smbRenameRecord{oldpath: oldpath, newpath: newpath})
	s.mu.Unlock()

	oldNorm, err := normalizeFakeSMBPath("rename", oldpath)
	if err != nil {
		return err
	}
	newNorm, err := normalizeFakeSMBPath("rename", newpath)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.files[oldNorm]
	if !ok {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: os.ErrNotExist}
	}
	if parent := smbParentPath(newNorm); parent != "" {
		if _, ok := s.dirs[parent]; !ok {
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: os.ErrNotExist}
		}
	}
	if _, exists := s.files[newNorm]; exists {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: os.ErrExist}
	}
	delete(s.files, oldNorm)
	s.files[newNorm] = entry
	return nil
}

func (s *fakeSMBShare) Remove(name string) error {
	s.mu.Lock()
	s.removeCalls = append(s.removeCalls, name)
	s.mu.Unlock()

	norm, err := normalizeFakeSMBPath("remove", name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[norm]; ok {
		delete(s.files, norm)
		return nil
	}
	if _, ok := s.dirs[norm]; ok && norm != "" && norm != "." {
		delete(s.dirs, norm)
		return nil
	}
	return &os.PathError{Op: "remove", Path: name, Err: os.ErrNotExist}
}

func (s *fakeSMBShare) Umount() error {
	s.umountCount.Add(1)
	s.umountOnce.Do(func() { close(s.umountCh) })
	return nil
}

func (s *fakeSMBShare) getMkdirCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.mkdirCalls...)
}

func (s *fakeSMBShare) getOpenFileCalls() []smbOpenRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smbOpenRecord(nil), s.openFileCalls...)
}

func (s *fakeSMBShare) getOpenCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.openCalls...)
}

func (s *fakeSMBShare) getRenameCalls() []smbRenameRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smbRenameRecord(nil), s.renameCalls...)
}

func (s *fakeSMBShare) getReadDirCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.readDirCalls...)
}

type fakeSMBFileHandle struct {
	share    *fakeSMBShare
	norm     string
	flag     int
	readBuf  *bytes.Reader
	writeBuf bytes.Buffer
	readErr  error
	writeErr error
	closeErr error
	closed   bool
}

func (f *fakeSMBFileHandle) Read(p []byte) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	return f.readBuf.Read(p)
}

func (f *fakeSMBFileHandle) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.writeBuf.Write(p)
}

func (f *fakeSMBFileHandle) Close() error {
	if f.closed {
		return nil
	}
	f.closed = true
	if f.closeErr != nil {
		return f.closeErr
	}
	if f.flag&(os.O_WRONLY|os.O_RDWR) != 0 || f.flag&os.O_CREATE != 0 {
		f.share.mu.Lock()
		entry := f.share.files[f.norm]
		if entry != nil {
			if f.flag&os.O_APPEND != 0 {
				entry.data = append(entry.data, f.writeBuf.Bytes()...)
			} else {
				entry.data = append([]byte(nil), f.writeBuf.Bytes()...)
			}
			entry.modTime = time.Now()
		}
		f.share.mu.Unlock()
	}
	return nil
}

func TestSMBProducer_SubdirMkdirAndCloseErrorAndOAppendOExcl(t *testing.T) {
	c := NewSMBComponent()

	t.Run("subdir_mkdir_on_unix", func(t *testing.T) {
		share := newFakeSMBShare()
		ep, err := c.CreateEndpoint("smb://example.com/share/sub/dir/file.txt")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		smbEp := ep.(*SMBEndpoint)
		smbEp.connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer: %v", err)
		}
		defer func() { _ = prod.Stop() }()

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("nested payload")
		if err := prod.Send(ex); err != nil {
			t.Fatalf("Send failed: %v", err)
		}

		mkds := share.getMkdirCalls()
		var hasSub, hasSubDir, hasDot bool
		for _, m := range mkds {
			if m == "sub" {
				hasSub = true
			}
			if m == "sub\\dir" {
				hasSubDir = true
			}
			if m == "." {
				hasDot = true
			}
		}
		if !hasSub || !hasSubDir || hasDot {
			t.Fatalf("expected mkdir calls for 'sub' and 'sub\\dir' without '.', got %v", mkds)
		}
		got, ok := share.getFile("sub\\dir\\file.txt")
		if !ok || got != "nested payload" {
			t.Fatalf("expected 'nested payload' at sub\\dir\\file.txt, got %q (ok=%v)", got, ok)
		}
	})

	t.Run("propagates_file_close_error", func(t *testing.T) {
		share := newFakeSMBShare()
		share.closeErrPaths["close-fail.txt"] = errors.New("flush failed on close")
		ep, err := c.CreateEndpoint("smb://example.com/share/close-fail.txt")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		smbEp := ep.(*SMBEndpoint)
		smbEp.connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer: %v", err)
		}
		defer func() { _ = prod.Stop() }()

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("data")
		err = prod.Send(ex)
		if err == nil || !strings.Contains(err.Error(), "flush failed on close") {
			t.Fatalf("expected Send to propagate Close error, got %v", err)
		}
	})

	t.Run("fileExist_append_uses_o_append", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("append.txt", "hello")
		ep, err := c.CreateEndpoint("smb://example.com/share/?fileExist=Append")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		smbEp := ep.(*SMBEndpoint)
		smbEp.connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		prod, err := ep.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer: %v", err)
		}
		defer func() { _ = prod.Stop() }()

		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(gocamel.CamelFileName, "append.txt")
		ex.SetBody(" world")
		if err := prod.Send(ex); err != nil {
			t.Fatalf("Send append failed: %v", err)
		}
		got, ok := share.getFile("append.txt")
		if !ok || got != "hello world" {
			t.Fatalf("expected 'hello world', got %q (ok=%v)", got, ok)
		}

		// Also verify creating a new file with FileExistAppend.
		exNew := gocamel.NewExchange(context.Background())
		exNew.GetIn().SetHeader(gocamel.CamelFileName, "append-new.txt")
		exNew.SetBody("created")
		if err := prod.Send(exNew); err != nil {
			t.Fatalf("Send append-new failed: %v", err)
		}
		gotNew, ok := share.getFile("append-new.txt")
		if !ok || gotNew != "created" {
			t.Fatalf("expected 'created', got %q (ok=%v)", gotNew, ok)
		}

		if len(share.getOpenCalls()) != 0 {
			t.Fatalf("FileExistAppend must not read existing file via Open, got %v", share.getOpenCalls())
		}
		var sawAppend bool
		for _, rec := range share.getOpenFileCalls() {
			if rec.path == "append.txt" && rec.flag&os.O_APPEND != 0 && rec.flag&os.O_CREATE != 0 && rec.flag&os.O_TRUNC == 0 {
				sawAppend = true
			}
		}
		if !sawAppend {
			t.Fatalf("expected OpenFile with O_APPEND|O_CREATE without O_TRUNC, got %+v", share.getOpenFileCalls())
		}
	})

	t.Run("fileExist_fail_and_ignore_use_o_excl", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("fail.txt", "first")
		share.seedFile("ignore.txt", "keep")
		share.openErrPaths["perm.txt"] = os.ErrPermission

		epFail, err := c.CreateEndpoint("smb://example.com/share/fail.txt?fileExist=Fail")
		if err != nil {
			t.Fatalf("CreateEndpoint fail: %v", err)
		}
		epFail.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		prodFail, err := epFail.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer fail: %v", err)
		}
		defer func() { _ = prodFail.Stop() }()

		exFail := gocamel.NewExchange(context.Background())
		exFail.SetBody("overwrite")
		if err := prodFail.Send(exFail); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected already exists error for FileExistFail, got %v", err)
		}
		if got, _ := share.getFile("fail.txt"); got != "first" {
			t.Fatalf("fail.txt must remain 'first', got %q", got)
		}

		epIgn, err := c.CreateEndpoint("smb://example.com/share/ignore.txt?fileExist=Ignore")
		if err != nil {
			t.Fatalf("CreateEndpoint ignore: %v", err)
		}
		epIgn.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		prodIgn, err := epIgn.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer ignore: %v", err)
		}
		defer func() { _ = prodIgn.Stop() }()

		exIgn := gocamel.NewExchange(context.Background())
		exIgn.SetBody("overwrite")
		if err := prodIgn.Send(exIgn); err != nil {
			t.Fatalf("expected nil for FileExistIgnore on existing file, got %v", err)
		}
		if got, _ := share.getFile("ignore.txt"); got != "keep" {
			t.Fatalf("ignore.txt must remain 'keep', got %q", got)
		}

		epPerm, err := c.CreateEndpoint("smb://example.com/share/perm.txt?fileExist=Ignore")
		if err != nil {
			t.Fatalf("CreateEndpoint perm: %v", err)
		}
		epPerm.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		prodPerm, err := epPerm.CreateProducer()
		if err != nil {
			t.Fatalf("CreateProducer perm: %v", err)
		}
		defer func() { _ = prodPerm.Stop() }()
		exPerm := gocamel.NewExchange(context.Background())
		exPerm.SetBody("denied")
		if err := prodPerm.Send(exPerm); err == nil {
			t.Fatal("expected permission error to fail even with FileExistIgnore")
		}

		var sawFailExcl, sawIgnExcl bool
		for _, rec := range share.getOpenFileCalls() {
			if rec.path == "fail.txt" && rec.flag&os.O_EXCL != 0 && rec.flag&os.O_CREATE != 0 {
				sawFailExcl = true
			}
			if rec.path == "ignore.txt" && rec.flag&os.O_EXCL != 0 && rec.flag&os.O_CREATE != 0 {
				sawIgnExcl = true
			}
		}
		if !sawFailExcl || !sawIgnExcl {
			t.Fatalf("expected O_EXCL|O_CREATE for FileExistFail and FileExistIgnore, got %+v", share.getOpenFileCalls())
		}
	})
}

func TestSMBConsumer_MoveSubdirAndSkipSymlinksAndRecursiveMoveDirAndLifecycle(t *testing.T) {
	c := NewSMBComponent()

	t.Run("move_subdir_and_leading_slash_trimmed", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("folder\\file.txt", "payload-1")
		share.seedFile("folder\\locked.txt.camelExclusiveReadLock", "payload-2")
		share.seedFile("folder\\sub\\deep.txt", "payload-3")

		moveSMBFile(share, "folder\\file.txt", "done")
		if got, ok := share.getFile("done\\file.txt"); !ok || got != "payload-1" {
			t.Fatalf("expected folder\\file.txt moved to done\\file.txt, ok=%v got=%q", ok, got)
		}
		if share.exists("folder\\file.txt") {
			t.Fatal("source folder\\file.txt should no longer exist")
		}

		moveSMBFile(share, "folder\\locked.txt.camelExclusiveReadLock", "/done")
		if got, ok := share.getFile("done\\locked.txt"); !ok || got != "payload-2" {
			t.Fatalf("expected move=/done to trim leading slash/backslash and move to done\\locked.txt, ok=%v got=%q", ok, got)
		}

		moveSMBFile(share, "folder\\sub\\deep.txt", "/archive/sub/")
		if got, ok := share.getFile("archive\\sub\\deep.txt"); !ok || got != "payload-3" {
			t.Fatalf("expected move=/archive/sub/ to move to archive\\sub\\deep.txt, ok=%v got=%q", ok, got)
		}

		for _, rec := range share.getRenameCalls() {
			if strings.HasPrefix(rec.newpath, "\\") || strings.HasPrefix(rec.newpath, "/") {
				t.Fatalf("rename destination must not have leading separator: %+v", rec)
			}
		}
	})

	t.Run("listSMBFiles_skips_symlinks", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\regular.txt", "ok")
		share.seedEntry("in\\link-file.txt", []byte("secret"), os.ModeSymlink|0o666)
		share.seedEntry("in\\link-dir", nil, os.ModeDir|os.ModeSymlink|0o755)
		share.seedFile("in\\link-dir\\nested.txt", "escape")

		ep, err := c.CreateEndpoint("smb://example.com/share/in?recursive=true")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		cons, err := ep.CreateConsumer(nil)
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		smbCons := cons.(*SMBConsumer)
		files := smbCons.listSMBFiles(context.Background(), share, "in")
		if len(files) != 1 || files[0].path != "in\\regular.txt" {
			t.Fatalf("expected only [in\\regular.txt] (skipping symlinks), got %+v", files)
		}
		for _, listed := range share.getReadDirCalls() {
			if strings.Contains(listed, "link-dir") {
				t.Fatalf("must not recurse into symlinked directory %q", listed)
			}
		}
	})

	t.Run("recursive_skips_move_and_moveFailed_dirs", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\a.txt", "ok-a")
		share.seedFile("in\\sub\\b.txt", "fail-b")
		share.seedFile("in\\.done\\already.txt", "old-done")
		share.seedFile("in\\.done\\deep\\older.txt", "old-deep")
		share.seedFile("in\\.error\\failed.txt", "old-err")

		ep, err := c.CreateEndpoint("smb://example.com/share/in?recursive=true&move=/in/.done&moveFailed=/in/.error&initialDelay=0s&delay=20ms")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}

		var mu sync.Mutex
		seen := make(map[string]int)
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			mu.Lock()
			seen[name]++
			mu.Unlock()
			if name == "b.txt" {
				return errors.New("boom")
			}
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = cons.Stop() }()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if share.exists("in\\.done\\a.txt") && share.exists("in\\.error\\b.txt") && len(share.getReadDirCalls()) >= 4 {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}

		if !share.exists("in\\.done\\a.txt") || !share.exists("in\\.error\\b.txt") {
			t.Fatalf("expected a.txt in .done and b.txt in .error")
		}
		mu.Lock()
		if seen["a.txt"] != 1 || seen["b.txt"] != 1 || seen["already.txt"] != 0 || seen["older.txt"] != 0 || seen["failed.txt"] != 0 {
			t.Fatalf("unexpected seen counts: %+v", seen)
		}
		mu.Unlock()
		for _, listed := range share.getReadDirCalls() {
			if strings.HasPrefix(listed, "in\\.done") || strings.HasPrefix(listed, "in\\.error") {
				t.Fatalf("must not descend into move/moveFailed directory %q", listed)
			}
		}
	})

	t.Run("readLock_markerFile_uses_o_excl", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\free.txt", "ok")
		share.seedFile("in\\locked.txt", "skip")
		share.seedFile("in\\locked.txt.camelLock", "existing-lock")

		ep, err := c.CreateEndpoint("smb://example.com/share/in?readLock=markerFile&noop=true&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}

		var mu sync.Mutex
		var seen []string
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			mu.Lock()
			seen = append(seen, name)
			mu.Unlock()
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = cons.Stop() }()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(seen)
			mu.Unlock()
			if n == 1 && !share.exists("in\\free.txt.camelLock") {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}

		mu.Lock()
		if len(seen) != 1 || seen[0] != "free.txt" {
			t.Fatalf("expected only [free.txt], got %v", seen)
		}
		mu.Unlock()
		if lockBody, ok := share.getFile("in\\locked.txt.camelLock"); !ok || lockBody != "existing-lock" {
			t.Fatalf("existing marker file must not be overwritten, ok=%v body=%q", ok, lockBody)
		}
		var sawExclMarker bool
		for _, rec := range share.getOpenFileCalls() {
			if rec.path == "in\\free.txt.camelLock" && rec.flag&os.O_CREATE != 0 && rec.flag&os.O_EXCL != 0 {
				sawExclMarker = true
			}
		}
		if !sawExclMarker {
			t.Fatalf("expected ReadLockMarkerFile to open marker with O_CREATE|O_EXCL, got %+v", share.getOpenFileCalls())
		}
	})

	t.Run("lifecycle_transition_serialized_and_post_stop_guard_and_dial_context", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\hold.txt", "data")

		var connCount atomic.Int32
		inProc := make(chan struct{})
		releaseProc := make(chan struct{})
		var procOnce sync.Once

		ep, err := c.CreateEndpoint("smb://example.com/share/in?noop=true&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			connCount.Add(1)
			return &smbConn{share: share}, nil
		}

		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			procOnce.Do(func() {
				close(inProc)
				<-releaseProc
			})
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		sc := cons.(*SMBConsumer)
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}

		select {
		case <-inProc:
		case <-time.After(2 * time.Second):
			t.Fatal("consumer did not enter processor")
		}

		stopPending := make(chan error, 1)
		go func() { stopPending <- cons.Stop() }()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			sc.mu.Lock()
			stopping := sc.stopping
			sc.mu.Unlock()
			if stopping {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}

		startAfterStop := make(chan error, 1)
		go func() { startAfterStop <- cons.Start(context.Background()) }()
		select {
		case <-startAfterStop:
			t.Fatal("Start returned before in-progress Stop finished")
		case <-time.After(40 * time.Millisecond):
		}

		close(releaseProc)
		if err := <-stopPending; err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := <-startAfterStop; err != nil {
			t.Fatalf("Start after Stop: %v", err)
		}
		sc.mu.Lock()
		started := sc.started
		sc.mu.Unlock()
		if !started {
			t.Fatal("expected consumer to be started after Start serialized behind Stop")
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("final Stop: %v", err)
		}

		// Stopped or stopping consumer must refuse to dial in doPoll.
		connsAfterStop := connCount.Load()
		sc.doPoll(context.Background())
		if connCount.Load() != connsAfterStop {
			t.Fatalf("stopped consumer must not dial (before=%d after=%d)", connsAfterStop, connCount.Load())
		}
		sc.mu.Lock()
		sc.started = true
		sc.stopping = true
		sc.mu.Unlock()
		sc.doPoll(context.Background())
		if connCount.Load() != connsAfterStop {
			t.Fatalf("stopping consumer must not dial")
		}
		sc.mu.Lock()
		sc.started = false
		sc.stopping = false
		sc.mu.Unlock()

		// Stop unblocks stalled ReadDir via abort.
		stallShare := newFakeSMBShare()
		stallShare.stallReadDir = make(chan struct{})
		stallShare.readDirEntered = make(chan struct{})
		defer close(stallShare.stallReadDir)
		epStall, err := c.CreateEndpoint("smb://example.com/share/in?initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint stall: %v", err)
		}
		epStall.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: stallShare}, nil
		}
		stallCons, err := epStall.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		if err != nil {
			t.Fatalf("CreateConsumer stall: %v", err)
		}
		if err := stallCons.Start(context.Background()); err != nil {
			t.Fatalf("Start stall: %v", err)
		}
		select {
		case <-stallShare.readDirEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("stalled consumer did not call ReadDir")
		}
		stopStall := make(chan error, 1)
		go func() { stopStall <- stallCons.Stop() }()
		select {
		case err := <-stopStall:
			if err != nil {
				t.Fatalf("Stop stall: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Stop hung on stalled ReadDir")
		}

		// Real DialContext cancellation during stalled SMB handshake (with username so negotiate runs).
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer ln.Close()
		doneServer := make(chan struct{})
		defer close(doneServer)
		go func() {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			defer conn.Close()
			<-doneServer
		}()
		epCancel, err := c.CreateEndpoint("smb://" + ln.Addr().String() + "/share/in?username=guest&connectTimeout=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint cancel: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err = epCancel.(*SMBEndpoint).connect(ctx)
		if err == nil {
			t.Fatal("expected connect to fail when ctx cancels during stalled SMB handshake")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("connect took %v despite 50ms ctx timeout", elapsed)
		}
	})

	t.Run("ipv6_uses_net_join_host_port", func(t *testing.T) {
		epV6, err := c.CreateEndpoint("smb://[::1]/share/out?connectTimeout=60ms")
		if err != nil {
			t.Fatalf("CreateEndpoint IPv6: %v", err)
		}
		smbEpV6 := epV6.(*SMBEndpoint)
		if addr := smbEpV6.targetAddr(); addr != "[::1]:445" {
			t.Fatalf("expected targetAddr '[::1]:445', got %q", addr)
		}
		if _, err := smbEpV6.connect(context.Background()); err != nil && strings.Contains(err.Error(), "missing port in address") {
			t.Fatalf("IPv6 default port failed with missing port: %v", err)
		}

		epV6Port, err := c.CreateEndpoint("smb://[::1]:1445/share/out")
		if err != nil {
			t.Fatalf("CreateEndpoint IPv6 with port: %v", err)
		}
		if addr := epV6Port.(*SMBEndpoint).targetAddr(); addr != "[::1]:1445" {
			t.Fatalf("expected targetAddr '[::1]:1445', got %q", addr)
		}
	})

	t.Run("maxMessagesPerPoll_counts_failures", func(t *testing.T) {
		share := newFakeSMBShare()
		for i := 1; i <= 4; i++ {
			share.seedFile(fmt.Sprintf("q\\%d.txt", i), fmt.Sprintf("body-%d", i))
		}
		ep, err := c.CreateEndpoint("smb://example.com/share/q?maxMessagesPerPoll=2&delete=true&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}

		var calls atomic.Int32
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			calls.Add(1)
			return errors.New("always fail")
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = cons.Stop() }()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if calls.Load() >= 2 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(60 * time.Millisecond)
		if got := calls.Load(); got != 2 {
			t.Fatalf("expected MaxMessagesPerPoll=2 even when processor fails, got %d calls", got)
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	})
}

func TestSMBEndpoint_ValidationURIAndStalledSMBHandshake(t *testing.T) {
	c := NewSMBComponent()

	for _, badURI := range []string{
		"://bad-uri",
		"smb://example.com/share/../../etc/passwd",
		"smb://example.com/share/../other",
		"smb://example.com/share/in%0d%0abad",
		"smb://example.com/share/in?move=../outside",
		"smb://example.com/share/in?move=bad%0a",
		"smb://example.com/share/in?moveFailed=../outside",
		"smb://example.com/share/in?moveFailed=bad%0d",
	} {
		if _, err := c.CreateEndpoint(badURI); err == nil {
			t.Errorf("expected CreateEndpoint(%q) to fail", badURI)
		}
	}

	ep, err := c.CreateEndpoint("smb://alice:topsecret@example.com/share/in?password=querysecret&maxMessageSize=1024")
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	redacted := ep.URI()
	if strings.Contains(redacted, "topsecret") || strings.Contains(redacted, "querysecret") {
		t.Fatalf("URI() leaked credentials: %q", redacted)
	}
	if ep.(*SMBEndpoint).maxMessageSize != 1024 {
		t.Fatalf("expected maxMessageSize=1024, got %d", ep.(*SMBEndpoint).maxMessageSize)
	}

	// Empty share name fails before dialing.
	epNoShare, err := c.CreateEndpoint("smb://127.0.0.1:1")
	if err != nil {
		t.Fatalf("CreateEndpoint no share: %v", err)
	}
	if _, err := epNoShare.(*SMBEndpoint).connect(context.Background()); err == nil || !strings.Contains(err.Error(), "no share name") {
		t.Fatalf("expected 'no share name' error, got %v", err)
	}

	// Real stalled SMB handshake with username=guest is unblocked immediately by Stop().
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(accepted)
		<-release
	}()
	epStall, err := c.CreateEndpoint("smb://" + ln.Addr().String() + "/share/in?username=guest&connectTimeout=10s&initialDelay=0s")
	if err != nil {
		t.Fatalf("CreateEndpoint stall: %v", err)
	}
	consStall, err := epStall.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
	if err != nil {
		t.Fatalf("CreateConsumer stall: %v", err)
	}
	if err := consStall.Start(context.Background()); err != nil {
		t.Fatalf("Start stall: %v", err)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not connect")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- consStall.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop stall: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop blocked on stalled SMB handshake")
	}
}

func TestSMBProducer_EdgeCasesReconnectAndDisconnect(t *testing.T) {
	c := NewSMBComponent()
	share := newFakeSMBShare()

	// Root endpoint without filename fails, invalid CamelFileName fails, body checks fail.
	epRoot, err := c.CreateEndpoint("smb://example.com/share?maxMessageSize=8")
	if err != nil {
		t.Fatalf("CreateEndpoint root: %v", err)
	}
	var dialCount atomic.Int32
	epRoot.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
		dialCount.Add(1)
		return &smbConn{share: share}, nil
	}
	prodRoot, err := epRoot.CreateProducer()
	if err != nil {
		t.Fatalf("CreateProducer root: %v", err)
	}
	defer func() { _ = prodRoot.Stop() }()

	if err := prodRoot.Send(nil); err == nil {
		t.Fatal("expected error for nil exchange")
	}

	exNoName := gocamel.NewExchange(context.Background())
	exNoName.SetBody("ok")
	if err := prodRoot.Send(exNoName); err == nil {
		t.Fatal("expected error when CamelFileName missing")
	}

	for _, badName := range []string{"../../evil.txt", "a/../b.txt", "bad\nname.txt", "."} {
		exBad := gocamel.NewExchange(context.Background())
		exBad.GetIn().SetHeader(gocamel.CamelFileName, badName)
		exBad.SetBody("ok")
		if err := prodRoot.Send(exBad); err == nil {
			t.Fatalf("expected error for bad CamelFileName %q", badName)
		}
	}

	exBadBody := gocamel.NewExchange(context.Background())
	exBadBody.GetIn().SetHeader(gocamel.CamelFileName, "x.txt")
	exBadBody.SetBody(12345)
	if err := prodRoot.Send(exBadBody); err == nil {
		t.Fatal("expected error for unsupported body type")
	}

	exTooBig := gocamel.NewExchange(context.Background())
	exTooBig.GetIn().SetHeader(gocamel.CamelFileName, "x.txt")
	exTooBig.SetBody(strings.NewReader("0123456789"))
	if err := prodRoot.Send(exTooBig); err == nil {
		t.Fatal("expected error for oversized reader body")
	}

	if dialCount.Load() != 0 {
		t.Fatalf("validation failures must not dial, got %d dials", dialCount.Load())
	}

	// Reader body + []byte body + persistent reconnect after Stat(".") fails.
	exReader := gocamel.NewExchange(context.Background())
	exReader.GetIn().SetHeader(gocamel.CamelFileName, "nested/r.txt")
	exReader.SetBody(strings.NewReader("reader"))
	if err := prodRoot.Send(exReader); err != nil {
		t.Fatalf("Send reader: %v", err)
	}
	if got, ok := share.getFile("nested\\r.txt"); !ok || got != "reader" {
		t.Fatalf("expected 'reader', ok=%v got=%q", ok, got)
	}

	share.mu.Lock()
	share.statErr = errors.New("broken pipe")
	share.mu.Unlock()
	exBytes := gocamel.NewExchange(context.Background())
	exBytes.GetIn().SetHeader(gocamel.CamelFileName, "b.txt")
	exBytes.SetBody([]byte("bytes"))
	if err := prodRoot.Send(exBytes); err != nil {
		t.Fatalf("Send after Stat failure: %v", err)
	}
	if dialCount.Load() != 2 {
		t.Fatalf("expected reconnect after Stat error (2 dials), got %d", dialCount.Load())
	}
	share.mu.Lock()
	share.statErr = nil
	share.mu.Unlock()

	// Write error & Mkdir error propagation.
	share.mu.Lock()
	share.writeErrPaths["werr.txt"] = errors.New("disk full")
	share.mkdirErrPaths["badparent"] = os.ErrPermission
	share.mu.Unlock()

	exWriteErr := gocamel.NewExchange(context.Background())
	exWriteErr.GetIn().SetHeader(gocamel.CamelFileName, "werr.txt")
	exWriteErr.SetBody("data")
	if err := prodRoot.Send(exWriteErr); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected write error, got %v", err)
	}

	exMkdirErr := gocamel.NewExchange(context.Background())
	exMkdirErr.GetIn().SetHeader(gocamel.CamelFileName, "badparent/f.txt")
	exMkdirErr.SetBody("data")
	if err := prodRoot.Send(exMkdirErr); err == nil {
		t.Fatal("expected mkdir permission error")
	}

	// Stop -> Send fails -> Start(nil) re-enables -> Send succeeds.
	if err := prodRoot.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	//lint:ignore SA1012 verify Start handles nil context cleanly
	if err := prodRoot.Start(nil); err != nil {
		t.Fatalf("Start(nil): %v", err)
	}
	exAfterRestart := gocamel.NewExchange(context.Background())
	exAfterRestart.GetIn().SetHeader(gocamel.CamelFileName, "after.txt")
	exAfterRestart.SetBody("ok")
	if err := prodRoot.Send(exAfterRestart); err != nil {
		t.Fatalf("Send after Start(nil): %v", err)
	}

	// disconnect=true dials and umounts per Send.
	discShare := newFakeSMBShare()
	var discDials atomic.Int32
	epDisc, err := c.CreateEndpoint("smb://example.com/share/disc.txt?disconnect=true")
	if err != nil {
		t.Fatalf("CreateEndpoint disconnect: %v", err)
	}
	epDisc.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
		discDials.Add(1)
		return &smbConn{share: discShare}, nil
	}
	prodDisc, err := epDisc.CreateProducer()
	if err != nil {
		t.Fatalf("CreateProducer disconnect: %v", err)
	}
	defer func() { _ = prodDisc.Stop() }()
	exDisc := gocamel.NewExchange(context.Background())
	exDisc.SetBody("one")
	if err := prodDisc.Send(exDisc); err != nil {
		t.Fatalf("Send disconnect 1: %v", err)
	}
	if err := prodDisc.Send(exDisc); err != nil {
		t.Fatalf("Send disconnect 2: %v", err)
	}
	if discDials.Load() != 2 || discShare.umountCount.Load() != 2 {
		t.Fatalf("expected 2 dials and 2 umounts, got dials=%d umounts=%d", discDials.Load(), discShare.umountCount.Load())
	}

	// Context cancellation waiting for producer lease.
	leaseProd := prodDisc.(*SMBProducer)
	if err := leaseProd.acquire(context.Background()); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	exCanceled := gocamel.NewExchange(cancelCtx)
	exCanceled.SetBody("x")
	if err := prodDisc.Send(exCanceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled while lease held, got %v", err)
	}
	leaseProd.release()

	// Trailing-slash subdir URI must reject dot CamelFileName values instead of
	// collapsing "subdir/." to "subdir" and treating the directory as a file.
	epSubdir, err := c.CreateEndpoint("smb://example.com/share/subdir/")
	if err != nil {
		t.Fatalf("CreateEndpoint subdir: %v", err)
	}
	var subdirDials atomic.Int32
	epSubdir.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
		subdirDials.Add(1)
		return &smbConn{share: newFakeSMBShare()}, nil
	}
	prodSubdir, err := epSubdir.CreateProducer()
	if err != nil {
		t.Fatalf("CreateProducer subdir: %v", err)
	}
	defer func() { _ = prodSubdir.Stop() }()
	for _, badDotName := range []string{".", "./", "./.", `.\.`} {
		exDot := gocamel.NewExchange(context.Background())
		exDot.GetIn().SetHeader(gocamel.CamelFileName, badDotName)
		exDot.SetBody("bad")
		if err := prodSubdir.Send(exDot); err == nil {
			t.Fatalf("expected error for subdir trailing-slash URI with CamelFileName=%q", badDotName)
		}
	}
	if subdirDials.Load() != 0 {
		t.Fatalf("expected zero dials on invalid dot CamelFileName, got %d", subdirDials.Load())
	}

	// realSMBShare must return an untyped nil smbFileHandle on error, never (*smb2.File)(nil).
	realShare := realSMBShare{Share: &smb2.Share{}}
	if f, err := realShare.Open(`\leading`); err == nil || f != nil {
		t.Fatalf("realSMBShare.Open error must return untyped nil handle, got f=%#v (%T), err=%v", f, f, err)
	}
	if f, err := realShare.OpenFile(`\leading`, os.O_RDONLY, 0); err == nil || f != nil {
		t.Fatalf("realSMBShare.OpenFile error must return untyped nil handle, got f=%#v (%T), err=%v", f, f, err)
	}
}

func TestSMBConsumer_ReadLocksAndPostProcessingEdgeCases(t *testing.T) {
	c := NewSMBComponent()

	t.Run("disconnect_true_filtering_unsafe_names_and_move_root", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\keep.txt", "kept")
		share.seedFile("in\\skip.tmp", "tmp")
		share.seedFile("in\\x.camelLock", "lock")
		share.seedFile("in\\y.camelExclusiveReadLock", "lock2")
		share.seedFile("in\\..evil", "ok-dotdot-prefix")

		// Also verify unsafe entry names returned directly by ReadDir are skipped.
		ep, err := c.CreateEndpoint("smb://example.com/share/in/?disconnect=true&move=/&exclude=\\.tmp$&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}

		var mu sync.Mutex
		var got []string
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			mu.Lock()
			got = append(got, name)
			mu.Unlock()
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = cons.Stop() }()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if share.exists("keep.txt") && !share.exists("in\\keep.txt") {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if !share.exists("keep.txt") {
			t.Fatal("expected keep.txt moved to share root without leading backslash")
		}
		if !share.exists("in\\skip.tmp") {
			t.Fatal("excluded .tmp file should remain")
		}
		mu.Lock()
		if len(got) != 2 {
			t.Fatalf("expected keep.txt and ..evil, got %v", got)
		}
		mu.Unlock()
	})

	t.Run("readLock_rename_errStopRouting_failure_restore_and_noop", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\r1.txt", "one")
		share.seedFile("in\\r2.txt", "two")
		share.seedFile("in\\busy.txt", "busy")
		share.seedFile("in\\busy.txt.camelExclusiveReadLock", "held")

		ep, err := c.CreateEndpoint("smb://example.com/share/in?readLock=rename&delete=true&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			if name == "r1.txt" {
				return gocamel.ErrStopRouting
			}
			return errors.New("fail-r2")
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = cons.Stop() }()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if !share.exists("in\\r1.txt") && share.exists("in\\r2.txt") && !share.exists("in\\r2.txt.camelExclusiveReadLock") {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if share.exists("in\\r1.txt") {
			t.Fatal("ErrStopRouting should delegate to OnComplete and delete r1.txt")
		}
		if !share.exists("in\\r2.txt") || share.exists("in\\r2.txt.camelExclusiveReadLock") {
			t.Fatal("failed exchange should restore r2.txt from lock file")
		}
		if held, _ := share.getFile("in\\busy.txt.camelExclusiveReadLock"); held != "held" {
			t.Fatalf("busy lock must not be overwritten, got %q", held)
		}

		// noop=true with readLock=rename restores lock file.
		epNoop, err := c.CreateEndpoint("smb://example.com/share/in?readLock=rename&noop=true&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint noop: %v", err)
		}
		epNoop.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		var seenNoop atomic.Int32
		consNoop, err := epNoop.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			seenNoop.Add(1)
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer noop: %v", err)
		}
		if err := consNoop.Start(context.Background()); err != nil {
			t.Fatalf("Start noop: %v", err)
		}
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if seenNoop.Load() >= 1 && share.exists("in\\r2.txt") && !share.exists("in\\r2.txt.camelExclusiveReadLock") {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := consNoop.Stop(); err != nil {
			t.Fatalf("Stop noop: %v", err)
		}
	})

	t.Run("readLock_changed_maxMessageSize_persistent_reconnect_and_errors", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("small.txt", "ok")
		share.seedFile("big.txt", "0123456789abcdef")

		ep, err := c.CreateEndpoint("smb://example.com/share/?readLock=changed&readLockCheckInterval=10ms&readLockTimeout=300ms&maxMessageSize=5&delete=true&initialDelay=0s&delay=20ms")
		if err != nil {
			t.Fatalf("CreateEndpoint: %v", err)
		}
		var dials atomic.Int32
		ep.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			dials.Add(1)
			return &smbConn{share: share}, nil
		}

		var mu sync.Mutex
		var got []string
		cons, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			mu.Lock()
			got = append(got, name)
			mu.Unlock()
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer: %v", err)
		}
		if err := cons.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = cons.Stop() }()

		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if !share.exists("small.txt") {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}

		// Force persistent reconnect via Stat(".") error and seed second small file.
		share.mu.Lock()
		share.statErr = errors.New("session expired")
		share.mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		share.mu.Lock()
		share.statErr = nil
		share.mu.Unlock()
		share.seedFile("second.txt", "hi")

		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if !share.exists("second.txt") {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		if err := cons.Stop(); err != nil {
			t.Fatalf("Stop: %v", err)
		}

		mu.Lock()
		if len(got) != 2 || got[0] != "small.txt" || got[1] != "second.txt" {
			t.Fatalf("expected [small.txt second.txt], got %v", got)
		}
		mu.Unlock()
		if !share.exists("big.txt") {
			t.Fatal("oversized big.txt must remain untouched")
		}
		if dials.Load() < 2 {
			t.Fatalf("expected reconnect after Stat error, dials=%d", dials.Load())
		}
	})

	t.Run("marker_close_error_open_error_and_parent_ctx_cancel_and_nil_ctx", func(t *testing.T) {
		share := newFakeSMBShare()
		share.seedFile("in\\bad-marker.txt", "a")
		share.closeErrPaths["in\\bad-marker.txt.camelLock"] = errors.New("close err")

		epMarker, err := c.CreateEndpoint("smb://example.com/share/in?readLock=markerFile&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint marker: %v", err)
		}
		epMarker.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: share}, nil
		}
		consMarker, err := epMarker.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			t.Fatal("processor must not be called when marker close fails")
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer marker: %v", err)
		}
		consMarker.(*SMBConsumer).started = true
		consMarker.(*SMBConsumer).doPoll(context.Background())
		if share.exists("in\\bad-marker.txt.camelLock") {
			t.Fatal("marker file should be removed when marker Close fails")
		}

		// Open error with readLock=rename restores lock file and does not invoke processor.
		shareOpenErr := newFakeSMBShare()
		shareOpenErr.seedFile("in\\unopenable.txt", "b")
		shareOpenErr.openErrPaths["in\\unopenable.txt.camelExclusiveReadLock"] = os.ErrPermission
		epOpenErr, err := c.CreateEndpoint("smb://example.com/share/in?readLock=rename&initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint openErr: %v", err)
		}
		epOpenErr.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: shareOpenErr}, nil
		}
		consOpenErr, err := epOpenErr.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			t.Fatal("processor must not be called when Open fails")
			return nil
		}))
		if err != nil {
			t.Fatalf("CreateConsumer openErr: %v", err)
		}
		consOpenErr.(*SMBConsumer).started = true
		consOpenErr.(*SMBConsumer).doPoll(context.Background())
		if !shareOpenErr.exists("in\\unopenable.txt") || shareOpenErr.exists("in\\unopenable.txt.camelExclusiveReadLock") {
			t.Fatal("lock file should be restored when Open fails")
		}

		// Start(nil) with Delay <= 0 must not panic, and parent ctx cancel exits cleanly.
		stallShare := newFakeSMBShare()
		stallShare.stallReadDir = make(chan struct{})
		stallShare.readDirEntered = make(chan struct{})
		defer close(stallShare.stallReadDir)
		epZero, err := c.CreateEndpoint("smb://example.com/share/in")
		if err != nil {
			t.Fatalf("CreateEndpoint zero: %v", err)
		}
		epZero.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: stallShare}, nil
		}
		zeroCons := &SMBConsumer{
			endpoint:  epZero.(*SMBEndpoint),
			processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
			opts:      gocamel.PollingOptions{Delay: 0, InitialDelay: -1 * time.Second},
		}
		//lint:ignore SA1012 intentionally verify Start(nil) does not panic
		if err := zeroCons.Start(nil); err != nil {
			t.Fatalf("Start(nil): %v", err)
		}
		select {
		case <-stallShare.readDirEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("zeroDelay consumer did not poll")
		}
		if err := zeroCons.Stop(); err != nil {
			t.Fatalf("Stop zeroDelay: %v", err)
		}

		// Parent context cancel unblocks stalled ReadDir via context.AfterFunc.
		stallShare2 := newFakeSMBShare()
		stallShare2.stallReadDir = make(chan struct{})
		stallShare2.readDirEntered = make(chan struct{})
		defer close(stallShare2.stallReadDir)
		epParent, err := c.CreateEndpoint("smb://example.com/share/in?initialDelay=0s&delay=5s")
		if err != nil {
			t.Fatalf("CreateEndpoint parent: %v", err)
		}
		epParent.(*SMBEndpoint).connectFn = func(_ context.Context, _ func(net.Conn)) (*smbConn, error) {
			return &smbConn{share: stallShare2}, nil
		}
		parentCons, err := epParent.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		if err != nil {
			t.Fatalf("CreateConsumer parent: %v", err)
		}
		pCtx, pCancel := context.WithCancel(context.Background())
		if err := parentCons.Start(pCtx); err != nil {
			t.Fatalf("Start parent: %v", err)
		}
		select {
		case <-stallShare2.readDirEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("parent consumer did not enter ReadDir")
		}
		pCancel()
		if err := parentCons.Stop(); err != nil {
			t.Fatalf("Stop after parent cancel: %v", err)
		}
	})
}
