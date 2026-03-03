package treasury

import "sync/atomic"

// Metrics tracks treasury operations for observability
// Uses simple counters that can be exposed via /metrics endpoint
type Metrics struct {
	btcBalanceSats   atomic.Int64
	usdBalanceCents  atomic.Int64
	conversionsTotal atomic.Int64
	satsConverted    atomic.Int64
	errorsTotal      atomic.Int64
}

// NewMetrics creates a new metrics instance
func NewMetrics() *Metrics {
	return &Metrics{}
}

// RecordBalances updates the current wallet balances
func (m *Metrics) RecordBalances(btcSats, usdCents int64) {
	m.btcBalanceSats.Store(btcSats)
	m.usdBalanceCents.Store(usdCents)
}

// RecordConversion records a successful conversion
func (m *Metrics) RecordConversion(sats int64) {
	m.conversionsTotal.Add(1)
	m.satsConverted.Add(sats)
}

// RecordError records a failed operation
func (m *Metrics) RecordError() {
	m.errorsTotal.Add(1)
}

// Snapshot returns current metric values
type Snapshot struct {
	BTCBalanceSats   int64 `json:"trandor_treasury_btc_balance_sats"`
	USDBalanceCents  int64 `json:"trandor_treasury_usd_balance_cents"`
	ConversionsTotal int64 `json:"trandor_treasury_conversions_total"`
	SatsConverted    int64 `json:"trandor_treasury_sats_converted_total"`
	ErrorsTotal      int64 `json:"trandor_treasury_conversion_errors"`
}

// GetSnapshot returns the current metric values
func (m *Metrics) GetSnapshot() Snapshot {
	return Snapshot{
		BTCBalanceSats:   m.btcBalanceSats.Load(),
		USDBalanceCents:  m.usdBalanceCents.Load(),
		ConversionsTotal: m.conversionsTotal.Load(),
		SatsConverted:    m.satsConverted.Load(),
		ErrorsTotal:      m.errorsTotal.Load(),
	}
}

// GetMetrics returns the metrics instance for the manager
func (mgr *Manager) GetMetrics() *Metrics {
	return mgr.metrics
}
