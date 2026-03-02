package billing

// ModelPrice represents the pricing for a model in USD per 1M tokens
type ModelPrice struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// ModelPricing contains pricing for all supported models (USD per 1M tokens)
var ModelPricing = map[string]ModelPrice{
	// GPT-5 family
	"gpt-5.2":     {InputPerMillion: 1.75, OutputPerMillion: 14.00},
	"gpt-5.2-pro": {InputPerMillion: 21.00, OutputPerMillion: 168.00},
	"gpt-5":       {InputPerMillion: 1.25, OutputPerMillion: 10.00},
	"gpt-5-mini":  {InputPerMillion: 0.25, OutputPerMillion: 2.00},
	"gpt-5-nano":  {InputPerMillion: 0.05, OutputPerMillion: 0.40},

	// GPT-4 family
	"gpt-4o":      {InputPerMillion: 2.50, OutputPerMillion: 10.00},
	"gpt-4o-mini": {InputPerMillion: 0.15, OutputPerMillion: 0.60},
	"gpt-4-turbo": {InputPerMillion: 10.00, OutputPerMillion: 30.00},
}

func GetModelPrice(model string) (ModelPrice, bool) {
	price, ok := ModelPricing[model]
	return price, ok
}
