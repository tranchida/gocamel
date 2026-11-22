package mongodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gocamel "github.com/tranchida/gocamel"
	"go.mongodb.org/mongo-driver/bson"
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

func TestMongoDB_FindOneNoDocsClearsInBody(t *testing.T) {
	_, client := startMockMongoServer(t, func(cmdName string, cmd bson.M) bson.D {
		if cmdName == "find" {
			return bson.D{
				{Key: "cursor", Value: bson.D{
					{Key: "id", Value: int64(0)},
					{Key: "ns", Value: "db.items"},
					{Key: "firstBatch", Value: bson.A{}},
				}},
				{Key: "ok", Value: float64(1)},
			}
		}
		return nil
	})

	comp := NewMongoDBComponent()
	comp.RegisterConnection("mock", CreateMongoDBConnection(client, "mongodb://mock", "db"))

	ep, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=findOne")
	require.NoError(t, err)

	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetBody(bson.M{"_id": "missing"})
	ex.GetOut().SetBody("stale-out")

	require.NoError(t, prod.Send(ex))

	assert.Nil(t, ex.GetIn().GetBody(), "FindOne with no documents must clear In.Body")
	assert.Nil(t, ex.GetOut().GetBody(), "FindOne with no documents must clear Out.Body")
	assert.Nil(t, ex.GetResponse().GetBody(), "GetResponse().GetBody() must be nil when FindOne returns no documents")

	total, ok := ex.GetOut().GetHeader(CamelMongoDbResultTotal)
	assert.True(t, ok, "CamelMongoDbResultTotal must be set on empty FindOne")
	assert.Equal(t, 0, total)
}

func TestMongoDB_ExecUpdateCriteriaTakesPrecedenceOverBodyFilter(t *testing.T) {
	srv, client := startMockMongoServer(t, func(cmdName string, cmd bson.M) bson.D {
		if cmdName == "update" {
			return bson.D{
				{Key: "n", Value: int32(1)},
				{Key: "nModified", Value: int32(1)},
				{Key: "ok", Value: float64(1)},
			}
		}
		return nil
	})

	comp := NewMongoDBComponent()
	comp.RegisterConnection("mock", CreateMongoDBConnection(client, "mongodb://mock", "db"))

	ep, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=update&allowHeaderOverride=true")
	require.NoError(t, err)

	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	t.Run("bson.M criteria header beats body filter", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbCriteria, bson.M{"source": "header"})
		ex.GetIn().SetBody(bson.M{
			"filter": bson.M{"source": "body"},
			"update": bson.M{"$set": bson.M{"updated": true}},
		})

		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		require.NotEmpty(t, cmds)
		last := cmds[len(cmds)-1]
		require.Equal(t, "update", last.Name)
		updates, ok := last.Doc["updates"].([]bson.M)
		require.True(t, ok)
		require.Len(t, updates, 1)
		q, ok := updates[0]["q"].(bson.M)
		require.True(t, ok)
		assert.Equal(t, "header", q["source"], "CamelMongoDbCriteria must take precedence over body filter")
	})

	t.Run("JSON string criteria header beats map body filter", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbCriteria, `{"source":"json-header"}`)
		ex.GetIn().SetBody(map[string]interface{}{
			"filter": map[string]interface{}{"source": "body"},
			"update": bson.M{"$set": bson.M{"updated": true}},
		})

		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		last := cmds[len(cmds)-1]
		updates := last.Doc["updates"].([]bson.M)
		q := updates[0]["q"].(bson.M)
		assert.Equal(t, "json-header", q["source"])
	})

	t.Run("empty JSON string criteria header cannot be bypassed by body filter", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbCriteria, `{}`)
		ex.GetIn().SetBody(bson.M{
			"filter": bson.M{"source": "body"},
			"update": bson.M{"$set": bson.M{"updated": true}},
		})

		err := prod.Send(ex)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty filter rejected")
	})
}

