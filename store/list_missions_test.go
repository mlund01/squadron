package store_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/store"
)

var _ = Describe("ListMissions", func() {
	runListMissionsTests := func(newBundle func() (*store.Bundle, func())) {
		var (
			bundle  *store.Bundle
			cleanup func()
		)

		BeforeEach(func() {
			bundle, cleanup = newBundle()
		})

		AfterEach(func() {
			cleanup()
		})

		It("returns empty list when no missions exist", func() {
			missions, total, err := bundle.Missions.ListMissions(10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(missions).To(BeEmpty())
			Expect(total).To(Equal(0))
		})

		It("lists all missions", func() {
			_, err := bundle.Missions.CreateMission("mission-a", `{"key":"val"}`, "{}")
			Expect(err).NotTo(HaveOccurred())
			time.Sleep(10 * time.Millisecond) // ensure different timestamps

			_, err = bundle.Missions.CreateMission("mission-b", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			missions, total, err := bundle.Missions.ListMissions(10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(2))
			Expect(missions).To(HaveLen(2))

			// Should be ordered by started_at desc (most recent first)
			Expect(missions[0].MissionName).To(Equal("mission-b"))
			Expect(missions[1].MissionName).To(Equal("mission-a"))
		})

		It("respects limit and offset", func() {
			for _, name := range []string{"m1", "m2", "m3", "m4", "m5"} {
				_, err := bundle.Missions.CreateMission(name, "{}", "{}")
				Expect(err).NotTo(HaveOccurred())
				time.Sleep(10 * time.Millisecond)
			}

			// Get first 2 (most recent: m5, m4)
			missions, total, err := bundle.Missions.ListMissions(2, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(5))
			Expect(missions).To(HaveLen(2))
			Expect(missions[0].MissionName).To(Equal("m5"))
			Expect(missions[1].MissionName).To(Equal("m4"))

			// Get next 2 (m3, m2)
			missions, total, err = bundle.Missions.ListMissions(2, 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(5))
			Expect(missions).To(HaveLen(2))
			Expect(missions[0].MissionName).To(Equal("m3"))
			Expect(missions[1].MissionName).To(Equal("m2"))

			// Get last 1 (m1)
			missions, total, err = bundle.Missions.ListMissions(2, 4)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(5))
			Expect(missions).To(HaveLen(1))
			Expect(missions[0].MissionName).To(Equal("m1"))
		})

		It("returns correct total even with offset beyond count", func() {
			_, err := bundle.Missions.CreateMission("only-one", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			missions, total, err := bundle.Missions.ListMissions(10, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(1))
			Expect(missions).To(BeEmpty())
		})

		It("includes status and timestamps", func() {
			id, err := bundle.Missions.CreateMission("tracked", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())

			err = bundle.Missions.UpdateMissionStatus(id, "completed")
			Expect(err).NotTo(HaveOccurred())

			missions, _, err := bundle.Missions.ListMissions(10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(missions).To(HaveLen(1))
			Expect(missions[0].Status).To(Equal("completed"))
			Expect(missions[0].FinishedAt).NotTo(BeNil())
		})
	}

	Context("Memory backend", func() {
		runListMissionsTests(func() (*store.Bundle, func()) {
			return store.NewMemoryBundle(), func() {}
		})
	})

	Context("SQLite backend", func() {
		runListMissionsTests(func() (*store.Bundle, func()) {
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
