package mongodb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCursor yields n documents {"i": index} and then optionally fails.
type fakeCursor struct {
	n, pos int
	err    error
}

func (c *fakeCursor) Next(context.Context) bool {
	if c.pos >= c.n {
		return false
	}
	c.pos++
	return true
}

func (c *fakeCursor) Decode(v interface{}) error {
	*(v.(*map[string]interface{})) = map[string]interface{}{"i": c.pos - 1}
	return nil
}

func (c *fakeCursor) Err() error { return c.err }

func TestDrainCursor_StopsAtMaxResults(t *testing.T) {
	cur := &fakeCursor{n: 50}
	results, truncated, err := drainCursor(context.Background(), cur, 10)
	require.NoError(t, err)
	assert.Len(t, results, 10)
	assert.True(t, truncated)
	assert.Equal(t, 9, results[9]["i"])
	assert.LessOrEqual(t, cur.pos, 11, "must not read far past the cap")
}

func TestDrainCursor_UnderCapNotTruncated(t *testing.T) {
	results, truncated, err := drainCursor(context.Background(), &fakeCursor{n: 10}, 10)
	require.NoError(t, err)
	assert.Len(t, results, 10)
	assert.False(t, truncated)
}

func TestDrainCursor_ZeroMeansUnlimited(t *testing.T) {
	results, truncated, err := drainCursor(context.Background(), &fakeCursor{n: 500}, 0)
	require.NoError(t, err)
	assert.Len(t, results, 500)
	assert.False(t, truncated)
}

func TestDrainCursor_PropagatesCursorError(t *testing.T) {
	_, _, err := drainCursor(context.Background(), &fakeCursor{n: 2, err: errors.New("boom")}, 10)
	assert.ErrorContains(t, err, "boom")
}

func TestMongoDBComponent_MaxResultsOption(t *testing.T) {
	component := NewMongoDBComponent()
	component.RegisterConnection("test", CreateMongoDBConnection(nil, "mongodb://test", "db"))

	endpoint, err := component.CreateEndpoint("mongodb://test?collection=items&operation=find")
	require.NoError(t, err)
	assert.Equal(t, int64(DefaultMaxResults), endpoint.(*MongoDBEndpoint).maxResults)

	endpoint, err = component.CreateEndpoint("mongodb://test?collection=items&operation=find&maxResults=0")
	require.NoError(t, err)
	assert.Equal(t, int64(0), endpoint.(*MongoDBEndpoint).maxResults)

	_, err = component.CreateEndpoint("mongodb://test?collection=items&maxResults=-1")
	assert.Error(t, err)
}
