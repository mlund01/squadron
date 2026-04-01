package mission

import "squadron/store"

// missionStoreAdapter adapts store.MissionStore to TaskStateStore.
type missionStoreAdapter struct {
	store store.MissionStore
}

func newTaskStateStore(s store.MissionStore) TaskStateStore {
	return &missionStoreAdapter{store: s}
}

func (a *missionStoreAdapter) UpdateTaskStateCAS(taskID string, expectedOld, newState TaskState, outputJSON, errMsg *string) (bool, error) {
	return a.store.UpdateTaskStatusCAS(taskID, string(expectedOld), string(newState), outputJSON, errMsg)
}

func (a *missionStoreAdapter) UpdateMissionStateCAS(missionID string, expectedOld, newState MissionState) (bool, error) {
	return a.store.UpdateMissionStatusCAS(missionID, string(expectedOld), string(newState))
}
