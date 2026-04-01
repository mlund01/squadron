package mission

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTaskState(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "TaskState Suite")
}

var _ = Describe("TaskStateManager", func() {
	var mgr *TaskStateManager

	BeforeEach(func() {
		mgr = NewTaskStateManager("mission-1", nil) // nil store = in-memory only
	})

	Describe("task lifecycle transitions", func() {
		BeforeEach(func() {
			mgr.RegisterTask("task-a", "id-a", TaskPending)
		})

		It("follows the happy path: pending → ready → running → completed", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskCompleted, nil, nil)).To(Succeed())
			Expect(mgr.IsCompleted("task-a")).To(BeTrue())
		})

		It("allows running → failed", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			errMsg := "something broke"
			Expect(mgr.TransitionTask("task-a", TaskFailed, nil, &errMsg)).To(Succeed())
			Expect(mgr.IsTerminal("task-a")).To(BeTrue())
		})

		It("allows running → stopping → stopped", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopping, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopped, nil, nil)).To(Succeed())
		})

		It("allows stopped → ready (resume)", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopping, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskStopped, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
		})

		It("allows failed → ready (retry)", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			errMsg := "oops"
			Expect(mgr.TransitionTask("task-a", TaskFailed, nil, &errMsg)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
		})

		It("rejects invalid transitions", func() {
			err := mgr.TransitionTask("task-a", TaskRunning, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid task transition"))
		})

		It("rejects completed → anything", func() {
			Expect(mgr.TransitionTask("task-a", TaskReady, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskRunning, nil, nil)).To(Succeed())
			Expect(mgr.TransitionTask("task-a", TaskCompleted, nil, nil)).To(Succeed())
			err := mgr.TransitionTask("task-a", TaskReady, nil, nil)
			Expect(err).To(HaveOccurred())
		})

		It("rejects unregistered tasks", func() {
			err := mgr.TransitionTask("nonexistent", TaskReady, nil, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not registered"))
		})
	})

	Describe("mission lifecycle transitions", func() {
		It("follows the happy path: pending → running → completed", func() {
			Expect(mgr.TransitionMission(MissionRunning)).To(Succeed())
			Expect(mgr.TransitionMission(MissionCompleted)).To(Succeed())
			Expect(mgr.GetMissionState()).To(Equal(MissionCompleted))
		})

		It("allows running → stopping → stopped → running (resume)", func() {
			Expect(mgr.TransitionMission(MissionRunning)).To(Succeed())
			Expect(mgr.TransitionMission(MissionStopping)).To(Succeed())
			Expect(mgr.TransitionMission(MissionStopped)).To(Succeed())
			Expect(mgr.TransitionMission(MissionRunning)).To(Succeed())
		})

		It("rejects invalid transitions", func() {
			err := mgr.TransitionMission(MissionCompleted)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("query methods", func() {
		BeforeEach(func() {
			mgr.RegisterTask("a", "id-a", TaskCompleted)
			mgr.RegisterTask("b", "id-b", TaskRunning)
			mgr.RegisterTask("c", "id-c", TaskPending)
		})

		It("IsCompleted returns correct values", func() {
			Expect(mgr.IsCompleted("a")).To(BeTrue())
			Expect(mgr.IsCompleted("b")).To(BeFalse())
		})

		It("IsInFlight returns correct values", func() {
			Expect(mgr.IsInFlight("b")).To(BeTrue())
			Expect(mgr.IsInFlight("a")).To(BeFalse())
		})

		It("AllCompleted returns false when tasks are pending", func() {
			Expect(mgr.AllCompleted()).To(BeFalse())
		})

		It("AnyInFlight returns true when tasks are running", func() {
			Expect(mgr.AnyInFlight()).To(BeTrue())
		})

		It("Snapshot returns a copy", func() {
			snap := mgr.Snapshot()
			Expect(snap).To(HaveLen(3))
			Expect(snap["a"]).To(Equal(TaskCompleted))
		})
	})

	Describe("StopAll", func() {
		It("transitions all running tasks to stopping", func() {
			mgr.RegisterTask("a", "id-a", TaskRunning)
			mgr.RegisterTask("b", "id-b", TaskRunning)
			mgr.RegisterTask("c", "id-c", TaskPending)

			mgr.StopAll()

			s, _ := mgr.GetTaskState("a")
			Expect(s).To(Equal(TaskStopping))
			s, _ = mgr.GetTaskState("b")
			Expect(s).To(Equal(TaskStopping))
			// Pending tasks should not be touched
			s, _ = mgr.GetTaskState("c")
			Expect(s).To(Equal(TaskPending))
		})
	})

	Describe("OnTransition callback", func() {
		It("fires on successful transition", func() {
			var called bool
			var gotFrom, gotTo TaskState
			mgr.OnTransition(func(name string, from, to TaskState) {
				called = true
				gotFrom = from
				gotTo = to
			})
			mgr.RegisterTask("x", "id-x", TaskPending)
			Expect(mgr.TransitionTask("x", TaskReady, nil, nil)).To(Succeed())
			Expect(called).To(BeTrue())
			Expect(gotFrom).To(Equal(TaskPending))
			Expect(gotTo).To(Equal(TaskReady))
		})
	})
})
