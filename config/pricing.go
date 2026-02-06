package config

// ModelPricing represents the cost per 1M tokens for a model
type ModelPricing struct {
	InputPer1M  float64 // Cost in USD per 1M input tokens
	OutputPer1M float64 // Cost in USD per 1M output tokens
}

// ModelPricingTable maps API model names to their pricing
// Prices are in USD per 1 million tokens
var ModelPricingTable = map[string]ModelPricing{
	// Anthropic models
	"claude-sonnet-4-20250514":   {InputPer1M: 3.00, OutputPer1M: 15.00},
	"claude-opus-4-20250514":     {InputPer1M: 15.00, OutputPer1M: 75.00},
	"claude-3-5-haiku-20241022":  {InputPer1M: 0.80, OutputPer1M: 4.00},
	"claude-3-5-sonnet-20241022": {InputPer1M: 3.00, OutputPer1M: 15.00},

	// OpenAI models
	"gpt-4o":      {InputPer1M: 2.50, OutputPer1M: 10.00},
	"gpt-4o-mini": {InputPer1M: 0.15, OutputPer1M: 0.60},
	"gpt-4-turbo": {InputPer1M: 10.00, OutputPer1M: 30.00},
	"o1":          {InputPer1M: 15.00, OutputPer1M: 60.00},
	"o1-mini":     {InputPer1M: 3.00, OutputPer1M: 12.00},
	"o3-mini":     {InputPer1M: 1.10, OutputPer1M: 4.40},

	// Gemini models
	"gemini-2.0-flash":     {InputPer1M: 0.10, OutputPer1M: 0.40},
	"gemini-1.5-pro":       {InputPer1M: 1.25, OutputPer1M: 5.00},
	"gemini-1.5-flash":     {InputPer1M: 0.075, OutputPer1M: 0.30},
	"gemini-2.0-flash-exp": {InputPer1M: 0.00, OutputPer1M: 0.00}, // Free experimental
}

// CalculateCost calculates the total cost for a given model and token counts
// Returns cost in USD
func CalculateCost(modelName string, inputTokens, outputTokens int) float64 {
	pricing, ok := ModelPricingTable[modelName]
	if !ok {
		return 0 // Unknown model, no pricing available
	}

	inputCost := float64(inputTokens) / 1_000_000 * pricing.InputPer1M
	outputCost := float64(outputTokens) / 1_000_000 * pricing.OutputPer1M

	return inputCost + outputCost
}
