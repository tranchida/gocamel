package gocamel

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newTestSQLSagaRepository(t *testing.T) *SQLSagaRepository {
	t.Helper()
	// synchronous(off): the bounded-scan test inserts over a thousand rows.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "sagas.db")+"?_pragma=synchronous(off)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewSQLSagaRepository(db, SQLSagaOptions{})
	require.NoError(t, repo.InitDB(context.Background()))
	return repo
}

func TestSQLSagaRepository_Contract(t *testing.T) {
	runSagaRepositoryContract(t, func(t *testing.T) SagaRepository {
		return newTestSQLSagaRepository(t)
	})
}

func TestSQLSagaRepository_InitDBIsIdempotent(t *testing.T) {
	repo := newTestSQLSagaRepository(t)
	assert.NoError(t, repo.InitDB(context.Background()))
}

func TestSQLSagaRepository_DefaultTableName(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	repo := NewSQLSagaRepository(db, SQLSagaOptions{})
	ctx := context.Background()
	require.NoError(t, repo.InitDB(ctx))
	require.NoError(t, repo.Save(ctx, "s1", SagaState{SagaID: "s1", Status: SagaRunning}))

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM gocamel_sagas").Scan(&count))
	assert.Equal(t, 1, count)
}

func TestSQLSagaRepository_RejectsInvalidTableName(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := NewSQLSagaRepository(db, SQLSagaOptions{TableName: "sagas; DROP TABLE x"})
	ctx := context.Background()
	assert.Error(t, repo.InitDB(ctx))
	assert.Error(t, repo.Save(ctx, "s1", SagaState{SagaID: "s1"}))
	_, err = repo.Load(ctx, "s1")
	assert.Error(t, err)
	_, err = repo.ListIncomplete(ctx)
	assert.Error(t, err)
	assert.Error(t, repo.Remove(ctx, "s1"))
}

func TestSQLSagaRepository_UnserializableMessageFailsSave(t *testing.T) {
	repo := newTestSQLSagaRepository(t)
	err := repo.Save(context.Background(), "s1", SagaState{
		SagaID:  "s1",
		Status:  SagaRunning,
		Message: SagaMessage{Body: make(chan int)},
	})
	assert.Error(t, err)
}

// The persistent-saga scenarios also run against SQLite so that recovery is
// exercised through a JSON round trip, as after a real process restart.
func TestSagaRecovery_SQLRepository(t *testing.T) {
	testSagaRecoveryAfterCrash(t, newTestSQLSagaRepository(t))
}

func TestSQLSagaRepository_GuardsNilDBAndCreatesIndexAndZeroUpdatedAt(t *testing.T) {
	ctx := context.Background()

	t.Run("nil db fails without panic", func(t *testing.T) {
		for _, repo := range []*SQLSagaRepository{
			NewSQLSagaRepository(nil, SQLSagaOptions{}),
			{},
		} {
			assert.Error(t, repo.InitDB(ctx))
			assert.Error(t, repo.Save(ctx, "s1", SagaState{SagaID: "s1", Status: SagaRunning}))
			_, err := repo.Load(ctx, "s1")
			assert.Error(t, err)
			_, err = repo.ListIncomplete(ctx)
			assert.Error(t, err)
			assert.Error(t, repo.Remove(ctx, "s1"))
		}
	})

	t.Run("composite index on status, updated_at, saga_id", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)

		repo := NewSQLSagaRepository(db, SQLSagaOptions{})
		require.NoError(t, repo.InitDB(ctx))

		rows, err := db.Query("PRAGMA index_list('gocamel_sagas')")
		require.NoError(t, err)
		defer rows.Close()

		var indexNames []string
		for rows.Next() {
			var seq int
			var name string
			var unique int
			var origin string
			var partial int
			require.NoError(t, rows.Scan(&seq, &name, &unique, &origin, &partial))
			indexNames = append(indexNames, name)
		}
		require.NoError(t, rows.Err())

		foundComposite := false
		for _, idxName := range indexNames {
			infoRows, err := db.Query("PRAGMA index_info('" + idxName + "')")
			require.NoError(t, err)
			var cols []string
			for infoRows.Next() {
				var seqno, cid int
				var colName string
				require.NoError(t, infoRows.Scan(&seqno, &cid, &colName))
				cols = append(cols, colName)
			}
			require.NoError(t, infoRows.Err())
			_ = infoRows.Close()
			if len(cols) == 3 && cols[0] == "status" && cols[1] == "updated_at" && cols[2] == "saga_id" {
				foundComposite = true
			}
		}
		assert.True(t, foundComposite, "expected composite index on (status, updated_at, saga_id), got indexes %v", indexNames)
	})

	t.Run("zero UpdatedAt sorts before real timestamps", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		db.SetMaxOpenConns(1)

		repo := NewSQLSagaRepository(db, SQLSagaOptions{})
		require.NoError(t, repo.InitDB(ctx))

		require.NoError(t, repo.Save(ctx, "real-ts", SagaState{
			SagaID:    "real-ts",
			Status:    SagaRunning,
			UpdatedAt: mustParseTestTime(t, "2026-01-01T00:00:00Z"),
		}))
		require.NoError(t, repo.Save(ctx, "zero-ts", SagaState{
			SagaID: "zero-ts",
			Status: SagaRunning,
		}))

		var storedUpdatedAt int64
		require.NoError(t, db.QueryRow("SELECT updated_at FROM gocamel_sagas WHERE saga_id = 'zero-ts'").Scan(&storedUpdatedAt))
		assert.Equal(t, int64(0), storedUpdatedAt)

		ids, err := repo.ListIncomplete(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"zero-ts", "real-ts"}, ids)
	})
}

func mustParseTestTime(t *testing.T, val string) (tm time.Time) {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, val)
	require.NoError(t, err)
	return tm
}

