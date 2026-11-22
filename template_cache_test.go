package gocamel

import (
	"context"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateCache_IsBoundedByMaxEntries(t *testing.T) {
	c := NewTemplateComponent()
	tmpl := template.Must(template.New("t").Parse("hello"))

	for i := 0; i < maxTemplateCacheEntries+100; i++ {
		c.cacheTemplate(fmt.Sprintf("key-%d", i), tmpl)
	}

	c.mu.RLock()
	size := len(c.cache)
	c.mu.RUnlock()
	assert.LessOrEqual(t, size, maxTemplateCacheEntries)
	assert.Equal(t, maxTemplateCacheEntries, size)
}

func TestTemplateCache_FullCacheStillUpdatesExistingKey(t *testing.T) {
	c := NewTemplateComponent()
	first := template.Must(template.New("t").Parse("first"))
	second := template.Must(template.New("t").Parse("second"))

	for i := 0; i < maxTemplateCacheEntries; i++ {
		c.cacheTemplate(fmt.Sprintf("key-%d", i), first)
	}
	c.cacheTemplate("key-0", second)

	assert.Same(t, second, c.getCachedTemplate("key-0"))
	assert.Nil(t, c.getCachedTemplate("unused-key"))
}

// newCachingTemplateProducer returns a producer whose endpoint has
// contentCache enabled, so executeTemplate goes through the component cache.
func newCachingTemplateProducer(c *TemplateComponent) *TemplateProducer {
	return &TemplateProducer{endpoint: &TemplateEndpoint{
		component:    c,
		contentCache: true,
		encoding:     "UTF-8",
	}}
}

func TestTemplateCache_ExecuteNeverExceedsCap(t *testing.T) {
	c := NewTemplateComponent()
	p := newCachingTemplateProducer(c)
	data := map[string]string{"Name": "World"}

	// Header-derived names: every distinct name would be a new cache key.
	for i := 0; i < maxTemplateCacheEntries+100; i++ {
		out, err := p.executeTemplate("Hello {{.Name}}", data, fmt.Sprintf("dir/t-%d.tmpl", i))
		require.NoError(t, err)
		require.Equal(t, "Hello World", out)
	}

	c.mu.RLock()
	size := len(c.cache)
	c.mu.RUnlock()
	assert.Equal(t, maxTemplateCacheEntries, size)
}

func TestTemplateCache_UncachedTemplateStillRendersThroughProducer(t *testing.T) {
	c := NewTemplateComponent()
	filler := template.Must(template.New("t").Parse("x"))
	for i := 0; i < maxTemplateCacheEntries; i++ {
		c.cacheTemplate(fmt.Sprintf("key-%d", i), filler)
	}

	// The cache is full, so a new template name is not stored, but it must
	// still be parsed and executed correctly on every call.
	p := newCachingTemplateProducer(c)
	data := map[string]string{"Name": "World"}
	for i := 0; i < 2; i++ {
		out, err := p.executeTemplate("Hello {{.Name}}", data, "overflow.tmpl")
		require.NoError(t, err)
		assert.Equal(t, "Hello World", out)
	}
	assert.Nil(t, c.getCachedTemplate(p.cacheKey("overflow.tmpl")))

	c.mu.RLock()
	size := len(c.cache)
	c.mu.RUnlock()
	assert.Equal(t, maxTemplateCacheEntries, size)
}

func TestTemplateComponent_CanonicalCacheKeyAndNoDiskReadOnCacheHit(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.tmpl")
	aPath := filepath.Join(dir, "a.tmpl")
	subDir := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(basePath, []byte("Base={{.Body}}"), 0o600))
	require.NoError(t, os.WriteFile(aPath, []byte("A={{.Body}}"), 0o600))

	comp := NewTemplateComponent()
	ep, err := comp.CreateEndpoint("template:" + basePath + "?contentCache=true&allowTemplateFromHeader=true")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer prod.Stop()

	// First Send via non-canonical path /dir/./a.tmpl populates the cache.
	ex1 := NewExchange(context.Background())
	ex1.GetIn().SetHeader(CamelTemplatePath, dir+"/./a.tmpl")
	ex1.GetIn().SetBody("one")
	require.NoError(t, prod.Send(ex1))
	assert.Equal(t, "A=one", ex1.GetIn().GetBody())

	// Delete a.tmpl from disk: subsequent Sends with non-canonical equivalent paths
	// must hit the cache without reading disk and must share one cache entry.
	require.NoError(t, os.Remove(aPath))

	ex2 := NewExchange(context.Background())
	ex2.GetIn().SetHeader(CamelTemplatePath, dir+"//a.tmpl")
	ex2.GetIn().SetBody("two")
	require.NoError(t, prod.Send(ex2))
	assert.Equal(t, "A=two", ex2.GetIn().GetBody())

	ex3 := NewExchange(context.Background())
	ex3.GetIn().SetHeader(CamelTemplatePath, dir+"/sub/../a.tmpl")
	ex3.GetIn().SetBody("three")
	require.NoError(t, prod.Send(ex3))
	assert.Equal(t, "A=three", ex3.GetIn().GetBody())

	comp.mu.RLock()
	cacheLen := len(comp.cache)
	comp.mu.RUnlock()
	// Start() precompiles base.tmpl (1 entry) + header override caches a.tmpl (1 entry).
	assert.Equal(t, 2, cacheLen)
}
