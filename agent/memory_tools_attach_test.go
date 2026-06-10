package agent_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/agent"
	"squadron/aitools"
	"squadron/config"
	"squadron/llm"
)

func TestAgentMemoryAttach(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Agent Memory Attach Suite")
}

// stubProvider satisfies llm.Provider so agent.New can construct an Agent
// without making any real LLM calls. The specs only inspect the tools map,
// never invoke Chat or ChatStream.
type stubProvider struct{}

func (stubProvider) Chat(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (stubProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

// stubMemoryStore is a minimal MemoryStore for the auto-attach branch.
type stubMemoryStore struct{}

func (stubMemoryStore) ResolvePath(string, string) (string, error) { return "", nil }
func (stubMemoryStore) MemoryInfos() []aitools.MemoryInfo          { return nil }

// minimalAgentOpts builds the bare minimum Options for agent.New: a config
// with one model and one agent, plus a stub provider so no real LLM client
// is needed.
func minimalAgentOpts(store aitools.MemoryStore) agent.Options {
	model := config.Model{
		Name:     "anthropic",
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key", // bypasses the "API key not set" guard
	}
	agentCfg := &config.Agent{
		Name:        "test_agent",
		Model:       "claude_sonnet_4",
		Personality: "x",
	}
	cfg := &config.Config{
		Models: []config.Model{model},
		Agents: []config.Agent{*agentCfg},
	}
	return agent.Options{
		Config:      cfg,
		AgentName:   "test_agent",
		AgentConfig: agentCfg,
		Provider:    stubProvider{},
		MemoryStore: store,
	}
}

// memoryToolNames lists the six tools that must appear iff MemoryStore is wired.
var memoryToolNames = []string{
	"file_list", "file_read", "file_create", "file_delete", "file_search", "file_grep",
}

var _ = Describe("agent.New file_* tool auto-attach", func() {
	It("attaches all six file_* tools when MemoryStore is non-nil", func() {
		a, err := agent.New(context.Background(), minimalAgentOpts(stubMemoryStore{}))
		Expect(err).NotTo(HaveOccurred())
		defer a.Close()

		tools := a.GetTools()
		for _, name := range memoryToolNames {
			Expect(tools).To(HaveKey(name), "%s should be auto-attached", name)
		}
	})

	It("omits all six file_* tools when MemoryStore is nil", func() {
		a, err := agent.New(context.Background(), minimalAgentOpts(nil))
		Expect(err).NotTo(HaveOccurred())
		defer a.Close()

		tools := a.GetTools()
		for _, name := range memoryToolNames {
			Expect(tools).NotTo(HaveKey(name), "%s should NOT be attached when no store is provided", name)
		}
	})

	It("wires the attached tools to the exact MemoryStore that was passed in", func() {
		// Sanity check: a regression where auto-attach used a different
		// store (or nil) would manifest as agents writing to the wrong slot
		// or panicking on a nil dereference.
		a, err := agent.New(context.Background(), minimalAgentOpts(stubMemoryStore{}))
		Expect(err).NotTo(HaveOccurred())
		defer a.Close()

		readTool, ok := a.GetTools()["file_read"].(*aitools.MemoryReadTool)
		Expect(ok).To(BeTrue(), "file_read should be a *MemoryReadTool")
		Expect(readTool.Store).NotTo(BeNil(), "file_read.Store should be wired to the provided MemoryStore")
	})
})
