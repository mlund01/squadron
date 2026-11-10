package aitools

import "context"

// Tools like `ask` need to know which mission/task they're running
// inside. The orchestrator threads those ids through ctx; tools read
// them with MissionContextFromContext.

type missionContextKey struct{}

type missionContext struct {
	missionID string
	taskID    string
}

func WithMissionContext(ctx context.Context, missionID, taskID string) context.Context {
	return context.WithValue(ctx, missionContextKey{}, missionContext{
		missionID: missionID,
		taskID:    taskID,
	})
}

// MissionContextFromContext returns ("", "") if not set.
func MissionContextFromContext(ctx context.Context) (missionID, taskID string) {
	mc, _ := ctx.Value(missionContextKey{}).(missionContext)
	return mc.missionID, mc.taskID
}

type missionThreadContextKey struct{}

// WithMissionThread attaches a mission thread ID to the execution context.
// A mission thread is a chain of related mission runs.
func WithMissionThread(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, missionThreadContextKey{}, threadID)
}

func MissionThreadFromContext(ctx context.Context) string {
	threadID, _ := ctx.Value(missionThreadContextKey{}).(string)
	return threadID
}
