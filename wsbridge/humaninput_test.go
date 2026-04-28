package wsbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/aitools"
	"squadron/humaninput"
	"squadron/store"
)

func TestWsbridgeHumanInput(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Wsbridge HumanInput Suite")
}

// newBareClient constructs a minimally-initialized Client with an
// in-process SQLite store suitable for exercising the human-input
// plumbing without a real WebSocket. The send channel is buffered so
// SendEvent can enqueue without a reader.
func newBareClient() *Client {
	dir := GinkgoT().TempDir()
	bundle, err := store.NewSQLiteBundle(filepath.Join(dir, "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(bundle.Close)

	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	return &Client{
		send:        make(chan []byte, 16),
		handlers:    make(map[protocol.MessageType]RequestHandler),
		pending:     make(map[string]chan *protocol.Envelope),
		humanInputs: newHumanInputListeners(),
		stores:      bundle,
		subscriptions: NewSubscriptionManager(),
		ctx:         ctx,
		stop:        cancel,
		done:        make(chan struct{}),
	}
}

var _ = Describe("humanInputListeners", func() {
	It("delivers a response to the registered listener and reports true", func() {
		l := newHumanInputListeners()
		ch := make(chan protocol.ResolveHumanInputPayload, 1)
		cancel := l.register("call-1", ch)
		defer cancel()

		ok := l.deliver(protocol.ResolveHumanInputPayload{ToolCallID: "call-1", Response: "hello"})
		Expect(ok).To(BeTrue())
		Expect((<-ch).Response).To(Equal("hello"))
	})

	It("reports false when no listener is registered", func() {
		l := newHumanInputListeners()
		ok := l.deliver(protocol.ResolveHumanInputPayload{ToolCallID: "ghost"})
		Expect(ok).To(BeFalse())
	})

	It("deregisters via the returned cancel func", func() {
		l := newHumanInputListeners()
		ch := make(chan protocol.ResolveHumanInputPayload, 1)
		cancel := l.register("call-1", ch)
		cancel()

		ok := l.deliver(protocol.ResolveHumanInputPayload{ToolCallID: "call-1"})
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("Client.AskHuman", func() {
	It("persists an open request and unblocks when a resolution arrives", func() {
		c := newBareClient()

		req := aitools.AskHumanRequest{
			ToolCallID: "call-42",
			MissionID:  "m-1",
			Question:   "pick one",
			Choices:    []string{"A", "B"},
		}

		type result struct {
			resp string
			err  error
		}
		done := make(chan result, 1)
		go func() {
			r, err := c.AskHuman(context.Background(), req)
			done <- result{r, err}
		}()

		// Wait for the open record to land so the race with resolution is tight.
		Eventually(func() string {
			rec, _ := c.stores.HumanInputs.GetByToolCallID("call-42")
			if rec == nil {
				return ""
			}
			return rec.State
		}).Should(Equal(store.HumanInputStateOpen))

		// Simulate commander delivering the response.
		env, _ := protocol.NewRequest(protocol.TypeResolveHumanInput, &protocol.ResolveHumanInputPayload{
			ToolCallID:      "call-42",
			Response:        "A",
			ResponderUserID: "user@example.com",
		})
		resp, err := c.handleResolveHumanInput(env)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp).NotTo(BeNil())

		var ack protocol.ResolveHumanInputResultPayload
		Expect(protocol.DecodePayload(resp, &ack)).To(Succeed())
		Expect(ack.Accepted).To(BeTrue())

		select {
		case r := <-done:
			Expect(r.err).NotTo(HaveOccurred())
			Expect(r.resp).To(Equal("A"))
		case <-time.After(time.Second):
			Fail("AskHuman did not return after resolution")
		}

		stored, err := c.stores.HumanInputs.GetByToolCallID("call-42")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.State).To(Equal(store.HumanInputStateResolved))
		Expect(stored.Response).NotTo(BeNil())
		Expect(*stored.Response).To(Equal("A"))
	})

	It("returns ctx.Err() when the caller's context is cancelled", func() {
		c := newBareClient()
		ctx, cancel := context.WithCancel(context.Background())

		req := aitools.AskHumanRequest{ToolCallID: "call-x", Question: "q"}
		done := make(chan error, 1)
		go func() {
			_, err := c.AskHuman(ctx, req)
			done <- err
		}()

		Eventually(func() *store.HumanInputRequestRecord {
			rec, _ := c.stores.HumanInputs.GetByToolCallID("call-x")
			return rec
		}).ShouldNot(BeNil())

		cancel()

		select {
		case err := <-done:
			Expect(err).To(MatchError(context.Canceled))
		case <-time.After(time.Second):
			Fail("AskHuman did not return after cancel")
		}
	})

	It("errors immediately when tool_call_id is empty", func() {
		c := newBareClient()
		_, err := c.AskHuman(context.Background(), aitools.AskHumanRequest{Question: "q"})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Client.handleGetHumanInputs", func() {
	It("returns the stored rows filtered by state", func() {
		c := newBareClient()

		Expect(c.stores.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "a", Question: "qa",
		})).To(Succeed())
		Expect(c.stores.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "b", Question: "qb",
		})).To(Succeed())
		_, err := c.stores.HumanInputs.ResolveRequest("b", "ok", "u")
		Expect(err).NotTo(HaveOccurred())

		env, _ := protocol.NewRequest(protocol.TypeGetHumanInputs, &protocol.GetHumanInputsPayload{
			State: store.HumanInputStateOpen,
		})
		resp, err := c.handleGetHumanInputs(env)
		Expect(err).NotTo(HaveOccurred())

		var result protocol.GetHumanInputsResultPayload
		Expect(protocol.DecodePayload(resp, &result)).To(Succeed())
		Expect(result.Total).To(Equal(1))
		Expect(result.HumanInputs).To(HaveLen(1))
		Expect(result.HumanInputs[0].ToolCallID).To(Equal("a"))
	})
})

var _ = Describe("Client.handleResolveHumanInput", func() {
	It("returns accepted=false for unknown tool call ids", func() {
		c := newBareClient()
		env, _ := protocol.NewRequest(protocol.TypeResolveHumanInput, &protocol.ResolveHumanInputPayload{
			ToolCallID: "missing",
			Response:   "x",
		})
		resp, err := c.handleResolveHumanInput(env)
		Expect(err).NotTo(HaveOccurred())
		var ack protocol.ResolveHumanInputResultPayload
		Expect(protocol.DecodePayload(resp, &ack)).To(Succeed())
		Expect(ack.Accepted).To(BeFalse())
		Expect(ack.Reason).To(ContainSubstring("not found"))
	})

	It("is idempotent — resolving twice does not fail", func() {
		c := newBareClient()
		Expect(c.stores.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "call-1",
			Question:   "q",
		})).To(Succeed())

		mk := func() *protocol.Envelope {
			env, _ := protocol.NewRequest(protocol.TypeResolveHumanInput, &protocol.ResolveHumanInputPayload{
				ToolCallID: "call-1",
				Response:   "A",
			})
			return env
		}

		resp1, err := c.handleResolveHumanInput(mk())
		Expect(err).NotTo(HaveOccurred())

		resp2, err := c.handleResolveHumanInput(mk())
		Expect(err).NotTo(HaveOccurred())

		var a1, a2 protocol.ResolveHumanInputResultPayload
		Expect(protocol.DecodePayload(resp1, &a1)).To(Succeed())
		Expect(protocol.DecodePayload(resp2, &a2)).To(Succeed())
		Expect(a1.Accepted).To(BeTrue())
		Expect(a2.Accepted).To(BeTrue())
	})
})

