package config

import "fmt"

// Notification event names. These mirror the terminal mission lifecycle
// event strings on the wire (protocol.EventMissionCompleted/Failed/Stopped)
// — kept as local constants so the config package stays decoupled from the
// wire protocol package.
const (
	NotifyMissionCompleted = "mission_completed"
	NotifyMissionFailed    = "mission_failed"
	// NotifyAllEvents is a convenience value usable in a channel's `events`
	// list that expands to every terminal event.
	NotifyAllEvents = "all"
)

// allNotifyEvents is what "all" expands to.
var allNotifyEvents = []string{
	NotifyMissionCompleted,
	NotifyMissionFailed,
}

func validNotifyEvent(e string) bool {
	switch e {
	case NotifyMissionCompleted, NotifyMissionFailed, NotifyAllEvents:
		return true
	}
	return false
}

// NotificationConfig is a mission's `notification { ... }` block. It is
// purely opt-in: a mission without the block has a nil *NotificationConfig
// and emits no notifications.
type NotificationConfig struct {
	Gateway       *NotificationChannel `json:"gateway,omitempty"`
	CommandCenter *NotificationChannel `json:"commandCenter,omitempty"`
}

// NotificationChannel configures one delivery channel within a mission's
// notification block.
type NotificationChannel struct {
	// Enabled toggles delivery. It defaults to true when the block is
	// present (set during parsing); set `enabled = false` to keep the
	// channel configured but turn delivery off.
	Enabled bool `hcl:"enabled,optional" json:"enabled"`
	// Events is the explicit list of terminal events that fire on this
	// channel. Required and non-empty. Valid values are mission_completed,
	// mission_failed, or "all" (every terminal event).
	Events []string `hcl:"events,optional" json:"events,omitempty"`
	// Channel is a gateway-only per-mission destination override. Empty
	// means "use the gateway's globally configured default channel". It is
	// rejected on the command_center channel.
	Channel string `hcl:"channel,optional" json:"channel,omitempty"`
}

// EffectiveEvents returns the resolved event set, expanding "all".
func (ch *NotificationChannel) EffectiveEvents() []string {
	if ch == nil {
		return nil
	}
	for _, e := range ch.Events {
		if e == NotifyAllEvents {
			return allNotifyEvents
		}
	}
	return ch.Events
}

// WantsEvent reports whether the channel should fire for the given event. A
// nil or disabled channel never fires.
func (ch *NotificationChannel) WantsEvent(event string) bool {
	if ch == nil || !ch.Enabled {
		return false
	}
	for _, e := range ch.EffectiveEvents() {
		if e == event {
			return true
		}
	}
	return false
}

// Validate checks the notification block in isolation. Cross-block checks
// (e.g. gateway channel requires a configured gateway) live in
// Config.Validate.
func (n *NotificationConfig) Validate() error {
	if n == nil {
		return nil
	}
	if n.Gateway == nil && n.CommandCenter == nil {
		return fmt.Errorf("notification: at least one of 'gateway' or 'command_center' must be set")
	}
	if err := n.Gateway.validate("gateway", true); err != nil {
		return err
	}
	if err := n.CommandCenter.validate("command_center", false); err != nil {
		return err
	}
	return nil
}

func (ch *NotificationChannel) validate(name string, allowChannel bool) error {
	if ch == nil {
		return nil
	}
	if len(ch.Events) == 0 {
		return fmt.Errorf("notification %s: 'events' is required (list one or more of %s, %s, or %q)",
			name, NotifyMissionCompleted, NotifyMissionFailed, NotifyAllEvents)
	}
	for _, e := range ch.Events {
		if !validNotifyEvent(e) {
			return fmt.Errorf("notification %s: invalid event %q (valid: %s, %s, %q)",
				name, e, NotifyMissionCompleted, NotifyMissionFailed, NotifyAllEvents)
		}
	}
	if !allowChannel && ch.Channel != "" {
		return fmt.Errorf("notification %s: 'channel' is only valid on the gateway channel", name)
	}
	return nil
}
