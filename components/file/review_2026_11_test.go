package file

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
		path, moveDir string
		want          bool
	}{
		{"/in/.done/a.txt", ".done", true},
		{"/in/sub/.done/a.txt", ".done", true},
		{"/in/a.txt", ".done", false},
		{"/in/x.done/a.txt", ".done", false},
		{"/in/a.txt", "", false},
		{"/in/a.txt", ".", false},
		{"/archive/a.txt", "/archive", true},
		{"/archive/sub/a.txt", "/archive", false},
	}
	for _, tc := range cases {
		if got := inPostProcessDir(filepath.FromSlash(tc.path), filepath.FromSlash(tc.moveDir)); got != tc.want {
			t.Errorf("inPostProcessDir(%q, %q) = %v, want %v", tc.path, tc.moveDir, got, tc.want)
		}
	}
}