// Silence unused sql import when only types are used.
var _ = sql.ErrNoRows

var _ = Describe("Client.cancelOpenHumanInputsForMission", func() {
	It("auto-resolves every open request for the failed mission and leaves other missions untouched", func() {
		c := newBareClient()

		// Two open rows on the failed mission, one on a different mission,
		// one already-resolved on the failed mission. After cancel, only
		// the two open rows on the failed mission should change.
		create := func(toolCallID, missionID, state string) {
			rec := &store.HumanInputRequestRecord{
				ToolCallID: toolCallID,
				MissionID:  missionID,
				Question:   "q",
			}
			Expect(c.stores.HumanInputs.CreateRequest(rec)).To(Succeed())
			if state == store.HumanInputStateResolved {
				_, err := c.stores.HumanInputs.ResolveRequest(toolCallID, "earlier", "user:earlier")
				Expect(err).NotTo(HaveOccurred())
			}
		}
		create("a-open", "failed-mission", store.HumanInputStateOpen)
		create("b-open", "failed-mission", store.HumanInputStateOpen)
		create("c-already", "failed-mission", store.HumanInputStateResolved)
		create("d-other", "other-mission", store.HumanInputStateOpen)

		c.cancelOpenHumanInputsForMission("failed-mission", "[cancelled: mission failed]")

		expectState := func(id, expectedState, expectedResponse string) {
			r, err := c.stores.HumanInputs.GetByToolCallID(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.State).To(Equal(expectedState), "row %s state", id)
			if expectedResponse != "" {
				Expect(r.Response).NotTo(BeNil(), "row %s response", id)
				Expect(*r.Response).To(Equal(expectedResponse), "row %s response", id)
			}
		}

		expectState("a-open", store.HumanInputStateResolved, "[cancelled: mission failed]")
		expectState("b-open", store.HumanInputStateResolved, "[cancelled: mission failed]")
		// already-resolved row keeps its prior response (Resolve is idempotent).
		expectState("c-already", store.HumanInputStateResolved, "earlier")
		// Different mission's open row is untouched.
		expectState("d-other", store.HumanInputStateOpen, "")
	})

	It("is a no-op when missionID is empty", func() {
		c := newBareClient()
		Expect(c.stores.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc", MissionID: "m1", Question: "q",
		})).To(Succeed())

		c.cancelOpenHumanInputsForMission("", "[cancelled]")

		r, err := c.stores.HumanInputs.GetByToolCallID("tc")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(store.HumanInputStateOpen),
			"empty missionID must not blanket-cancel — that would resolve every open row in the system")
	})

	It("wakes any AskHuman caller waiting on a row that gets cancelled", func() {
		c := newBareClient()

		req := aitools.AskHumanRequest{ToolCallID: "live-call", MissionID: "doomed", Question: "?"}
		done := make(chan error, 1)
		go func() {
			_, err := c.AskHuman(context.Background(), req)
			done <- err
		}()

		Eventually(func() *store.HumanInputRequestRecord {
			r, _ := c.stores.HumanInputs.GetByToolCallID("live-call")
			return r
		}).ShouldNot(BeNil())

		c.cancelOpenHumanInputsForMission("doomed", "[cancelled: mission failed]")

		select {
		case err := <-done:
			Expect(err).NotTo(HaveOccurred(),
				"AskHuman should return cleanly with the sentinel response, not error out")
		case <-time.After(time.Second):
			Fail("AskHuman did not unblock when its row was cancelled")
		}
	})
})

