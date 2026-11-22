package file

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A relative move/moveFailed directory is resolved next to the consumed file,
// not against the working directory of the process.
func TestFileConsumer_RelativeMoveIsNextToTheFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"good.txt", "bad.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ep, err := NewFileComponent().CreateEndpoint("file://" + dir + "?readLock=none&move=.done&moveFailed=.error")
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		if name, _ := ex.GetHeaderAsString(gocamel.CamelFileName); name == "bad.txt" {
			return os.ErrInvalid
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer consumer.Stop()
	t.Cleanup(func() { _ = os.RemoveAll(".done"); _ = os.RemoveAll(".error") })

	waitFor(t, "good.txt in .done", func() bool { return exists(filepath.Join(dir, ".done", "good.txt")) })
	waitFor(t, "bad.txt in .error", func() bool { return exists(filepath.Join(dir, ".error", "bad.txt")) })
	if exists(".done") || exists(".error") {
		t.Error("move directories must not be created in the working directory")
	}
}

// With recursive=true the move directory lies inside the watched tree: the
// files moved there must not be consumed again.
func TestFileConsumer_RecursiveDoesNotReingestMovedFiles(t *testing.T) {
	dir := t.TempDir()
	ep, err := NewFileComponent().CreateEndpoint("file://" + dir + "?readLock=none&recursive=true&move=.done")
	if err != nil {
		t.Fatal(err)
	}
	var processed atomic.Int32
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		processed.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer consumer.Stop()

	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		waitFor(t, name+" in .done", func() bool { return exists(filepath.Join(dir, ".done", name)) })
	}
	// Let any event raised by the moves reach the consumer.
	<-time.After(300 * time.Millisecond)
	if got := processed.Load(); got != 2 {
		t.Errorf("processed %d exchanges, want 2", got)
	}
	if exists(filepath.Join(dir, ".done", ".done")) {
		t.Error("a moved file was consumed again")
	}
}

// Start must not route the files already in the directory itself: a backlog
// would keep the route, and the routes after it, from starting.
func TestFileConsumer_StartDoesNotWaitForTheBacklog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backlog.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	ep, err := NewFileComponent().CreateEndpoint("file://" + dir + "?readLock=none")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		close(entered)
		<-release
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan error, 1)
	go func() { started <- consumer.Start(context.Background()) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Start blocked on the processing of the existing files")
	}

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the existing file was not consumed")
	}
	close(release)
	if err := consumer.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestInPostProcessDir(t *testing.T) {
	cases := []struct {
		rootDir, path, moveDir string
		want                   bool
	}{
		{"/in", "/in/.done/a.txt", ".done", true},
		{"/in", "/in/sub/.done/a.txt", ".done", true},
		{"/in", "/in/a.txt", ".done", false},
		{"/in", "/in/x.done/a.txt", ".done", false},
		{"/in", "/in/a.txt", "", false},
		{"/in", "/in/a.txt", ".", false},
		{"/in", "/archive/a.txt", "/archive", true},
		{"/in", "/archive/sub/a.txt", "/archive", false},
		{"/in/.done", "/in/.done/a.txt", ".done", false},
		{"/in/.done", "/in/.done/.done/a.txt", ".done", true},
		{"/in/sub", "/in/sub/done/a.txt", "sub/done", false},
		{"/in", "/in/sub/done/a.txt", "sub/done", true},
		{"/in", "/outside/.done/a.txt", ".done", false},
	}
	for _, tc := range cases {
		if got := inPostProcessDir(filepath.FromSlash(tc.rootDir), filepath.FromSlash(tc.path), filepath.FromSlash(tc.moveDir)); got != tc.want {
			t.Errorf("inPostProcessDir(%q, %q, %q) = %v, want %v", tc.rootDir, tc.path, tc.moveDir, got, tc.want)
		}
	}
}

