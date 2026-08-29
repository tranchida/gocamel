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

func TestMongoDBProducer_UpdateRejectsEmptyFilterWithoutOptIn(t *testing.T) {
	// A body without a "filter" key must not fall through to an empty
	// filter that would mass-update the whole collection.
	producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{}}

	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody(bson.M{"$set": bson.M{"status": "archived"}})

	err := producer.execUpdate(context.Background(), exchange, nil)
	if err == nil || !strings.Contains(err.Error(), "empty filter rejected") {
		t.Fatalf("expected empty-filter rejection, got %v", err)
	}
	if !strings.Contains(err.Error(), "allowUpdateAll") {
		t.Fatalf("expected the error to mention allowUpdateAll, got %v", err)
	}
}

func TestMongoDBProducer_UpdateRejectsEmptyMapFilterWithoutOptIn(t *testing.T) {
	producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{}}
	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody(map[string]interface{}{
		"filter": map[string]interface{}{},
		"update": bson.M{"$set": bson.M{"status": "archived"}},
	})
	err := producer.execUpdate(context.Background(), exchange, nil)
	if err == nil || !strings.Contains(err.Error(), "empty filter rejected") {
		t.Fatalf("expected empty-filter rejection, got %v", err)
	}
}

func TestMongoDBProducer_UpdateAllowsEmptyFilterWithOptIn(t *testing.T) {
	// With the opt-in, an update-only body passes the empty-filter guard and
	// reaches UpdateMany. The mongo driver panics on a nil collection (method
	// on a nil *Collection), so the panic itself is the proof the guard was
	// passed: the no-opt-in sibling test never gets this far.
	producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{allowUpdateAll: true}}

	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody(bson.M{"$set": bson.M{"status": "archived"}})

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = producer.execUpdate(context.Background(), exchange, nil)
	}()
	if !panicked {
		t.Fatal("expected execUpdate to reach UpdateMany (nil-collection panic) with allowUpdateAll=true")
	}
}

func TestMongoDBProducer_UpdateWithExplicitFilterNotRejected(t *testing.T) {
	// Sanity check: a body carrying an explicit "filter" key never hits the
	// guard, regardless of the opt-in flag. It reaches UpdateMany, which
	// panics on the nil collection (see the opt-in test above for why the
	// panic is the expected observable).
	producer := &MongoDBProducer{endpoint: &MongoDBEndpoint{}}

	exchange := gocamel.NewExchange(context.Background())
	exchange.SetBody(bson.M{"filter": bson.M{"status": "active"}, "update": bson.M{"$set": bson.M{"seen": true}}})

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = producer.execUpdate(context.Background(), exchange, nil)
	}()
	if !panicked {
		t.Fatal("expected execUpdate to reach UpdateMany with an explicit filter")
	}
}

func TestMongoDBComponent_AllowUpdateAllRequiresExplicitOptIn(t *testing.T) {
	component := NewMongoDBComponent()
	component.RegisterConnection("test", CreateMongoDBConnection(nil, "mongodb://test", "db"))

	endpoint, err := component.CreateEndpoint("mongodb://test?collection=items&operation=update&allowUpdateAll=true")
	if err != nil {
		t.Fatalf("CreateEndpoint failed: %v", err)
	}
	if !endpoint.(*MongoDBEndpoint).allowUpdateAll {
		t.Fatal("expected allowUpdateAll to be enabled")
	}

	endpoint, err = component.CreateEndpoint("mongodb://test?collection=items&operation=update")
	if err != nil {
		t.Fatalf("CreateEndpoint failed: %v", err)
	}
	if endpoint.(*MongoDBEndpoint).allowUpdateAll {
		t.Fatal("allowUpdateAll must default to false")
	}

	if _, err := component.CreateEndpoint("mongodb://test?collection=items&allowUpdateAll=maybe"); err == nil {
		t.Fatal("expected invalid allowUpdateAll value to be rejected")
	}
}
