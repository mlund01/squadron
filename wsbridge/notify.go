package wsbridge

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/config"
	"squadron/notification"
)

// NotifySink adapts the wsbridge Client to notification.Sink, pushing
// mission-lifecycle notifications to the command center as TypeNotification
// envelopes. When no command center is connected it silently no-ops.
type NotifySink struct {
	client *Client
}

// NewNotifySink wraps a Client as a notification.Sink.
func NewNotifySink(client *Client) *NotifySink {
	return &NotifySink{client: client}
}

// Notify sends the Record to the command center. The per-channel config is
// unused here — the command center has no channel override.
func (s *NotifySink) Notify(ctx context.Context, _ *config.NotificationChannel, rec notification.Record) error {
	if s == nil || s.client == nil || !s.client.IsConnected() {
		return nil
	}

	payload := protocol.NotificationPayload{
		MissionID:   rec.MissionID,
		MissionName: rec.MissionName,
		Event:       rec.Event,
		Title:       rec.Title,
		Message:     rec.Message,
		OccurredAt:  rec.OccurredAt.UTC().Format(time.RFC3339Nano),
		Error:       rec.Error,
	}
	if len(rec.Outputs) > 0 {
		if b, err := json.Marshal(rec.Outputs); err == nil {
			payload.Outputs = b
		}
	}

	env, err := protocol.NewEvent(protocol.TypeNotification, &payload)
	if err != nil {
		return err
	}
	return s.client.SendEvent(env)
}
