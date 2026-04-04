package wsbridge

import (
	"sync"
	"time"
)

const (
	// How long a subscription lives without a pulse before it expires
	subscriptionTTL = 30 * time.Second
	// How often to check for expired subscriptions
	cleanupInterval = 10 * time.Second
)

// GlobalEvents are always sent when there's a global subscriber
var GlobalEvents = map[string]bool{
	"mission_started":   true,
	"mission_completed": true,
	"mission_failed":    true,
	"mission_stopped":   true,
	"mission_resumed":   true,
	"session_turn":      true, // for cost tracking
}

// subscription represents an active event subscription
type subscription struct {
	scope     string // "global" or "mission"
	missionID string // only for scope="mission"
	lastPulse time.Time
}

// SubscriptionManager tracks what events the commander wants to receive.
// Events are only sent if they match an active subscription.
type SubscriptionManager struct {
	mu   sync.RWMutex
	subs map[string]*subscription // key: "global" or "mission:{id}"
	done chan struct{}
}

// NewSubscriptionManager creates a new manager and starts the cleanup goroutine.
func NewSubscriptionManager() *SubscriptionManager {
	sm := &SubscriptionManager{
		subs: make(map[string]*subscription),
		done: make(chan struct{}),
	}
	go sm.cleanupLoop()
	return sm
}

// Subscribe adds or refreshes a subscription.
func (sm *SubscriptionManager) Subscribe(scope, missionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := scope
	if scope == "mission" && missionID != "" {
		key = "mission:" + missionID
	}

	sm.subs[key] = &subscription{
		scope:     scope,
		missionID: missionID,
		lastPulse: time.Now(),
	}
}

// Unsubscribe removes a subscription.
func (sm *SubscriptionManager) Unsubscribe(scope, missionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key := scope
	if scope == "mission" && missionID != "" {
		key = "mission:" + missionID
	}
	delete(sm.subs, key)
}

// ShouldSend checks if an event should be sent based on active subscriptions.
func (sm *SubscriptionManager) ShouldSend(eventType, missionID string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// No subscriptions at all → send nothing
	if len(sm.subs) == 0 {
		return false
	}

	// Check if there's a mission-specific subscription for this event's mission
	if missionID != "" {
		if sub, ok := sm.subs["mission:"+missionID]; ok && !sm.isExpired(sub) {
			return true
		}
	}

	// Check if this is a global event and there's a global subscriber
	if GlobalEvents[eventType] {
		if sub, ok := sm.subs["global"]; ok && !sm.isExpired(sub) {
			return true
		}
	}

	return false
}

// HasAnySubscription returns true if there are any active subscriptions.
func (sm *SubscriptionManager) HasAnySubscription() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, sub := range sm.subs {
		if !sm.isExpired(sub) {
			return true
		}
	}
	return false
}

// Stop shuts down the cleanup goroutine.
func (sm *SubscriptionManager) Stop() {
	close(sm.done)
}

func (sm *SubscriptionManager) isExpired(sub *subscription) bool {
	return time.Since(sub.lastPulse) > subscriptionTTL
}

func (sm *SubscriptionManager) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.mu.Lock()
			for key, sub := range sm.subs {
				if sm.isExpired(sub) {
					delete(sm.subs, key)
				}
			}
			sm.mu.Unlock()
		case <-sm.done:
			return
		}
	}
}
