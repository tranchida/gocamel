package gocamel

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uriRecordingComponent records the URIs it is asked to create endpoints for.
// Its endpoints are pollable and always return no message.
type uriRecordingComponent struct {
	mu   sync.Mutex
	uris []string
}

func (c *uriRecordingComponent) CreateEndpoint(uri string) (Endpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uris = append(c.uris, uri)
	return &uriRecordingEndpoint{uri: uri}, nil
}

func (c *uriRecordingComponent) lastURI(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	require.NotEmpty(t, c.uris, "no endpoint was created")
	return c.uris[len(c.uris)-1]
}

type uriRecordingEndpoint struct{ uri string }

func (e *uriRecordingEndpoint) URI() string { return e.uri }
func (e *uriRecordingEndpoint) CreateProducer() (Producer, error) {
	return nil, nil
}
func (e *uriRecordingEndpoint) CreateConsumer(Processor) (Consumer, error) {
	return nil, nil
}
func (e *uriRecordingEndpoint) Poll(ctx context.Context, timeout time.Duration) (*Exchange, error) {
	return nil, nil
}

func TestPollEnrich_HeaderValueCannotInjectEndpointOptions(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("chan", rec)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("ch", "x?bufferSize=1000000000")

	p := NewPollEnricher(camelCtx, "chan:${header.ch}", nil, 0)
	require.NoError(t, p.Process(ex))

	got := rec.lastURI(t)
	assert.Equal(t, "chan:x%3FbufferSize=1000000000", got)
	assert.NotContains(t, got, "?")
}

func TestPollEnrich_QueryPlaceholderIsQueryEscaped(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("seda", rec)

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("size", "10&allowHeaderOverride=true")

	p := NewPollEnricher(camelCtx, "seda:queue?size=${header.size}", nil, 0)
	require.NoError(t, p.Process(ex))

	got := rec.lastURI(t)
	assert.Equal(t, "seda:queue?size=10%26allowHeaderOverride%3Dtrue", got)
}

func TestPollEnrich_ExchangePropertyExpressionResolves(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("chan", rec)

	ex := NewExchange(context.Background())
	ex.SetProperty("ch", "orders")

	// exchangeProperty is only reachable through the full Simple language.
	p := NewPollEnricher(camelCtx, "chan:${exchangeProperty.ch}", nil, 0)
	require.NoError(t, p.Process(ex))

	assert.Equal(t, "chan:orders", rec.lastURI(t))
}

func TestPollEnrich_PropertyInjectionIsEscaped(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("seda", rec)

	ex := NewExchange(context.Background())
	ex.SetProperty("size", "1&allowHeaderOverride=true")

	p := NewPollEnricher(camelCtx, "seda:q?size=${exchangeProperty.size}", nil, 0)
	require.NoError(t, p.Process(ex))

	assert.Equal(t, "seda:q?size=1%26allowHeaderOverride%3Dtrue", rec.lastURI(t))
}

func TestPollEnrich_FailingExpressionReturnsEvaluationError(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("chan", rec)

	// random(0) is rejected by the Simple language: the argument must be positive.
	p := NewPollEnricher(camelCtx, "chan:${random(0)}", nil, 0)
	err := p.Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pollEnrich: failed to evaluate URI expression")
	assert.Contains(t, err.Error(), "random argument must be positive")

	// No endpoint is created from an unresolved URI.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Empty(t, rec.uris)
}

func TestPollEnrich_UnparseableExpressionReturnsParseError(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("chan", rec)

	// A URI above MaxSimpleInputBytes is rejected by ParseSimpleTemplate.
	uri := "chan:${header.ch}" + strings.Repeat("a", MaxSimpleInputBytes)
	p := NewPollEnricher(camelCtx, uri, nil, 0)
	err := p.Process(NewExchange(context.Background()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pollEnrich: failed to parse URI expression")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Empty(t, rec.uris)
}

func TestPollEnrich_StaticURIIsUnchanged(t *testing.T) {
	camelCtx := NewCamelContext()
	rec := &uriRecordingComponent{}
	camelCtx.AddComponent("chan", rec)

	ex := NewExchange(context.Background())
	p := NewPollEnricher(camelCtx, "chan:static?bufferSize=4", nil, 0)
	require.NoError(t, p.Process(ex))

	assert.Equal(t, "chan:static?bufferSize=4", rec.lastURI(t))
}