func TestMongoDB_IsEmptyFilterRejectsBSONEmptyTypesAndParsesStringCriteria(t *testing.T) {
	emptyCases := []struct {
		name   string
		filter interface{}
	}{
		{"nil", nil},
		{"empty map", map[string]interface{}{}},
		{"empty bson.M", bson.M{}},
		{"empty bson.D", bson.D{}},
		{"empty bson.Raw", bson.Raw{}},
		{"5-byte empty bson.Raw doc", bson.Raw{5, 0, 0, 0, 0}},
		{"empty struct", struct{}{}},
		{"empty string", ""},
		{"whitespace string", "   \t\n "},
		{"empty JSON object", "{}"},
		{"spaced empty JSON object", " { } "},
		{"invalid JSON string", "{not-json"},
	}

	for _, tc := range emptyCases {
		t.Run("empty_"+tc.name, func(t *testing.T) {
			assert.True(t, isEmptyMongoFilter(tc.filter), "expected filter %#v to be treated as empty", tc.filter)
		})
	}

	nonEmptyRaw, err := bson.Marshal(bson.M{"status": "active"})
	require.NoError(t, err)

	nonEmptyCases := []struct {
		name   string
		filter interface{}
	}{
		{"non-empty bson.M", bson.M{"status": "active"}},
		{"non-empty bson.D", bson.D{{Key: "status", Value: "active"}}},
		{"non-empty bson.Raw", bson.Raw(nonEmptyRaw)},
		{"non-empty struct", struct{ Status string }{Status: "active"}},
		{"non-empty JSON string", `{"status":"active"}`},
	}

	for _, tc := range nonEmptyCases {
		t.Run("nonempty_"+tc.name, func(t *testing.T) {
			assert.False(t, isEmptyMongoFilter(tc.filter), "expected filter %#v to be non-empty", tc.filter)
		})
	}

	prod := &MongoDBProducer{endpoint: &MongoDBEndpoint{allowHeaderOverride: true}}

	ex := gocamel.NewExchange(context.Background())
	ex.GetIn().SetHeader(CamelMongoDbCriteria, `{"kind":"json"}`)
	got, err := prod.extractFilter(ex)
	require.NoError(t, err)
	assert.Equal(t, bson.M{"kind": "json"}, got)

	exEmpty := gocamel.NewExchange(context.Background())
	exEmpty.GetIn().SetHeader(CamelMongoDbCriteria, `{}`)
	got, err = prod.extractFilter(exEmpty)
	require.NoError(t, err)
	assert.True(t, isEmptyMongoFilter(got))

	exWhitespace := gocamel.NewExchange(context.Background())
	exWhitespace.SetBody("   ")
	got, err = prod.extractFilter(exWhitespace)
	require.NoError(t, err)
	assert.True(t, isEmptyMongoFilter(got))

	exInvalid := gocamel.NewExchange(context.Background())
	exInvalid.GetIn().SetHeader(CamelMongoDbCriteria, "{bad-json")
	_, err = prod.extractFilter(exInvalid)
	assert.Error(t, err)
}

