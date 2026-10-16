package gocamel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// H-2: Simple language padstart / padend bounded execution
func TestSimpleLanguage_PadStartPadEnd_Bounded(t *testing.T) {
	ex := NewExchange(context.Background())
	ex.GetIn().SetBody("42")

	// Valid padstart
	res, err := evaluateSimpleExpression("${body.padstart(5, '0')}", ex)
	require.NoError(t, err)
	assert.Equal(t, "00042", res)

	// Valid padend
	res, err = evaluateSimpleExpression("${body.padend(5, '0')}", ex)
	require.NoError(t, err)
	assert.Equal(t, "42000", res)

	// Oversized target length rejected
	_, err = evaluateSimpleExpression(fmt.Sprintf("${body.padstart(%d, '0')}", MaxSimpleInputBytes+10), ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds MaxSimpleInputBytes")

	// Empty padChar returns original string
	res, err = evaluateSimpleExpression("${body.padstart(10, '')}", ex)
	require.NoError(t, err)
	assert.Equal(t, "42", res)

	// Regex pattern length bounded in eval
	oversizedPattern := strings.Repeat("a", MaxSimpleInputBytes+1)
	_, err = evaluateSimpleExpression(fmt.Sprintf("${body} regex '%s'", oversizedPattern), ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds MaxSimpleInputBytes")
}

// H-3: RecipientList does not double interpolate
func TestRecipientList_NoDoubleInterpolation(t *testing.T) {
	c := NewCamelContext()
	c.AddComponent("direct", NewDirectComponent())

	var received string
	_, err := c.CreateRouteBuilder().
		From("direct:target").
		ProcessFunc(func(e *Exchange) error {
			received = e.GetIn().GetBody().(string)
			return nil
		}).
		Build()
	require.NoError(t, err)

	_, err = c.CreateRouteBuilder().
		From("direct:entry").
		RecipientList("${header.dest}").
		End().
		Build()
	require.NoError(t, err)

	require.NoError(t, c.Start())
	defer c.Stop()

	ep, err := c.CreateEndpoint("direct:entry")
	require.NoError(t, err)
	producer, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, producer.Start(context.Background()))
	defer producer.Stop()

	ex := NewExchange(context.Background())
	// Destination resolves to direct:target via Simple language
	ex.GetIn().SetHeader("dest", "direct:target")
	// Body contains simple language syntax which should NOT be evaluated as a URI
	ex.GetIn().SetBody("payload-${not-a-header}")
	require.NoError(t, producer.Send(ex))

	assert.Equal(t, "payload-${not-a-header}", received)
}

// H-4: Unbounded readers protected with limit
func TestDataFormat_BoundedReaders(t *testing.T) {
	// JSONPath source normalization with limited reader
	var buf bytes.Buffer
	buf.WriteString(`{"name": "test"}`)
	source, err := normalizeJSONSource(&buf)
	require.NoError(t, err)
	sourceMap, ok := source.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test", sourceMap["name"])

	// CSV unmarshal
	csv := NewCSVDataFormat()
	csvData := "a,b,c\n1,2,3\n"
	unmarshaled, err := csv.Unmarshal(NewExchange(context.Background()), strings.NewReader(csvData))
	require.NoError(t, err)
	records, ok := unmarshaled.([][]string)
	require.True(t, ok)
	assert.Len(t, records, 2)
}

// H-5: MemoryAggregationRepository DeepCopy
func TestMemoryAggregationRepository_DeepCopy(t *testing.T) {
	repo := NewMemoryAggregationRepository()
	ctx := context.Background()

	orig := NewExchange(ctx)
	orig.GetIn().SetHeader("custom", "val1")
	orig.GetIn().SetBody("body1")

	err := repo.Add(ctx, "k1", orig)
	require.NoError(t, err)

	// Mutate original
	orig.GetIn().SetHeader("custom", "mutated")
	orig.GetIn().SetBody("mutated-body")

	// Repository should retain deep-copied state
	retrieved, err := repo.Get(ctx, "k1")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	val, ok := retrieved.GetIn().GetHeader("custom")
	assert.True(t, ok)
	assert.Equal(t, "val1", val)
	assert.Equal(t, "body1", retrieved.GetIn().GetBody())

	// Mutate retrieved
	retrieved.GetIn().SetHeader("custom", "second-mutation")
	secondGet, err := repo.Get(ctx, "k1")
	require.NoError(t, err)
	val2, ok2 := secondGet.GetIn().GetHeader("custom")
	assert.True(t, ok2)
	assert.Equal(t, "val1", val2)
}

// M-1: TemplateComponent caching and Enrich Out reset
func TestTemplateComponent_CacheAndEnrichOutReset(t *testing.T) {
	tempDir := t.TempDir()
	tmplPath := filepath.Join(tempDir, "test.tmpl")
	require.NoError(t, os.WriteFile(tmplPath, []byte("Hello {{.Headers.name}}!"), 0600))

	c := NewCamelContext()
	comp := NewTemplateComponent()
	c.AddComponent("template", comp)
	c.AddComponent("direct", NewDirectComponent())

	ep, err := c.CreateEndpoint(fmt.Sprintf("template:%s?contentCache=true", tmplPath))
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer prod.Stop()

	ex := NewExchange(context.Background())
	ex.GetIn().SetHeader("name", "GoCamel")
	require.NoError(t, prod.Send(ex))
	assert.Equal(t, "Hello GoCamel!", ex.GetIn().GetBody())

	// Overwrite template file on disk: cached content must still be served
	require.NoError(t, os.WriteFile(tmplPath, []byte("Modified template"), 0600))
	ex2 := NewExchange(context.Background())
	ex2.GetIn().SetHeader("name", "GoCamel2")
	require.NoError(t, prod.Send(ex2))
	assert.Equal(t, "Hello GoCamel2!", ex2.GetIn().GetBody())

	// Test Enrich resets Out on copied exchange
	var enricherReceivedOut any
	_, err = c.CreateRouteBuilder().
		From("direct:enrich-test").
		ProcessFunc(func(e *Exchange) error {
			enricherReceivedOut = e.GetOut().GetBody()
			e.GetIn().SetBody("enrichment-data")
			return nil
		}).
		Build()
	require.NoError(t, err)

	require.NoError(t, c.Start())
	defer c.Stop()

	enricher := NewContentEnricher(c, "direct:enrich-test", nil)

	parentEx := NewExchange(context.Background())
	parentEx.GetIn().SetBody("original-body")
	parentEx.GetOut().SetBody("stale-out")
	require.NoError(t, enricher.Process(parentEx))

	// The enrich sub-processor should not see stale Out from earlier pipeline step
	assert.Nil(t, enricherReceivedOut)
	// Without strategy, body is replaced with enrichment data
	assert.Equal(t, "enrichment-data", parentEx.GetIn().GetBody())
}

// M-7: ValidateProcessor concurrent Process safety
func TestValidateProcessor_ConcurrentSafe(t *testing.T) {
	vp := NewValidateProcessor("${header.kind == 'valid'}")

	var wg sync.WaitGroup
	errs := make([]error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ex := NewExchange(context.Background())
			if idx%2 == 0 {
				ex.GetIn().SetHeader("kind", "valid")
			} else {
				ex.GetIn().SetHeader("kind", "invalid")
			}
			errs[idx] = vp.Process(ex)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if i%2 == 0 {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
	}
}

// M-8: NetComponent bufferSize validation
func TestNetComponent_BufferSizeValidation(t *testing.T) {
	nc := NewNetComponent()

	_, err := nc.CreateEndpoint("net:tcp://127.0.0.1:9099?bufferSize=-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bufferSize")

	_, err = nc.CreateEndpoint("net:tcp://127.0.0.1:9099?bufferSize=0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bufferSize")

	_, err = nc.CreateEndpoint(fmt.Sprintf("net:tcp://127.0.0.1:9099?bufferSize=%d", DefaultMaxBodySize+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bufferSize")

	ep, err := nc.CreateEndpoint("net:tcp://127.0.0.1:9099?bufferSize=8192")
	require.NoError(t, err)
	require.NotNil(t, ep)
}

// M-9: SQL parameter extraction with typed slices
func TestSQLProducer_ExtractParameters_TypedSlice(t *testing.T) {
	// []string slice
	strSlice := []string{"foo", "bar"}
	unpacked := toAnySlice(strSlice)
	assert.Equal(t, []any{"foo", "bar"}, unpacked)

	// []int slice
	intSlice := []int{10, 20, 30}
	unpacked = toAnySlice(intSlice)
	assert.Equal(t, []any{10, 20, 30}, unpacked)

	// []byte slice should NOT be unpacked (treated as single binary value)
	byteSlice := []byte("hello")
	unpacked = toAnySlice(byteSlice)
	assert.Nil(t, unpacked)
}

// M-11: RouteBuilder and ChoiceBuilder preserve first error
func TestRouteBuilder_PreservesFirstError(t *testing.T) {
	rb := NewRouteBuilder(NewCamelContext())
	firstErr := errors.New("first build error")
	secondErr := errors.New("second build error")

	rb.recordErr(firstErr)
	rb.recordErr(secondErr)

	_, err := rb.Build()
	require.Error(t, err)
	assert.ErrorIs(t, err, firstErr)
	assert.NotErrorIs(t, err, secondErr)

	cb := rb.Choice()
	cbFirst := errors.New("first choice error")
	cbSecond := errors.New("second choice error")
	cb.recordErr(cbFirst)
	cb.recordErr(cbSecond)
	assert.ErrorIs(t, cb.err, cbFirst)
}
