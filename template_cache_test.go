package gocamel

import (
	"fmt"
	"html/template"
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
