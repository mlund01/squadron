package notification_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/config"
	"squadron/notification"
)

type fakeSink struct {
	events []string
}

func (s *fakeSink) Notify(_ context.Context, _ *config.NotificationChannel, rec notification.Record) error {
	s.events = append(s.events, rec.Event)
	return nil
}

var _ = Describe("Dispatcher", func() {
	var (
		gw *fakeSink
		cc *fakeSink
		d  *notification.Dispatcher
	)

	BeforeEach(func() {
		gw = &fakeSink{}
		cc = &fakeSink{}
		d = notification.NewDispatcher(gw, cc)
	})

	rec := func(event string) notification.Record {
		return notification.Record{MissionID: "m1", MissionName: "m", Event: event}
	}

	It("no-ops when the mission has no notification config", func() {
		d.Dispatch(context.Background(), nil, rec(config.NotifyMissionCompleted))
		Expect(gw.events).To(BeEmpty())
		Expect(cc.events).To(BeEmpty())
	})

	It("fans out to both enabled channels when the event matches", func() {
		cfg := &config.NotificationConfig{
			Gateway:       &config.NotificationChannel{Enabled: true},
			CommandCenter: &config.NotificationChannel{Enabled: true},
		}
		d.Dispatch(context.Background(), cfg, rec(config.NotifyMissionCompleted))
		Expect(gw.events).To(ConsistOf(config.NotifyMissionCompleted))
		Expect(cc.events).To(ConsistOf(config.NotifyMissionCompleted))
	})

	It("respects a per-channel event filter", func() {
		cfg := &config.NotificationConfig{
			Gateway:       &config.NotificationChannel{Enabled: true, Events: []string{config.NotifyMissionFailed}},
			CommandCenter: &config.NotificationChannel{Enabled: true},
		}
		d.Dispatch(context.Background(), cfg, rec(config.NotifyMissionCompleted))
		Expect(gw.events).To(BeEmpty()) // filtered out
		Expect(cc.events).To(ConsistOf(config.NotifyMissionCompleted))

		d.Dispatch(context.Background(), cfg, rec(config.NotifyMissionFailed))
		Expect(gw.events).To(ConsistOf(config.NotifyMissionFailed))
	})

	It("skips a disabled channel", func() {
		cfg := &config.NotificationConfig{
			Gateway: &config.NotificationChannel{Enabled: false},
		}
		d.Dispatch(context.Background(), cfg, rec(config.NotifyMissionCompleted))
		Expect(gw.events).To(BeEmpty())
	})

	It("skips a channel with no sink wired", func() {
		// Only a command-center sink is wired; gateway sink is nil.
		d = notification.NewDispatcher(nil, cc)
		cfg := &config.NotificationConfig{
			Gateway:       &config.NotificationChannel{Enabled: true},
			CommandCenter: &config.NotificationChannel{Enabled: true},
		}
		Expect(func() {
			d.Dispatch(context.Background(), cfg, rec(config.NotifyMissionStopped))
		}).NotTo(Panic())
		Expect(cc.events).To(ConsistOf(config.NotifyMissionStopped))
	})
})
