package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/satilligence/satilligence/internal/db"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionBanned   = errors.New("session is banned")
)

type Store struct {
	db *db.DB
}

func NewStore(database *db.DB) *Store {
	return &Store{db: database}
}

func (s *Store) Create(ctx context.Context, macaroonID string) (*Session, error) {
	var session Session
	err := s.db.Pool().QueryRow(ctx, `
		INSERT INTO sessions (macaroon_id)
		VALUES ($1)
		RETURNING id, macaroon_id, balance_sats, nwc_connection, strikes, banned, created_at, last_used_at
	`, macaroonID).Scan(
		&session.ID,
		&session.MacaroonID,
		&session.BalanceSats,
		&session.NWCConnection,
		&session.Strikes,
		&session.Banned,
		&session.CreatedAt,
		&session.LastUsedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	return &session, nil
}

func (s *Store) GetByMacaroonID(ctx context.Context, macaroonID string) (*Session, error) {
	var session Session
	err := s.db.Pool().QueryRow(ctx, `
		SELECT id, macaroon_id, balance_sats, nwc_connection, strikes, banned, created_at, last_used_at
		FROM sessions
		WHERE macaroon_id = $1
	`, macaroonID).Scan(
		&session.ID,
		&session.MacaroonID,
		&session.BalanceSats,
		&session.NWCConnection,
		&session.Strikes,
		&session.Banned,
		&session.CreatedAt,
		&session.LastUsedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}

func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*Session, error) {
	var session Session
	err := s.db.Pool().QueryRow(ctx, `
		SELECT id, macaroon_id, balance_sats, nwc_connection, strikes, banned, created_at, last_used_at
		FROM sessions
		WHERE id = $1
	`, id).Scan(
		&session.ID,
		&session.MacaroonID,
		&session.BalanceSats,
		&session.NWCConnection,
		&session.Strikes,
		&session.Banned,
		&session.CreatedAt,
		&session.LastUsedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &session, nil
}

func (s *Store) UpdateLastUsed(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.db.Pool().Exec(ctx, `
		UPDATE sessions SET last_used_at = NOW() WHERE id = $1
	`, sessionID)
	return err
}

func (s *Store) CreditBalance(ctx context.Context, sessionID uuid.UUID, amountSats int64, invoiceID string) error {
	tx, err := s.db.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Update balance
	_, err = tx.Exec(ctx, `
		UPDATE sessions SET balance_sats = balance_sats + $1 WHERE id = $2
	`, amountSats, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record ledger entry
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger (session_id, type, amount_sats, invoice_id)
		VALUES ($1, 'deposit', $2, $3)
	`, sessionID, amountSats, invoiceID)
	if err != nil {
		return fmt.Errorf("failed to record ledger entry: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *Store) DebitBalance(ctx context.Context, sessionID uuid.UUID, amountSats int64, reference string) error {
	tx, err := s.db.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Update balance
	_, err = tx.Exec(ctx, `
		UPDATE sessions SET balance_sats = balance_sats - $1 WHERE id = $2
	`, amountSats, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record ledger entry
	_, err = tx.Exec(ctx, `
		INSERT INTO ledger (session_id, type, amount_sats, reference)
		VALUES ($1, 'usage', $2, $3)
	`, sessionID, amountSats, reference)
	if err != nil {
		return fmt.Errorf("failed to record ledger entry: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *Store) AddStrike(ctx context.Context, sessionID uuid.UUID, maxStrikes int) (banned bool, err error) {
	var strikes int
	err = s.db.Pool().QueryRow(ctx, `
		UPDATE sessions
		SET strikes = strikes + 1,
		    banned = CASE WHEN strikes + 1 >= $2 THEN TRUE ELSE banned END
		WHERE id = $1
		RETURNING strikes, banned
	`, sessionID, maxStrikes).Scan(&strikes, &banned)
	if err != nil {
		return false, fmt.Errorf("failed to add strike: %w", err)
	}
	return banned, nil
}

func (s *Store) LogUsage(ctx context.Context, log *UsageLog) error {
	_, err := s.db.Pool().Exec(ctx, `
		INSERT INTO usage_logs (session_id, model, prompt_tokens, completion_tokens, cost_usd, cost_sats)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, log.SessionID, log.Model, log.PromptTokens, log.CompletionTokens, log.CostUSD, log.CostSats)
	return err
}

func (s *Store) SetNWCConnection(ctx context.Context, sessionID uuid.UUID, nwcConnection string) error {
	_, err := s.db.Pool().Exec(ctx, `
		UPDATE sessions SET nwc_connection = $1 WHERE id = $2
	`, nwcConnection, sessionID)
	return err
}
