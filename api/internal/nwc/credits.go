package nwc

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CreditStore manages NWC wallet credits
type CreditStore struct {
	pool *pgxpool.Pool
}

// NewCreditStore creates a new credit store
func NewCreditStore(pool *pgxpool.Pool) *CreditStore {
	return &CreditStore{pool: pool}
}

// Credit represents a wallet's credit balance
type Credit struct {
	WalletPubkey string
	BalanceSats  int64
	TotalPaid    int64
	TotalCost    int64
	RequestCount int
}

// GetCredit retrieves the current credit balance for a wallet
func (s *CreditStore) GetCredit(ctx context.Context, walletPubkey string) (int64, error) {
	var balance int64
	err := s.pool.QueryRow(ctx,
		"SELECT balance_sats FROM nwc_credits WHERE wallet_pubkey = $1",
		walletPubkey,
	).Scan(&balance)

	if err != nil {
		// No credits yet, return 0
		return 0, nil
	}

	return balance, nil
}

// ApplyCredit reduces the credit balance and returns the amount applied
// Returns the amount of credit used (up to the requested amount)
func (s *CreditStore) ApplyCredit(ctx context.Context, walletPubkey string, requestedSats int64) (int64, error) {
	var applied int64

	// Use a transaction to atomically check and update
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Get current balance
	var currentBalance int64
	err = tx.QueryRow(ctx,
		"SELECT balance_sats FROM nwc_credits WHERE wallet_pubkey = $1 FOR UPDATE",
		walletPubkey,
	).Scan(&currentBalance)

	if err != nil {
		// No credits, nothing to apply
		return 0, nil
	}

	if currentBalance <= 0 {
		return 0, nil
	}

	// Calculate how much credit to apply
	if currentBalance >= requestedSats {
		applied = requestedSats
	} else {
		applied = currentBalance
	}

	// Update the balance
	_, err = tx.Exec(ctx,
		"UPDATE nwc_credits SET balance_sats = balance_sats - $1, updated_at = NOW() WHERE wallet_pubkey = $2",
		applied, walletPubkey,
	)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	return applied, nil
}

// AddCredit adds credit to a wallet's balance after a request completes
// paidSats is what the user paid, actualCostSats is the real cost
func (s *CreditStore) AddCredit(ctx context.Context, walletPubkey string, paidSats, actualCostSats int64) error {
	overpayment := paidSats - actualCostSats
	if overpayment < 0 {
		overpayment = 0
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO nwc_credits (wallet_pubkey, balance_sats, total_paid, total_cost, request_count)
		VALUES ($1, $2, $3, $4, 1)
		ON CONFLICT (wallet_pubkey) DO UPDATE SET
			balance_sats = nwc_credits.balance_sats + $2,
			total_paid = nwc_credits.total_paid + $3,
			total_cost = nwc_credits.total_cost + $4,
			request_count = nwc_credits.request_count + 1,
			updated_at = NOW()
	`, walletPubkey, overpayment, paidSats, actualCostSats)

	return err
}
