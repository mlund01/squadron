// Package notification delivers mission-lifecycle notifications
// (mission_completed / mission_failed / mission_stopped) to the channels a
// mission opted into via its `notification { ... }` config block.
//
// It is intentionally separate from human-input: notifications are one-way,
// informational, and never block a mission. The dispatcher fans a Record out
// to the enabled, event-matching channels; missions without a notification
// block produce no Records.
package notification

import (
	"context"
	"log"
	"time"

	"squadron/config"
)

// Record is a single mission-lifecycle notification.
type Record struct {
	MissionID   string
	MissionName string
	// Event is one of config.NotifyMission{Completed,Failed,Stopped}.
	Event      string
	Title      string
	Message    string
	OccurredAt time.Time
	// Error is set for mission_failed.
	Error string
	// Outputs is the aggregated task-output map, set for mission_completed.
	Outputs map[string]any
}

// Sink delivers a Record to one external surface. The per-channel
// NotificationChannel config (including the gateway channel override) is
// passed through so the sink can honor it.
type Sink interface {
	Notify(ctx context.Context, ch *config.NotificationChannel, rec Record) error
}

// Dispatcher fans Records out to the configured sinks.
type Dispatcher struct {
	gateway       Sink
	commandCenter Sink
}

// NewDispatcher wires the available sinks. Either may be nil (e.g. no gateway
// configured), in which case the corresponding channel is skipped.
func NewDispatcher(gateway, commandCenter Sink) *Dispatcher {
	return &Dispatcher{gateway: gateway, commandCenter: commandCenter}
}

// Dispatch resolves the mission's notification config and delivers the Record
// to every enabled channel whose event filter matches. A nil cfg (mission has
// no notification block) is a no-op.
func (d *Dispatcher) Dispatch(ctx context.Context, cfg *config.NotificationConfig, rec Record) {
	if d == nil || cfg == nil {
		return
	}
	if d.gateway != nil && cfg.Gateway.WantsEvent(rec.Event) {
		if err := d.gateway.Notify(ctx, cfg.Gateway, rec); err != nil {
			log.Printf("notification: gateway channel: %v", err)
		}
	}
	if d.commandCenter != nil && cfg.CommandCenter.WantsEvent(rec.Event) {
		if err := d.commandCenter.Notify(ctx, cfg.CommandCenter, rec); err != nil {
			log.Printf("notification: command_center channel: %v", err)
		}
	}
}
