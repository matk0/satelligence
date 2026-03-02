package billing

import (
	"github.com/satilligence/satilligence/internal/blink"
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
