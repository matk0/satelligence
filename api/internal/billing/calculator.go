package billing

import (
	"log/slog"

	"github.com/trandor/trandor/internal/blink"
)

type Calculator struct {
	priceFeed     *blink.PriceFeed
	markupPercent float64
}

func NewCalculator(priceFeed *blink.PriceFeed, markupPercent float64) *Calculator {
	return &Calculator{
		priceFeed:     priceFeed,
		markupPercent: markupPercent,
	}
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	Model            string
}

type Cost struct {
	BaseUSD   float64
	MarkupUSD float64
	TotalUSD  float64
	TotalSats int64
}

func (c *Calculator) Calculate(usage Usage) (*Cost, error) {
	price, ok := GetModelPrice(usage.Model)
	if !ok {
		slog.Warn("model not found in pricing, using gpt-5.2", "model", usage.Model)
		price = ModelPricing["gpt-5.2"]
	}

	// Calculate base cost in USD
	inputCost := float64(usage.PromptTokens) * (price.InputPerMillion / 1_000_000)
	outputCost := float64(usage.CompletionTokens) * (price.OutputPerMillion / 1_000_000)
	baseUSD := inputCost + outputCost

	// Apply markup
	markupUSD := baseUSD * (c.markupPercent / 100)
	totalUSD := baseUSD + markupUSD

	// Convert to sats
	totalSats := c.priceFeed.USDToSats(totalUSD)

	// Minimum charge of 1 sat
	if totalSats < 1 {
		totalSats = 1
	}

	slog.Debug("calculated cost",
		"model", usage.Model,
		"prompt_tokens", usage.PromptTokens,
		"completion_tokens", usage.CompletionTokens,
		"input_cost_usd", inputCost,
		"output_cost_usd", outputCost,
		"total_usd", totalUSD,
		"total_sats", totalSats,
	)

	return &Cost{
		BaseUSD:   baseUSD,
		MarkupUSD: markupUSD,
		TotalUSD:  totalUSD,
		TotalSats: totalSats,
	}, nil
}

// EstimateCost estimates the cost based on input text and max output tokens
// Returns estimated cost in sats (without safety multiplier - caller should apply)
func (c *Calculator) EstimateCost(model string, inputText string, maxOutputTokens int) int64 {
	price, ok := GetModelPrice(model)
	if !ok {
		slog.Warn("model not found in pricing for estimate, using gpt-5.2", "model", model)
		price = ModelPricing["gpt-5.2"]
	}

	// Estimate input tokens: ~4 characters per token (conservative for English)
	estimatedInputTokens := len(inputText) / 4
	if estimatedInputTokens < 10 {
		estimatedInputTokens = 10 // minimum
	}

	// Calculate estimated cost
	inputCostUSD := float64(estimatedInputTokens) * (price.InputPerMillion / 1_000_000)
	outputCostUSD := float64(maxOutputTokens) * (price.OutputPerMillion / 1_000_000)
	totalUSD := (inputCostUSD + outputCostUSD) * (1 + c.markupPercent/100)

	sats := c.priceFeed.USDToSats(totalUSD)
	if sats < 1 {
		sats = 1
	}

	slog.Info("estimated cost",
		"model", model,
		"input_chars", len(inputText),
		"estimated_input_tokens", estimatedInputTokens,
		"max_output_tokens", maxOutputTokens,
		"input_cost_usd", inputCostUSD,
		"output_cost_usd", outputCostUSD,
		"total_usd", totalUSD,
		"estimated_sats", sats,
		"price_input_per_m", price.InputPerMillion,
		"price_output_per_m", price.OutputPerMillion,
	)

	return sats
}

// EstimateMaxCost estimates maximum cost assuming max_tokens for both input and output
// Used by L402 prepaid balance flow
func (c *Calculator) EstimateMaxCost(model string, maxTokens int) int64 {
	price, ok := GetModelPrice(model)
	if !ok {
		price = ModelPricing["gpt-5.2"]
	}

	// Estimate: assume max tokens for both input and output (conservative)
	maxCostUSD := float64(maxTokens) * (price.InputPerMillion / 1_000_000)
	maxCostUSD += float64(maxTokens) * (price.OutputPerMillion / 1_000_000)
	maxCostUSD *= (1 + c.markupPercent/100)

	return c.priceFeed.USDToSats(maxCostUSD)
}

// TestUSDToSats exposes the price feed conversion for debugging
func (c *Calculator) TestUSDToSats(usd float64) int64 {
	return c.priceFeed.USDToSats(usd)
}

// GetBTCPrice returns the current BTC price for debugging
func (c *Calculator) GetBTCPrice() float64 {
	price, _ := c.priceFeed.GetBTCPrice()
	return price
}