func TestMongoDB_AcceptsBsonMAndBsonDSortAndRejectsNilConnAndInvalidBool(t *testing.T) {
	comp := NewMongoDBComponent()
	comp.RegisterConnection("nilconn", nil)
	comp.SetDefaultConnection(nil)

	t.Run("rejects nil connection without panicking", func(t *testing.T) {
		assert.NotPanics(t, func() {
			_, err := comp.CreateEndpoint("mongodb://nilconn?database=db&collection=items&operation=find")
			assert.Error(t, err)
		})
	})

	srv, client := startMockMongoServer(t, func(cmdName string, cmd bson.M) bson.D {
		switch cmdName {
		case "find":
			return bson.D{
				{Key: "cursor", Value: bson.D{
					{Key: "id", Value: int64(0)},
					{Key: "ns", Value: "db.items"},
					{Key: "firstBatch", Value: bson.A{bson.D{{Key: "name", Value: "a"}}}},
				}},
				{Key: "ok", Value: float64(1)},
			}
		case "insert":
			return bson.D{{Key: "n", Value: int32(1)}, {Key: "ok", Value: float64(1)}}
		case "update":
			return bson.D{{Key: "n", Value: int32(1)}, {Key: "nModified", Value: int32(1)}, {Key: "ok", Value: float64(1)}}
		default:
			return bson.D{{Key: "ok", Value: float64(1)}}
		}
	})

	comp.RegisterConnection("mock", CreateMongoDBConnection(client, "mongodb://mock", "db"))

	t.Run("rejects invalid allowHeaderOverride bool", func(t *testing.T) {
		_, err := comp.CreateEndpoint("mongodb://mock?collection=items&allowHeaderOverride=maybe")
		assert.Error(t, err, "invalid allowHeaderOverride boolean must be rejected")
	})

	t.Run("accepts bson.M in insertOne", func(t *testing.T) {
		epInsertOne, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=insertOne")
		require.NoError(t, err)
		prodInsertOne, err := epInsertOne.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prodInsertOne.Start(context.Background()))
		defer func() { _ = prodInsertOne.Stop() }()

		ex1 := gocamel.NewExchange(context.Background())
		ex1.SetBody(bson.M{"name": "one"})
		assert.NoError(t, prodInsertOne.Send(ex1))
	})

	t.Run("accepts []bson.M in insert", func(t *testing.T) {
		epInsert, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=insert")
		require.NoError(t, err)
		prodInsert, err := epInsert.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prodInsert.Start(context.Background()))
		defer func() { _ = prodInsert.Stop() }()

		ex2 := gocamel.NewExchange(context.Background())
		ex2.SetBody([]bson.M{{"name": "many"}})
		assert.NoError(t, prodInsert.Send(ex2))
	})

	t.Run("accepts bson.M in save", func(t *testing.T) {
		epSave, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=save")
		require.NoError(t, err)
		prodSave, err := epSave.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prodSave.Start(context.Background()))
		defer func() { _ = prodSave.Stop() }()

		ex3 := gocamel.NewExchange(context.Background())
		ex3.SetBody(bson.M{"_id": "doc-1", "name": "saved"})
		assert.NoError(t, prodSave.Send(ex3))

		ex4 := gocamel.NewExchange(context.Background())
		ex4.SetBody(bson.M{"name": "new-saved"})
		assert.NoError(t, prodSave.Send(ex4))
	})

	t.Run("accepts bson.D in CamelMongoDbSort", func(t *testing.T) {
		epFind, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=find")
		require.NoError(t, err)
		prodFind, err := epFind.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, prodFind.Start(context.Background()))
		defer func() { _ = prodFind.Stop() }()

		ex5 := gocamel.NewExchange(context.Background())
		ex5.GetIn().SetHeader(CamelMongoDbSort, bson.D{{Key: "name", Value: 1}, {Key: "age", Value: -1}})
		require.NoError(t, prodFind.Send(ex5))

		cmds := srv.Commands()
		var findCmd bson.M
		for _, c := range cmds {
			if c.Name == "find" {
				findCmd = c.Doc
			}
		}
		require.NotNil(t, findCmd)
		assert.Contains(t, findCmd, "sort", "bson.D CamelMongoDbSort must be forwarded to MongoDB find command")
	})
}

