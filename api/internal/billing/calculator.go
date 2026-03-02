package billing

import (
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
		// Default to gpt-4o pricing if model not found
		price = ModelPricing["gpt-4o"]
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
		price = ModelPricing["gpt-4o"]
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

	return sats
}

// EstimateMaxCost estimates maximum cost assuming max_tokens for both input and output
// Used by L402 prepaid balance flow
func (c *Calculator) EstimateMaxCost(model string, maxTokens int) int64 {
	price, ok := GetModelPrice(model)
	if !ok {
		price = ModelPricing["gpt-4o"]
	}

	// Estimate: assume max tokens for both input and output (conservative)
	maxCostUSD := float64(maxTokens) * (price.InputPerMillion / 1_000_000)
	maxCostUSD += float64(maxTokens) * (price.OutputPerMillion / 1_000_000)
	maxCostUSD *= (1 + c.markupPercent/100)

	return c.priceFeed.USDToSats(maxCostUSD)
}
