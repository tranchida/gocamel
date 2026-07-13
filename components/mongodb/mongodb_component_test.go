package mongodb

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestMongoDBProducer_RemoveRejectsEmptyFilter(t *testing.T) {
	producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{}}

	tests := []struct {
		name     string
		criteria interface{}
		body     interface{}
	}{
		{name: "nil body"},
		{name: "empty map body", body: bson.M{}},
		{name: "empty JSON document", body: "{}"},
		{name: "empty criteria header", criteria: bson.D{}, body: bson.M{"id": 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exchange := gocamel.NewExchange(context.Background())
			exchange.SetBody(tt.body)
			if tt.criteria != nil {
				exchange.SetHeader(CamelMongoDbCriteria, tt.criteria)
			}

			err := producer.execRemove(context.Background(), exchange, nil)
			if err == nil || !strings.Contains(err.Error(), "empty filter rejected") {
				t.Fatalf("expected empty-filter rejection, got %v", err)
			}
		})
	}
}

func TestMongoDBComponent_AllowDeleteAllRequiresExplicitOptIn(t *testing.T) {
	component := NewMongoDBComponent()
	component.RegisterConnection("test", CreateMongoDBConnection(nil, "mongodb://test", "db"))

	endpoint, err := component.CreateEndpoint("mongodb://test?collection=items&operation=remove&allowDeleteAll=true")
	if err != nil {
		t.Fatalf("CreateEndpoint failed: %v", err)
	}
	if !endpoint.(*MongoDBEndpoint).allowDeleteAll {
		t.Fatal("expected allowDeleteAll to be enabled")
	}

	if _, err := component.CreateEndpoint("mongodb://test?collection=items&allowDeleteAll=maybe"); err == nil {
		t.Fatal("expected invalid allowDeleteAll value to be rejected")
	}
}

func TestIsEmptyMongoFilter(t *testing.T) {
	if !isEmptyMongoFilter(map[string]interface{}{}) || !isEmptyMongoFilter(bson.D{}) {
		t.Fatal("expected empty map and document to be detected")
	}
	if isEmptyMongoFilter(bson.M{"status": "active"}) {
		t.Fatal("non-empty filter must not be treated as empty")
	}
}
