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