func TestFileConsumer_RejectsSymlinkSwapDuringWaitForStable(t *testing.T) {
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("outside-secret"), 0o600))

	parent := t.TempDir()
	watchDir := filepath.Join(parent, "inbox")
	require.NoError(t, os.MkdirAll(watchDir, 0o755))

	target := filepath.Join(watchDir, "swap.txt")
	require.NoError(t, os.WriteFile(target, nil, 0o600))

	ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=changed&readLockMinLength=1&readLockCheckInterval=20ms&readLockTimeout=600ms&readLockMinAge=0s")
	require.NoError(t, err)

	var processed atomic.Int32
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)

	fc := consumer.(*FileConsumer)
	opts := gocamel.ParsePollingOptions(fc.url)
	opts.ReadLock = gocamel.ReadLockChanged
	opts.ReadLockCheckInterval = 20 * time.Millisecond

	done := make(chan struct{})
	go func() {
		defer close(done)
		fc.ingest(context.Background(), target, opts, true)
	}()

	// Wait until WaitForStable is actively polling on the 0-byte file, then
	// atomically swap target with a symlink pointing outside watchDir.
	time.Sleep(50 * time.Millisecond)
	tmpLink := filepath.Join(parent, "swap.link")
	require.NoError(t, os.Symlink(outsideFile, tmpLink))
	require.NoError(t, os.Rename(tmpLink, target))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ingest timed out")
	}
	assert.Zero(t, processed.Load(), "symlink swapped during WaitForStable must not be ingested")
}

func TestFileConsumer_EnforcesMaxSizeAfterWaitForStable(t *testing.T) {
	watchDir := t.TempDir()
	target := filepath.Join(watchDir, "grow.bin")
	require.NoError(t, os.WriteFile(target, nil, 0o600))

	ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=changed&readLockMinLength=1&readLockCheckInterval=20ms&readLockTimeout=600ms&readLockMinAge=0s")
	require.NoError(t, err)

	var processed atomic.Int32
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)

	fc := consumer.(*FileConsumer)
	opts := gocamel.ParsePollingOptions(fc.url)
	opts.ReadLock = gocamel.ReadLockChanged
	opts.ReadLockCheckInterval = 20 * time.Millisecond

	done := make(chan struct{})
	go func() {
		defer close(done)
		fc.ingest(context.Background(), target, opts, true)
	}()

	// Grow the 0-byte file past DefaultMaxBodySize while WaitForStable polls.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.Truncate(target, gocamel.DefaultMaxBodySize+1))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ingest timed out")
	}
	assert.Zero(t, processed.Load(), "file growing past DefaultMaxBodySize during WaitForStable must be rejected")
}

func TestFileConsumer_RenameReadLockUnreadableDoesNotSpin(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores 0000 file permissions")
	}

	watchDir := t.TempDir()
	stagingDir := t.TempDir()
	target := filepath.Join(watchDir, "unreadable.txt")
	lockTarget := target + ".camelExclusiveReadLock"

	observer, err := fsnotify.NewWatcher()
	require.NoError(t, err)
	defer observer.Close()
	require.NoError(t, observer.Add(watchDir))

	var createsOnFile, createsOnLock atomic.Int32
	obsDone := make(chan struct{})
	obsCtx, obsCancel := context.WithCancel(context.Background())
	defer func() {
		obsCancel()
		<-obsDone
	}()
	go func() {
		defer close(obsDone)
		for {
			select {
			case <-obsCtx.Done():
				return
			case ev, ok := <-observer.Events:
				if !ok {
					return
				}
				if ev.Op&fsnotify.Create == 0 {
					continue
				}
				switch ev.Name {
				case target:
					createsOnFile.Add(1)
				case lockTarget:
					createsOnLock.Add(1)
				}
			case <-observer.Errors:
			}
		}
	}()

	ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=rename")
	require.NoError(t, err)

	ready := make(chan struct{})
	var processed atomic.Int32
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		if name, _ := ex.GetHeaderAsString(gocamel.CamelFileName); name == "ready.txt" {
			close(ready)
			return nil
		}
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)
	// Place a canary file before Start so we know the initial startup Walk has
	// completed before introducing the 0000 file (eliminates double-trigger
	// between initial Walk and fsnotify.Create).
	require.NoError(t, os.WriteFile(filepath.Join(watchDir, "ready.txt"), []byte("ok"), 0o600))
	require.NoError(t, consumer.Start(context.Background()))
	defer consumer.Stop()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for startup walk canary")
	}

	staged := filepath.Join(stagingDir, "unreadable.txt")
	require.NoError(t, os.WriteFile(staged, []byte("secret"), 0o600))
	require.NoError(t, os.Chmod(staged, 0o000))
	t.Cleanup(func() {
		_ = os.Chmod(target, 0o600)
		_ = os.Chmod(lockTarget, 0o600)
	})
	require.NoError(t, os.Rename(staged, target))

	waitFor(t, "at least one lock rename attempt", func() bool {
		return createsOnLock.Load() >= 1 && exists(target)
	})
	// Give any busy-loop ample time to re-trigger.
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, int32(1), createsOnLock.Load(), "rename readLock must roll back once without spinning")
	assert.LessOrEqual(t, createsOnFile.Load(), int32(2), "rollback must be suppressed via ignoreNext")
	assert.Zero(t, processed.Load())
	assert.True(t, exists(target), "original file must be restored")
	assert.False(t, exists(lockTarget), "lock file must not remain")
}

