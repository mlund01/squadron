package aitools_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/aitools"
)

// fakeBridge records the request it was called with and returns a
// canned response (or error). It can optionally block until the
// caller's ctx expires to exercise timeout paths.
type fakeBridge struct {
	got    aitools.HumanInputRequest
	reply  string
	err    error
	block  bool
}

func (f *fakeBridge) AskHuman(ctx context.Context, req aitools.HumanInputRequest) (string, error) {
	f.got = req
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.reply, f.err
}

var _ = Describe("HumanInputTool", func() {
	It("returns [no human available] when no bridge is attached", func() {
		tool := &aitools.HumanInputTool{}
		out := tool.Call(context.Background(), `{"question":"are we ok?"}`)
		Expect(out).To(Equal(aitools.NoCommanderObservation))
	})

	It("passes the question and choices to the bridge and returns its response", func() {
		br := &fakeBridge{reply: "Option A"}
		tool := &aitools.HumanInputTool{Bridge: br}

		out := tool.Call(context.Background(), `{"question":"pick one","choices":["A","B"]}`)
		Expect(out).To(Equal("Option A"))
		Expect(br.got.Question).To(Equal("pick one"))
		Expect(br.got.Choices).To(Equal([]string{"A", "B"}))
		Expect(br.got.ToolCallID).NotTo(BeEmpty())
	})

	It("reads mission and task ids from context", func() {
		br := &fakeBridge{reply: "ok"}
		tool := &aitools.HumanInputTool{Bridge: br}

		ctx := aitools.WithMissionContext(context.Background(), "m-42", "t-7")
		tool.Call(ctx, `{"question":"hi"}`)

		Expect(br.got.MissionID).To(Equal("m-42"))
		Expect(br.got.TaskID).To(Equal("t-7"))
	})

	It("returns a timeout observation when timeout_seconds elapses", func() {
		br := &fakeBridge{block: true}
		tool := &aitools.HumanInputTool{Bridge: br}

		out := tool.Call(context.Background(), `{"question":"are you there?","timeout_seconds":0.05}`)
		Expect(out).To(ContainSubstring("no human response within"))
	})

	It("returns a cancellation observation when the outer ctx is cancelled", func() {
		br := &fakeBridge{block: true}
		tool := &aitools.HumanInputTool{Bridge: br}

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		out := tool.Call(ctx, `{"question":"are you there?"}`)
		Expect(out).To(ContainSubstring("cancelled"))
	})

	It("surfaces bridge errors to the agent", func() {
		br := &fakeBridge{err: errors.New("boom")}
		tool := &aitools.HumanInputTool{Bridge: br}

		out := tool.Call(context.Background(), `{"question":"hi"}`)
		Expect(out).To(Equal("Error: boom"))
	})

	It("rejects missing question with a clear error", func() {
		br := &fakeBridge{}
		tool := &aitools.HumanInputTool{Bridge: br}

		out := tool.Call(context.Background(), `{}`)
		Expect(out).To(ContainSubstring("question is required"))
	})

	It("rejects malformed params without calling the bridge", func() {
		br := &fakeBridge{reply: "should-not-return"}
		tool := &aitools.HumanInputTool{Bridge: br}

		out := tool.Call(context.Background(), `{bad json`)
		Expect(out).To(ContainSubstring("invalid parameters"))
		Expect(br.got.ToolCallID).To(BeEmpty())
	})

	It("forwards multi_select=true to the bridge when choices are present", func() {
		br := &fakeBridge{reply: `["A","C"]`}
		tool := &aitools.HumanInputTool{Bridge: br}

		out := tool.Call(context.Background(),
			`{"question":"pick any","choices":["A","B","C"],"multi_select":true}`)
		Expect(out).To(Equal(`["A","C"]`))
		Expect(br.got.MultiSelect).To(BeTrue())
		Expect(br.got.Choices).To(Equal([]string{"A", "B", "C"}))
	})

	It("force-disables multi_select when no choices are supplied (multi-select free-text is meaningless)", func() {
		br := &fakeBridge{reply: "freeform answer"}
		tool := &aitools.HumanInputTool{Bridge: br}

		tool.Call(context.Background(), `{"question":"hi","multi_select":true}`)
		Expect(br.got.MultiSelect).To(BeFalse(),
			"multi_select must be ignored without choices, not propagated to the bridge")
	})

	It("defaults multi_select to false when omitted", func() {
		br := &fakeBridge{reply: "A"}
		tool := &aitools.HumanInputTool{Bridge: br}

		tool.Call(context.Background(), `{"question":"pick","choices":["A","B"]}`)
		Expect(br.got.MultiSelect).To(BeFalse())
	})
})
