package payment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/trandor/trandor/internal/billing"
	"github.com/trandor/trandor/internal/blink"
	"github.com/trandor/trandor/internal/nwc"
)

// ChargeStatus represents the outcome of a post-charge operation
type ChargeStatus string

const (
	ChargeSuccess       ChargeStatus = "success"
	ChargeInvoiceFailed ChargeStatus = "invoice_failed"
	ChargePaymentFailed ChargeStatus = "payment_failed"
	ChargeZeroCost      ChargeStatus = "zero_cost"
	ChargeNoUsage       ChargeStatus = "no_usage"
	ChargePending       ChargeStatus = "pending"
)

// ChargeResult contains the outcome of a post-charge operation
type ChargeResult struct {
	Status      ChargeStatus
	AmountSats  int64
	AmountUSD   float64
}

// Blacklister is an interface for blacklisting wallets
type Blacklister interface {
	Add(pubkey string)
}

// Charger handles post-charge payment operations
type Charger struct {
	blinkClient *blink.Client
	blacklist   Blacklister
}

// NewCharger creates a new Charger instance
func NewCharger(blinkClient *blink.Client, blacklist Blacklister) *Charger {
	return &Charger{
		blinkClient: blinkClient,
		blacklist:   blacklist,
	}
}

// PostCharge charges the user for actual usage after the request completes.
// It creates a Lightning invoice and requests payment via NWC.
// Returns the charge result with status and amount.
func (c *Charger) PostCharge(
	ctx context.Context,
	nwcClient *nwc.Client,
	cost *billing.Cost,
	description string,
) *ChargeResult {
	if cost.TotalSats <= 0 {
		return &ChargeResult{
			Status:     ChargeZeroCost,
			AmountSats: 0,
			AmountUSD:  0,
		}
	}

	invoice, err := c.blinkClient.CreateInvoice(ctx, cost.TotalSats, description)
	if err != nil {
		slog.Error("failed to create invoice for post-charge",
			"error", err,
			"amount", cost.TotalSats,
		)
		return &ChargeResult{
			Status:     ChargeInvoiceFailed,
			AmountSats: cost.TotalSats,
			AmountUSD:  cost.TotalUSD,
		}
	}

	_, err = nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
	if err != nil {
		walletPubkey := nwcClient.WalletPubkey()

		// Only blacklist for actual payment failures, not infrastructure issues
		if nwc.IsInfrastructureError(err) {
			slog.Warn("post-charge failed due to infrastructure issue (not blacklisting)",
				"error", err,
				"amount_sats", cost.TotalSats,
				"wallet_pubkey", walletPubkey,
			)
		} else {
			slog.Warn("post-charge failed, accepting loss and blacklisting wallet",
				"error", err,
				"amount_sats", cost.TotalSats,
				"wallet_pubkey", walletPubkey,
			)
			// Blacklist the wallet to prevent future scams
			if c.blacklist != nil {
				c.blacklist.Add(walletPubkey)
			}
		}
		return &ChargeResult{
			Status:     ChargePaymentFailed,
			AmountSats: cost.TotalSats,
			AmountUSD:  cost.TotalUSD,
		}
	}

	slog.Info("post-charge successful", "amount_sats", cost.TotalSats)
	return &ChargeResult{
		Status:     ChargeSuccess,
		AmountSats: cost.TotalSats,
		AmountUSD:  cost.TotalUSD,
	}
}

// PostChargeAsync charges the user asynchronously (for streaming responses).
// It creates a new context with timeout and runs the charge in the background.
// Returns immediately with ChargePending status.
func (c *Charger) PostChargeAsync(
	nwcClient *nwc.Client,
	cost *billing.Cost,
	description string,
) *ChargeResult {
	if cost.TotalSats <= 0 {
		return &ChargeResult{
			Status:     ChargeZeroCost,
			AmountSats: 0,
			AmountUSD:  0,
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		invoice, err := c.blinkClient.CreateInvoice(ctx, cost.TotalSats, description)
		if err != nil {
			slog.Error("failed to create invoice for streaming post-charge",
				"error", err,
				"amount", cost.TotalSats,
			)
			return
		}

		_, err = nwcClient.PayInvoice(ctx, invoice.PaymentRequest, 30*time.Second)
		if err != nil {
			walletPubkey := nwcClient.WalletPubkey()

			// Only blacklist for actual payment failures, not infrastructure issues
			if nwc.IsInfrastructureError(err) {
				slog.Warn("streaming post-charge failed due to infrastructure issue (not blacklisting)",
					"error", err,
					"amount_sats", cost.TotalSats,
					"wallet_pubkey", walletPubkey,
				)
			} else {
				slog.Warn("streaming post-charge failed, accepting loss and blacklisting wallet",
					"error", err,
					"amount_sats", cost.TotalSats,
					"wallet_pubkey", walletPubkey,
				)
				// Blacklist the wallet to prevent future scams
				if c.blacklist != nil {
					c.blacklist.Add(walletPubkey)
				}
			}
			return
		}

		slog.Info("streaming post-charge successful", "amount_sats", cost.TotalSats)
	}()

	return &ChargeResult{
		Status:     ChargePending,
		AmountSats: cost.TotalSats,
		AmountUSD:  cost.TotalUSD,
	}
}

// FormatDescription creates a standard charge description
func FormatDescription(model string, requestType string) string {
	return fmt.Sprintf("Trandor: %s %s", model, requestType)
}
