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

// fetchPriceFromCoinGecko gets BTC price from CoinGecko API
func (p *PriceFeed) fetchPriceFromCoinGecko(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd", nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
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

	slog.Info("BTC price updated", "price_usd", result.Bitcoin.USD, "source", "coingecko")

	return nil
}

func (p *PriceFeed) Start(ctx context.Context) {
	// Initial fetch
	if err := p.fetchPriceFromCoinGecko(ctx); err != nil {
		slog.Error("failed to fetch initial price", "error", err)
		// Set a default price so the system can still function
		p.mu.Lock()
		p.btcPrice = 100000 // Fallback default ~$100k
		p.updatedAt = time.Now()
		p.mu.Unlock()
		slog.Warn("using fallback BTC price", "price", 100000)
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.fetchPriceFromCoinGecko(ctx); err != nil {
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

	// Convert USD to BTC, then to sats
	btc := usd / btcPrice
	sats := btc * 100_000_000

	// Round up to nearest sat (minimum 1)
	result := int64(sats)
	if sats > float64(result) {
		result++
	}
	if result < 1 {
		result = 1
	}

	return result
}