func TestMongoDB_OperationsAndHeaderOverridesCoverage(t *testing.T) {
	srv, client := startMockMongoServer(t, func(cmdName string, cmd bson.M) bson.D {
		switch cmdName {
		case "find":
			if limit, ok := cmd["limit"]; ok && (limit == int32(1) || limit == int64(1)) {
				return bson.D{
					{Key: "cursor", Value: bson.D{
						{Key: "id", Value: int64(0)},
						{Key: "ns", Value: "db.items"},
						{Key: "firstBatch", Value: bson.A{bson.D{{Key: "_id", Value: "1"}, {Key: "name", Value: "found"}}}},
					}},
					{Key: "ok", Value: float64(1)},
				}
			}
			// Return 3 docs with non-zero cursor ID when maxResults=2 to also exercise cursor.Close (killCursors).
			return bson.D{
				{Key: "cursor", Value: bson.D{
					{Key: "id", Value: int64(99)},
					{Key: "ns", Value: "db.items"},
					{Key: "firstBatch", Value: bson.A{
						bson.D{{Key: "i", Value: 1}},
						bson.D{{Key: "i", Value: 2}},
						bson.D{{Key: "i", Value: 3}},
					}},
				}},
				{Key: "ok", Value: float64(1)},
			}
		case "aggregate":
			return bson.D{
				{Key: "cursor", Value: bson.D{
					{Key: "id", Value: int64(0)},
					{Key: "ns", Value: "db.items"},
					{Key: "firstBatch", Value: bson.A{bson.D{{Key: "n", Value: int64(7)}}}},
				}},
				{Key: "ok", Value: float64(1)},
			}
		case "insert":
			return bson.D{{Key: "n", Value: int32(2)}, {Key: "ok", Value: float64(1)}}
		case "delete":
			return bson.D{{Key: "n", Value: int32(3)}, {Key: "ok", Value: float64(1)}}
		default:
			return bson.D{{Key: "ok", Value: float64(1)}}
		}
	})

	comp := NewMongoDBComponent()
	comp.SetDefaultConnection(CreateMongoDBConnection(client, "mongodb://default", "defaultdb"))

	epDefault, err := comp.CreateEndpoint("mongodb://?collection=items&operation=find&maxResults=2")
	require.NoError(t, err)
	assert.Equal(t, "mongodb://?collection=items&operation=find&maxResults=2", epDefault.URI())

	_, err = epDefault.CreateConsumer(nil)
	assert.Error(t, err)

	prod, err := epDefault.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	t.Run("find with limit skip sort and truncation closes cursor", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbLimit, 5)
		ex.GetIn().SetHeader(CamelMongoDbSkip, 1)
		ex.GetIn().SetHeader(CamelMongoDbSort, bson.M{"i": 1})
		ex.SetBody(`{"status":"ok"}`)
		require.NoError(t, prod.Send(ex))

		truncated, ok := ex.GetOut().GetHeader(CamelMongoDbResultsTruncated)
		require.True(t, ok)
		assert.Equal(t, true, truncated)

		total, ok := ex.GetOut().GetHeader(CamelMongoDbResultTotal)
		require.True(t, ok)
		assert.Equal(t, 2, total)

		// Verify killCursors was sent on cursor close.
		var sawKill bool
		for _, c := range srv.Commands() {
			if c.Name == "killCursors" {
				sawKill = true
			}
		}
		assert.True(t, sawKill, "cursor.Close must send killCursors for open cursor")
	})

	t.Run("header overrides for database collection operation and count/findOne/remove/insert", func(t *testing.T) {
		epOverride, err := comp.CreateEndpoint("mongodb://?allowHeaderOverride=true&allowDeleteAll=true")
		require.NoError(t, err)
		p2, err := epOverride.CreateProducer()
		require.NoError(t, err)
		require.NoError(t, p2.Start(context.Background()))
		defer func() { _ = p2.Stop() }()

		// count
		exCount := gocamel.NewExchange(context.Background())
		exCount.SetHeader(CamelMongoDbDatabase, "otherdb")
		exCount.SetHeader(CamelMongoDbCollection, "items")
		exCount.SetHeader(CamelMongoDbOperation, "count")
		require.NoError(t, p2.Send(exCount))
		assert.Equal(t, int64(7), exCount.GetOut().GetBody())

		// findOne found
		exFindOne := gocamel.NewExchange(context.Background())
		exFindOne.SetHeader(CamelMongoDbDatabase, "otherdb")
		exFindOne.SetHeader(CamelMongoDbCollection, "items")
		exFindOne.SetHeader(CamelMongoDbOperation, "findOne")
		exFindOne.SetBody(bson.D{{Key: "_id", Value: "1"}})
		require.NoError(t, p2.Send(exFindOne))
		bodyMap, ok := exFindOne.GetOut().GetBody().(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "found", bodyMap["name"])
		assert.Equal(t, 1, exFindOne.GetOut().Headers[CamelMongoDbResultTotal])

		// remove with allowDeleteAll=true
		exRemove := gocamel.NewExchange(context.Background())
		exRemove.SetHeader(CamelMongoDbDatabase, "otherdb")
		exRemove.SetHeader(CamelMongoDbCollection, "items")
		exRemove.SetHeader(CamelMongoDbOperation, "remove")
		require.NoError(t, p2.Send(exRemove))
		assert.Equal(t, int64(3), exRemove.GetOut().GetBody())

		// insert empty slice & []map[string]interface{}
		exInsertEmpty := gocamel.NewExchange(context.Background())
		exInsertEmpty.SetHeader(CamelMongoDbDatabase, "otherdb")
		exInsertEmpty.SetHeader(CamelMongoDbCollection, "items")
		exInsertEmpty.SetHeader(CamelMongoDbOperation, "insert")
		exInsertEmpty.SetBody([]interface{}{})
		require.NoError(t, p2.Send(exInsertEmpty))
		assert.Equal(t, 0, exInsertEmpty.GetOut().Headers[CamelMongoDbResultTotal])

		exInsertMaps := gocamel.NewExchange(context.Background())
		exInsertMaps.SetHeader(CamelMongoDbDatabase, "otherdb")
		exInsertMaps.SetHeader(CamelMongoDbCollection, "items")
		exInsertMaps.SetHeader(CamelMongoDbOperation, "insert")
		exInsertMaps.SetBody([]map[string]interface{}{{"a": 1}, {"a": 2}})
		require.NoError(t, p2.Send(exInsertMaps))
		assert.Equal(t, 2, exInsertMaps.GetOut().Headers[CamelMongoDbResultTotal])

		// unsupported operation & missing operation & nil exchange
		exBadOp := gocamel.NewExchange(context.Background())
		exBadOp.SetHeader(CamelMongoDbDatabase, "otherdb")
		exBadOp.SetHeader(CamelMongoDbCollection, "items")
		exBadOp.SetHeader(CamelMongoDbOperation, "dropDatabase")
		assert.Error(t, p2.Send(exBadOp))

		exNoOp := gocamel.NewExchange(context.Background())
		assert.Error(t, p2.Send(exNoOp))
		assert.Error(t, p2.Send(nil))
	})
}

