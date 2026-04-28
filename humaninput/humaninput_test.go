package humaninput

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"squadron/store"
)

func TestHumanInput(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HumanInput Suite")
}

func newBundle() *store.Bundle {
	dir := GinkgoT().TempDir()
	bundle, err := store.NewSQLiteBundle(filepath.Join(dir, "test.db"))
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(bundle.Close)
	return bundle
}

// recordingListener captures DeliverResolution calls so tests can
// assert which tool calls woke up.
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

var _ = Describe("Resolve", func() {
	var (
		bundle *store.Bundle
		notif  *Notifier
		lis    *recordingListener
	)

	BeforeEach(func() {
		bundle = newBundle()
		notif = New()
		lis = &recordingListener{}
	})

	createOpen := func(toolCallID, missionID string) {
		err := bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
			MissionID:  missionID,
			ToolCallID: toolCallID,
			Question:   "?",
		})
		Expect(err).NotTo(HaveOccurred())
	}

	It("resolves an open request, wakes the listener, and publishes a Resolved event", func() {
		createOpen("tc-1", "m1")
		ch, cancel := notif.Subscribe()
		defer cancel()

		out, err := Resolve(bundle, lis, notif, "tc-1", "yes", "user:alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AlreadyResolved).To(BeFalse())
		Expect(out.NotFound).To(BeFalse())
		Expect(out.Record.State).To(Equal(store.HumanInputStateResolved))
		Expect(out.Record.Response).NotTo(BeNil())
		Expect(*out.Record.Response).To(Equal("yes"))

		Eventually(lis.snapshot, time.Second, 10*time.Millisecond).Should(ConsistOf("tc-1"),
			"the listener must be woken so the blocking AskHuman call returns")

		Eventually(ch, time.Second).Should(Receive(MatchFields(IgnoreExtras, Fields{
			"Kind": Equal(EventKindResolved),
		})))
	})

	It("returns AlreadyResolved=true and the prior record without re-resolving when called twice", func() {
		createOpen("tc-1", "m1")
		_, err := Resolve(bundle, lis, notif, "tc-1", "first", "user:alice")
		Expect(err).NotTo(HaveOccurred())

		out, err := Resolve(bundle, lis, notif, "tc-1", "second", "user:bob")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AlreadyResolved).To(BeTrue(),
			"second Resolve must report already-resolved instead of overwriting the first answer")
		Expect(out.Record.Response).NotTo(BeNil())
		Expect(*out.Record.Response).To(Equal("first"),
			"the original response must survive a duplicate Resolve")

		// Listener fired once — the second (already-resolved) call must NOT
		// wake the listener again, because there's no agent waiting.
		Expect(lis.snapshot()).To(ConsistOf("tc-1"))
	})

	It("returns NotFound=true for a tool_call_id that never existed", func() {
		out, err := Resolve(bundle, lis, notif, "ghost", "x", "user:alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.NotFound).To(BeTrue())
		Expect(lis.snapshot()).To(BeEmpty())
	})

	It("does not fire the listener or notifier when no listener/notifier is supplied", func() {
		createOpen("tc-1", "m1")
		out, err := Resolve(bundle, nil, nil, "tc-1", "ok", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.AlreadyResolved).To(BeFalse())
		Expect(out.Record.State).To(Equal(store.HumanInputStateResolved))
	})
})

var _ = Describe("Notifier", func() {
	It("delivers events to every subscriber", func() {
		n := New()
		ch1, c1 := n.Subscribe()
		defer c1()
		ch2, c2 := n.Subscribe()
		defer c2()

		n.Publish(Event{Kind: EventKindCreated, Record: store.HumanInputRequestRecord{ToolCallID: "tc"}})

		Eventually(ch1, time.Second).Should(Receive(MatchFields(IgnoreExtras, Fields{
			"Kind": Equal(EventKindCreated),
		})))
		Eventually(ch2, time.Second).Should(Receive(MatchFields(IgnoreExtras, Fields{
			"Kind": Equal(EventKindCreated),
		})))
	})

	It("stops delivering after the cancel func is called", func() {
		n := New()
		ch, cancel := n.Subscribe()
		cancel()

		n.Publish(Event{Kind: EventKindResolved, Record: store.HumanInputRequestRecord{ToolCallID: "tc"}})

		// Cancelled subscription's channel is closed — receive should
		// not get the event, and after a short wait the channel reads
		// the zero value with ok=false (drained).
		select {
		case ev, ok := <-ch:
			Expect(ok).To(BeFalse(), "cancelled channel should be closed: got %#v", ev)
		case <-time.After(100 * time.Millisecond):
			// Also acceptable: nothing arrives. The point is the
			// publish above does not wake the consumer.
		}
	})

	It("drops events for a slow subscriber rather than blocking the publisher", func() {
		n := New()
		ch, cancel := n.Subscribe()
		defer cancel()

		// 32 fits in the buffer; the 33rd should be dropped silently
		// rather than block Publish.
		published := int32(0)
		go func() {
			for i := 0; i < 64; i++ {
				n.Publish(Event{Kind: EventKindCreated, Record: store.HumanInputRequestRecord{ToolCallID: "tc"}})
				atomic.AddInt32(&published, 1)
			}
		}()

		Eventually(func() int32 { return atomic.LoadInt32(&published) }, time.Second).
			Should(Equal(int32(64)), "Publish must not block on a slow subscriber")

		// The buffer ought to have at most 32 events queued; we don't
		// care about exact count — just that we never blocked.
		drained := 0
		for {
			select {
			case <-ch:
				drained++
			default:
				Expect(drained).To(BeNumerically(">", 0))
				return
			}
		}
	})
})
