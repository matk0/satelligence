package blink

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
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
		BtcSatPrice struct {
			Base   int64 `json:"base"`
			Offset int64 `json:"offset"`
		} `json:"btcSatPrice"`
		UsdCentPrice struct {
			Base   int64 `json:"base"`
			Offset int64 `json:"offset"`
		} `json:"usdCentPrice"`
	} `json:"realtimePrice"`
}

func (p *PriceFeed) fetchPriceFromBlink(ctx context.Context) error {
	query := `
		query RealtimePrice {
			realtimePrice {
				btcSatPrice {
					base
					offset
				}
				usdCentPrice {
					base
					offset
				}
			}
		}
	`

	var result realtimePriceResponse
	if err := p.client.execute(ctx, query, nil, &result); err != nil {
		return err
	}

	// Calculate BTC price from the response
	// btcSatPrice represents price per sat, we need to convert to BTC price
	// The base and offset format: actual_value = base * 10^(-offset)
	satPriceBase := float64(result.RealtimePrice.BtcSatPrice.Base)
	satPriceOffset := result.RealtimePrice.BtcSatPrice.Offset

	// Price per sat in USD
	var satPriceUSD float64
	if satPriceOffset > 0 {
		divisor := float64(1)
		for i := int64(0); i < satPriceOffset; i++ {
			divisor *= 10
		}
		satPriceUSD = satPriceBase / divisor
	} else {
		satPriceUSD = satPriceBase
	}

	// BTC price = sat price * 100,000,000
	btcPriceUSD := satPriceUSD * 100_000_000

	p.mu.Lock()
	p.btcPrice = btcPriceUSD
	p.updatedAt = time.Now()
	p.mu.Unlock()

	return nil
}

// fetchPriceFromCoinGecko is a fallback for when Blink API is unavailable
func (p *PriceFeed) fetchPriceFromCoinGecko(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd", nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Bitcoin struct {
			USD float64 `json:"usd"`
		} `json:"bitcoin"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	p.mu.Lock()
	p.btcPrice = result.Bitcoin.USD
	p.updatedAt = time.Now()
	p.mu.Unlock()

	return nil
}

func (p *PriceFeed) fetchPrice(ctx context.Context) error {
	// Try Blink first
	if err := p.fetchPriceFromBlink(ctx); err != nil {
		slog.Warn("blink price fetch failed, trying coingecko", "error", err)
		// Fallback to CoinGecko
		return p.fetchPriceFromCoinGecko(ctx)
	}
	return nil
}

func (p *PriceFeed) Start(ctx context.Context) {
	// Initial fetch
	if err := p.fetchPrice(ctx); err != nil {
		slog.Error("failed to fetch initial price", "error", err)
		// Set a default price so the system can still function
		p.mu.Lock()
		p.btcPrice = 60000 // Fallback default
		p.updatedAt = time.Now()
		p.mu.Unlock()
		slog.Warn("using fallback BTC price", "price", 60000)
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