func TestMongoDB_UpdateRejectsInvalidOrUnmarshalableFilterEvenWithAllowUpdateAll(t *testing.T) {
	srv, client := startMockMongoServer(t, func(cmdName string, cmd bson.M) bson.D {
		if cmdName == "update" {
			return bson.D{
				{Key: "n", Value: int32(1)},
				{Key: "nModified", Value: int32(1)},
				{Key: "ok", Value: float64(1)},
			}
		}
		return bson.D{{Key: "ok", Value: float64(1)}}
	})

	comp := NewMongoDBComponent()
	comp.RegisterConnection("mock", CreateMongoDBConnection(client, "mongodb://mock", "db"))

	ep, err := comp.CreateEndpoint("mongodb://mock?collection=items&operation=update&allowUpdateAll=true&allowHeaderOverride=true")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	invalidFilters := []struct {
		name   string
		filter interface{}
	}{
		{"int", 123},
		{"float64", 3.14},
		{"slice", []interface{}{"a"}},
		{"unmarshalable map", map[string]interface{}{"fn": func() {}}},
		{"unmarshalable bson.M", bson.M{"fn": func() {}}},
		{"unmarshalable bson.D", bson.D{{Key: "fn", Value: func() {}}}},
		{"malformed bson.Raw", bson.Raw{1, 2, 3}},
		{"invalid JSON string", "{bad-json"},
	}

	for _, tc := range invalidFilters {
		t.Run("body_filter/"+tc.name, func(t *testing.T) {
			beforeCmds := len(srv.Commands())
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody(map[string]interface{}{
				"filter": tc.filter,
				"update": bson.M{"$set": bson.M{"updated": true}},
			})
			err := prod.Send(ex)
			require.Error(t, err, "invalid body filter %T must fail even with allowUpdateAll=true", tc.filter)
			assert.Len(t, srv.Commands(), beforeCmds, "must not send UpdateMany to MongoDB on invalid filter")
		})

		t.Run("header_criteria/"+tc.name, func(t *testing.T) {
			beforeCmds := len(srv.Commands())
			ex := gocamel.NewExchange(context.Background())
			ex.GetIn().SetHeader(CamelMongoDbCriteria, tc.filter)
			ex.SetBody(bson.M{"$set": bson.M{"updated": true}})
			err := prod.Send(ex)
			require.Error(t, err, "invalid criteria header %T must fail even with allowUpdateAll=true", tc.filter)
			assert.Len(t, srv.Commands(), beforeCmds, "must not send UpdateMany to MongoDB on invalid criteria")
		})
	}

	validEmptyFilters := []struct {
		name   string
		filter interface{}
	}{
		{"nil", nil},
		{"empty string", ""},
		{"empty JSON", "{}"},
		{"empty bson.M", bson.M{}},
		{"empty bson.D", bson.D{}},
		{"empty bson.Raw", bson.Raw{}},
	}

	for _, tc := range validEmptyFilters {
		t.Run("valid_empty_with_allowUpdateAll/"+tc.name, func(t *testing.T) {
			ex := gocamel.NewExchange(context.Background())
			ex.SetBody(map[string]interface{}{
				"filter": tc.filter,
				"update": bson.M{"$set": bson.M{"updated": true}},
			})
			require.NoError(t, prod.Send(ex))
			cmds := srv.Commands()
			require.NotEmpty(t, cmds)
			last := cmds[len(cmds)-1]
			require.Equal(t, "update", last.Name)
			updates := last.Doc["updates"].([]bson.M)
			assert.Equal(t, bson.M{}, updates[0]["q"])
		})
	}
}

