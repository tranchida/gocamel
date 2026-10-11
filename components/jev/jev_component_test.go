package jev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gocamel "github.com/tranchida/gocamel"
)

const testQuestions = `{"is_ironic":{"type":"noul","instructions":"Is it ironic?","criteria":{"true":"ironic","false":"sincere"}}}`

const okResponse = `{"answers":{
 "is_ironic":{"type":"noul","noul":0.97},
 "degree":{"type":"choice","choice":"Second degré","confidence":0.9,"probabilities":{"Premier degré":0.05,"Second degré":0.95}}}}`

type stub struct {
	*httptest.Server
	req  *http.Request
	body []byte
}

func newStub(t *testing.T, status int, resp string) *stub {
	t.Helper()
	s := &stub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.req = r
		s.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(s.Close)
	return s
}

func producer(t *testing.T, uri string) *JevProducer {
	t.Helper()
	ep, err := NewJevComponent().CreateEndpoint(uri)
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	return prod.(*JevProducer)
}

func uriFor(base string, extra string) string {
	return "jev:decisions?apiKey=k&baseURL=" + url.QueryEscape(base) + "&questions=" + url.QueryEscape(testQuestions) + extra
}

func TestSend_Success(t *testing.T) {
	s := newStub(t, http.StatusOK, okResponse)
	p := producer(t, uriFor(s.URL, ""))
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("Bravo, vraiment.")
	require.NoError(t, p.Send(ex))

	assert.Equal(t, "/decisions", s.req.URL.Path)
	assert.Equal(t, "Bearer k", s.req.Header.Get("Authorization"))

	var sent map[string]any
	require.NoError(t, json.Unmarshal(s.body, &sent))
	assert.Equal(t, DefaultModel, sent["model"])
	assert.Equal(t, "Bravo, vraiment.", sent["state"])
	assert.Contains(t, sent["questions"], "is_ironic")

	answers, ok := ex.GetOut().GetBody().(map[string]Answer)
	require.True(t, ok)
	assert.InDelta(t, 0.97, answers["is_ironic"].Noul, 1e-9)
	assert.Equal(t, "Second degré", answers["degree"].Choice)
	assert.InDelta(t, 0.95, answers["degree"].Probabilities["Second degré"], 1e-9)

	m, _ := ex.GetOut().GetHeader(HeaderModel)
	assert.Equal(t, DefaultModel, m)
	d, ok := ex.GetOut().GetHeader(HeaderElapsed)
	require.True(t, ok)
	assert.IsType(t, time.Duration(0), d)
}

func TestSend_StructuredStateIsJSON(t *testing.T) {
	s := newStub(t, http.StatusOK, okResponse)
	p := producer(t, uriFor(s.URL, ""))
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody(map[string]string{"user_message": "hi"})
	require.NoError(t, p.Send(ex))

	var sent struct {
		State map[string]string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(s.body, &sent))
	assert.Equal(t, "hi", sent.State["user_message"])
}

func TestSend_APIError(t *testing.T) {
	s := newStub(t, http.StatusUnauthorized, `{"error":"bad key"}`)
	p := producer(t, uriFor(s.URL, ""))
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("x")
	err := p.Send(ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "bad key")
}

func TestSend_EmptyBodyRejected(t *testing.T) {
	s := newStub(t, http.StatusOK, okResponse)
	p := producer(t, uriFor(s.URL, ""))
	require.NoError(t, p.Start(context.Background()))
	require.Error(t, p.Send(gocamel.NewExchange(context.Background())))
	assert.Nil(t, s.req)
}

func TestSend_HeaderOverrideIgnoredByDefault(t *testing.T) {
	s := newStub(t, http.StatusOK, okResponse)
	p := producer(t, uriFor(s.URL, ""))
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("x")
	ex.GetIn().SetHeader(HeaderModel, "evil/model")
	ex.GetIn().SetHeader(HeaderQuestions, `{"q":{"type":"noul","instructions":"?"}}`)
	require.NoError(t, p.Send(ex))

	var sent map[string]any
	require.NoError(t, json.Unmarshal(s.body, &sent))
	assert.Equal(t, DefaultModel, sent["model"])
	assert.Contains(t, sent["questions"], "is_ironic")
}

func TestSend_HeaderOverrideWhenAllowed(t *testing.T) {
	s := newStub(t, http.StatusOK, okResponse)
	p := producer(t, uriFor(s.URL, "&allowHeaderOverride=true"))
	require.NoError(t, p.Start(context.Background()))

	ex := gocamel.NewExchange(context.Background())
	ex.SetBody("x")
	ex.GetIn().SetHeader(HeaderModel, "typesafe/jev-other")
	ex.GetIn().SetHeader(HeaderQuestions, `{"q":{"type":"noul","instructions":"?"}}`)
	require.NoError(t, p.Send(ex))

	var sent map[string]any
	require.NoError(t, json.Unmarshal(s.body, &sent))
	assert.Equal(t, "typesafe/jev-other", sent["model"])
	assert.Contains(t, sent["questions"], "q")
	assert.NotContains(t, sent["questions"], "is_ironic")
}

func TestStart_Validation(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	p := producer(t, "jev:decisions?questions="+url.QueryEscape(testQuestions))
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "apiKey")

	p = producer(t, "jev:decisions?apiKey=k")
	err = p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "questions")
}

func TestStart_APIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	p := producer(t, "jev:decisions?questions="+url.QueryEscape(testQuestions))
	require.NoError(t, p.Start(context.Background()))
	assert.Equal(t, "env-key", p.apiKey)
}

func TestCreateEndpoint_Validation(t *testing.T) {
	c := NewJevComponent()
	for name, uri := range map[string]string{
		"bad baseURL scheme": "jev:decisions?baseURL=ftp://x",
		"bad timeout":        "jev:decisions?timeout=-1s",
		"bad questions json": "jev:decisions?questions=notjson",
		"empty questions":    "jev:decisions?questions=%7B%7D",
	} {
		_, err := c.CreateEndpoint(uri)
		assert.Error(t, err, name)
	}
}

func TestEndpoint_URIRedactsKeyAndNoConsumer(t *testing.T) {
	ep, err := NewJevComponent().CreateEndpoint("jev:decisions?apiKey=secret&questions=" + url.QueryEscape(testQuestions))
	require.NoError(t, err)
	assert.NotContains(t, ep.URI(), "secret")
	_, err = ep.CreateConsumer(nil)
	assert.Error(t, err)
}

func TestSend_NotStarted(t *testing.T) {
	p := producer(t, "jev:decisions?apiKey=k&questions="+url.QueryEscape(testQuestions))
	require.Error(t, p.Send(gocamel.NewExchange(context.Background())))
}
