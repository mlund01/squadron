package agent

import "squadron/llm"

// reasoningRelay translates native-reasoning StreamChunk fields into a
// matched start / delta(s) / close sequence on a streamer. Any tool-call
// event in the chunk also force-closes an open reasoning window, so the
// streamer never sees a tool event interleaved inside a reasoning span.
//
// Callers can also force-close the window from outside (Close) — useful
// when visible answer content arrives or the LLM call ends without an
// explicit ReasoningDone from the provider.
type reasoningRelay struct {
	onStart func()
	onDelta func(string) // may be nil to discard deltas
	onClose func()
	open    bool
}

func newReasoningRelay(onStart func(), onDelta func(string), onClose func()) *reasoningRelay {
	return &reasoningRelay{onStart: onStart, onDelta: onDelta, onClose: onClose}
}

func (r *reasoningRelay) Handle(chunk llm.StreamChunk) {
	if chunk.ReasoningStart {
		r.Close()
		r.onStart()
		r.open = true
	}
	if chunk.ReasoningDelta != "" {
		if !r.open {
			r.onStart()
			r.open = true
		}
		if r.onDelta != nil {
			r.onDelta(chunk.ReasoningDelta)
		}
	}
	if chunk.ReasoningDone {
		r.Close()
	}
	if chunk.ToolCallStart != nil || chunk.ToolCallDone != nil {
		r.Close()
	}
}

func (r *reasoningRelay) Close() {
	if r.open {
		r.onClose()
		r.open = false
	}
}
