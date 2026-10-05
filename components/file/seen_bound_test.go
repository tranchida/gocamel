package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestFileConsumer_SeenMapIsBounded(t *testing.T) {
	const files, limit = 12, 5
	dir := t.TempDir()
	paths := make([]string, files)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("f%02d.txt", i))
		require.NoError(t, os.WriteFile(paths[i], []byte("x"), 0600))
	}

	ep, err := NewFileComponent().CreateEndpoint(fmt.Sprintf("file://%s?readLock=none&noop=true&maxSeenEntries=%d", dir, limit))
	require.NoError(t, err)
	received := make(chan string, files*2)
	consumer, err := ep.CreateConsumer(gocamel.ProcessorFunc(func(ex *gocamel.Exchange) error {
		path, _ := ex.GetHeader(gocamel.CamelFilePath)
		received <- path.(string)
		return nil
	}))
	require.NoError(t, err)
	require.NoError(t, consumer.Start(context.Background()))
	t.Cleanup(func() { _ = consumer.Stop() })

	// The startup snapshot ingests every pre-existing file synchronously.
	assert.Len(t, received, files)

	fc := consumer.(*FileConsumer)
	fc.mu.Lock()
	seenLen := len(fc.seen)
	fc.mu.Unlock()
	assert.Equal(t, limit, seenLen, "seen map must stay bounded by maxSeenEntries")

	opts := gocamel.ParsePollingOptions(fc.url)
	before := len(received)
	// A recently-seen, unchanged file is still deduplicated.
	fc.ingest(context.Background(), paths[files-1], opts, false)
	assert.Len(t, received, before, "recently seen file must not be re-ingested")

	// The oldest entry was evicted, so it is ingested again.
	fc.ingest(context.Background(), paths[0], opts, false)
	assert.Len(t, received, before+1, "evicted entry is forgotten")
}
