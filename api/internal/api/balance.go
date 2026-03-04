package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/trandor/trandor/internal/nwc"
)

// BalanceCheckResult contains the outcome of a balance check
type BalanceCheckResult struct {
	OK            bool  // true if balance is sufficient or check was skipped
	BalanceSats   int64 // wallet balance (0 if check failed)
	EstimatedCost int64 // the estimated cost that was checked against
	SkippedCheck  bool  // true if wallet doesn't support get_balance
}

// CheckMinBalance checks if wallet has at least minBalanceSats.
// Used to verify minimum balance requirement ($0.50) before proceeding with post-charge flow.
// If the wallet doesn't support get_balance, returns SkippedCheck=true to signal
// that we should fall back to pre-charge flow.
func CheckMinBalance(
	ctx context.Context,
	nwcClient *nwc.Client,
	minBalanceSats int64,
	timeout time.Duration,
) *BalanceCheckResult {
	balanceSats, err := nwcClient.GetBalance(ctx, timeout)
	if err != nil {
		slog.Warn("failed to check wallet balance, will use pre-charge flow", "error", err)
		// Wallet doesn't support get_balance - signal to use pre-charge flow
		return &BalanceCheckResult{
			OK:            false,
			BalanceSats:   0,
			EstimatedCost: minBalanceSats,
			SkippedCheck:  true,
		}
	}

	if balanceSats < minBalanceSats {
		slog.Info("wallet balance below minimum requirement",
			"balance_sats", balanceSats,
			"min_balance_sats", minBalanceSats,
		)
		return &BalanceCheckResult{
			OK:            false,
			BalanceSats:   balanceSats,
			EstimatedCost: minBalanceSats,
			SkippedCheck:  false,
		}
	}

	slog.Debug("wallet balance meets minimum requirement",
		"balance_sats", balanceSats,
		"min_balance_sats", minBalanceSats,
	)
	return &BalanceCheckResult{
		OK:            true,
		BalanceSats:   balanceSats,
		EstimatedCost: minBalanceSats,
		SkippedCheck:  false,
	}
}

// CheckBalance verifies the wallet has sufficient balance for the estimated cost.
// If the wallet doesn't support get_balance, it returns OK with SkippedCheck=true.
// Returns OK=false only when balance is confirmed insufficient.
// Deprecated: Use CheckMinBalance for the new billing flow.
func CheckBalance(
	ctx context.Context,
	nwcClient *nwc.Client,
	estimatedCost int64,
	timeout time.Duration,
) *BalanceCheckResult {
	balanceSats, err := nwcClient.GetBalance(ctx, timeout)
	if err != nil {
		slog.Warn("failed to check wallet balance", "error", err)
		// Continue anyway - some wallets may not support get_balance
		return &BalanceCheckResult{
			OK:            true,
			BalanceSats:   0,
			EstimatedCost: estimatedCost,
			SkippedCheck:  true,
		}
	}

	if balanceSats < estimatedCost {
		slog.Info("wallet balance too low for estimated cost",
			"balance_sats", balanceSats,
			"estimated_max_cost_sats", estimatedCost,
		)
		return &BalanceCheckResult{
			OK:            false,
			BalanceSats:   balanceSats,
			EstimatedCost: estimatedCost,
			SkippedCheck:  false,
		}
	}

	slog.Debug("wallet balance OK",
		"balance_sats", balanceSats,
		"estimated_max_cost_sats", estimatedCost,
	)
	return &BalanceCheckResult{
		OK:            true,
		BalanceSats:   balanceSats,
		EstimatedCost: estimatedCost,
		SkippedCheck:  false,
	}
}
