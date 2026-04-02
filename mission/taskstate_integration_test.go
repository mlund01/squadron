package mission

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"squadron/config"
	"squadron/store"
)

var _ = Describe("TaskStateManager with SQLite store", func() {
	var (
		bundle *store.Bundle
		mgr    *TaskStateManager
	)

	BeforeEach(func() {
		var err error
		bundle, err = store.NewBundle(&config.StorageConfig{Backend: "sqlite", Path: ":memory:"})
		Expect(err).NotTo(HaveOccurred())

		// Create a mission and task in the DB
		missionID, err := bundle.Missions.CreateMission("test", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())

		taskID, err := bundle.Missions.CreateTask(missionID, "task-a", "{}")
		Expect(err).NotTo(HaveOccurred())

		// Task starts as 'pending' in DB, mission starts as 'running' (DB default)
		mgr = NewTaskStateManager(missionID, newTaskStateStore(bundle.Missions))
		mgr.missionState = MissionRunning // match DB default
		mgr.RegisterTask("task-a", taskID, TaskPending)
	})

	AfterEach(func() {
		bundle.Close()
	})

	Describe("CAS transitions persist to DB", func() {
		It("transitions pending → ready → running → completed", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())

			output := `{"result": "done"}`
			Expect(mgr.TransitionTask("task-a", TaskCompleted, &output, nil)).To(Succeed())

			// Verify DB state
			task, err := bundle.Missions.GetTask(mgr.taskIDs["task-a"])
			Expect(err).NotTo(HaveOccurred())
			Expect(task.Status).To(Equal("completed"))
		})

		It("rejects CAS when DB state doesn't match", func() {
			// Transition in-memory to ready
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())

			// Externally change DB status behind the manager's back
			bundle.Missions.UpdateTaskStatus(mgr.taskIDs["task-a"], "failed", nil, nil)

			// CAS should fail because DB has 'failed' but manager expects 'ready'
			err := mgr.TransitionTask("task-a", TaskRunning, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("state conflict"))
		})

		It("handles stop lifecycle: running → stopping → stopped", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopping, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopped, nil, nil)).To(Succeed())

			task, err := bundle.Missions.GetTask(mgr.taskIDs["task-a"])
			Expect(err).NotTo(HaveOccurred())
			Expect(task.Status).To(Equal("stopped"))
		})

		It("resumes from stopped: stopped → ready → running", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopping, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopped, nil, nil)).To(Succeed())

			// Resume
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())

			task, err := bundle.Missions.GetTask(mgr.taskIDs["task-a"])
			Expect(err).NotTo(HaveOccurred())
			Expect(task.Status).To(Equal("running"))
		})
	})

	Describe("ForceState", func() {
		It("updates in-memory state without DB write", func() {
			mgr.ForceState("task-a", TaskCompleted)

			s, ok := mgr.GetTaskState("task-a")
			Expect(ok).To(BeTrue())
			Expect(s).To(Equal(TaskCompleted))

			// DB should still be pending (no write happened)
			task, err := bundle.Missions.GetTask(mgr.taskIDs["task-a"])
			Expect(err).NotTo(HaveOccurred())
			Expect(task.Status).To(Equal("pending"))
		})
	})

	Describe("SetTaskID and GetTaskID", func() {
		It("updates the task ID for later CAS", func() {
			mgr2 := NewTaskStateManager("m2", newTaskStateStore(bundle.Missions))
			mgr2.RegisterTask("fresh-task", "", TaskPending)

			Expect(mgr2.GetTaskID("fresh-task")).To(Equal(""))

			// Simulate runTask creating the DB record
			missionID2, _ := bundle.Missions.CreateMission("test2", "{}", "{}")
			taskID2, _ := bundle.Missions.CreateTask(missionID2, "fresh-task", "{}")

			mgr2.SetTaskID("fresh-task", taskID2)
			Expect(mgr2.GetTaskID("fresh-task")).To(Equal(taskID2))

			// CAS should now work
			Expect(mgr2.TransitionTask("fresh-task", TaskReady, nil, nil)).To(Succeed())

			task, err := bundle.Missions.GetTask(taskID2)
			Expect(err).NotTo(HaveOccurred())
			Expect(task.Status).To(Equal("ready"))
		})

		It("skips DB write when task ID is empty", func() {
			mgr2 := NewTaskStateManager("m2", newTaskStateStore(bundle.Missions))
			mgr2.RegisterTask("no-db-task", "", TaskPending)

			// Should succeed in-memory even without DB
			Expect(mgr2.TransitionTask("no-db-task", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr2.TransitionTask("no-db-task", TaskRunning, nil, nil)).To(Succeed())

			s, _ := mgr2.GetTaskState("no-db-task")
			Expect(s).To(Equal(TaskRunning))
		})
	})

	Describe("Mission state CAS", func() {
		It("transitions mission running → stopping → stopped", func() {
			mgr.missionState = MissionRunning
			Expect(mgr.TransitionMission(MissionStopping)).To(Succeed())
			Expect(mgr.TransitionMission(MissionStopped)).To(Succeed())
			Expect(mgr.GetMissionState()).To(Equal(MissionStopped))
		})

		It("transitions mission stopped → running (resume)", func() {
			// First get to stopped state in both memory and DB
			Expect(mgr.TransitionMission(MissionStopping)).To(Succeed())
			Expect(mgr.TransitionMission(MissionStopped)).To(Succeed())
			// Now resume
			Expect(mgr.TransitionMission(MissionRunning)).To(Succeed())
			Expect(mgr.GetMissionState()).To(Equal(MissionRunning))
		})
	})

	Describe("Resume scenario: register with actual DB status", func() {
		It("registers stopped tasks correctly for resume", func() {
			// Simulate a stopped task in DB
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopping, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopped, nil, nil)).To(Succeed())

			// Create a new manager simulating resume
			taskID := mgr.GetTaskID("task-a")
			mgr2 := NewTaskStateManager(mgr.missionID, newTaskStateStore(bundle.Missions))
			mgr2.RegisterTask("task-a", taskID, TaskStopped)

			// Should be able to resume: stopped → ready → running
			Expect(mgr2.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr2.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr2.IsInFlight("task-a")).To(BeTrue())
		})
	})
})
