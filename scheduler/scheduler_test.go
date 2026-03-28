package scheduler_test

import (
	"sync"
	"testing"
	"time"

	"squadron/config"
	"squadron/scheduler"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestScheduler(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scheduler Suite")
}

var _ = Describe("Schedule.ToCron", func() {
	It("compiles 'at' with single time", func() {
		s := config.Schedule{At: []string{"09:00"}}
		Expect(s.ToCron()).To(Equal("0 9 * * *"))
	})

	It("compiles 'at' with multiple times same minute", func() {
		s := config.Schedule{At: []string{"09:00", "17:00"}}
		Expect(s.ToCron()).To(Equal("0 9,17 * * *"))
	})

	It("compiles 'at' with different minutes", func() {
		s := config.Schedule{At: []string{"09:30", "17:45"}}
		Expect(s.ToCron()).To(Equal("30,45 9,17 * * *"))
	})

	It("compiles 'at' with weekdays", func() {
		s := config.Schedule{At: []string{"09:00"}, Weekdays: []string{"mon", "fri"}}
		Expect(s.ToCron()).To(Equal("0 9 * * 1,5"))
	})

	It("compiles 'every' sub-hour", func() {
		s := config.Schedule{Every: "15m"}
		Expect(s.ToCron()).To(Equal("*/15 * * * *"))
	})

	It("compiles 'every' hourly", func() {
		s := config.Schedule{Every: "2h"}
		Expect(s.ToCron()).To(Equal("0 */2 * * *"))
	})

	It("compiles 'every' with weekdays", func() {
		s := config.Schedule{Every: "1h", Weekdays: []string{"mon", "tue", "wed", "thu", "fri"}}
		Expect(s.ToCron()).To(Equal("0 */1 * * 1,2,3,4,5"))
	})

	It("passes through cron expression", func() {
		s := config.Schedule{Cron: "0 0 * * sun"}
		Expect(s.ToCron()).To(Equal("0 0 * * sun"))
	})
})

var _ = Describe("ParseSchedule", func() {
	Describe("cron expressions", func() {
		It("computes next fire time for a cron schedule", func() {
			sched := &config.Schedule{Cron: "0 9 * * *"} // daily at 9am
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			base := time.Date(2026, 3, 26, 8, 0, 0, 0, time.UTC)
			next := nf(base)
			Expect(next.Hour()).To(Equal(9))
			Expect(next.Minute()).To(Equal(0))
			Expect(next.Day()).To(Equal(26))
		})

		It("wraps to next day when past the cron time", func() {
			sched := &config.Schedule{Cron: "0 9 * * *"}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			base := time.Date(2026, 3, 26, 10, 0, 0, 0, time.UTC)
			next := nf(base)
			Expect(next.Hour()).To(Equal(9))
			Expect(next.After(base)).To(BeTrue())
		})

		It("respects timezone", func() {
			sched := &config.Schedule{
				Cron:     "0 9 * * *",
				Timezone: "America/Chicago",
			}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			loc, _ := time.LoadLocation("America/Chicago")
			base := time.Date(2026, 3, 26, 8, 0, 0, 0, loc)
			next := nf(base)
			Expect(next.Location().String()).To(Equal("America/Chicago"))
			Expect(next.Hour()).To(Equal(9))
		})
	})

	Describe("at schedules", func() {
		It("computes next fire for single at time", func() {
			sched := &config.Schedule{At: []string{"09:00"}, Timezone: "UTC"}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			// At 8am, next should be 9am same day
			base := time.Date(2026, 3, 26, 8, 0, 0, 0, time.UTC)
			next := nf(base)
			Expect(next.Hour()).To(Equal(9))
			Expect(next.Day()).To(Equal(26))
		})

		It("computes next fire for multiple at times", func() {
			sched := &config.Schedule{At: []string{"09:00", "17:00"}, Timezone: "UTC"}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			// At 10am, next should be 5pm same day
			base := time.Date(2026, 3, 26, 10, 0, 0, 0, time.UTC)
			next := nf(base)
			Expect(next.Hour()).To(Equal(17))
			Expect(next.Day()).To(Equal(26))

			// At 6pm, next should be 9am next day
			base2 := time.Date(2026, 3, 26, 18, 0, 0, 0, time.UTC)
			next2 := nf(base2)
			Expect(next2.Hour()).To(Equal(9))
			Expect(next2.Day()).To(Equal(27))
		})

		It("filters by weekdays", func() {
			sched := &config.Schedule{
				At:       []string{"09:00"},
				Weekdays: []string{"mon"},
				Timezone: "UTC",
			}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			// 2026-03-26 is a Thursday
			base := time.Date(2026, 3, 26, 8, 0, 0, 0, time.UTC)
			next := nf(base)
			Expect(next.Weekday()).To(Equal(time.Monday))
			Expect(next.Hour()).To(Equal(9))
		})
	})

	Describe("every schedules", func() {
		It("fires every 15 minutes", func() {
			sched := &config.Schedule{Every: "15m", Timezone: "UTC"}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			base := time.Date(2026, 3, 26, 10, 10, 0, 0, time.UTC)
			next := nf(base)
			// */15 fires at :00, :15, :30, :45 — next after :10 is :15
			Expect(next.Hour()).To(Equal(10))
			Expect(next.Minute()).To(Equal(15))
		})

		It("fires every 2 hours", func() {
			sched := &config.Schedule{Every: "2h", Timezone: "UTC"}
			nf, err := scheduler.ParseSchedule(sched)
			Expect(err).NotTo(HaveOccurred())

			base := time.Date(2026, 3, 26, 9, 30, 0, 0, time.UTC)
			next := nf(base)
			// 0 */2 fires at 0:00, 2:00, 4:00, ..., 10:00 — next after 9:30 is 10:00
			Expect(next.Hour()).To(Equal(10))
			Expect(next.Minute()).To(Equal(0))
		})
	})
})

var _ = Describe("Schedule.Validate", func() {
	It("rejects when no mode is set", func() {
		s := &config.Schedule{}
		Expect(s.Validate()).To(HaveOccurred())
	})

	It("rejects when multiple modes are set", func() {
		s := &config.Schedule{At: []string{"09:00"}, Every: "1h"}
		Expect(s.Validate()).To(HaveOccurred())
	})

	It("rejects every that doesn't divide into 60m", func() {
		s := &config.Schedule{Every: "7m"}
		Expect(s.Validate()).To(HaveOccurred())
	})

	It("rejects every that doesn't divide into 24h", func() {
		s := &config.Schedule{Every: "5h"}
		Expect(s.Validate()).To(HaveOccurred())
	})

	It("accepts valid every intervals", func() {
		for _, v := range []string{"1m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "12h"} {
			s := &config.Schedule{Every: v}
			Expect(s.Validate()).NotTo(HaveOccurred(), "expected %s to be valid", v)
		}
	})

	It("rejects weekdays with cron", func() {
		s := &config.Schedule{Cron: "0 9 * * *", Weekdays: []string{"mon"}}
		Expect(s.Validate()).To(HaveOccurred())
	})
})

var _ = Describe("Scheduler", func() {
	It("fires missions on schedule", func() {
		var mu sync.Mutex
		fired := make(map[string]int)

		s := scheduler.New(func(missionName, source string, inputs map[string]string) {
			mu.Lock()
			fired[missionName]++
			mu.Unlock()
		})
		defer s.Stop()

		cfg := &config.Config{
			Missions: []config.Mission{
				{
					Name:        "fast",
					MaxParallel: 3,
					Schedules: []config.Schedule{
						{Every: "1m"}, // fastest valid cron interval
					},
				},
			},
		}
		s.UpdateConfig(cfg)

		// The cron fires at the next minute boundary, so we just verify it parses and starts
		// without error. Full integration timing tests would need clock mocking.
		time.Sleep(100 * time.Millisecond)
	})

	It("respects concurrency limits", func() {
		var mu sync.Mutex
		var attempts int

		s := scheduler.New(func(missionName, source string, inputs map[string]string) {
			mu.Lock()
			attempts++
			mu.Unlock()
		})
		defer s.Stop()

		cfg := &config.Config{
			Missions: []config.Mission{
				{
					Name:        "limited",
					MaxParallel: 1,
					Schedules:   []config.Schedule{{Every: "1m"}},
				},
			},
		}
		s.UpdateConfig(cfg)

		// Occupy the single slot
		ok := s.NotifyMissionStarted("limited")
		Expect(ok).To(BeTrue())
		ok = s.NotifyMissionStarted("limited")
		Expect(ok).To(BeFalse())

		// Release
		s.NotifyMissionDone("limited")
		ok = s.NotifyMissionStarted("limited")
		Expect(ok).To(BeTrue())
	})

	It("stops cleanly", func() {
		s := scheduler.New(func(missionName, source string, inputs map[string]string) {})

		cfg := &config.Config{
			Missions: []config.Mission{
				{
					Name:        "stopper",
					MaxParallel: 3,
					Schedules:   []config.Schedule{{Every: "1m"}},
				},
			},
		}
		s.UpdateConfig(cfg)
		s.Stop()
		// Should not panic on double stop
		s.Stop()
	})
})