func TestMongoDB_NilCriteriaHeaderFallsBackToBodyFilterConsistently(t *testing.T) {
	srv, client := startMockMongoServer(t, func(cmdName string, cmd bson.M) bson.D {
		switch cmdName {
		case "find":
			return bson.D{
				{Key: "cursor", Value: bson.D{
					{Key: "id", Value: int64(0)},
					{Key: "ns", Value: "db.items"},
					{Key: "firstBatch", Value: bson.A{bson.D{{Key: "source", Value: "body"}}}},
				}},
				{Key: "ok", Value: float64(1)},
			}
		case "aggregate":
			return bson.D{
				{Key: "cursor", Value: bson.D{
					{Key: "id", Value: int64(0)},
					{Key: "ns", Value: "db.items"},
					{Key: "firstBatch", Value: bson.A{bson.D{{Key: "n", Value: int64(1)}}}},
				}},
				{Key: "ok", Value: float64(1)},
			}
		case "delete":
			return bson.D{{Key: "n", Value: int32(1)}, {Key: "ok", Value: float64(1)}}
		case "update":
			return bson.D{{Key: "n", Value: int32(1)}, {Key: "nModified", Value: int32(1)}, {Key: "ok", Value: float64(1)}}
		default:
			return bson.D{{Key: "ok", Value: float64(1)}}
		}
	})

	comp := NewMongoDBComponent()
	comp.RegisterConnection("mock", CreateMongoDBConnection(client, "mongodb://mock", "db"))

	ep, err := comp.CreateEndpoint("mongodb://mock?collection=items&allowHeaderOverride=true")
	require.NoError(t, err)
	prod, err := ep.CreateProducer()
	require.NoError(t, err)
	require.NoError(t, prod.Start(context.Background()))
	defer func() { _ = prod.Stop() }()

	t.Run("find falls back to body filter when CamelMongoDbCriteria is nil", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbOperation, "find")
		ex.GetIn().SetHeader(CamelMongoDbCriteria, nil)
		ex.SetBody(bson.M{"source": "body"})
		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		last := cmds[len(cmds)-1]
		require.Equal(t, "find", last.Name)
		assert.Equal(t, bson.M{"source": "body"}, last.Doc["filter"])
	})

	t.Run("findOne falls back to body filter when CamelMongoDbCriteria is nil", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbOperation, "findOne")
		ex.GetIn().SetHeader(CamelMongoDbCriteria, nil)
		ex.SetBody(bson.M{"source": "body"})
		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		last := cmds[len(cmds)-1]
		require.Equal(t, "find", last.Name)
		assert.Equal(t, bson.M{"source": "body"}, last.Doc["filter"])
	})

	t.Run("count falls back to body filter when CamelMongoDbCriteria is nil", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbOperation, "count")
		ex.GetIn().SetHeader(CamelMongoDbCriteria, nil)
		ex.SetBody(bson.M{"source": "body"})
		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		last := cmds[len(cmds)-1]
		require.Equal(t, "aggregate", last.Name)
		pipeline, ok := last.Doc["pipeline"].(bson.A)
		require.True(t, ok)
		require.NotEmpty(t, pipeline)
		firstStage, ok := pipeline[0].(bson.M)
		require.True(t, ok)
		assert.Equal(t, bson.M{"source": "body"}, firstStage["$match"])
	})

	t.Run("remove falls back to body filter when CamelMongoDbCriteria is nil", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbOperation, "remove")
		ex.GetIn().SetHeader(CamelMongoDbCriteria, nil)
		ex.SetBody(bson.M{"source": "body"})
		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		last := cmds[len(cmds)-1]
		require.Equal(t, "delete", last.Name)
		deletes := last.Doc["deletes"].([]bson.M)
		assert.Equal(t, bson.M{"source": "body"}, deletes[0]["q"])
	})

	t.Run("update falls back to body filter when CamelMongoDbCriteria is nil", func(t *testing.T) {
		ex := gocamel.NewExchange(context.Background())
		ex.GetIn().SetHeader(CamelMongoDbOperation, "update")
		ex.GetIn().SetHeader(CamelMongoDbCriteria, nil)
		ex.SetBody(bson.M{
			"filter": bson.M{"source": "body"},
			"update": bson.M{"$set": bson.M{"ok": true}},
		})
		require.NoError(t, prod.Send(ex))

		cmds := srv.Commands()
		last := cmds[len(cmds)-1]
		require.Equal(t, "update", last.Name)
		updates := last.Doc["updates"].([]bson.M)
		assert.Equal(t, bson.M{"source": "body"}, updates[0]["q"])
	})
}
