package gocamel

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
)

// RSSDataFormat converts between RSS XML and RSSFeed values.
//
// It mirrors Apache Camel's RSS dataformat at the GoCamel level: there are no
// dataformat options, marshal produces XML, and unmarshal produces a feed model.
type RSSDataFormat struct{}

// RSSFeed is the Go model used by RSSDataFormat.
type RSSFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr,omitempty"`
	Channel RSSChannel `xml:"channel"`
}

// RSSChannel represents an RSS channel.
type RSSChannel struct {
	Title       string    `xml:"title,omitempty"`
	Link        string    `xml:"link,omitempty"`
	Description string    `xml:"description,omitempty"`
	Language    string    `xml:"language,omitempty"`
	Copyright   string    `xml:"copyright,omitempty"`
	PubDate     string    `xml:"pubDate,omitempty"`
	LastBuild   string    `xml:"lastBuildDate,omitempty"`
	Items       []RSSItem `xml:"item,omitempty"`
}

// RSSItem represents an RSS item entry.
type RSSItem struct {
	Title       string `xml:"title,omitempty"`
	Link        string `xml:"link,omitempty"`
	Description string `xml:"description,omitempty"`
	Author      string `xml:"author,omitempty"`
	Category    string `xml:"category,omitempty"`
	Comments    string `xml:"comments,omitempty"`
	GUID        string `xml:"guid,omitempty"`
	PubDate     string `xml:"pubDate,omitempty"`
	Source      string `xml:"source,omitempty"`
}

// NewRSSDataFormat creates the RSS dataformat.
func NewRSSDataFormat() *RSSDataFormat {
	return &RSSDataFormat{}
}

// Marshal converts an RSSFeed into an XML string.
func (f *RSSDataFormat) Marshal(_ *Exchange, body any) (any, error) {
	feed, err := coerceRSSFeed(body)
	if err != nil {
		return nil, err
	}
	if feed.Version == "" {
		feed.Version = "2.0"
	}
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rss marshal failed: %w", err)
	}
	return xml.Header + string(out), nil
}

// Unmarshal converts RSS XML into an RSSFeed.
func (f *RSSDataFormat) Unmarshal(_ *Exchange, body any) (any, error) {
	data, err := bodyToBytes(body)
	if err != nil {
		return nil, fmt.Errorf("rss unmarshal expects XML string, []byte, or io.Reader: %w", err)
	}
	var feed RSSFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("rss unmarshal failed: %w", err)
	}
	if feed.XMLName.Local != "rss" {
		return nil, fmt.Errorf("rss unmarshal expected <rss> root element, got <%s>", feed.XMLName.Local)
	}
	return &feed, nil
}

func coerceRSSFeed(body any) (*RSSFeed, error) {
	switch v := body.(type) {
	case *RSSFeed:
		if v == nil {
			return nil, fmt.Errorf("rss marshal expects non-nil *RSSFeed")
		}
		feed := *v
		return &feed, nil
	case RSSFeed:
		feed := v
		return &feed, nil
	default:
		return nil, fmt.Errorf("rss marshal expects RSSFeed or *RSSFeed, got %T", body)
	}
}

func bodyToBytes(body any) ([]byte, error) {
	switch v := body.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	case io.Reader:
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(v); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("got %T", body)
	}
}
