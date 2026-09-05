package mongodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "gitlab.com/tranchida/gocamel"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestReviewSavePreservesIDOnFailure(t *testing.T) {
	// NewClient intentionally avoids starting monitor goroutines: this test
	// exercises the failed-operation path without a MongoDB deployment.
	//lint:ignore SA1019 NewClient is required for this no-I/O regression test.
	client, err := mongo.NewClient()
	require.NoError(t, err)
	coll := client.Database("test").Collection("items")
	doc := map[string]interface{}{"_id": "existing-id", "value": "new"}
	ex := gocamel.NewExchange(context.Background())
	ex.SetBody(doc)
	p := &MongoDBProducer{}
	require.Error(t, p.execSave(context.Background(), ex, coll))
	assert.Equal(t, "existing-id", doc["_id"], "a retry must update the same document")
}
