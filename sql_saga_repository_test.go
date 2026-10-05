package gocamel

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

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
