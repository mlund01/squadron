package llm

// ModelPricing holds per-million-token costs for a model.
type ModelPricing struct {
	Input      float64 // Cost per 1M input tokens
	Output     float64 // Cost per 1M output tokens
	CacheRead  float64 // Cost per 1M cached input tokens (prompt cache hit)
	CacheWrite float64 // Cost per 1M cache write tokens (prompt cache creation)
}

// TurnCost holds the computed cost for a single LLM turn.
type TurnCost struct {
	InputCost      float64 `json:"inputCost"`
	OutputCost     float64 `json:"outputCost"`
	CacheReadCost  float64 `json:"cacheReadCost"`
	CacheWriteCost float64 `json:"cacheWriteCost"`
	TotalCost      float64 `json:"totalCost"`
}

// ComputeTurnCost calculates the cost for a turn given token counts and pricing.
func ComputeTurnCost(pricing *ModelPricing, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int) TurnCost {
	if pricing == nil {
		return TurnCost{}
	}
	cost := TurnCost{
		InputCost:      float64(inputTokens) / 1_000_000 * pricing.Input,
		OutputCost:     float64(outputTokens) / 1_000_000 * pricing.Output,
		CacheReadCost:  float64(cacheReadTokens) / 1_000_000 * pricing.CacheRead,
		CacheWriteCost: float64(cacheWriteTokens) / 1_000_000 * pricing.CacheWrite,
	}
	cost.TotalCost = cost.InputCost + cost.OutputCost + cost.CacheReadCost + cost.CacheWriteCost
	return cost
}

