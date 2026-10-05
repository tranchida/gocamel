package gocamel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// SQLSagaRepository is a SQL-based implementation of SagaRepository, so the
// pending compensations of a saga survive a process crash. Like
// SQLAggregationRepository it only depends on database/sql: the caller opens
// the *sql.DB with the driver of its choice and calls InitDB once.
//
// Table layout (default name gocamel_sagas):
//
//	saga_id    VARCHAR(255) PRIMARY KEY
//	status     VARCHAR(32)  -- duplicated from the JSON for ListIncomplete
//	state      TEXT         -- SagaState as JSON
//	updated_at BIGINT       -- Unix nanoseconds, portable and sortable
type SQLSagaRepository struct {
	db        *sql.DB
	tableName string
	// initErr holds a misconfiguration error surfaced on first use (see
	// SQLAggregationRepository).
	initErr error
	// UseDollarParam uses dollar parameters (true for PostgreSQL $1, $2; false for MySQL/SQLite ?, ?)
	UseDollarParam bool
}

// SQLSagaOptions contains the options for configuring SQLSagaRepository.
type SQLSagaOptions struct {
	TableName      string
	UseDollarParam bool
}

// NewSQLSagaRepository creates a new SQLSagaRepository instance.
func NewSQLSagaRepository(db *sql.DB, opts SQLSagaOptions) *SQLSagaRepository {
	tableName := opts.TableName
	if tableName == "" {
		tableName = "gocamel_sagas"
	}
	repo := &SQLSagaRepository{
		db:             db,
		tableName:      tableName,
		UseDollarParam: opts.UseDollarParam,
	}
	// SECURITY: the table name is spliced into every statement; see
	// NewSQLAggregationRepository.
	if !tableNamePattern.MatchString(tableName) {
		repo.initErr = fmt.Errorf("invalid saga table name %q: must match %s", tableName, tableNamePattern)
	}
	return repo
}

// InitDB creates the table if it doesn't exist.
func (r *SQLSagaRepository) InitDB(ctx context.Context) error {
	if r.initErr != nil {
		return r.initErr
	}
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			saga_id VARCHAR(255) PRIMARY KEY,
			status VARCHAR(32) NOT NULL,
			state TEXT NOT NULL,
			updated_at BIGINT NOT NULL
		)
	`, r.tableName)
	_, err := r.db.ExecContext(ctx, query)
	return err
}

// Save inserts or replaces the saga state.
func (r *SQLSagaRepository) Save(ctx context.Context, sagaID string, state SagaState) error {
	if r.initErr != nil {
		return r.initErr
	}
	jsonData, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal saga %s state: %w", sagaID, err)
	}

	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf(`
			INSERT INTO %s (saga_id, status, state, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT(saga_id) DO UPDATE SET status = $2, state = $3, updated_at = $4
		`, r.tableName)
	} else {
		// REPLACE INTO for SQLite and MySQL, as in SQLAggregationRepository.
		query = fmt.Sprintf(`
			REPLACE INTO %s (saga_id, status, state, updated_at)
			VALUES (?, ?, ?, ?)
		`, r.tableName)
	}

	if _, err := r.db.ExecContext(ctx, query, sagaID, string(state.Status), string(jsonData), state.UpdatedAt.UnixNano()); err != nil {
		return fmt.Errorf("failed to save saga %s: %w", sagaID, err)
	}
	return nil
}

// Load returns the saga state, or ErrSagaNotFound.
func (r *SQLSagaRepository) Load(ctx context.Context, sagaID string) (SagaState, error) {
	if r.initErr != nil {
		return SagaState{}, r.initErr
	}
	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf("SELECT state FROM %s WHERE saga_id = $1", r.tableName)
	} else {
		query = fmt.Sprintf("SELECT state FROM %s WHERE saga_id = ?", r.tableName)
	}

	var jsonData string
	if err := r.db.QueryRowContext(ctx, query, sagaID).Scan(&jsonData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SagaState{}, ErrSagaNotFound
		}
		return SagaState{}, fmt.Errorf("failed to fetch saga %s: %w", sagaID, err)
	}

	var state SagaState
	if err := json.Unmarshal([]byte(jsonData), &state); err != nil {
		return SagaState{}, fmt.Errorf("failed to unmarshal saga %s state: %w", sagaID, err)
	}
	return state, nil
}

// ListIncomplete returns up to DefaultSagaListLimit running or compensating
// saga IDs, oldest first. The bound is applied while reading rows rather than
// with a dialect-specific LIMIT clause.
func (r *SQLSagaRepository) ListIncomplete(ctx context.Context) ([]string, error) {
	if r.initErr != nil {
		return nil, r.initErr
	}
	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf("SELECT saga_id FROM %s WHERE status IN ($1, $2) ORDER BY updated_at, saga_id", r.tableName)
	} else {
		query = fmt.Sprintf("SELECT saga_id FROM %s WHERE status IN (?, ?) ORDER BY updated_at, saga_id", r.tableName)
	}
	rows, err := r.db.QueryContext(ctx, query, string(SagaRunning), string(SagaCompensating))
	if err != nil {
		return nil, fmt.Errorf("failed to list incomplete sagas: %w", err)
	}
	defer rows.Close()
	var ids []string
	for len(ids) < DefaultSagaListLimit && rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan saga id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Remove deletes the saga state.
func (r *SQLSagaRepository) Remove(ctx context.Context, sagaID string) error {
	if r.initErr != nil {
		return r.initErr
	}
	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf("DELETE FROM %s WHERE saga_id = $1", r.tableName)
	} else {
		query = fmt.Sprintf("DELETE FROM %s WHERE saga_id = ?", r.tableName)
	}
	if _, err := r.db.ExecContext(ctx, query, sagaID); err != nil {
		return fmt.Errorf("failed to remove saga %s: %w", sagaID, err)
	}
	return nil
}
