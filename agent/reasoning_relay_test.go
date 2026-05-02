package agent

import (
	"strings"
	"testing"

	"squadron/llm"
)

// recorder captures the relay's callback events in order so tests can assert
// the exact start/delta/close sequence.
type recorder struct {
	starts int
	closes int
	deltas []string
	events []string // trace of "start" / "delta:..." / "close" in arrival order
}

func (r *recorder) onStart() {
	r.starts++
	r.events = append(r.events, "start")
}

func (r *recorder) onDelta(s string) {
	r.deltas = append(r.deltas, s)
	r.events = append(r.events, "delta:"+s)
}

func (r *recorder) onClose() {
	r.closes++
	r.events = append(r.events, "close")
}

func newRecorder() (*recorder, *reasoningRelay) {
	r := &recorder{}
	return r, newReasoningRelay(r.onStart, r.onDelta, r.onClose)
}

func TestReasoningRelay_StartDeltaDone(t *testing.T) {
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningStart: true})
	relay.Handle(llm.StreamChunk{ReasoningDelta: "hello "})
	relay.Handle(llm.StreamChunk{ReasoningDelta: "world"})
	relay.Handle(llm.StreamChunk{ReasoningDone: true})

	want := "start delta:hello  delta:world close"
	if got := strings.Join(r.events, " "); got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestReasoningRelay_DeltaWithoutExplicitStart(t *testing.T) {
	// Some providers may emit a delta as the first reasoning event without
	// a preceding ReasoningStart. The relay must implicitly open the window.
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningDelta: "implicit"})
	relay.Handle(llm.StreamChunk{ReasoningDone: true})

	if r.starts != 1 || r.closes != 1 {
		t.Fatalf("starts=%d closes=%d, want 1/1", r.starts, r.closes)
	}
	if len(r.deltas) != 1 || r.deltas[0] != "implicit" {
		t.Fatalf("deltas=%v, want [implicit]", r.deltas)
	}
}

func TestReasoningRelay_BackToBackStartsForceClose(t *testing.T) {
	// A new ReasoningStart while a window is open must close the prior
	// window before opening the new one — never two starts in a row.
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningStart: true})
	relay.Handle(llm.StreamChunk{ReasoningDelta: "first"})
	relay.Handle(llm.StreamChunk{ReasoningStart: true}) // implicit close + new start
	relay.Handle(llm.StreamChunk{ReasoningDelta: "second"})
	relay.Handle(llm.StreamChunk{ReasoningDone: true})

	want := "start delta:first close start delta:second close"
	if got := strings.Join(r.events, " "); got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestReasoningRelay_ToolCallForcesClose(t *testing.T) {
	// Tool-call events must close any open reasoning window. Providers
	// may emit a function_call mid-reasoning without a separate
	// ReasoningDone — the relay catches that.
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningStart: true})
	relay.Handle(llm.StreamChunk{ReasoningDelta: "thinking..."})
	relay.Handle(llm.StreamChunk{ToolCallStart: &llm.ToolCallStartChunk{ID: "x", Name: "y"}})

	if r.starts != 1 || r.closes != 1 {
		t.Fatalf("starts=%d closes=%d, want 1/1", r.starts, r.closes)
	}
	want := "start delta:thinking... close"
	if got := strings.Join(r.events, " "); got != want {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestReasoningRelay_ToolCallDoneAlsoCloses(t *testing.T) {
	id := "x"
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningStart: true})
	relay.Handle(llm.StreamChunk{ToolCallDone: &id})

	if r.closes != 1 {
		t.Fatalf("closes=%d, want 1", r.closes)
	}
}

func TestReasoningRelay_ExplicitClose(t *testing.T) {
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningStart: true})
	relay.Handle(llm.StreamChunk{ReasoningDelta: "x"})
	relay.Close()

	if r.closes != 1 {
		t.Fatalf("first Close: closes=%d, want 1", r.closes)
	}

	// Idempotent — second Close on a closed relay is a no-op.
	relay.Close()
	if r.closes != 1 {
		t.Fatalf("second Close: closes=%d, want still 1", r.closes)
	}
}

func TestReasoningRelay_NilDeltaCallback(t *testing.T) {
	// Some callers (commander) buffer text themselves; others discard.
	// A nil onDelta must be safe — the relay only uses it when set.
	starts, closes := 0, 0
	relay := newReasoningRelay(
		func() { starts++ },
		nil,
		func() { closes++ },
	)

	relay.Handle(llm.StreamChunk{ReasoningStart: true})
	relay.Handle(llm.StreamChunk{ReasoningDelta: "ignored"})
	relay.Handle(llm.StreamChunk{ReasoningDone: true})

	if starts != 1 || closes != 1 {
		t.Fatalf("starts=%d closes=%d, want 1/1", starts, closes)
	}
}

func TestReasoningRelay_NoOpsBeforeStart(t *testing.T) {
	// ReasoningDone or ToolCall events with no prior start must not fire
	// a spurious onClose.
	r, relay := newRecorder()

	relay.Handle(llm.StreamChunk{ReasoningDone: true})
	id := "x"
	relay.Handle(llm.StreamChunk{ToolCallDone: &id})

	if r.starts != 0 || r.closes != 0 {
		t.Fatalf("starts=%d closes=%d, want 0/0", r.starts, r.closes)
	}
}