func TestFileConsumer_ForgetsDeletedOrMovedFileFromSeen(t *testing.T) {
	fixedTime := time.Now().Add(-time.Minute).Truncate(time.Second)
	payload := []byte("identical-payload")

	for _, tc := range []struct {
		name      string
		query     string
		postDir   string
		failFirst bool
	}{
		{name: "delete", query: "readLock=none&delete=true"},
		{name: "move", query: "readLock=none&move=.done", postDir: ".done"},
		{name: "moveFailed", query: "readLock=none&moveFailed=.error", postDir: ".error", failFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			watchDir := t.TempDir()
			stagingDir := t.TempDir()
			target := filepath.Join(watchDir, "item.txt")

			stage := func() string {
				s := filepath.Join(stagingDir, "item.txt")
				require.NoError(t, os.WriteFile(s, payload, 0o600))
				require.NoError(t, os.Chtimes(s, fixedTime, fixedTime))
				return s
			}
			require.NoError(t, os.Rename(stage(), target))

			ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?" + tc.query)
			require.NoError(t, err)

			var processed atomic.Int32
			consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
				n := processed.Add(1)
				if tc.failFirst && n == 1 {
					return os.ErrInvalid
				}
				return nil
			}))
			require.NoError(t, err)
			require.NoError(t, consumer.Start(context.Background()))
			defer consumer.Stop()

			waitFor(t, "first ingestion removed source", func() bool {
				return processed.Load() == 1 && !exists(target)
			})
			if tc.postDir != "" {
				waitFor(t, "moved file present", func() bool {
					return exists(filepath.Join(watchDir, tc.postDir, "item.txt"))
				})
				// Remove the archived copy so recreating item.txt exercises seen eviction cleanly.
				_ = os.Remove(filepath.Join(watchDir, tc.postDir, "item.txt"))
			}

			// Wait briefly for the watcher goroutine to finish OnComplete/forget and loop back.
			fc := consumer.(*FileConsumer)
			waitFor(t, "seen forgets removed file", func() bool {
				fc.mu.Lock()
				defer fc.mu.Unlock()
				_, ok := fc.seen[target]
				return !ok && len(fc.seenOrder) == len(fc.seen)
			})

			// Recreate the exact same filename, byte length, and mtime.
			require.NoError(t, os.Rename(stage(), target))
			waitFor(t, "recreated file processed again", func() bool {
				return processed.Load() == 2
			})
		})
	}
}

func TestFileConsumer_NonRecursiveStartupSkipsSubdirs(t *testing.T) {
	watchDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(watchDir, "top.txt"), []byte("top"), 0o600))
	subDir := filepath.Join(watchDir, "sub")
	require.NoError(t, os.MkdirAll(filepath.Join(subDir, "deep"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "deep", "more.txt"), []byte("more"), 0o600))

	lockedDir := filepath.Join(watchDir, "locked")
	require.NoError(t, os.MkdirAll(lockedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lockedDir, "hidden.txt"), []byte("hidden"), 0o600))
	if os.Geteuid() != 0 {
		require.NoError(t, os.Chmod(lockedDir, 0o000))
		t.Cleanup(func() { _ = os.Chmod(lockedDir, 0o755) })
	}

	ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=none&noop=true")
	require.NoError(t, err)

	var seen []string
	var processed atomic.Int32
	done := make(chan string, 4)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		name, _ := ex.GetHeaderAsString(gocamel.CamelFileName)
		done <- name
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	defer consumer.Stop()

	select {
	case name := <-done:
		seen = append(seen, name)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for top-level file")
	}
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, int32(1), processed.Load(), "non-recursive startup must only ingest top-level files, got %v", seen)
	assert.Equal(t, []string{"top.txt"}, seen)
}