// ─────────────────────────────────────────────────────────────────────────────
// SetHumanInputNotifier wires the in-process notifier as the *single*
// source of "resolved" wire events. This is the bug fix that made
// gateway-side resolves push commander UI in real time. These tests
// pin the contract so a future regression can't silently drop SSE
// updates again.
// ─────────────────────────────────────────────────────────────────────────────

// drainEnvelopeOfType consumes from c.send until it finds an envelope
// matching the given message type, or times out. Other envelopes are
// skipped so unrelated emits (e.g. EventHumanInputRequested fired by
// AskHuman) don't fail the test.
func drainEnvelopeOfType(c *Client, msgType protocol.MessageType, timeout time.Duration) (*protocol.Envelope, *protocol.MissionEventPayload) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case data := <-c.send:
			var env protocol.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				continue
			}
			if env.Type != msgType {
				continue
			}
			var payload protocol.MissionEventPayload
			_ = protocol.DecodePayload(&env, &payload)
			return &env, &payload
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, nil
}

var _ = Describe("Client.SetHumanInputNotifier", func() {
	It("translates EventKindResolved publishes into wire events that commander can pick up", func() {
		c := newBareClient()
		c.subscriptions.Subscribe("mission", "m-1")

		notif := humaninput.New()
		c.SetHumanInputNotifier(notif)

		// Stage a row in the store so emitHumanInputResolved has data to
		// reference (the wire event reads MissionID from the record).
		Expect(c.stores.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-1", MissionID: "m-1", Question: "?",
		})).To(Succeed())

		notif.Publish(humaninput.Event{
			Kind: humaninput.EventKindResolved,
			Record: store.HumanInputRequestRecord{
				ToolCallID: "tc-1",
				MissionID:  "m-1",
				Response:   strPtr("yes"),
				State:      store.HumanInputStateResolved,
			},
		})

		env, payload := drainEnvelopeOfType(c, protocol.TypeMissionEvent, time.Second)
		Expect(env).NotTo(BeNil(), "resolved event must reach the wire (this is what drives commander UI updates)")
		Expect(payload.EventType).To(Equal(protocol.EventHumanInputResolved))
		Expect(payload.MissionID).To(Equal("m-1"))
	})

	It("does NOT translate EventKindCreated to resolved wire events", func() {
		c := newBareClient()
		c.subscriptions.Subscribe("mission", "m-1")

		notif := humaninput.New()
		c.SetHumanInputNotifier(notif)

		notif.Publish(humaninput.Event{
			Kind:   humaninput.EventKindCreated,
			Record: store.HumanInputRequestRecord{ToolCallID: "tc-1", MissionID: "m-1"},
		})

		// Brief wait — there shouldn't be a resolved event from a Created publish.
		env, _ := drainEnvelopeOfType(c, protocol.TypeMissionEvent, 200*time.Millisecond)
		Expect(env).To(BeNil(),
			"Created publishes are handled by the AskHuman emit path, not the notifier subscription")
	})

	It("is a safe no-op when nil is passed (disables the subscription)", func() {
		c := newBareClient()
		Expect(func() { c.SetHumanInputNotifier(nil) }).NotTo(Panic())
	})

	It("stops emitting wire events after the client's ctx is cancelled (subscription teardown)", func() {
		c := newBareClient()
		c.subscriptions.Subscribe("mission", "m-1")

		notif := humaninput.New()
		c.SetHumanInputNotifier(notif)

		// Cancel the client — the subscription goroutine should exit.
		c.stop()

		// Give the goroutine a moment to observe ctx.Done.
		time.Sleep(50 * time.Millisecond)

		notif.Publish(humaninput.Event{
			Kind:   humaninput.EventKindResolved,
			Record: store.HumanInputRequestRecord{ToolCallID: "tc-1", MissionID: "m-1"},
		})

		env, _ := drainEnvelopeOfType(c, protocol.TypeMissionEvent, 150*time.Millisecond)
		Expect(env).To(BeNil(), "no wire events should fire after Stop()")
	})
})

