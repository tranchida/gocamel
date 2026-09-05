package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "gitlab.com/tranchida/gocamel"
)

func TestReviewFileStartupStatus(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("data"), 0600))
	c := gocamel.NewCamelContext()
	c.AddComponent("file", NewFileComponent())
	var route *gocamel.Route
	var err error
	route, err = c.CreateRouteBuilder().From("file://" + dir + "?noop=true").ProcessFunc(func(*gocamel.Exchange) error { _ = c.IsStarted(); _ = route.IsStarted(); return nil }).Build()
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- c.Start() }()
	select {
	case err := <-done:
		require.NoError(t, err)
		defer c.Stop()
	case <-time.After(time.Second):
		t.Fatal("startup status query deadlocked")
	}
}
func TestReviewFileMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-output")
	ep, err := NewFileComponent().CreateEndpoint("file://" + dir + "/")
	require.NoError(t, err)
	p, err := ep.CreateProducer()
	require.NoError(t, err)
	e := gocamel.NewExchange(context.Background())
	e.SetBody("data")
	e.SetHeader(gocamel.CamelFileName, "output.txt")
	require.NoError(t, p.Send(e))
	_, err = os.Stat(filepath.Join(dir, "output.txt"))
	assert.NoError(t, err)
}
