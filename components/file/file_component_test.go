package file

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestCreateEndpoint_RejectsTraversal(t *testing.T) {
	c := NewFileComponent()
	for _, uri := range []string{
		"file://../../etc/passwd",
		"file://data/../../etc/passwd",
	} {
		if _, err := c.CreateEndpoint(uri); err == nil {
			t.Errorf("expected %q to be rejected as traversal", uri)
		}
	}
}

// A filename may legitimately contain two adjacent dots. The previous
// strings.Contains(path, "..") check rejected these.
func TestCreateEndpoint_AllowsDoubleDotInFilename(t *testing.T) {
	c := NewFileComponent()
	dir := t.TempDir()
	uri := "file://" + filepath.Join(dir, "report..v2.txt")
	if _, err := c.CreateEndpoint(uri); err != nil {
		t.Fatalf("expected filename with '..' to be accepted, got %v", err)
	}
}

func TestCreateEndpoint_RejectsNullByte(t *testing.T) {
	c := NewFileComponent()
	if _, err := c.CreateEndpoint("file:///tmp/evil\x00.txt"); err == nil {
		t.Error("expected null byte to be rejected")
	}
}

func TestFileProducer_WritesBody(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")

	c := NewFileComponent()
	ep, err := c.CreateEndpoint("file://" + target)
	if err != nil {
		t.Fatal(err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatal(err)
	}

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("hello")
	if err := prod.Send(ex); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestFileProducer_RejectsUnsupportedBody(t *testing.T) {
	dir := t.TempDir()
	c := NewFileComponent()
	ep, _ := c.CreateEndpoint("file://" + filepath.Join(dir, "x.txt"))
	prod, _ := ep.CreateProducer()

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody(42)
	if err := prod.Send(ex); err == nil {
		t.Error("expected an error for an int body")
	}
}

// waitForStableFile must not report a file as ready while it is still growing.
func TestWaitForStableFile_WaitsForSlowWriter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "slow.bin")

	f, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			f.Write([]byte("chunk-of-data"))
			f.Sync()
			time.Sleep(60 * time.Millisecond)
		}
		f.Close()
	}()

	ok := waitForStableFile(context.Background(), target, 5*time.Second, 50*time.Millisecond)
	wg.Wait()
	if !ok {
		t.Fatal("expected the file to stabilise before the timeout")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	// All five chunks must be present: the point of the read lock is that we
	// do not observe a partially written file.
	if len(got) != len("chunk-of-data")*5 {
		t.Errorf("got %d bytes, want %d (partial read)", len(got), len("chunk-of-data")*5)
	}
}

func TestWaitForStableFile_TimesOut(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "growing.bin")
	f, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f.Write([]byte("x"))
				f.Sync()
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	ok := waitForStableFile(context.Background(), target, 300*time.Millisecond, 50*time.Millisecond)
	close(stop)
	wg.Wait()
	if ok {
		t.Error("expected a continuously growing file to time out")
	}
}

func TestWaitForStableFile_MissingFile(t *testing.T) {
	if waitForStableFile(context.Background(), filepath.Join(t.TempDir(), "nope"), time.Second, 10*time.Millisecond) {
		t.Error("expected false for a missing file")
	}
}

func TestWaitForStableFile_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForStableFile(ctx, target, time.Second, 50*time.Millisecond) {
		t.Error("expected false when the context is already cancelled")
	}
}

// The directory consumer must deliver the complete file, not a truncated
// prefix observed at Create time.
func TestFileConsumer_DeliversCompleteFile(t *testing.T) {
	dir := t.TempDir()
	c := NewFileComponent()
	ep, err := c.CreateEndpoint("file://" + dir)
	if err != nil {
		t.Fatal(err)
	}

	payload := make([]byte, 0, 64*1024)
	for i := 0; i < 64*1024; i++ {
		payload = append(payload, byte('a'+i%26))
	}

	received := make(chan []byte, 1)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		body, _ := ex.GetIn().GetBody().([]byte)
		select {
		case received <- body:
		default:
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

	time.Sleep(150 * time.Millisecond)

	// Write in chunks with pauses, the pattern that produced truncated reads.
	target := filepath.Join(dir, "payload.dat")
	f, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(payload); i += 8 * 1024 {
		end := min(i+8*1024, len(payload))
		if _, err := f.Write(payload[i:end]); err != nil {
			t.Fatal(err)
		}
		f.Sync()
		time.Sleep(30 * time.Millisecond)
	}
	f.Close()

	select {
	case got := <-received:
		if len(got) != len(payload) {
			t.Errorf("received %d bytes, want %d (partial read)", len(got), len(payload))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the file exchange")
	}
}

func TestFileConsumer_SetsCamelHeaders(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "testfile.txt")
	if err := os.WriteFile(filePath, []byte("camel-test"), 0644); err != nil {
		t.Fatal(err)
	}

	c := NewFileComponent()
	ep, err := c.CreateEndpoint("file://" + filePath)
	if err != nil {
		t.Fatal(err)
	}

	received := make(chan *gocamel.Exchange, 1)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		received <- ex
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer consumer.Stop()

	select {
	case ex := <-received:
		name, ok := ex.GetIn().GetHeader(gocamel.CamelFileName)
		if !ok || name != "testfile.txt" {
			t.Errorf("expected CamelFileName 'testfile.txt', got %v", name)
		}
		path, ok := ex.GetIn().GetHeader(gocamel.CamelFilePath)
		if !ok || path != filePath {
			t.Errorf("expected CamelFilePath %q, got %v", filePath, path)
		}
		length, ok := ex.GetIn().GetHeader(gocamel.CamelFileLength)
		if !ok || length != int64(len("camel-test")) {
			t.Errorf("expected CamelFileLength %d, got %v", len("camel-test"), length)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for consumer")
	}
}

func TestFileProducer_WritesWithCamelFileNameHeader(t *testing.T) {
	dir := t.TempDir()
	c := NewFileComponent()
	ep, err := c.CreateEndpoint("file://" + dir)
	if err != nil {
		t.Fatal(err)
	}
	prod, err := ep.CreateProducer()
	if err != nil {
		t.Fatal(err)
	}

	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetHeader(gocamel.CamelFileName, "nested.txt")
	ex.SetBody("dynamic filename content")
	if err := prod.Send(ex); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "nested.txt")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dynamic filename content" {
		t.Errorf("got %q, want 'dynamic filename content'", string(got))
	}
}
