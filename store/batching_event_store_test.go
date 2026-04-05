package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/store"
)

var _ = Describe("BatchingEventStore", func() {
	var (
		bundle  *store.Bundle
		cleanup func()
	)

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "batch-test-*")
		Expect(err).NotTo(HaveOccurred())

		dbPath := filepath.Join(dir, "test.db")
		bundle, err = store.NewSQLiteBundle(dbPath)
		Expect(err).NotTo(HaveOccurred())

		cleanup = func() {
			bundle.Close()
			os.RemoveAll(dir)
		}
	})

	AfterEach(func() {
		cleanup()
	})

	makeEvent := func(id, missionID string) store.MissionEvent {
		return store.MissionEvent{
			ID:        id,
			MissionID: missionID,
			EventType: "task_started",
			DataJSON:  `{"taskName":"test"}`,
			CreatedAt: time.Now(),
		}
	}

	makeLargeEvent := func(id, missionID string, payloadBytes int) store.MissionEvent {
		return store.MissionEvent{
			ID:        id,
			MissionID: missionID,
			EventType: "agent_tool_complete",
			DataJSON:  fmt.Sprintf(`{"result":"%s"}`, strings.Repeat("x", payloadBytes)),
			CreatedAt: time.Now(),
		}
	}

	It("flushes events on read (GetEventsByMission)", func() {
		missionID, err := bundle.Missions.CreateMission("m1", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())

		for i := 0; i < 5; i++ {
			Expect(bundle.Events.StoreEvent(makeEvent("evt-"+string(rune('a'+i)), missionID))).To(Succeed())
		}

		// Read should flush and return all events
		results, err := bundle.Events.GetEventsByMission(missionID, 100, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(5))
	})

	It("flushes events on Close", func() {
		dir, err := os.MkdirTemp("", "batch-close-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(dir)

		dbPath := filepath.Join(dir, "test.db")
		b1, err := store.NewSQLiteBundle(dbPath)
		Expect(err).NotTo(HaveOccurred())

		missionID, err := b1.Missions.CreateMission("m1", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())

		for i := 0; i < 3; i++ {
			Expect(b1.Events.StoreEvent(makeEvent("evt-close-"+string(rune('a'+i)), missionID))).To(Succeed())
		}

		// Close flushes
		b1.Close()

		// Reopen and verify events were persisted
		b2, err := store.NewSQLiteBundle(dbPath)
		Expect(err).NotTo(HaveOccurred())
		defer b2.Close()

		results, err := b2.Events.GetEventsByMission(missionID, 100, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3))
	})

	It("auto-flushes after time interval", func() {
		missionID, err := bundle.Missions.CreateMission("m1", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())

		Expect(bundle.Events.StoreEvent(makeEvent("evt-timer-1", missionID))).To(Succeed())

		// Wait for the flush interval (default 500ms) plus some margin
		time.Sleep(800 * time.Millisecond)

		results, err := bundle.Events.GetEventsByMission(missionID, 100, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(1))
	})

	It("flushes when buffered payload size exceeds threshold", func() {
		dir, err := os.MkdirTemp("", "batch-size-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(dir)

		dbPath := filepath.Join(dir, "test.db")
		// Use a raw SQLite store so we can wrap it with a small maxBytes
		rawBundle, err := store.NewSQLiteBundle(dbPath)
		Expect(err).NotTo(HaveOccurred())
		defer rawBundle.Close()

		// Create a batching store with a 1 KB threshold and very long timer
		// so only the size trigger fires
		batcher := store.NewBatchingEventStoreWithOptions(rawBundle.Events, 1024, 10*time.Second)
		defer batcher.Close()

		missionID, err := rawBundle.Missions.CreateMission("m1", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())

		// Write a few small events — should stay buffered
		for i := 0; i < 3; i++ {
			Expect(batcher.StoreEvent(makeEvent(fmt.Sprintf("small-%d", i), missionID))).To(Succeed())
		}

		// Give flush goroutine a moment to run (it shouldn't — we're under threshold)
		time.Sleep(50 * time.Millisecond)

		// Query directly on the raw store (bypassing batcher flush-on-read)
		results, err := rawBundle.Events.GetEventsByMission(missionID, 100, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(0), "small events should still be buffered")

		// Now add a large event that pushes us over the 1 KB threshold
		Expect(batcher.StoreEvent(makeLargeEvent("big-1", missionID, 2000))).To(Succeed())

		// Give flush goroutine time to process
		time.Sleep(50 * time.Millisecond)

		results, err = rawBundle.Events.GetEventsByMission(missionID, 100, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(4), "all events should be flushed after exceeding byte threshold")
	})

	It("batch-inserts via StoreEvents pass-through", func() {
		missionID, err := bundle.Missions.CreateMission("m1", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())

		events := []store.MissionEvent{
			makeEvent("evt-batch-a", missionID),
			makeEvent("evt-batch-b", missionID),
			makeEvent("evt-batch-c", missionID),
		}
		Expect(bundle.Events.StoreEvents(events)).To(Succeed())

		results, err := bundle.Events.GetEventsByMission(missionID, 100, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(3))
	})
})
