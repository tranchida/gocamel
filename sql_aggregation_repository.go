package gocamel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SQLAggregationRepository est une implémentation basée sur SQL de AggregationRepository.
type SQLAggregationRepository struct {
	db             *sql.DB
	tableName      string
	useDollarParam bool // true pour PostgreSQL ($1, $2), false pour MySQL/SQLite (?, ?)
}

// SQLAggregationOptions contient les options pour configurer SQLAggregationRepository.
type SQLAggregationOptions struct {
	TableName      string
	UseDollarParam bool
}

// ExchangeData est utilisé pour sérialiser le contenu de l'Exchange en JSON.
type ExchangeData struct {
	Body       any            `json:"body"`
	Headers    map[string]any `json:"headers"`
	Properties map[string]any `json:"properties"`
	Created    time.Time      `json:"created"`
	Modified   time.Time      `json:"modified"`
}

// NewSQLAggregationRepository crée une nouvelle instance de SQLAggregationRepository.
func NewSQLAggregationRepository(db *sql.DB, opts SQLAggregationOptions) *SQLAggregationRepository {
	tableName := opts.TableName
	if tableName == "" {
		tableName = "camel_aggregations"
	}
	return &SQLAggregationRepository{
		db:             db,
		tableName:      tableName,
		useDollarParam: opts.UseDollarParam,
	}
}

// InitDB crée la table si elle n'existe pas.
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

// Add ajoute ou met à jour un échange dans le repository.
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
	if r.useDollarParam {
		query = fmt.Sprintf(`
			INSERT INTO %s (correlation_key, exchange_data)
			VALUES ($1, $2)
			ON CONFLICT(correlation_key) DO UPDATE SET exchange_data = $2
		`, r.tableName)
	} else {
		// Compatible avec SQLite et MySQL (ON DUPLICATE KEY UPDATE ou REPLACE INTO)
		// On utilise REPLACE INTO pour SQLite par simplicité et compatibilité large dans le cadre de ce clone.
		// Pour un vrai support multi-DB en production, il faudrait une abstraction plus fine.
		query = fmt.Sprintf(`
			REPLACE INTO %s (correlation_key, exchange_data)
			VALUES (?, ?)
		`, r.tableName)
	}

	_, err = r.db.ExecContext(ctx, query, key, string(jsonData))
	return err
}

// Get récupère un échange par sa clé de corrélation.
func (r *SQLAggregationRepository) Get(ctx context.Context, key string) (*Exchange, error) {
	var query string
	if r.useDollarParam {
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

// Remove supprime un échange du repository.
func (r *SQLAggregationRepository) Remove(ctx context.Context, key string) error {
	var query string
	if r.useDollarParam {
		query = fmt.Sprintf("DELETE FROM %s WHERE correlation_key = $1", r.tableName)
	} else {
		query = fmt.Sprintf("DELETE FROM %s WHERE correlation_key = ?", r.tableName)
	}

	_, err := r.db.ExecContext(ctx, query, key)
	return err
}
