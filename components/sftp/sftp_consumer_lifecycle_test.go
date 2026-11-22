package sftp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

func TestSFTPConsumer_StopWaitsForPollingWorker(t *testing.T) {
	consumer := &SFTPConsumer{}
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

func TestSFTPProducer_SendAfterStopFailsWithoutDialing(t *testing.T) {
	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint("sftp://127.0.0.1:1/file.txt?strictHostKeyChecking=false&connectTimeout=100ms")
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

func TestSFTPConsumer_ConcurrentStartStop(t *testing.T) {
	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint("sftp://127.0.0.1:1/incoming?strictHostKeyChecking=false&initialDelay=5s")
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

func TestSFTPConsumer_StopUnblocksStalledHandshake(t *testing.T) {
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

	c := NewSFTPComponent()
	ep, err := c.CreateEndpoint(fmt.Sprintf(
		"sftp://%s:%s/inbox?connectTimeout=10000&initialDelay=0s&strictHostKeyChecking=false", host, port))
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

func TestSFTPConsumer_SerializedLifecycleAtomicMarkerLockNoPosixClobberAndRecursiveSkipMove(t *testing.T) {
	c := NewSFTPComponent()

	t.Run("serialized_start_stop_and_no_post_stop_reconnect", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addDir(t, "/in")
		srv.addFile(t, "/in/hold.txt", "data")

		inProc := make(chan struct{})
		releaseProc := make(chan struct{})
		var procCount atomic.Int32
		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?noop=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			if procCount.Add(1) == 1 {
				close(inProc)
				<-releaseProc
			}
			return nil
		}))
		require.NoError(t, err)
		sc := consumer.(*SFTPConsumer)

		require.NoError(t, consumer.Start(context.Background()))
		select {
		case <-inProc:
		case <-time.After(2 * time.Second):
			t.Fatal("consumer did not enter processor")
		}

		// Two concurrent Stop() calls must both wait for the worker to finish.
		stop1Done := make(chan error, 1)
		stop2Done := make(chan error, 1)
		go func() { stop1Done <- consumer.Stop() }()
		require.Eventually(t, func() bool {
			sc.mu.Lock()
			defer sc.mu.Unlock()
			return sc.stopping
		}, time.Second, 5*time.Millisecond)

		go func() { stop2Done <- consumer.Stop() }()
		select {
		case <-stop2Done:
			t.Fatal("concurrent Stop returned while worker was still active")
		case <-time.After(40 * time.Millisecond):
		}

		close(releaseProc)
		require.NoError(t, <-stop1Done)
		require.NoError(t, <-stop2Done)

		// Start() racing an in-progress Stop() must serialize after Stop() and leave started=true.
		inProc2 := make(chan struct{})
		releaseProc2 := make(chan struct{})
		var proc2Once sync.Once
		ep2, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?noop=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
		require.NoError(t, err)
		consumer2, err := ep2.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			proc2Once.Do(func() {
				close(inProc2)
				<-releaseProc2
			})
			return nil
		}))
		require.NoError(t, err)
		sc2 := consumer2.(*SFTPConsumer)

		require.NoError(t, consumer2.Start(context.Background()))
		select {
		case <-inProc2:
		case <-time.After(2 * time.Second):
			t.Fatal("consumer2 did not enter processor")
		}

		stopPending := make(chan error, 1)
		go func() { stopPending <- consumer2.Stop() }()
		require.Eventually(t, func() bool {
			sc2.mu.Lock()
			defer sc2.mu.Unlock()
			return sc2.stopping
		}, time.Second, 5*time.Millisecond)

		startAfterStop := make(chan error, 1)
		go func() { startAfterStop <- consumer2.Start(context.Background()) }()
		select {
		case <-startAfterStop:
			t.Fatal("Start returned before in-progress Stop finished")
		case <-time.After(40 * time.Millisecond):
		}

		close(releaseProc2)
		require.NoError(t, <-stopPending)
		require.NoError(t, <-startAfterStop)

		sc2.mu.Lock()
		startedAfterRace := sc2.started
		sc2.mu.Unlock()
		assert.True(t, startedAfterRace, "Start serialized behind Stop must leave consumer started")
		require.NoError(t, consumer2.Stop())

		// Stopped or stopping consumer must refuse to dial in doPoll/getClients.
		connsAfterStop := srv.connCount.Load()
		sc2.doPoll(context.Background())
		assert.Equal(t, connsAfterStop, srv.connCount.Load(), "must not dial when consumer is stopped")

		sc2.mu.Lock()
		sc2.started = true
		sc2.stopping = true
		sc2.mu.Unlock()
		sc2.doPoll(context.Background())
		assert.Equal(t, connsAfterStop, srv.connCount.Load(), "must not dial while consumer is stopping")

		sc2.mu.Lock()
		sc2.stopping = false
		sc2.started = false
		sc2.mu.Unlock()

		// Start(nil) and Delay <= 0 must not panic.
		zeroDelayConsumer := &SFTPConsumer{
			endpoint:  ep2.(*SFTPEndpoint),
			processor: gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }),
			opts:      gocamel.PollingOptions{Delay: 0, InitialDelay: 0},
		}
		//lint:ignore SA1012 intentionally verify Start handles nil context without panicking
		require.NoError(t, zeroDelayConsumer.Start(nil))
		require.NoError(t, zeroDelayConsumer.Stop())

		// Stop unblocks a consumer stalled inside List/ReadDir.
		stallCh := make(chan struct{})
		defer close(stallCh)
		srv.handler.mu.Lock()
		srv.handler.stallListCh = stallCh
		srv.handler.mu.Unlock()
		epStall, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
		require.NoError(t, err)
		stallConsumer, err := epStall.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, stallConsumer.Start(context.Background()))
		require.Eventually(t, func() bool {
			return len(srv.listedDirs()) > 0
		}, 2*time.Second, 10*time.Millisecond)
		stopStallDone := make(chan error, 1)
		go func() { stopStallDone <- stallConsumer.Stop() }()
		select {
		case err := <-stopStallDone:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Stop hung on stalled SFTP directory listing")
		}
	})

	t.Run("readLock_markerFile_uses_o_excl", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/free.txt", "ok")
		srv.addFile(t, "/in/locked.txt", "skip-me")
		srv.addFile(t, "/in/locked.txt.camelLock", "existing-lock")
		srv.clearRecords()

		var seen []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?readLock=markerFile&noop=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
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
			return len(seen) == 1 && !srv.exists("/in/free.txt.camelLock")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"free.txt"}, seen)
		mu.Unlock()
		lockContent, ok := srv.getFile("/in/locked.txt.camelLock")
		require.True(t, ok)
		assert.Equal(t, "existing-lock", lockContent, "existing marker lock must not be clobbered")

		var sawExclMarker bool
		for _, rec := range srv.openRecords() {
			if rec.path == "/in/free.txt.camelLock" && rec.flags.Write && rec.flags.Creat && rec.flags.Excl {
				sawExclMarker = true
			}
		}
		assert.True(t, sawExclMarker, "ReadLockMarkerFile must create marker with O_EXCL")
	})

	t.Run("readLock_rename_uses_rename_without_posix_clobber", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/free.txt", "ok")
		srv.addFile(t, "/in/busy.txt", "busy-data")
		srv.addFile(t, "/in/busy.txt.camelExclusiveReadLock", "held-by-other")
		srv.clearRecords()

		var seen []string
		var mu sync.Mutex
		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?readLock=rename&delete=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
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
			return !srv.exists("/in/free.txt") && !srv.exists("/in/free.txt.camelExclusiveReadLock")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		mu.Lock()
		assert.Equal(t, []string{"free.txt"}, seen)
		mu.Unlock()
		held, ok := srv.getFile("/in/busy.txt.camelExclusiveReadLock")
		require.True(t, ok)
		assert.Equal(t, "held-by-other", held, "ReadLockRename must not clobber an existing lock file")
		assert.True(t, srv.exists("/in/busy.txt"), "busy.txt must remain untouched when lock already exists")

		var sawRename bool
		for _, cmd := range srv.cmdCalls() {
			assert.NotEqual(t, "PosixRename", cmd.method, "ReadLockRename must not use PosixRename")
			if cmd.method == "Rename" && cmd.path == "/in/free.txt" {
				sawRename = true
			}
		}
		assert.True(t, sawRename, "ReadLockRename must use standard Rename")
	})

	t.Run("moveSFTPFile_falls_back_to_rename_on_op_unsupported", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/move.txt", "moved-content")
		srv.handler.mu.Lock()
		srv.handler.posixRenameErr = sftp.ErrSSHFxOpUnsupported
		srv.handler.mu.Unlock()
		srv.clearRecords()

		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?move=/done&strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			return srv.exists("/done/move.txt") && !srv.exists("/in/move.txt")
		}, 2*time.Second, 15*time.Millisecond)
		require.NoError(t, consumer.Stop())

		got, ok := srv.getFile("/done/move.txt")
		require.True(t, ok)
		assert.Equal(t, "moved-content", got)
	})

	t.Run("recursive_skips_move_and_moveFailed_dirs", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		srv.addFile(t, "/in/a.txt", "ok-a")
		srv.addFile(t, "/in/sub/b.txt", "fail-b")
		srv.addFile(t, "/in/.done/already.txt", "old-done")
		srv.addFile(t, "/in/.done/deep/older.txt", "old-deep")
		srv.addFile(t, "/in/.error/failed.txt", "old-err")
		srv.clearRecords()

		var processedMu sync.Mutex
		seen := make(map[string]int)
		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/in?recursive=true&move=/in/.done&moveFailed=/in/.error&strictHostKeyChecking=false&initialDelay=0s&delay=30ms", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
			name, _ := ex.GetIn().GetHeaderAsString(gocamel.CamelFileName)
			processedMu.Lock()
			seen[name]++
			processedMu.Unlock()
			if name == "b.txt" {
				return errors.New("boom")
			}
			return nil
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			return srv.exists("/in/.done/a.txt") && srv.exists("/in/.error/b.txt")
		}, 3*time.Second, 20*time.Millisecond)
		require.Eventually(t, func() bool {
			return len(srv.listedDirs()) >= 4
		}, 2*time.Second, 20*time.Millisecond)
		require.NoError(t, consumer.Stop())

		processedMu.Lock()
		assert.Equal(t, 1, seen["a.txt"])
		assert.Equal(t, 1, seen["b.txt"])
		assert.Zero(t, seen["already.txt"])
		assert.Zero(t, seen["older.txt"])
		assert.Zero(t, seen["failed.txt"])
		processedMu.Unlock()

		for _, listed := range srv.listedDirs() {
			assert.False(t, strings.HasPrefix(listed, "/in/.done"), "must not descend into move dir %q", listed)
			assert.False(t, strings.HasPrefix(listed, "/in/.error"), "must not descend into moveFailed dir %q", listed)
		}
	})

	t.Run("posix_paths_and_ipv6_default_port", func(t *testing.T) {
		epDefaultV6, err := c.CreateEndpoint("sftp://[::1]/in?strictHostKeyChecking=false&connectTimeout=100ms")
		require.NoError(t, err)
		prodDefaultV6, err := epDefaultV6.CreateProducer()
		require.NoError(t, err)
		t.Cleanup(func() { _ = prodDefaultV6.Stop() })
		exV6Probe := gocamel.NewExchange(context.Background())
		exV6Probe.SetBody("probe")
		if err := prodDefaultV6.Send(exV6Probe); err != nil {
			assert.NotContains(t, err.Error(), "missing port in address")
		}

		srv6 := newFakeSFTPServer(t, "tcp6", "[::1]:0")
		epV6, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/nested/dir/file.txt?strictHostKeyChecking=false", srv6.addr()))
		require.NoError(t, err)
		prodV6, err := epV6.CreateProducer()
		require.NoError(t, err)
		exV6 := gocamel.NewExchange(context.Background())
		exV6.SetBody("ipv6-posix")
		require.NoError(t, prodV6.Send(exV6))
		require.NoError(t, prodV6.Stop())
		got, ok := srv6.getFile("/nested/dir/file.txt")
		require.True(t, ok)
		assert.Equal(t, "ipv6-posix", got)
	})

	t.Run("maxMessagesPerPoll_counts_failures", func(t *testing.T) {
		srv := newFakeSFTPServer(t, "tcp", "127.0.0.1:0")
		for i := 1; i <= 4; i++ {
			srv.addFile(t, fmt.Sprintf("/q/%d.txt", i), fmt.Sprintf("body-%d", i))
		}

		var calls atomic.Int32
		ep, err := c.CreateEndpoint(fmt.Sprintf("sftp://%s/q?maxMessagesPerPoll=2&delete=true&strictHostKeyChecking=false&initialDelay=0s&delay=5s", srv.addr()))
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			calls.Add(1)
			return errors.New("always fail")
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		t.Cleanup(func() { _ = consumer.Stop() })

		require.Eventually(t, func() bool {
			return calls.Load() >= 2
		}, 2*time.Second, 10*time.Millisecond)
		assert.Equal(t, int32(2), calls.Load())
		assert.Never(t, func() bool {
			return calls.Load() > 2
		}, 80*time.Millisecond, 10*time.Millisecond)
		require.NoError(t, consumer.Stop())
	})
}
