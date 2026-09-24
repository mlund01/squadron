package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

func prepareRouteInputs(routes []RouteDefinition, target string, raw map[string]json.RawMessage) (map[string]string, error) {
	if target == "none" {
		if len(raw) > 0 {
			return nil, fmt.Errorf("route inputs require a selected mission target")
		}
		return nil, nil
	}
	var selected *RouteDefinition
	for index := range routes {
		if routes[index].Target == target {
			selected = &routes[index]
			break
		}
	}
	if selected == nil {
		// The harness owns target validation and returns the authoritative error.
		values, err := normalizeRouteInputs(raw)
		return values, err
	}
	if !selected.IsMission {
		values, err := normalizeRouteInputs(raw)
		return values, err
	}

	known := make(map[string]RouteInputDefinition, len(selected.Inputs))
	for _, input := range selected.Inputs {
		known[input.Name] = input
	}
	values := make(map[string]string, len(raw))
	var problems []string
	for _, input := range selected.Inputs {
		value, present := raw[input.Name]
		if !present {
			if input.Required {
				problems = append(problems, fmt.Sprintf("inputs.%s is required (%s)", input.Name, input.Description))
			}
			continue
		}
		text, err := normalizeRouteInput(value)
		if err == nil {
			err = validateNativeRouteInput(input, value)
		}
		if err == nil {
			err = validateCanonicalRouteInput(input, text)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("inputs.%s: %s", input.Name, err))
			continue
		}
		values[input.Name] = text
	}
	for name := range raw {
		if _, ok := known[name]; !ok {
			problems = append(problems, fmt.Sprintf("inputs.%s is not declared by this mission", name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("mission %q inputs: %s", target, strings.Join(problems, "; "))
	}
	return values, nil
}

func normalizeRouteInputs(raw map[string]json.RawMessage) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	values := make(map[string]string, len(raw))
	for name, value := range raw {
		text, err := normalizeRouteInput(value)
		if err != nil {
			return nil, fmt.Errorf("inputs.%s: %w", name, err)
		}
		values[name] = text
	}
	return values, nil
}

func normalizeRouteInput(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("input value is empty")
	}
	if bytes.Equal(raw, []byte("null")) {
		return "", fmt.Errorf("null is not an input value; omit optional inputs to use their defaults")
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func validateNativeRouteInput(input RouteInputDefinition, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' {
		return nil
	}
	kind := trimmed[0]
	valid := false
	switch input.Type {
	case "bool":
		valid = kind == 't' || kind == 'f'
	case "number", "integer":
		valid = kind == '-' || kind >= '0' && kind <= '9'
	case "list":
		valid = kind == '['
	case "object", "map", "file":
		valid = kind == '{'
	case "string":
		valid = false
	default:
		valid = true
	}
	if !valid {
		return fmt.Errorf("expected %s", input.Type)
	}
	return nil
}

func validateCanonicalRouteInputs(route RouteDefinition, values map[string]string) error {
	known := make(map[string]RouteInputDefinition, len(route.Inputs))
	var problems []string
	for _, input := range route.Inputs {
		known[input.Name] = input
		value, present := values[input.Name]
		if !present {
			if input.Required {
				problems = append(problems, fmt.Sprintf("inputs.%s is required (%s)", input.Name, input.Description))
			}
			continue
		}
		if err := validateCanonicalRouteInput(input, value); err != nil {
			problems = append(problems, fmt.Sprintf("inputs.%s: %s", input.Name, err))
		}
	}
	for name := range values {
		if _, ok := known[name]; !ok {
			problems = append(problems, fmt.Sprintf("inputs.%s is not declared by this mission", name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func validateCanonicalRouteInput(input RouteInputDefinition, value string) error {
	if input.Required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("required value must not be empty")
	}
	switch input.Type {
	case "string":
		return nil
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("expected bool")
		}
	case "number", "integer":
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("expected %s", input.Type)
		}
		if input.Type == "integer" && math.Trunc(number) != number {
			return fmt.Errorf("expected integer")
		}
	case "list":
		var decoded []any
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			return fmt.Errorf("expected list")
		}
	case "object", "map":
		var decoded map[string]any
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			return fmt.Errorf("expected %s", input.Type)
		}
	case "file":
		if strings.HasPrefix(strings.TrimSpace(value), "{") {
			var decoded map[string]any
			if err := json.Unmarshal([]byte(value), &decoded); err != nil {
				return fmt.Errorf("expected file path or upload envelope")
			}
		}
	}
	return nil
}
