package store

import (
	"encoding/json"
	"fmt"
)

// unmarshalChoices parses the choices_json column back into a string
// slice. Shared between sqlite and postgres scanners.
func unmarshalChoices(raw string, out *[]string) error {
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode choices_json: %w", err)
	}
	return nil
}
