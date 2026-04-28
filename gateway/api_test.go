package gateway

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/humaninput"
	"squadron/store"
)

// All gateway-package suites share one Ginkgo Test entry. The manager_test
// already declares it; this file's specs auto-register into the same
// suite via the `var _ = Describe(...)` pattern.

// recordingListener captures DeliverResolution calls so tests can
// assert that a gateway-side resolve woke the in-process listener.
type recordingListener struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingListener) DeliverResolution(toolCallID, response, responderUserID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, toolCallID)
}

func (r *recordingListener) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func newAPITestBundle() *store.Bundle {
	dir := GinkgoT().TempDir()
	bundle, err := store.NewSQLiteBundle(filepath.Join(dir, "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(bundle.Close)
	return bundle
}

func newSquadronAPI(stores *store.Bundle, listener humaninput.Listener) (*squadronAPI, *humaninput.Notifier) {
	notif := humaninput.New()
	return &squadronAPI{
		stores:   stores,
		notifier: notif,
		listener: listener,
	}, notif
}

var _ = Describe("squadronAPI.ListHumanInputs", func() {
	It("returns rows ordered newest-first by default", func() {
		bundle := newAPITestBundle()
		api, _ := newSquadronAPI(bundle, nil)

		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-1", Question: "first", RequestedAt: time.Now().Add(-2 * time.Minute),
		})).To(Succeed())
		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-2", Question: "second", RequestedAt: time.Now().Add(-1 * time.Minute),
		})).To(Succeed())

		rows, total, err := api.ListHumanInputs(context.Background(), gwsdk.HumanInputFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(total).To(Equal(2))
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].ToolCallID).To(Equal("tc-2"), "default order is newest-first")
		Expect(rows[1].ToolCallID).To(Equal("tc-1"))
	})

	It("flips to oldest-first for catch-up replay", func() {
		bundle := newAPITestBundle()
		api, _ := newSquadronAPI(bundle, nil)

		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-1", Question: "older", RequestedAt: time.Now().Add(-2 * time.Minute),
		})).To(Succeed())
		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-2", Question: "newer", RequestedAt: time.Now().Add(-1 * time.Minute),
		})).To(Succeed())

		rows, _, err := api.ListHumanInputs(context.Background(), gwsdk.HumanInputFilter{OldestFirst: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows[0].ToolCallID).To(Equal("tc-1"), "oldest-first must replay older rows first")
	})

	It("filters by state and mission_id", func() {
		bundle := newAPITestBundle()
		api, _ := newSquadronAPI(bundle, nil)

		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "open-m1", MissionID: "m1", Question: "?",
		})).To(Succeed())
		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "open-m2", MissionID: "m2", Question: "?",
		})).To(Succeed())
		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "resolved-m1", MissionID: "m1", Question: "?",
		})).To(Succeed())
		_, err := bundle.HumanInputs.ResolveRequest("resolved-m1", "x", "user")
		Expect(err).NotTo(HaveOccurred())

		// State filter alone.
		rows, _, err := api.ListHumanInputs(context.Background(), gwsdk.HumanInputFilter{State: gwsdk.HumanInputStateOpen})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(2))

		// State + mission filter.
		rows, _, err = api.ListHumanInputs(context.Background(),
			gwsdk.HumanInputFilter{State: gwsdk.HumanInputStateOpen, MissionID: "m1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ToolCallID).To(Equal("open-m1"))
	})

	It("propagates the multi_select flag through the SDK shape", func() {
		bundle := newAPITestBundle()
		api, _ := newSquadronAPI(bundle, nil)

		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID:  "multi",
			Question:    "pick any",
			Choices:     []string{"A", "B", "C"},
			MultiSelect: true,
		})).To(Succeed())

		rows, _, err := api.ListHumanInputs(context.Background(), gwsdk.HumanInputFilter{})
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].MultiSelect).To(BeTrue(), "MultiSelect must round-trip from store → SDK shape")
		Expect(rows[0].Choices).To(Equal([]string{"A", "B", "C"}))
	})

	It("errors when the store bundle has no human-input store", func() {
		api := &squadronAPI{stores: &store.Bundle{}, notifier: humaninput.New()}
		_, _, err := api.ListHumanInputs(context.Background(), gwsdk.HumanInputFilter{})
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("squadronAPI.ResolveHumanInput", func() {
	It("resolves an open request, wakes the listener, publishes to notifier", func() {
		bundle := newAPITestBundle()
		listener := &recordingListener{}
		api, notif := newSquadronAPI(bundle, listener)

		ch, cancel := notif.Subscribe()
		defer cancel()

		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-1", Question: "?",
		})).To(Succeed())

		out, err := api.ResolveHumanInput(context.Background(), "tc-1", "yes", "discord:alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AlreadyResolved).To(BeFalse())
		Expect(out.NotFound).To(BeFalse())
		Expect(out.Record.State).To(Equal(gwsdk.HumanInputStateResolved))
		Expect(out.Record.Response).To(Equal("yes"))
		Expect(out.Record.ResponderUserID).To(Equal("discord:alice"))

		Eventually(listener.snapshot, time.Second, 10*time.Millisecond).
			Should(ConsistOf("tc-1"), "blocked AskHuman caller must be woken")

		Eventually(ch, time.Second).Should(Receive(),
			"notifier must publish the Resolved event so other gateways and wire-event emitter see it")
	})

	It("returns AlreadyResolved=true on a duplicate resolve, with the prior record intact", func() {
		bundle := newAPITestBundle()
		api, _ := newSquadronAPI(bundle, nil)

		Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			ToolCallID: "tc-1", Question: "?",
		})).To(Succeed())

		_, err := api.ResolveHumanInput(context.Background(), "tc-1", "first", "alice")
		Expect(err).NotTo(HaveOccurred())

		out, err := api.ResolveHumanInput(context.Background(), "tc-1", "second-attempt", "bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AlreadyResolved).To(BeTrue())
		Expect(out.Record.Response).To(Equal("first"),
			"the original answer must survive a duplicate resolve — never overwritten")
		Expect(out.Record.ResponderUserID).To(Equal("alice"))
	})

	It("returns NotFound=true for an unknown tool_call_id without erroring", func() {
		bundle := newAPITestBundle()
		api, _ := newSquadronAPI(bundle, nil)

		out, err := api.ResolveHumanInput(context.Background(), "ghost", "x", "alice")
		Expect(err).NotTo(HaveOccurred(),
			"NotFound is a non-error outcome — gateways should treat it as a stale event, not a retry trigger")
		Expect(out.NotFound).To(BeTrue())
	})
})
