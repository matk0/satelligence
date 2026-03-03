package billing

// ModelPrice represents the pricing for a model in USD per 1M tokens
type ModelPrice struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// ModelPricing contains pricing for supported models (USD per 1M tokens)
// https://developers.openai.com/api/docs/models/gpt-5.2
var ModelPricing = map[string]ModelPrice{
	"gpt-5.2": {InputPerMillion: 1.75, OutputPerMillion: 14.00},
}

// GetModelPrice returns the price for a model
func GetModelPrice(model string) (ModelPrice, bool) {
	price, ok := ModelPricing[model]
	return price, ok
}
