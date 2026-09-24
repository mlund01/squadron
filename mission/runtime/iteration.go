package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// IterationPolicy is the immutable execution policy for one declared iterated
// task. It is copied into the durable plan before any child is dispatched.
type IterationPolicy struct {
	Dataset          string `json:"dataset"`
	Parallel         bool   `json:"parallel"`
	MaxRetries       int    `json:"maxRetries,omitempty"`
	ConcurrencyLimit int    `json:"concurrencyLimit"`
	StartDelayMS     int    `json:"startDelayMs,omitempty"`
	Smoketest        bool   `json:"smoketest,omitempty"`
}

// IterationChild is one stable task instance generated from a frozen dataset
// item. Item is canonical JSON so equality, hashing, replay, and model input do
// not depend on the producer's whitespace or object-key order.
type IterationChild struct {
	Name      string          `json:"name"`
	Index     int             `json:"index"`
	Item      json.RawMessage `json:"item"`
	DependsOn []string        `json:"dependsOn,omitempty"`
}

// IterationPlan is the durable expansion boundary for an iterated task. Once
// accepted, recovery uses this stored plan rather than reading a mutable
// dataset again.
type IterationPlan struct {
	ParentTask    string           `json:"parentTask"`
	DatasetDigest string           `json:"datasetDigest"`
	Policy        IterationPolicy  `json:"policy"`
	Children      []IterationChild `json:"children"`
}

// BuildIterationPlan freezes ordered dataset items into stable child task
// identities. Scheduling remains a harness responsibility; DependsOn encodes
// only the policy gates that must survive restart (sequential order and the
// optional parallel smoketest).
func BuildIterationPlan(parentTask string, policy IterationPolicy, items []json.RawMessage) (IterationPlan, error) {
	if parentTask == "" {
		return IterationPlan{}, fmt.Errorf("iteration parent task is required")
	}
	if policy.Dataset == "" {
		return IterationPlan{}, fmt.Errorf("iteration dataset is required")
	}
	if policy.MaxRetries < 0 || policy.ConcurrencyLimit < 0 || policy.StartDelayMS < 0 {
		return IterationPlan{}, fmt.Errorf("iteration limits cannot be negative")
	}
	if !policy.Parallel && (policy.Smoketest || policy.StartDelayMS > 0 || policy.ConcurrencyLimit > 1) {
		return IterationPlan{}, fmt.Errorf("sequential iteration cannot use parallel scheduling options")
	}
	if policy.ConcurrencyLimit == 0 {
		if policy.Parallel {
			policy.ConcurrencyLimit = 5
		} else {
			policy.ConcurrencyLimit = 1
		}
	}

	plan := IterationPlan{ParentTask: parentTask, Policy: policy, Children: make([]IterationChild, len(items))}
	digest := sha256.New()
	for index, raw := range items {
		canonical, err := canonicalIterationItem(raw)
		if err != nil {
			return IterationPlan{}, fmt.Errorf("iteration item %d: %w", index, err)
		}
		name := fmt.Sprintf("%s[%d]", parentTask, index)
		child := IterationChild{Name: name, Index: index, Item: canonical}
		if !policy.Parallel && index > 0 {
			child.DependsOn = []string{plan.Children[index-1].Name}
		} else if policy.Parallel && policy.Smoketest && index > 0 {
			child.DependsOn = []string{plan.Children[0].Name}
		}
		plan.Children[index] = child
		_, _ = fmt.Fprintf(digest, "%d:", len(canonical))
		_, _ = digest.Write(canonical)
	}
	plan.DatasetDigest = "sha256:" + hex.EncodeToString(digest.Sum(nil))
	return plan, nil
}

func canonicalIterationItem(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("invalid JSON: multiple values")
		}
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return canonical, nil
}