// DefaultPricing contains hardcoded pricing for known models.
// Keyed by the API model name. Prices are per 1M tokens.
// Sources: platform.claude.com/docs/en/about-claude/pricing, developers.openai.com/api/docs/pricing
// Last verified: April 2026
var DefaultPricing = map[string]*ModelPricing{
	// === Anthropic (verified April 2026) ===
	// Cache read = 0.1x input, cache write (5min) = 1.25x input
	"claude-opus-4-7": {
		Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25,
	},
	"claude-opus-4-6": {
		Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25,
	},
	"claude-sonnet-4-6": {
		Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75,
	},
	"claude-haiku-4-5-20251001": {
		Input: 1.00, Output: 5.00, CacheRead: 0.10, CacheWrite: 1.25,
	},
	// Legacy Anthropic models
	"claude-opus-4-5-20251101": {
		Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25,
	},
	"claude-opus-4-20250514": {
		Input: 15.00, Output: 75.00, CacheRead: 1.50, CacheWrite: 18.75,
	},
	"claude-sonnet-4-5-20250929": {
		Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75,
	},
	"claude-sonnet-4-20250514": {
		Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75,
	},
	"claude-3-5-haiku-20241022": {
		Input: 0.80, Output: 4.00, CacheRead: 0.08, CacheWrite: 1.00,
	},
	"claude-3-5-sonnet-20241022": {
		Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75,
	},

	// === OpenAI (verified April 2026) ===
	// Cached input = 10% of input (standard OpenAI rate); cache write = input price.
	// Current flagship: gpt-5.5 family (released 2026-04-23)
	"gpt-5.5": {
		Input: 5.00, Output: 30.00, CacheRead: 0.50, CacheWrite: 5.00,
	},
	"gpt-5.5-pro": {
		Input: 30.00, Output: 180.00, CacheRead: 3.00, CacheWrite: 30.00,
	},
	"gpt-5.4": {
		Input: 2.50, Output: 15.00, CacheRead: 0.25, CacheWrite: 2.50,
	},
	"gpt-5.4-mini": {
		Input: 0.75, Output: 4.50, CacheRead: 0.075, CacheWrite: 0.75,
	},
	"gpt-5.4-nano": {
		Input: 0.20, Output: 1.25, CacheRead: 0.02, CacheWrite: 0.20,
	},
	"gpt-5.4-pro": {
		Input: 30.00, Output: 180.00, CacheRead: 3.00, CacheWrite: 30.00,
	},
	"gpt-5.3-codex": {
		Input: 1.75, Output: 14.00, CacheRead: 0.175, CacheWrite: 1.75,
	},
	"gpt-5.2": {
		Input: 1.75, Output: 14.00, CacheRead: 0.175, CacheWrite: 1.75,
	},
	"gpt-5": {
		Input: 2.50, Output: 15.00, CacheRead: 0.25, CacheWrite: 2.50,
	},
	"gpt-5-mini": {
		Input: 0.75, Output: 4.50, CacheRead: 0.075, CacheWrite: 0.75,
	},
	"gpt-5-nano": {
		Input: 0.20, Output: 1.25, CacheRead: 0.02, CacheWrite: 0.20,
	},
	"gpt-4.1": {
		Input: 2.00, Output: 8.00, CacheRead: 0.50, CacheWrite: 2.00,
	},
	"gpt-4.1-mini": {
		Input: 0.40, Output: 1.60, CacheRead: 0.10, CacheWrite: 0.40,
	},
	"gpt-4.1-nano": {
		Input: 0.10, Output: 0.40, CacheRead: 0.025, CacheWrite: 0.10,
	},
	"gpt-4o": {
		Input: 2.50, Output: 10.00, CacheRead: 1.25, CacheWrite: 2.50,
	},
	"gpt-4o-mini": {
		Input: 0.15, Output: 0.60, CacheRead: 0.075, CacheWrite: 0.15,
	},
	"gpt-4-turbo": {
		Input: 10.00, Output: 30.00, CacheRead: 10.00, CacheWrite: 10.00,
	},
	"o1": {
		Input: 15.00, Output: 60.00, CacheRead: 7.50, CacheWrite: 15.00,
	},
	"o1-mini": {
		Input: 1.10, Output: 4.40, CacheRead: 0.55, CacheWrite: 1.10,
	},
	"o3": {
		Input: 10.00, Output: 40.00, CacheRead: 2.50, CacheWrite: 10.00,
	},
	"o3-mini": {
		Input: 1.10, Output: 4.40, CacheRead: 0.55, CacheWrite: 1.10,
	},
	"o4-mini": {
		Input: 1.10, Output: 4.40, CacheRead: 0.55, CacheWrite: 1.10,
	},

	// === Google Gemini (verified April 2026) ===
	// Cache read pricing from Google's pricing page; cache write = input price
	// Gemini 3.x uses tiered pricing (≤200k / >200k); we use the ≤200k tier.
	"gemini-3.1-pro-preview": {
		Input: 2.00, Output: 12.00, CacheRead: 0.20, CacheWrite: 2.00,
	},
	"gemini-3.1-flash-lite-preview": {
		Input: 0.25, Output: 1.50, CacheRead: 0.025, CacheWrite: 0.25,
	},
	"gemini-3-flash-preview": {
		Input: 0.50, Output: 3.00, CacheRead: 0.05, CacheWrite: 0.50,
	},
	"gemini-2.5-pro": {
		Input: 1.25, Output: 10.00, CacheRead: 0.125, CacheWrite: 1.25,
	},
	"gemini-2.5-flash": {
		Input: 0.30, Output: 2.50, CacheRead: 0.03, CacheWrite: 0.30,
	},
	"gemini-2.5-flash-lite": {
		Input: 0.10, Output: 0.40, CacheRead: 0.01, CacheWrite: 0.10,
	},
	"gemini-2.0-flash": {
		Input: 0.10, Output: 0.40, CacheRead: 0.025, CacheWrite: 0.10,
	},
	"gemini-2.0-flash-lite": {
		Input: 0.075, Output: 0.30, CacheRead: 0.01875, CacheWrite: 0.075,
	},
	"gemini-2.0-flash-exp": {
		Input: 0.10, Output: 0.40, CacheRead: 0.025, CacheWrite: 0.10,
	},
	"gemini-1.5-pro": {
		Input: 1.25, Output: 5.00, CacheRead: 0.3125, CacheWrite: 1.25,
	},
	"gemini-1.5-flash": {
		Input: 0.075, Output: 0.30, CacheRead: 0.01875, CacheWrite: 0.075,
	},
}

// GetPricing looks up pricing for a model, checking overrides first then defaults.
// Returns nil if no pricing is known for the model.
func GetPricing(apiModelName string, overrides map[string]*ModelPricing) *ModelPricing {
	if overrides != nil {
		if p, ok := overrides[apiModelName]; ok {
			return p
		}
	}
	if p, ok := DefaultPricing[apiModelName]; ok {
		return p
	}
	return nil
}

// GetDefaultPricing looks up pricing from the built-in table only.
func GetDefaultPricing(apiModelName string) *ModelPricing {
	return DefaultPricing[apiModelName]
}
