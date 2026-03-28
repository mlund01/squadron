package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"squadron/config"
)

// FireFunc is called when a schedule fires. missionName identifies which mission,
// source describes the trigger (e.g., "schedule[0]", "webhook").
type FireFunc func(missionName, source string, inputs map[string]string)

// Scheduler manages timer-based mission firing and concurrency tracking.
type Scheduler struct {
	fireFn  FireFunc
	timers  map[string][]*time.Timer // missionName -> active timers
	running map[string]int           // missionName -> count of running instances
	limits  map[string]int           // missionName -> max_parallel
	configs map[string]*missionSched // missionName -> schedule config
	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

type missionSched struct {
	schedules []schedEntry
}

type schedEntry struct {
	nextFire NextFireFunc
	index    int
	inputs   map[string]string
}

// New creates a new Scheduler that calls fireFn when a schedule fires.
func New(fireFn FireFunc) *Scheduler {
	return &Scheduler{
		fireFn:  fireFn,
		timers:  make(map[string][]*time.Timer),
		running: make(map[string]int),
		limits:  make(map[string]int),
		configs: make(map[string]*missionSched),
		stopCh:  make(chan struct{}),
	}
}

// UpdateConfig replaces the current schedule configuration.
// Stops all existing timers and starts new ones based on the config.
func (s *Scheduler) UpdateConfig(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return
	}

	// Stop all existing timers
	s.stopTimersLocked()

	// Clear old configs
	s.configs = make(map[string]*missionSched)
	s.limits = make(map[string]int)

	if cfg == nil {
		return
	}

	// Register concurrency limits for all missions
	for _, m := range cfg.Missions {
		s.limits[m.Name] = m.MaxParallel
	}

	// Build schedules for missions that have them
	for _, m := range cfg.Missions {
		if len(m.Schedules) == 0 {
			continue
		}

		ms := &missionSched{}
		for i, sched := range m.Schedules {
			nf, err := ParseSchedule(&sched)
			if err != nil {
				log.Printf("scheduler: mission %q schedule[%d]: %v (skipping)", m.Name, i, err)
				continue
			}
			ms.schedules = append(ms.schedules, schedEntry{nextFire: nf, index: i, inputs: sched.Inputs})
		}

		if len(ms.schedules) > 0 {
			s.configs[m.Name] = ms
			s.startTimersLocked(m.Name, ms)
		}
	}
}

// NotifyMissionStarted increments the running count for a mission.
// Returns false if the mission is at capacity (caller should skip the run).
func (s *Scheduler) NotifyMissionStarted(missionName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := s.limits[missionName]
	if limit <= 0 {
		limit = 3
	}
	if s.running[missionName] >= limit {
		return false
	}
	s.running[missionName]++
	return true
}

// NotifyMissionDone decrements the running count for a mission.
func (s *Scheduler) NotifyMissionDone(missionName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running[missionName] > 0 {
		s.running[missionName]--
	}
}

// Stop stops all timers and prevents further scheduling.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stopCh)
	s.stopTimersLocked()
}

func (s *Scheduler) stopTimersLocked() {
	for name, timers := range s.timers {
		for _, t := range timers {
			t.Stop()
		}
		delete(s.timers, name)
	}
}

func (s *Scheduler) startTimersLocked(missionName string, ms *missionSched) {
	for _, entry := range ms.schedules {
		s.scheduleNextLocked(missionName, entry)
	}
}

// scheduleNextLocked sets up the next timer for a schedule entry.
// MUST be called with s.mu held.
func (s *Scheduler) scheduleNextLocked(missionName string, entry schedEntry) {
	now := time.Now()
	next := entry.nextFire(now)
	delay := next.Sub(now)
	if delay < 0 {
		delay = time.Second // safety: fire almost immediately
	}

	timer := time.AfterFunc(delay, func() {
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}

		// Check concurrency
		limit := s.limits[missionName]
		if limit <= 0 {
			limit = 3
		}
		atCapacity := s.running[missionName] >= limit
		if atCapacity {
			log.Printf("scheduler: mission %q at capacity (%d/%d), skipping scheduled run", missionName, s.running[missionName], limit)
		}

		// Re-schedule for next occurrence regardless of whether we fire
		s.scheduleNextLocked(missionName, entry)
		s.mu.Unlock()

		if atCapacity {
			return
		}

		source := "schedule"
		if entry.index > 0 {
			source = fmt.Sprintf("schedule[%d]", entry.index)
		}
		// Fire the mission (this is async — fireFn is expected to be non-blocking)
		s.fireFn(missionName, source, entry.inputs)
	})

	s.timers[missionName] = append(s.timers[missionName], timer)
}
