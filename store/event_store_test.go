package store_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/store"
)

var _ = Describe("EventStore", func() {
	runEventStoreTests := func(newBundle func() (*store.Bundle, func())) {
		var (
			bundle  *store.Bundle
			cleanup func()
			events  store.EventStore
		)

		BeforeEach(func() {
			bundle, cleanup = newBundle()
			events = bundle.Events
		})

		AfterEach(func() {
			cleanup()
		})

		It("stores and retrieves events by mission", func() {
			missionID, err := bundle.Missions.CreateMission("test-mission", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			event := store.MissionEvent{
				ID:        "evt-1",
				MissionID: missionID,
				EventType: "mission_started",
				DataJSON:  `{"missionName":"test-mission","missionId":"` + missionID + `","taskCount":2}`,
				CreatedAt: time.Now(),
			}
			Expect(events.StoreEvent(event)).To(Succeed())

			results, err := events.GetEventsByMission(missionID, 100, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ID).To(Equal("evt-1"))
			Expect(results[0].EventType).To(Equal("mission_started"))
			Expect(results[0].MissionID).To(Equal(missionID))
			Expect(results[0].TaskID).To(BeNil())
		})

		It("stores events with task references", func() {
			missionID, err := bundle.Missions.CreateMission("test-mission", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			taskID, err := bundle.Missions.CreateTask(missionID, "task-1", "{}")
			Expect(err).NotTo(HaveOccurred())

			event := store.MissionEvent{
				ID:        "evt-2",
				MissionID: missionID,
				TaskID:    &taskID,
				EventType: "task_started",
				DataJSON:  `{"taskName":"task-1","objective":"do stuff"}`,
				CreatedAt: time.Now(),
			}
			Expect(events.StoreEvent(event)).To(Succeed())

			results, err := events.GetEventsByTask(taskID, 100, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ID).To(Equal("evt-2"))
			Expect(*results[0].TaskID).To(Equal(taskID))
		})

		It("stores events with session and iteration references", func() {
			missionID, err := bundle.Missions.CreateMission("test-mission", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			taskID, err := bundle.Missions.CreateTask(missionID, "task-1", "{}")
			Expect(err).NotTo(HaveOccurred())

			sessionID, err := bundle.Sessions.CreateSession(taskID, "agent", "scraper", "gpt-4", nil)
			Expect(err).NotTo(HaveOccurred())

			iterIdx := 3
			event := store.MissionEvent{
				ID:             "evt-3",
				MissionID:      missionID,
				TaskID:         &taskID,
				SessionID:      &sessionID,
				IterationIndex: &iterIdx,
				EventType:      "iteration_started",
				DataJSON:       `{"taskName":"task-1","index":3,"objective":"item 3"}`,
				CreatedAt:      time.Now(),
			}
			Expect(events.StoreEvent(event)).To(Succeed())

			results, err := events.GetEventsByMission(missionID, 100, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(*results[0].SessionID).To(Equal(sessionID))
			Expect(*results[0].IterationIndex).To(Equal(3))
		})

		It("respects limit and offset for pagination", func() {
			missionID, err := bundle.Missions.CreateMission("test-mission", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			for i := 0; i < 5; i++ {
				event := store.MissionEvent{
					ID:        "evt-" + string(rune('a'+i)),
					MissionID: missionID,
					EventType: "task_started",
					DataJSON:  `{}`,
					CreatedAt: time.Now(),
				}
				Expect(events.StoreEvent(event)).To(Succeed())
			}

			// Get first 2
			results, err := events.GetEventsByMission(missionID, 2, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(2))

			// Get next 2
			results, err = events.GetEventsByMission(missionID, 2, 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(2))

			// Get last 1
			results, err = events.GetEventsByMission(missionID, 2, 4)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
		})

		It("returns empty slice when no events match", func() {
			results, err := events.GetEventsByMission("nonexistent", 100, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(BeEmpty())

			results, err = events.GetEventsByTask("nonexistent", 100, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(BeEmpty())
		})
	}

	Context("Memory backend", func() {
		runEventStoreTests(func() (*store.Bundle, func()) {
			return store.NewMemoryBundle(), func() {}
		})
	})

	Context("SQLite backend", func() {
		runEventStoreTests(func() (*store.Bundle, func()) {
			dir, err := os.MkdirTemp("", "store-test-*")
			Expect(err).NotTo(HaveOccurred())

			dbPath := filepath.Join(dir, "test.db")
			bundle, err := store.NewSQLiteBundle(dbPath)
			Expect(err).NotTo(HaveOccurred())

			return bundle, func() {
				bundle.Close()
				os.RemoveAll(dir)
			}
		})
	})
})
