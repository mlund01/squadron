package aitools

import "context"

// missionContextKey is a private context key for mission/task identifiers
// that scoped tools (like ask_human) read when invoked. The orchestrator
// sets these before calling a tool; tools read them via
// MissionContextFromContext.
type missionContextKey struct{}

type missionContext struct {
	missionID string
	taskID    string
}

// WithMissionContext returns a child context carrying the mission and
// task identifiers. Call this at the orchestrator boundary just before
// invoking a tool.
func WithMissionContext(ctx context.Context, missionID, taskID string) context.Context {
	return context.WithValue(ctx, missionContextKey{}, missionContext{
		missionID: missionID,
		taskID:    taskID,
	})
}

// MissionContextFromContext returns the mission and task identifiers
// previously set via WithMissionContext, or empty strings if unset.
func MissionContextFromContext(ctx context.Context) (missionID, taskID string) {
	mc, _ := ctx.Value(missionContextKey{}).(missionContext)
	return mc.missionID, mc.taskID
}
