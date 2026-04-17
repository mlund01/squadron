package config

import "fmt"

// Budget defines spending limits for a mission or task.
// At least one of Tokens or Dollars must be set. Whichever limit is reached
// first causes the owning task (and therefore the mission) to fail.
type Budget struct {
	// Tokens is the cumulative token budget (input + output + cache).
	// Nil means no token limit. Applies to every LLM call charged to the scope.
	Tokens *int64 `json:"tokens,omitempty"`
	// Dollars is the cumulative dollar budget. Nil means no dollar limit.
	// Models without configured pricing contribute $0, so a pure dollar budget
	// cannot constrain local/unpriced models — pair it with a token budget.
	Dollars *float64 `json:"dollars,omitempty"`
}

// Validate checks that at least one field is set.
func (b *Budget) Validate() error {
	if b == nil {
		return nil
	}
	if b.Tokens == nil && b.Dollars == nil {
		return fmt.Errorf("budget: at least one of 'tokens' or 'dollars' must be set")
	}
	if b.Tokens != nil && *b.Tokens <= 0 {
		return fmt.Errorf("budget: tokens must be > 0")
	}
	if b.Dollars != nil && *b.Dollars <= 0 {
		return fmt.Errorf("budget: dollars must be > 0")
	}
	return nil
}
