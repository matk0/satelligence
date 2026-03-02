package billing

// ModelPrice represents the pricing for a model in USD per 1M tokens
type ModelPrice struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// ModelPricing contains pricing for all supported models
var ModelPricing = map[string]ModelPrice{
	// OpenAI models
	"gpt-4o":        {InputPerMillion: 5.00, OutputPerMillion: 15.00},
	"gpt-4o-mini":   {InputPerMillion: 0.15, OutputPerMillion: 0.60},
	"gpt-4-turbo":   {InputPerMillion: 10.00, OutputPerMillion: 30.00},
	"gpt-4":         {InputPerMillion: 30.00, OutputPerMillion: 60.00},
	"gpt-3.5-turbo": {InputPerMillion: 0.50, OutputPerMillion: 1.50},

	// Placeholder for future providers
	// "claude-3-opus":   {InputPerMillion: 15.00, OutputPerMillion: 75.00},
	// "claude-3-sonnet": {InputPerMillion: 3.00, OutputPerMillion: 15.00},
}

func GetModelPrice(model string) (ModelPrice, bool) {
	price, ok := ModelPricing[model]
	return price, ok
}
