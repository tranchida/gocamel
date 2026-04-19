package gocamel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SQLAggregationRepository is a SQL-based implementation of AggregationRepository.
type SQLAggregationRepository struct {
	db             *sql.DB
	tableName      string
	// UseDollarParam uses dollar parameters (true for PostgreSQL $1, $2; false for MySQL/SQLite ?, ?)
	UseDollarParam bool
}

// SQLAggregationOptions contains the options for configuring SQLAggregationRepository.
type SQLAggregationOptions struct {
	TableName      string
	UseDollarParam bool
}

// ExchangeData is used to serialize the Exchange content to JSON.
type ExchangeData struct {
	Body       any            `json:"body"`
	Headers    map[string]any `json:"headers"`
	Properties map[string]any `json:"properties"`
	Created    time.Time      `json:"created"`
	Modified   time.Time      `json:"modified"`
}

// NewSQLAggregationRepository creates a new SQLAggregationRepository instance.
func NewSQLAggregationRepository(db *sql.DB, opts SQLAggregationOptions) *SQLAggregationRepository {
	tableName := opts.TableName
	if tableName == "" {
		tableName = "camel_aggregations"
	}
	return &SQLAggregationRepository{
		db:             db,
		tableName:      tableName,
		UseDollarParam: opts.UseDollarParam,
	}
}

// InitDB creates the table if it doesn't exist.
func (r *SQLAggregationRepository) InitDB(ctx context.Context) error {
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			correlation_key VARCHAR(255) PRIMARY KEY,
			exchange_data TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`, r.tableName)
	_, err := r.db.ExecContext(ctx, query)
	return err
}

// Add adds or updates an exchange in the repository.
func (r *SQLAggregationRepository) Add(ctx context.Context, key string, exchange *Exchange) error {
	data := ExchangeData{
		Body:       exchange.GetIn().GetBody(),
		Headers:    exchange.GetIn().GetHeaders(),
		Properties: exchange.GetProperties(),
		Created:    exchange.Created,
		Modified:   exchange.Modified,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal exchange data: %w", err)
	}

	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf(`
			INSERT INTO %s (correlation_key, exchange_data)
			VALUES ($1, $2)
			ON CONFLICT(correlation_key) DO UPDATE SET exchange_data = $2
		`, r.tableName)
	} else {
		// Compatible with SQLite and MySQL (ON DUPLICATE KEY UPDATE or REPLACE INTO)
		// We use REPLACE INTO for SQLite for simplicity and broad compatibility within the scope of this clone.
		// For true multi-DB production support, a finer abstraction would be needed.
		query = fmt.Sprintf(`
			REPLACE INTO %s (correlation_key, exchange_data)
			VALUES (?, ?)
		`, r.tableName)
	}

	_, err = r.db.ExecContext(ctx, query, key, string(jsonData))
	return err
}

// Get retrieves an exchange by its correlation key.
func (r *SQLAggregationRepository) Get(ctx context.Context, key string) (*Exchange, error) {
	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf("SELECT exchange_data FROM %s WHERE correlation_key = $1", r.tableName)
	} else {
		query = fmt.Sprintf("SELECT exchange_data FROM %s WHERE correlation_key = ?", r.tableName)
	}

	row := r.db.QueryRowContext(ctx, query, key)
	var jsonData string
	err := row.Scan(&jsonData)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to fetch exchange data: %w", err)
	}

	var data ExchangeData
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal exchange data: %w", err)
	}

	// Reconstruct the exchange
	exchange := NewExchange(ctx)
	exchange.GetIn().SetBody(data.Body)
	exchange.GetIn().SetHeaders(data.Headers)
	exchange.SetProperties(data.Properties)
	exchange.Created = data.Created
	exchange.Modified = data.Modified

	return exchange, nil
}

// Remove removes an exchange from the repository.
func (r *SQLAggregationRepository) Remove(ctx context.Context, key string) error {
	var query string
	if r.UseDollarParam {
		query = fmt.Sprintf("DELETE FROM %s WHERE correlation_key = $1", r.tableName)
	} else {
		query = fmt.Sprintf("DELETE FROM %s WHERE correlation_key = ?", r.tableName)
	}

	_, err := r.db.ExecContext(ctx, query, key)
	return err
}
