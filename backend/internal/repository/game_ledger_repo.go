package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// gameLedgerRepository persists idempotency bookkeeping for game transactions.
// Unlike the payment idempotency repository, every statement goes through
// clientFromContext so it participates in the caller's transaction: the
// idempotency row, the balance change and the audit row commit atomically.
type gameLedgerRepository struct {
	client *dbent.Client
}

func NewGameLedgerRepository(client *dbent.Client) service.GameLedgerRepository {
	return &gameLedgerRepository{client: client}
}

func (r *gameLedgerRepository) InsertIdempotency(ctx context.Context, scope, keyHash, fingerprint string, expiresAt time.Time) (int64, bool, error) {
	const query = `
		INSERT INTO idempotency_records (
			scope, idempotency_key_hash, request_fingerprint, status, expires_at
		) VALUES ($1, $2, $3, 'processing', $4)
		ON CONFLICT (scope, idempotency_key_hash) DO NOTHING
		RETURNING id
	`
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, scope, keyHash, fingerprint, expiresAt)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return 0, false, nil
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func (r *gameLedgerRepository) GetByIdempotencyKey(ctx context.Context, scope, keyHash string) (string, string, string, error) {
	const query = `
		SELECT status, COALESCE(response_body, ''), COALESCE(error_reason, '')
		FROM idempotency_records
		WHERE scope = $1 AND idempotency_key_hash = $2
	`
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, scope, keyHash)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", "", "", sql.ErrNoRows
	}
	var status, responseBody, errorReason string
	if err := rows.Scan(&status, &responseBody, &errorReason); err != nil {
		return "", "", "", err
	}
	return status, responseBody, errorReason, nil
}

func (r *gameLedgerRepository) MarkSucceeded(ctx context.Context, id int64, responseBody string) error {
	const query = `
		UPDATE idempotency_records
		SET status = 'succeeded', response_body = $2, error_reason = NULL, locked_until = NULL, updated_at = NOW()
		WHERE id = $1
		RETURNING id
	`
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, id, responseBody)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return errors.New("idempotency record not found")
	}
	return nil
}

func (r *gameLedgerRepository) MarkFailed(ctx context.Context, id int64, errorReason string) error {
	const query = `
		UPDATE idempotency_records
		SET status = 'failed', error_reason = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING id
	`
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, id, errorReason)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return errors.New("idempotency record not found")
	}
	return nil
}
