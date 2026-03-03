package treasury

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/trandor/trandor/config"
	"github.com/trandor/trandor/internal/blink"
)

// Manager handles automatic BTC-to-Stablesats conversion
type Manager struct {
	client  *blink.Client
	config  config.TreasuryConfig
	metrics *Metrics

	mu            sync.RWMutex
	lastCheck     time.Time
	lastBTCSats   int64
	lastUSDCents  int64
	lastError     error
	totalConverted int64

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewManager creates a new treasury manager
func NewManager(client *blink.Client, cfg config.TreasuryConfig) *Manager {
	return &Manager{
		client:  client,
		config:  cfg,
		metrics: NewMetrics(),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start begins the background polling loop
func (m *Manager) Start(ctx context.Context) {
	if !m.config.Enabled {
		slog.Info("treasury manager disabled")
		close(m.doneCh)
		return
	}

	slog.Info("starting treasury manager",
		"threshold_sats", m.config.ThresholdSats,
		"min_sats", m.config.MinSats,
		"interval", m.config.Interval,
		"retain_buffer", m.config.RetainBuffer,
	)

	// Run initial check
	m.checkAndConvert(ctx)

	ticker := time.NewTicker(m.config.Interval)
	defer ticker.Stop()

	go func() {
		defer close(m.doneCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.checkAndConvert(ctx)
			}
		}
	}()
}

// Stop gracefully stops the manager
func (m *Manager) Stop() {
	close(m.stopCh)
	<-m.doneCh
}

// checkAndConvert checks balances and converts if threshold exceeded
func (m *Manager) checkAndConvert(ctx context.Context) {
	balances, err := m.client.GetWalletBalances(ctx)
	if err != nil {
		slog.Error("failed to get wallet balances", "error", err)
		m.mu.Lock()
		m.lastError = err
		m.lastCheck = time.Now()
		m.mu.Unlock()
		m.metrics.RecordError()
		return
	}

	m.mu.Lock()
	m.lastCheck = time.Now()
	m.lastBTCSats = balances.BTCSats
	m.lastUSDCents = balances.USDCents
	m.lastError = nil
	m.mu.Unlock()

	m.metrics.RecordBalances(balances.BTCSats, balances.USDCents)

	slog.Debug("treasury balance check",
		"btc_sats", balances.BTCSats,
		"usd_cents", balances.USDCents,
	)

	// Calculate how much we can convert
	// We keep RetainBuffer sats for refunds, convert the rest above threshold
	available := balances.BTCSats - m.config.RetainBuffer
	if available <= 0 {
		return
	}

	excess := balances.BTCSats - m.config.ThresholdSats
	if excess <= 0 {
		return
	}

	// Convert the lesser of available (after buffer) and excess
	toConvert := min(available, excess)

	// Don't convert less than minimum
	if toConvert < m.config.MinSats {
		slog.Debug("conversion amount below minimum",
			"to_convert", toConvert,
			"min_sats", m.config.MinSats,
		)
		return
	}

	slog.Info("converting BTC to Stablesats",
		"amount_sats", toConvert,
		"btc_balance", balances.BTCSats,
		"threshold", m.config.ThresholdSats,
	)

	result, err := m.client.TransferBTCToUSD(ctx, toConvert)
	if err != nil {
		slog.Error("failed to convert BTC to USD", "error", err, "amount_sats", toConvert)
		m.mu.Lock()
		m.lastError = err
		m.mu.Unlock()
		m.metrics.RecordError()
		return
	}

	slog.Info("successfully converted BTC to Stablesats",
		"amount_sats", toConvert,
		"status", result.Status,
		"transaction_id", result.TransactionID,
	)

	m.mu.Lock()
	m.totalConverted += toConvert
	m.mu.Unlock()

	m.metrics.RecordConversion(toConvert)
}

// Status returns the current treasury status
type Status struct {
	Enabled        bool      `json:"enabled"`
	LastCheck      time.Time `json:"last_check,omitempty"`
	BTCSats        int64     `json:"btc_sats"`
	USDCents       int64     `json:"usd_cents"`
	TotalConverted int64     `json:"total_converted_sats"`
	LastError      string    `json:"last_error,omitempty"`
	ThresholdSats  int64     `json:"threshold_sats"`
	RetainBuffer   int64     `json:"retain_buffer_sats"`
}

// GetStatus returns the current treasury status
func (m *Manager) GetStatus() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := Status{
		Enabled:        m.config.Enabled,
		LastCheck:      m.lastCheck,
		BTCSats:        m.lastBTCSats,
		USDCents:       m.lastUSDCents,
		TotalConverted: m.totalConverted,
		ThresholdSats:  m.config.ThresholdSats,
		RetainBuffer:   m.config.RetainBuffer,
	}

	if m.lastError != nil {
		status.LastError = m.lastError.Error()
	}

	return status
}
