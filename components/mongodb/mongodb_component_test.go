package mongodb

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"

	gocamel "gitlab.com/tranchida/gocamel"
)

func TestMongoDBProducer_RemoveRejectsEmptyFilter(t *testing.T) {
	tests := []struct {
		name                string
		criteria            interface{}
		body                interface{}
		allowHeaderOverride bool
	}{
		{name: "nil body"},
		{name: "empty map body", body: bson.M{}},
		{name: "empty JSON document", body: "{}"},
		// An empty criteria header only applies when the endpoint opts in
		// with allowHeaderOverride=true.
		{name: "empty criteria header", criteria: bson.D{}, body: bson.M{"id": 1}, allowHeaderOverride: true},
		// Without the opt-in, the header is ignored and the body filter is used.
		{name: "empty criteria header ignored without opt-in", criteria: bson.D{}, body: bson.M{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{
				allowHeaderOverride: tt.allowHeaderOverride,
			}}

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

func TestMongoDBComponent_AllowHeaderOverrideParsing(t *testing.T) {
	component := NewMongoDBComponent()
	component.RegisterConnection("test", CreateMongoDBConnection(nil, "mongodb://test", "db"))

	// Default: allowHeaderOverride is false.
	endpoint, err := component.CreateEndpoint("mongodb://test?collection=items&operation=find")
	if err != nil {
		t.Fatalf("CreateEndpoint failed: %v", err)
	}
	if endpoint.(*MongoDBEndpoint).allowHeaderOverride {
		t.Fatal("expected allowHeaderOverride to default to false")
	}

	// Explicit opt-in.
	endpoint, err = component.CreateEndpoint("mongodb://test?collection=items&operation=find&allowHeaderOverride=true")
	if err != nil {
		t.Fatalf("CreateEndpoint failed: %v", err)
	}
	if !endpoint.(*MongoDBEndpoint).allowHeaderOverride {
		t.Fatal("expected allowHeaderOverride to be enabled")
	}
}

func TestMongoDBProducer_CollectionHeadersIgnoredWithoutOptIn(t *testing.T) {
	// Without allowHeaderOverride, CamelMongoDbDatabase/CamelMongoDbCollection
	// headers are ignored: an endpoint relying on them must fail with the
	// "not configured" errors instead of resolving them from headers.
	producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{
		connection: &MongoDBConnection{},
	}}

	exchange := gocamel.NewExchange(context.Background())
	exchange.SetHeader(CamelMongoDbDatabase, "headerdb")
	exchange.SetHeader(CamelMongoDbCollection, "headercoll")

	_, err := producer.getCollection(exchange)
	if err == nil || !strings.Contains(err.Error(), "no database configured") {
		t.Fatalf("expected 'no database configured' error, got %v", err)
	}
}

func TestMongoDBProducer_OperationHeaderRequiresOptIn(t *testing.T) {
	tests := []struct {
		name                string
		allowHeaderOverride bool
		wantOperation       string
	}{
		{name: "without flag the URI operation is used", allowHeaderOverride: false, wantOperation: "find"},
		{name: "with flag the header operation is used", allowHeaderOverride: true, wantOperation: "remove"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{
				operation:           "find",
				allowHeaderOverride: tt.allowHeaderOverride,
			}}

			exchange := gocamel.NewExchange(context.Background())
			exchange.SetHeader(CamelMongoDbOperation, "remove")

			if got := producer.getOperation(exchange); got != tt.wantOperation {
				t.Fatalf("expected operation %q, got %q", tt.wantOperation, got)
			}
		})
	}
}

func TestMongoDBProducer_CriteriaHeaderRequiresOptIn(t *testing.T) {
	bodyFilter := bson.M{"status": "active"}
	headerFilter := bson.M{"status": "deleted"}

	tests := []struct {
		name                string
		allowHeaderOverride bool
		wantFilter          interface{}
	}{
		{name: "without flag the body filter is used", allowHeaderOverride: false, wantFilter: bodyFilter},
		{name: "with flag the header criteria is used", allowHeaderOverride: true, wantFilter: headerFilter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{
				allowHeaderOverride: tt.allowHeaderOverride,
			}}

			exchange := gocamel.NewExchange(context.Background())
			exchange.SetBody(bodyFilter)
			exchange.SetHeader(CamelMongoDbCriteria, headerFilter)

			got, err := producer.extractFilter(exchange)
			if err != nil {
				t.Fatalf("extractFilter failed: %v", err)
			}
			if !reflect.DeepEqual(got, tt.wantFilter) {
				t.Fatalf("expected filter %v, got %v", tt.wantFilter, got)
			}
		})
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