func TestFileProducer_ErrorsOnDirectoryWithoutFileName(t *testing.T) {
	c := NewFileComponent()

	t.Run("existing directory without header", func(t *testing.T) {
		dir := t.TempDir()
		ep, err := c.CreateEndpoint("file://" + dir)
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("data")
		assert.Error(t, prod.Send(ex))
	})

	t.Run("existing directory with fileExist=Ignore and no header", func(t *testing.T) {
		dir := t.TempDir()
		ep, err := c.CreateEndpoint("file://" + dir + "?fileExist=Ignore")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("data")
		assert.Error(t, prod.Send(ex))
	})

	t.Run("missing trailing-slash directory without header", func(t *testing.T) {
		missingDir := filepath.Join(t.TempDir(), "new-output")
		ep, err := c.CreateEndpoint("file://" + missingDir + "/")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetBody("data")
		require.Error(t, prod.Send(ex))
		assert.False(t, exists(missingDir), "missing directory endpoint must not create a regular file when CamelFileName is absent")
	})

	t.Run("existing directory with dot CamelFileName", func(t *testing.T) {
		dir := t.TempDir()
		ep, err := c.CreateEndpoint("file://" + dir)
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetHeader(gocamel.CamelFileName, ".")
		ex.SetBody("data")
		assert.Error(t, prod.Send(ex))
	})

	t.Run("missing trailing-slash directory with dot CamelFileName", func(t *testing.T) {
		missingDir := filepath.Join(t.TempDir(), "new-output")
		ep, err := c.CreateEndpoint("file://" + missingDir + "/")
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetHeader(gocamel.CamelFileName, ".")
		ex.SetBody("data")
		require.Error(t, prod.Send(ex))
		assert.False(t, exists(missingDir))
	})

	t.Run("existing directory with nil CamelFileName", func(t *testing.T) {
		dir := t.TempDir()
		ep, err := c.CreateEndpoint("file://" + dir)
		require.NoError(t, err)
		prod, err := ep.CreateProducer()
		require.NoError(t, err)

		ex := gocamel.NewExchange(context.Background())
		ex.SetHeader(gocamel.CamelFileName, nil)
		ex.SetBody("data")
		assert.Error(t, prod.Send(ex))
	})
}

func TestInPostProcessDir_RootNameCollision(t *testing.T) {
	// Watched directory itself ends with ".done", matching move=.done.
	inbox := filepath.Join(t.TempDir(), ".done")
	require.NoError(t, os.MkdirAll(inbox, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "input.txt"), []byte("payload"), 0o600))

	ep, err := NewFileComponent().CreateEndpoint("file://" + inbox + "?readLock=none&move=.done")
	require.NoError(t, err)

	var processed atomic.Int32
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	defer consumer.Stop()

	waitFor(t, "input.txt moved into nested .done", func() bool {
		return exists(filepath.Join(inbox, ".done", "input.txt"))
	})
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, int32(1), processed.Load())
}

func TestFileConsumer_WatchFileSecurity(t *testing.T) {
	c := NewFileComponent()

	t.Run("rejects symlink target", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.txt")
		require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))

		dir := t.TempDir()
		link := filepath.Join(dir, "link.txt")
		require.NoError(t, os.Symlink(outside, link))

		ep, err := c.CreateEndpoint("file://" + link)
		require.NoError(t, err)
		var processed atomic.Int32
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			processed.Add(1)
			return nil
		}))
		require.NoError(t, err)

		err = consumer.Start(context.Background())
		require.Error(t, err)
		assert.Zero(t, processed.Load())
	})

	t.Run("rejects oversized file", func(t *testing.T) {
		dir := t.TempDir()
		big := filepath.Join(dir, "big.bin")
		require.NoError(t, os.WriteFile(big, nil, 0o600))
		require.NoError(t, os.Truncate(big, gocamel.DefaultMaxBodySize+1))

		ep, err := c.CreateEndpoint("file://" + big)
		require.NoError(t, err)
		var processed atomic.Int32
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			processed.Add(1)
			return nil
		}))
		require.NoError(t, err)

		err = consumer.Start(context.Background())
		require.Error(t, err)
		assert.Zero(t, processed.Load())
	})

	t.Run("does not skip checks when Lstat fails", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing.txt")
		ep, err := c.CreateEndpoint("file://" + missing)
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error { return nil }))
		require.NoError(t, err)
		fc := consumer.(*FileConsumer)
		assert.Error(t, fc.watchFile(context.Background()))
	})

	t.Run("readRegularFile enforces non-regular and ReadLimited cap", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := readRegularFile(dir, 1024)
		require.Error(t, err)

		small := filepath.Join(dir, "ten.txt")
		require.NoError(t, os.WriteFile(small, []byte("0123456789"), 0o600))
		_, _, err = readRegularFile(small, 5)
		require.Error(t, err)

		data, fi, err := readRegularFile(small, 10)
		require.NoError(t, err)
		assert.Equal(t, []byte("0123456789"), data)
		assert.Equal(t, int64(10), fi.Size())
	})
}

