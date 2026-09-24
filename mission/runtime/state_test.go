package runtime

import "testing"

func TestStateMachinesRejectTerminalTransitions(t *testing.T) {
	t.Parallel()

	if CanTransitionRun(RunCompleted, RunRunning) {
		t.Fatal("completed run must be terminal")
	}
	if CanTransitionCommander(CommanderCompleted, CommanderActive) {
		t.Fatal("completed commander session must be terminal")
	}
	if CanTransitionTask(TaskCompleted, TaskReady) {
		t.Fatal("completed task must be terminal")
	}
	if CanTransitionAttempt(AttemptCompleted, AttemptRunning) {
		t.Fatal("completed attempt must be terminal")
	}
}

func TestStateMachinesPermitResumePaths(t *testing.T) {
	t.Parallel()

	if !CanTransitionRun(RunStopped, RunRunning) {
		t.Fatal("stopped run must be resumable")
	}
	if !CanTransitionCommander(CommanderStopped, CommanderActive) {
		t.Fatal("stopped commander session must be resumable")
	}
	if !CanTransitionTask(TaskRetryPending, TaskReady) {
		t.Fatal("retry-pending task must return to ready")
	}
}