func strPtr(s string) *string { return &s }

// ─────────────────────────────────────────────────────────────────────────────
// End-to-end: mission failure → cancel open requests → wire events fire
//
// Pulls cancel + notifier subscription together to verify the full
// chain commander UI relies on: when a mission fails, every open
// request for that mission becomes resolved AND a wire event is
// emitted for each so the Inbox clears in real time.
// ─────────────────────────────────────────────────────────────────────────────
var _ = Describe("Mission failure cancel → wire event chain", func() {
	It("emits one resolved wire event per cancelled row, and only for the failing mission", func() {
		c := newBareClient()
		// Subscribe globally so wire events with any missionID flow.
		c.subscriptions.Subscribe("global", "")
		c.SetHumanInputNotifier(humaninput.New())

		// Two open rows on doomed mission; one on a different mission.
		create := func(toolCallID, missionID string) {
			Expect(c.stores.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				ToolCallID: toolCallID, MissionID: missionID, Question: "?",
			})).To(Succeed())
		}
		create("doomed-1", "doomed")
		create("doomed-2", "doomed")
		create("survivor", "other-mission")

		c.cancelOpenHumanInputsForMission("doomed", "[cancelled: mission failed]")

		// Drain wire events. We expect exactly two resolved events for
		// the two doomed rows; the survivor must not generate one
		// because its row stayed open.
		seenToolCalls := map[string]bool{}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) && len(seenToolCalls) < 2 {
			env, payload := drainEnvelopeOfType(c, protocol.TypeMissionEvent, 100*time.Millisecond)
			if env == nil {
				continue
			}
			if payload.EventType != protocol.EventHumanInputResolved {
				continue
			}
			data, ok := payload.Data.(map[string]any)
			Expect(ok).To(BeTrue(), "payload data should be a map")
			tc, _ := data["toolCallId"].(string)
			seenToolCalls[tc] = true
		}

		Expect(seenToolCalls).To(HaveKey("doomed-1"))
		Expect(seenToolCalls).To(HaveKey("doomed-2"))
		Expect(seenToolCalls).NotTo(HaveKey("survivor"),
			"the row on a different mission must not have been cancelled — and must not emit a resolved event")

		// And the surviving row's state in the store reflects that.
		surv, err := c.stores.HumanInputs.GetByToolCallID("survivor")
		Expect(err).NotTo(HaveOccurred())
		Expect(surv.State).To(Equal(store.HumanInputStateOpen))
	})
})