func TestFileConsumer_RejectsSymlinkedSubdirAndInvalidRegex(t *testing.T) {
	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0o600))

	watchDir := t.TempDir()
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(watchDir, "escape")))

	ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=none")
	require.NoError(t, err)
	var processed atomic.Int32
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		processed.Add(1)
		return nil
	}))
	require.NoError(t, err)

	fc := consumer.(*FileConsumer)
	opts := gocamel.ParsePollingOptions(fc.url)
	fc.ingest(context.Background(), filepath.Join(watchDir, "escape", "secret.txt"), opts, false)
	assert.Zero(t, processed.Load(), "file reached through symlinked subdirectory must be rejected")

	// Invalid regex fails closed (Task 14).
	require.NoError(t, os.WriteFile(filepath.Join(watchDir, "ok.txt"), []byte("ok"), 0o600))
	epBad, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=none&include=[invalid")
	require.NoError(t, err)
	var processedBad atomic.Int32
	consumerBad, err := epBad.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
		processedBad.Add(1)
		return nil
	}))
	require.NoError(t, err)
	fcBad := consumerBad.(*FileConsumer)
	fcBad.ingest(context.Background(), filepath.Join(watchDir, "ok.txt"), gocamel.ParsePollingOptions(fcBad.url), false)
	assert.Zero(t, processedBad.Load(), "invalid include regex must fail closed")
}

func TestFileConsumer_ForgetMaintainsSeenOrder(t *testing.T) {
	fc := &FileConsumer{maxSeenEntries: 2}
	now := time.Now()
	fc.remember("a", 1, now)
	fc.remember("b", 1, now)
	fc.forget("a")
	fc.remember("a", 1, now)
	fc.remember("c", 1, now) // should evict "b", NOT the re-added "a"

	assert.False(t, fc.alreadySeen("b", 1, now), "oldest remaining entry 'b' must be evicted")
	assert.True(t, fc.alreadySeen("a", 1, now), "re-added 'a' must remain in seen")
	assert.True(t, fc.alreadySeen("c", 1, now), "newest 'c' must remain in seen")
	assert.Equal(t, []string{"a", "c"}, fc.seenOrder)
}

func TestFileConsumer_OnFailureRenameRollbackAndStopRouting(t *testing.T) {
	t.Run("OnFailure restores rename lock when moveFailed is unset", func(t *testing.T) {
		watchDir := t.TempDir()
		target := filepath.Join(watchDir, "fail.txt")
		require.NoError(t, os.WriteFile(target, []byte("data"), 0o600))

		ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=rename")
		require.NoError(t, err)
		var attempts atomic.Int32
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			attempts.Add(1)
			return os.ErrInvalid
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		defer consumer.Stop()

		waitFor(t, "failed file processed and restored", func() bool {
			return attempts.Load() == 1 && exists(target) && !exists(target+".camelExclusiveReadLock")
		})
	})

	t.Run("ErrStopRouting triggers OnComplete delete", func(t *testing.T) {
		watchDir := t.TempDir()
		target := filepath.Join(watchDir, "stop.txt")
		require.NoError(t, os.WriteFile(target, []byte("data"), 0o600))

		ep, err := NewFileComponent().CreateEndpoint("file://" + watchDir + "?readLock=none&delete=true")
		require.NoError(t, err)
		consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(*gocamel.Exchange) error {
			return gocamel.ErrStopRouting
		}))
		require.NoError(t, err)
		require.NoError(t, consumer.Start(context.Background()))
		defer consumer.Stop()

		waitFor(t, "stop.txt deleted on ErrStopRouting", func() bool {
			return !exists(target)
		})
	})
}
