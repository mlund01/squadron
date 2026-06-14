package gateway

import (
	"context"

	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/config"
	"squadron/notification"
)

// NotifySink adapts the gateway Manager to notification.Sink so the
// notification dispatcher can deliver mission-lifecycle notifications to the
// configured gateway subprocess.
type NotifySink struct {
	mgr *Manager
}

// NewNotifySink wraps a Manager as a notification.Sink.
func NewNotifySink(mgr *Manager) *NotifySink {
	return &NotifySink{mgr: mgr}
}

// Notify converts the Record (honoring the mission's per-channel gateway
// override) and forwards it to the gateway subprocess.
func (s *NotifySink) Notify(ctx context.Context, ch *config.NotificationChannel, rec notification.Record) error {
	if s == nil || s.mgr == nil {
		return nil
	}
	channel := ""
	if ch != nil {
		channel = ch.Channel
	}
	return s.mgr.Notify(ctx, gwsdk.NotificationRecord{
		MissionID:   rec.MissionID,
		MissionName: rec.MissionName,
		Event:       rec.Event,
		Title:       rec.Title,
		Message:     rec.Message,
		OccurredAt:  rec.OccurredAt,
		Error:       rec.Error,
		Channel:     channel,
	})
}
