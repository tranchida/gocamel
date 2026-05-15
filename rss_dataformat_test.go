package gocamel

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const rssSample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>GoCamel News</title>
    <link>https://example.com</link>
    <description>Integration updates</description>
    <item>
      <title>Camel RSS support</title>
      <link>https://example.com/rss</link>
      <description>RSS dataformat added</description>
      <guid>rss-1</guid>
      <pubDate>Sat, 09 May 2026 10:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`

func TestRSSDataFormat_Unmarshal(t *testing.T) {
	df := NewRSSDataFormat()
	result, err := df.Unmarshal(nil, rssSample)
	require.NoError(t, err)

	feed, ok := result.(*RSSFeed)
	require.True(t, ok)
	assert.Equal(t, "2.0", feed.Version)
	assert.Equal(t, "GoCamel News", feed.Channel.Title)
	require.Len(t, feed.Channel.Items, 1)
	assert.Equal(t, "Camel RSS support", feed.Channel.Items[0].Title)
	assert.Equal(t, "rss-1", feed.Channel.Items[0].GUID)
}

func TestRSSDataFormat_Marshal(t *testing.T) {
	df := NewRSSDataFormat()
	result, err := df.Marshal(nil, RSSFeed{
		Channel: RSSChannel{
			Title:       "GoCamel News",
			Link:        "https://example.com",
			Description: "Integration updates",
			Items: []RSSItem{
				{Title: "Camel RSS support", Link: "https://example.com/rss"},
			},
		},
	})
	require.NoError(t, err)

	xmlBody, ok := result.(string)
	require.True(t, ok)
	assert.Contains(t, xmlBody, `<?xml version="1.0" encoding="UTF-8"?>`)
	assert.Contains(t, xmlBody, `<rss version="2.0">`)
	assert.Contains(t, xmlBody, `<title>GoCamel News</title>`)
	assert.Contains(t, xmlBody, `<title>Camel RSS support</title>`)
}

func TestRSSDataFormat_RouteBuilderDSL(t *testing.T) {
	ctx := NewCamelContext()
	route, err := ctx.CreateRouteBuilder().
		From("direct:start").
		UnmarshalRSS().
		MarshalRSS().
		Build()
	require.NoError(t, err)

	ex := NewExchange(context.Background())
	ex.GetIn().SetBody(rssSample)
	require.NoError(t, route.Process(ex))

	body, ok := ex.GetOut().GetBody().(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(body, xml.Header))
	assert.Contains(t, body, `<title>GoCamel News</title>`)
}

func TestRSSDataFormat_UnmarshalRejectsNonRSSRoot(t *testing.T) {
	df := NewRSSDataFormat()
	_, err := df.Unmarshal(nil, `<feed><title>Atom</title></feed>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected <rss> root element")
}

// TestRSSDataFormat_UnmarshalRejectsDOCTYPE verifies that DOCTYPE declarations
// are refused. Allowing them would expose the consumer to recursive internal
// entity expansion attacks (Billion Laughs / CWE-776).
func TestRSSDataFormat_UnmarshalRejectsDOCTYPE(t *testing.T) {
	df := NewRSSDataFormat()
	payload := `<?xml version="1.0"?>
<!DOCTYPE lolz [
  <!ENTITY lol "lol">
  <!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;">
]>
<rss version="2.0"><channel><title>&lol2;</title></channel></rss>`
	_, err := df.Unmarshal(nil, payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DOCTYPE")
}
