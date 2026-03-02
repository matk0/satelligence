package blink

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type PriceFeed struct {
	client    *Client
	mu        sync.RWMutex
	btcPrice  float64 // USD per BTC
	updatedAt time.Time
}

func NewPriceFeed(client *Client) *PriceFeed {
	return &PriceFeed{
		client: client,
	}
}

type realtimePriceResponse struct {
	RealtimePrice struct {
		BtcSatPrice         float64 `json:"btcSatPrice"`
		UsdCentPrice        float64 `json:"usdCentPrice"`
		DenominatorCurrency string  `json:"denominatorCurrency"`
	} `json:"realtimePrice"`
}

func (p *PriceFeed) fetchPrice(ctx context.Context) error {
	query := `
		query RealtimePrice {
			realtimePrice {
				btcSatPrice
				usdCentPrice
				denominatorCurrency
			}
		}
	`

	var result realtimePriceResponse
	if err := p.client.execute(ctx, query, nil, &result); err != nil {
		return err
	}

	// btcSatPrice is the price per sat in USD cents
	// To get BTC price in USD: (1 sat = btcSatPrice cents) => (100M sats = btcSatPrice * 100M cents)
	// BTC price = (btcSatPrice * 100,000,000) / 100 USD
	btcPriceUSD := (result.RealtimePrice.BtcSatPrice * 100_000_000) / 100

	p.mu.Lock()
	p.btcPrice = btcPriceUSD
	p.updatedAt = time.Now()
	p.mu.Unlock()

	return nil
}

func (p *PriceFeed) Start(ctx context.Context) {
	// Initial fetch
	if err := p.fetchPrice(ctx); err != nil {
		slog.Error("failed to fetch initial price", "error", err)
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.fetchPrice(ctx); err != nil {
				slog.Error("failed to fetch price", "error", err)
			}
		}
	}
}

func (p *PriceFeed) GetBTCPrice() (priceUSD float64, updatedAt time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.btcPrice, p.updatedAt
}

// USDToSats converts USD amount to satoshis
func (p *PriceFeed) USDToSats(usd float64) int64 {
	p.mu.RLock()
	btcPrice := p.btcPrice
	p.mu.RUnlock()

	if btcPrice == 0 {
		return 0
	}

	// Add 2% safety margin for volatility
	btcPriceWithMargin := btcPrice * 0.98

	btc := usd / btcPriceWithMargin
	sats := btc * 100_000_000

	// Round up to nearest sat
	return int64(sats) + 1
}
