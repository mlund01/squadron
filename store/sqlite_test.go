package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/zclconf/go-cty/cty"

	"squadron/store"
)

func newSQLiteBundle() (*store.Bundle, func()) {
	dir, err := os.MkdirTemp("", "sqlite-test-*")
	Expect(err).NotTo(HaveOccurred())

	dbPath := filepath.Join(dir, "test.db")
	bundle, err := store.NewSQLiteBundle(dbPath)
	Expect(err).NotTo(HaveOccurred())

	return bundle, func() {
		bundle.Close()
		os.RemoveAll(dir)
	}
}

// helper to create a mission + task for tests that need one
func seedMissionAndTask(bundle *store.Bundle) (missionID, taskID string) {
	missionID, err := bundle.Missions.CreateMission("test-mission", `{"key":"val"}`, `{"cfg":true}`)
	Expect(err).NotTo(HaveOccurred())
	taskID, err = bundle.Missions.CreateTask(missionID, "test-task", `{"timeout":30}`)
	Expect(err).NotTo(HaveOccurred())
	return
}

var _ = Describe("SQLite MissionStore", func() {
	var (
		bundle  *store.Bundle
		cleanup func()
	)

	BeforeEach(func() {
		bundle, cleanup = newSQLiteBundle()
	})

	AfterEach(func() {
		cleanup()
	})

	// =========================================================================
	// 1. CreateMission
	// =========================================================================
	Describe("CreateMission", func() {
		It("returns a valid ID and stores name, status, and config", func() {
			id, err := bundle.Missions.CreateMission("my-mission", `{"input":"data"}`, `{"cfg":1}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())

			m, err := bundle.Missions.GetMission(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.MissionName).To(Equal("my-mission"))
			Expect(m.Status).To(Equal("running"))
			Expect(m.InputValuesJSON).To(Equal(`{"input":"data"}`))
			Expect(m.ConfigJSON).To(Equal(`{"cfg":1}`))
			Expect(m.StartedAt).NotTo(BeZero())
			Expect(m.FinishedAt).To(BeNil())
		})
	})

	// =========================================================================
	// 2. GetMission
	// =========================================================================
	Describe("GetMission", func() {
		It("retrieves a mission by ID with all fields", func() {
			id, err := bundle.Missions.CreateMission("detailed", `{"a":"b"}`, `{"x":true}`)
			Expect(err).NotTo(HaveOccurred())

			m, err := bundle.Missions.GetMission(id)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.ID).To(Equal(id))
			Expect(m.MissionName).To(Equal("detailed"))
			Expect(m.InputValuesJSON).To(Equal(`{"a":"b"}`))
			Expect(m.ConfigJSON).To(Equal(`{"x":true}`))
			Expect(m.Status).To(Equal("running"))
		})

		It("returns an error for a nonexistent ID", func() {
			_, err := bundle.Missions.GetMission("does-not-exist")
			Expect(err).To(HaveOccurred())
		})
	})

	// =========================================================================
	// 3. UpdateMissionStatus
	// =========================================================================
	Describe("UpdateMissionStatus", func() {
		It("updates status to completed and sets finished_at", func() {
			id, _ := bundle.Missions.CreateMission("m", "{}", "{}")

			err := bundle.Missions.UpdateMissionStatus(id, "completed")
			Expect(err).NotTo(HaveOccurred())

			m, _ := bundle.Missions.GetMission(id)
			Expect(m.Status).To(Equal("completed"))
			Expect(m.FinishedAt).NotTo(BeNil())
		})

		It("updates status to failed and sets finished_at", func() {
			id, _ := bundle.Missions.CreateMission("m", "{}", "{}")

			err := bundle.Missions.UpdateMissionStatus(id, "failed")
			Expect(err).NotTo(HaveOccurred())

			m, _ := bundle.Missions.GetMission(id)
			Expect(m.Status).To(Equal("failed"))
			Expect(m.FinishedAt).NotTo(BeNil())
		})

		It("does not set finished_at for non-terminal statuses", func() {
			id, _ := bundle.Missions.CreateMission("m", "{}", "{}")

			err := bundle.Missions.UpdateMissionStatus(id, "paused")
			Expect(err).NotTo(HaveOccurred())

			m, _ := bundle.Missions.GetMission(id)
			Expect(m.Status).To(Equal("paused"))
			Expect(m.FinishedAt).To(BeNil())
		})
	})

	// =========================================================================
	// 4. CreateTask
	// =========================================================================
	Describe("CreateTask", func() {
		It("creates a task linked to a mission with config", func() {
			missionID, _ := bundle.Missions.CreateMission("m", "{}", "{}")

			taskID, err := bundle.Missions.CreateTask(missionID, "scrape", `{"retries":3}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(taskID).NotTo(BeEmpty())

			t, err := bundle.Missions.GetTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.MissionID).To(Equal(missionID))
			Expect(t.TaskName).To(Equal("scrape"))
			Expect(t.ConfigJSON).To(Equal(`{"retries":3}`))
			Expect(t.Status).To(Equal("pending"))
		})
	})

	// =========================================================================
	// 5. UpdateTaskStatus
	// =========================================================================
	Describe("UpdateTaskStatus", func() {
		It("updates status, output, and error fields", func() {
			missionID, taskID := seedMissionAndTask(bundle)
			_ = missionID

			output := `{"result":"ok"}`
			err := bundle.Missions.UpdateTaskStatus(taskID, "completed", &output, nil)
			Expect(err).NotTo(HaveOccurred())

			t, _ := bundle.Missions.GetTask(taskID)
			Expect(t.Status).To(Equal("completed"))
			Expect(t.OutputJSON).NotTo(BeNil())
			Expect(*t.OutputJSON).To(Equal(`{"result":"ok"}`))
			Expect(t.FinishedAt).NotTo(BeNil())
			Expect(t.Error).To(BeNil())
		})

		It("stores an error message on failure", func() {
			_, taskID := seedMissionAndTask(bundle)

			errMsg := "something broke"
			err := bundle.Missions.UpdateTaskStatus(taskID, "failed", nil, &errMsg)
			Expect(err).NotTo(HaveOccurred())

			t, _ := bundle.Missions.GetTask(taskID)
			Expect(t.Status).To(Equal("failed"))
			Expect(t.Error).NotTo(BeNil())
			Expect(*t.Error).To(Equal("something broke"))
			Expect(t.FinishedAt).NotTo(BeNil())
		})
	})

	// =========================================================================
	// 6. GetTask
	// =========================================================================
	Describe("GetTask", func() {
		It("retrieves a task by ID", func() {
			_, taskID := seedMissionAndTask(bundle)

			t, err := bundle.Missions.GetTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.ID).To(Equal(taskID))
			Expect(t.TaskName).To(Equal("test-task"))
			Expect(t.StartedAt).NotTo(BeNil())
		})

		It("returns an error for a nonexistent task", func() {
			_, err := bundle.Missions.GetTask("nonexistent")
			Expect(err).To(HaveOccurred())
		})
	})

	// =========================================================================
	// 7. GetTasksByMission
	// =========================================================================
	Describe("GetTasksByMission", func() {
		It("returns all tasks for a mission", func() {
			missionID, _ := bundle.Missions.CreateMission("m", "{}", "{}")
			_, err := bundle.Missions.CreateTask(missionID, "task-a", "{}")
			Expect(err).NotTo(HaveOccurred())
			_, err = bundle.Missions.CreateTask(missionID, "task-b", "{}")
			Expect(err).NotTo(HaveOccurred())

			tasks, err := bundle.Missions.GetTasksByMission(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(tasks).To(HaveLen(2))

			names := []string{tasks[0].TaskName, tasks[1].TaskName}
			Expect(names).To(ContainElements("task-a", "task-b"))
		})

		It("returns empty slice for mission with no tasks", func() {
			missionID, _ := bundle.Missions.CreateMission("empty", "{}", "{}")
			tasks, err := bundle.Missions.GetTasksByMission(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(tasks).To(BeEmpty())
		})
	})

	// =========================================================================
	// 8. GetTaskByName
	// =========================================================================
	Describe("GetTaskByName", func() {
		It("retrieves a task by mission ID and name", func() {
			missionID, _ := bundle.Missions.CreateMission("m", "{}", "{}")
			taskID, _ := bundle.Missions.CreateTask(missionID, "unique-name", `{"k":"v"}`)

			t, err := bundle.Missions.GetTaskByName(missionID, "unique-name")
			Expect(err).NotTo(HaveOccurred())
			Expect(t.ID).To(Equal(taskID))
			Expect(t.ConfigJSON).To(Equal(`{"k":"v"}`))
		})

		It("returns an error when no task matches", func() {
			missionID, _ := bundle.Missions.CreateMission("m", "{}", "{}")
			_, err := bundle.Missions.GetTaskByName(missionID, "nope")
			Expect(err).To(HaveOccurred())
		})
	})

	// =========================================================================
	// 9. StoreTaskOutput
	// =========================================================================
	Describe("StoreTaskOutput", func() {
		It("persists iteration outputs with dataset metadata", func() {
			_, taskID := seedMissionAndTask(bundle)

			dsName := "results"
			dsIdx := 0
			itemID := "item-42"
			err := bundle.Missions.StoreTaskOutput(taskID, &dsName, &dsIdx, &itemID, `{"answer":"42"}`)
			Expect(err).NotTo(HaveOccurred())

			outputs, err := bundle.Missions.GetTaskOutputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(outputs).To(HaveLen(1))
			Expect(outputs[0].TaskID).To(Equal(taskID))
			Expect(*outputs[0].DatasetName).To(Equal("results"))
			Expect(*outputs[0].DatasetIndex).To(Equal(0))
			Expect(*outputs[0].ItemID).To(Equal("item-42"))
			Expect(outputs[0].OutputJSON).To(Equal(`{"answer":"42"}`))
		})

		It("stores outputs with nil optional fields", func() {
			_, taskID := seedMissionAndTask(bundle)

			err := bundle.Missions.StoreTaskOutput(taskID, nil, nil, nil, `{"plain":"output"}`)
			Expect(err).NotTo(HaveOccurred())

			outputs, err := bundle.Missions.GetTaskOutputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(outputs).To(HaveLen(1))
			Expect(outputs[0].DatasetName).To(BeNil())
			Expect(outputs[0].DatasetIndex).To(BeNil())
			Expect(outputs[0].ItemID).To(BeNil())
		})
	})

	// =========================================================================
	// 10. GetTaskOutputs
	// =========================================================================
	Describe("GetTaskOutputs", func() {
		It("retrieves all outputs for a task ordered by index then time", func() {
			_, taskID := seedMissionAndTask(bundle)

			idx0, idx1 := 0, 1
			Expect(bundle.Missions.StoreTaskOutput(taskID, nil, &idx1, nil, `{"second":true}`)).To(Succeed())
			Expect(bundle.Missions.StoreTaskOutput(taskID, nil, &idx0, nil, `{"first":true}`)).To(Succeed())

			outputs, err := bundle.Missions.GetTaskOutputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(outputs).To(HaveLen(2))
			// Ordered by dataset_index ASC
			Expect(*outputs[0].DatasetIndex).To(Equal(0))
			Expect(*outputs[1].DatasetIndex).To(Equal(1))
		})

		It("returns empty slice when no outputs exist", func() {
			_, taskID := seedMissionAndTask(bundle)
			outputs, err := bundle.Missions.GetTaskOutputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(outputs).To(BeEmpty())
		})
	})

	// =========================================================================
	// 11. StoreTaskInput / GetTaskInputs
	// =========================================================================
	Describe("StoreTaskInput and GetTaskInputs", func() {
		It("stores and retrieves task inputs with iteration index", func() {
			_, taskID := seedMissionAndTask(bundle)

			idx0, idx1 := 0, 1
			Expect(bundle.Missions.StoreTaskInput(taskID, &idx0, "Process item A")).To(Succeed())
			Expect(bundle.Missions.StoreTaskInput(taskID, &idx1, "Process item B")).To(Succeed())

			inputs, err := bundle.Missions.GetTaskInputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(inputs).To(HaveLen(2))
			Expect(inputs[0].Objective).To(Equal("Process item A"))
			Expect(*inputs[0].IterationIndex).To(Equal(0))
			Expect(inputs[1].Objective).To(Equal("Process item B"))
			Expect(*inputs[1].IterationIndex).To(Equal(1))
		})

		It("stores inputs without iteration index", func() {
			_, taskID := seedMissionAndTask(bundle)

			Expect(bundle.Missions.StoreTaskInput(taskID, nil, "Single execution objective")).To(Succeed())

			inputs, err := bundle.Missions.GetTaskInputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(inputs).To(HaveLen(1))
			Expect(inputs[0].IterationIndex).To(BeNil())
			Expect(inputs[0].Objective).To(Equal("Single execution objective"))
			Expect(inputs[0].CreatedAt).NotTo(BeZero())
		})

		It("returns empty slice when no inputs exist", func() {
			_, taskID := seedMissionAndTask(bundle)
			inputs, err := bundle.Missions.GetTaskInputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(inputs).To(BeEmpty())
		})
	})

	// =========================================================================
	// 12. SetSubtasks / GetSubtasks / CompleteSubtask
	// =========================================================================
	Describe("Subtask lifecycle", func() {
		It("sets subtasks, marks first as in_progress", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, err := bundle.Sessions.CreateSession(taskID, "commander", "agent-1", "gpt-4", nil)
			Expect(err).NotTo(HaveOccurred())

			Expect(bundle.Missions.SetSubtasks(taskID, sessionID, nil, []string{"Step 1", "Step 2", "Step 3"})).To(Succeed())

			subtasks, err := bundle.Missions.GetSubtasks(taskID, sessionID, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(subtasks).To(HaveLen(3))

			Expect(subtasks[0].Title).To(Equal("Step 1"))
			Expect(subtasks[0].Status).To(Equal("in_progress"))
			Expect(subtasks[0].Index).To(Equal(0))

			Expect(subtasks[1].Title).To(Equal("Step 2"))
			Expect(subtasks[1].Status).To(Equal("pending"))

			Expect(subtasks[2].Title).To(Equal("Step 3"))
			Expect(subtasks[2].Status).To(Equal("pending"))
		})

		It("completes subtasks in order and advances the next one", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "commander", "agent-1", "gpt-4", nil)
			bundle.Missions.SetSubtasks(taskID, sessionID, nil, []string{"A", "B", "C"})

			// Complete first subtask
			Expect(bundle.Missions.CompleteSubtask(taskID, sessionID, nil)).To(Succeed())

			subtasks, _ := bundle.Missions.GetSubtasks(taskID, sessionID, nil)
			Expect(subtasks[0].Status).To(Equal("completed"))
			Expect(subtasks[0].CompletedAt).NotTo(BeNil())
			Expect(subtasks[1].Status).To(Equal("in_progress"))
			Expect(subtasks[2].Status).To(Equal("pending"))

			// Complete second subtask
			Expect(bundle.Missions.CompleteSubtask(taskID, sessionID, nil)).To(Succeed())

			subtasks, _ = bundle.Missions.GetSubtasks(taskID, sessionID, nil)
			Expect(subtasks[1].Status).To(Equal("completed"))
			Expect(subtasks[2].Status).To(Equal("in_progress"))
		})

		It("supports subtasks with iteration index", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "commander", "agent-1", "gpt-4", nil)

			idx := 5
			Expect(bundle.Missions.SetSubtasks(taskID, sessionID, &idx, []string{"Iter step"})).To(Succeed())

			subtasks, err := bundle.Missions.GetSubtasks(taskID, sessionID, &idx)
			Expect(err).NotTo(HaveOccurred())
			Expect(subtasks).To(HaveLen(1))
			Expect(*subtasks[0].IterationIndex).To(Equal(5))
		})

		It("replaces subtasks when SetSubtasks is called again", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "commander", "agent-1", "gpt-4", nil)

			bundle.Missions.SetSubtasks(taskID, sessionID, nil, []string{"Old 1", "Old 2"})
			bundle.Missions.SetSubtasks(taskID, sessionID, nil, []string{"New 1"})

			subtasks, _ := bundle.Missions.GetSubtasks(taskID, sessionID, nil)
			Expect(subtasks).To(HaveLen(1))
			Expect(subtasks[0].Title).To(Equal("New 1"))
		})

		It("GetSubtasksByTask returns all subtasks for a task", func() {
			_, taskID := seedMissionAndTask(bundle)
			sess1, _ := bundle.Sessions.CreateSession(taskID, "commander", "a1", "gpt-4", nil)
			sess2, _ := bundle.Sessions.CreateSession(taskID, "agent", "a2", "gpt-4", nil)

			bundle.Missions.SetSubtasks(taskID, sess1, nil, []string{"S1-A"})
			bundle.Missions.SetSubtasks(taskID, sess2, nil, []string{"S2-A", "S2-B"})

			all, err := bundle.Missions.GetSubtasksByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(HaveLen(3))
		})

		It("returns error when completing with no pending subtasks", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "commander", "agent-1", "gpt-4", nil)

			bundle.Missions.SetSubtasks(taskID, sessionID, nil, []string{"Only"})
			Expect(bundle.Missions.CompleteSubtask(taskID, sessionID, nil)).To(Succeed())

			// No more pending subtasks
			err := bundle.Missions.CompleteSubtask(taskID, sessionID, nil)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("SQLite SessionStore", func() {
	var (
		bundle  *store.Bundle
		cleanup func()
	)

	BeforeEach(func() {
		bundle, cleanup = newSQLiteBundle()
	})

	AfterEach(func() {
		cleanup()
	})

	// =========================================================================
	// 13. CreateSession
	// =========================================================================
	Describe("CreateSession", func() {
		It("returns an ID and stores role, agent, model", func() {
			_, taskID := seedMissionAndTask(bundle)

			sessionID, err := bundle.Sessions.CreateSession(taskID, "agent", "scraper", "claude-3", nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessionID).NotTo(BeEmpty())

			sessions, err := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal(sessionID))
			Expect(sessions[0].Role).To(Equal("agent"))
			Expect(sessions[0].AgentName).To(Equal("scraper"))
			Expect(sessions[0].Model).To(Equal("claude-3"))
			Expect(sessions[0].Status).To(Equal("running"))
		})

		It("stores iteration index when provided", func() {
			_, taskID := seedMissionAndTask(bundle)
			idx := 7
			sessionID, err := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", &idx)
			Expect(err).NotTo(HaveOccurred())

			sessions, _ := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal(sessionID))
			Expect(sessions[0].IterationIndex).NotTo(BeNil())
			Expect(*sessions[0].IterationIndex).To(Equal(7))
		})
	})

	// =========================================================================
	// 14. AppendMessage
	// =========================================================================
	Describe("AppendMessage", func() {
		It("adds messages to a session", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			now := time.Now().UTC().Truncate(time.Millisecond)
			Expect(bundle.Sessions.AppendMessage(sessionID, "user", "hello", now, now)).To(Succeed())
			Expect(bundle.Sessions.AppendMessage(sessionID, "assistant", "hi back", now, now.Add(time.Second))).To(Succeed())

			msgs, err := bundle.Sessions.GetMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(msgs).To(HaveLen(2))
			Expect(msgs[0].Role).To(Equal("user"))
			Expect(msgs[0].Content).To(Equal("hello"))
			Expect(msgs[1].Role).To(Equal("assistant"))
			Expect(msgs[1].Content).To(Equal("hi back"))
		})
	})

	// =========================================================================
	// 15. GetMessages
	// =========================================================================
	Describe("GetMessages", func() {
		It("retrieves messages in insertion order", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			now := time.Now().UTC().Truncate(time.Millisecond)
			for i := 0; i < 5; i++ {
				bundle.Sessions.AppendMessage(sessionID, "user", fmt.Sprintf("msg-%d", i), now, now)
			}

			msgs, err := bundle.Sessions.GetMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(msgs).To(HaveLen(5))
			for i, msg := range msgs {
				Expect(msg.Content).To(Equal(fmt.Sprintf("msg-%d", i)))
			}
		})

		It("returns empty slice for session with no messages", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			msgs, err := bundle.Sessions.GetMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(msgs).To(BeEmpty())
		})
	})

	// =========================================================================
	// 15a. AppendStructuredMessage / GetStructuredMessages
	// =========================================================================
	Describe("AppendStructuredMessage", func() {
		It("round-trips every part type through the parts table", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			now := time.Now().UTC().Truncate(time.Millisecond)
			isErrFalse := false
			isErrTrue := true

			// Cover every column on the parts table at least once.
			textPart := store.MessagePart{Type: "text", Text: "hello"}
			imgPart := store.MessagePart{Type: "image", ImageData: "AAAA", ImageMediaType: "image/png"}
			toolUsePart := store.MessagePart{
				Type:             "tool_use",
				ToolUseID:        "call_1",
				ToolName:         "do_thing",
				ToolInputJSON:    `{"x":1}`,
				ThoughtSignature: []byte{0xde, 0xad, 0xbe, 0xef},
			}
			toolResultOK := store.MessagePart{Type: "tool_result", ToolUseID: "call_1", Text: "ok", IsError: &isErrFalse}
			toolResultErr := store.MessagePart{Type: "tool_result", ToolUseID: "call_2", Text: "boom", IsError: &isErrTrue}
			thinkPart := store.MessagePart{
				Type:                 "thinking",
				Text:                 "reasoning",
				ThinkingSignature:    "sig123",
				ThinkingRedactedData: "",
				ProviderID:           "rs_abc",
				EncryptedContent:     "encblob",
			}
			rawPart := store.MessagePart{
				Type:             "provider_raw",
				ProviderName:     "anthropic",
				ProviderType:     "server_tool_use",
				ProviderDataJSON: `{"foo":"bar"}`,
			}

			parts := []store.MessagePart{textPart, imgPart, toolUsePart, toolResultOK, toolResultErr, thinkPart, rawPart}
			Expect(bundle.Sessions.AppendStructuredMessage(sessionID, "assistant", "audit text", parts, now, now)).To(Succeed())

			got, err := bundle.Sessions.GetStructuredMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			Expect(got[0].Role).To(Equal("assistant"))
			Expect(got[0].Content).To(Equal("audit text"))
			Expect(got[0].Parts).To(HaveLen(len(parts)))

			// Spot-check round-trip preserved every nontrivial field.
			Expect(got[0].Parts[0]).To(Equal(textPart))
			Expect(got[0].Parts[1]).To(Equal(imgPart))
			Expect(got[0].Parts[2].ThoughtSignature).To(Equal([]byte{0xde, 0xad, 0xbe, 0xef}))
			Expect(got[0].Parts[2].ToolInputJSON).To(Equal(`{"x":1}`))
			Expect(got[0].Parts[3].IsError).NotTo(BeNil())
			Expect(*got[0].Parts[3].IsError).To(BeFalse())
			Expect(got[0].Parts[4].IsError).NotTo(BeNil())
			Expect(*got[0].Parts[4].IsError).To(BeTrue())
			Expect(got[0].Parts[5].ProviderID).To(Equal("rs_abc"))
			Expect(got[0].Parts[5].EncryptedContent).To(Equal("encblob"))
			Expect(got[0].Parts[6].ProviderDataJSON).To(Equal(`{"foo":"bar"}`))
		})

		It("preserves ordering across multiple messages with multiple parts each", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			now := time.Now().UTC().Truncate(time.Millisecond)
			Expect(bundle.Sessions.AppendStructuredMessage(sessionID, "user", "u1", []store.MessagePart{
				{Type: "text", Text: "u1-a"},
				{Type: "text", Text: "u1-b"},
			}, now, now)).To(Succeed())
			Expect(bundle.Sessions.AppendStructuredMessage(sessionID, "assistant", "a1", []store.MessagePart{
				{Type: "text", Text: "a1-a"},
				{Type: "tool_use", ToolUseID: "c", ToolName: "n", ToolInputJSON: "{}"},
			}, now, now)).To(Succeed())

			got, err := bundle.Sessions.GetStructuredMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(2))
			Expect(got[0].Parts).To(HaveLen(2))
			Expect(got[0].Parts[0].Text).To(Equal("u1-a"))
			Expect(got[0].Parts[1].Text).To(Equal("u1-b"))
			Expect(got[1].Parts).To(HaveLen(2))
			Expect(got[1].Parts[1].Type).To(Equal("tool_use"))
			Expect(got[1].Parts[1].ToolUseID).To(Equal("c"))
		})

		It("falls back to legacy content when message has no parts (e.g. pre-migration row)", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			now := time.Now().UTC().Truncate(time.Millisecond)
			Expect(bundle.Sessions.AppendMessage(sessionID, "user", "legacy text", now, now)).To(Succeed())

			got, err := bundle.Sessions.GetStructuredMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(HaveLen(1))
			Expect(got[0].Role).To(Equal("user"))
			Expect(got[0].Content).To(Equal("legacy text"))
			Expect(got[0].Parts).To(BeEmpty())
		})
	})

	// =========================================================================
	// 16. CompleteSession
	// =========================================================================
	Describe("CompleteSession", func() {
		It("sets status to completed and finished_at when err is nil", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			bundle.Sessions.CompleteSession(sessionID, nil)

			sessions, _ := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].Status).To(Equal("completed"))
			Expect(sessions[0].FinishedAt).NotTo(BeNil())
		})

		It("sets status to failed when err is provided", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			bundle.Sessions.CompleteSession(sessionID, fmt.Errorf("something failed"))

			sessions, _ := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(sessions[0].Status).To(Equal("failed"))
			Expect(sessions[0].FinishedAt).NotTo(BeNil())
		})
	})

	// =========================================================================
	// 17. ReopenSession
	// =========================================================================
	Describe("ReopenSession", func() {
		It("clears finished_at and sets status back to running", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			bundle.Sessions.CompleteSession(sessionID, nil)
			sessions, _ := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(sessions[0].Status).To(Equal("completed"))
			Expect(sessions[0].FinishedAt).NotTo(BeNil())

			bundle.Sessions.ReopenSession(sessionID)

			sessions, _ = bundle.Sessions.GetSessionsByTask(taskID)
			Expect(sessions[0].Status).To(Equal("running"))
			Expect(sessions[0].FinishedAt).To(BeNil())
		})
	})

	// =========================================================================
	// 18. GetSessionsByTask
	// =========================================================================
	Describe("GetSessionsByTask", func() {
		It("returns all sessions for a task", func() {
			_, taskID := seedMissionAndTask(bundle)

			bundle.Sessions.CreateSession(taskID, "commander", "cmd", "gpt-4", nil)
			bundle.Sessions.CreateSession(taskID, "agent", "scraper", "claude-3", nil)
			bundle.Sessions.CreateSession(taskID, "agent", "writer", "gpt-4", nil)

			sessions, err := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(HaveLen(3))
		})

		It("returns empty slice for task with no sessions", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessions, err := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(BeEmpty())
		})
	})

	// =========================================================================
	// 19. CreateChatSession
	// =========================================================================
	Describe("CreateChatSession", func() {
		It("creates a chat session with role=chat", func() {
			id, err := bundle.Sessions.CreateChatSession("my-agent", "gpt-4")
			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())

			// Verify via ListChatSessions
			sessions, total, err := bundle.Sessions.ListChatSessions("my-agent", 10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(1))
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal(id))
			Expect(sessions[0].Role).To(Equal("chat"))
			Expect(sessions[0].AgentName).To(Equal("my-agent"))
			Expect(sessions[0].Model).To(Equal("gpt-4"))
		})
	})

	// =========================================================================
	// 20. ListChatSessions
	// =========================================================================
	Describe("ListChatSessions", func() {
		It("returns paginated chat sessions ordered by started_at desc", func() {
			for i := 0; i < 5; i++ {
				_, err := bundle.Sessions.CreateChatSession("agent", fmt.Sprintf("model-%d", i))
				Expect(err).NotTo(HaveOccurred())
				time.Sleep(10 * time.Millisecond)
			}

			sessions, total, err := bundle.Sessions.ListChatSessions("agent", 2, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(5))
			Expect(sessions).To(HaveLen(2))
			// Most recent first
			Expect(sessions[0].Model).To(Equal("model-4"))
			Expect(sessions[1].Model).To(Equal("model-3"))

			// Next page
			sessions, total, err = bundle.Sessions.ListChatSessions("agent", 2, 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(5))
			Expect(sessions).To(HaveLen(2))
			Expect(sessions[0].Model).To(Equal("model-2"))
		})

		It("filters by agent name when provided", func() {
			bundle.Sessions.CreateChatSession("agent-a", "m1")
			bundle.Sessions.CreateChatSession("agent-b", "m1")
			bundle.Sessions.CreateChatSession("agent-a", "m2")

			sessions, total, err := bundle.Sessions.ListChatSessions("agent-a", 10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(2))
			Expect(sessions).To(HaveLen(2))
		})

		It("returns all chat sessions when agent name is empty", func() {
			bundle.Sessions.CreateChatSession("agent-a", "m1")
			bundle.Sessions.CreateChatSession("agent-b", "m1")

			sessions, total, err := bundle.Sessions.ListChatSessions("", 10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(2))
			Expect(sessions).To(HaveLen(2))
		})

		It("excludes completed sessions", func() {
			id, _ := bundle.Sessions.CreateChatSession("agent", "m")
			bundle.Sessions.CompleteSession(id, nil)

			sessions, total, err := bundle.Sessions.ListChatSessions("agent", 10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(0))
			Expect(sessions).To(BeEmpty())
		})
	})

	// =========================================================================
	// Tool Results
	// =========================================================================
	Describe("StoreToolResult / GetToolResultsByTask", func() {
		It("stores and retrieves a tool result with toolCallId", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, err := bundle.Sessions.CreateSession(taskID, "agent", "scraper", "claude-3", nil)
			Expect(err).NotTo(HaveOccurred())

			start := time.Now().UTC().Truncate(time.Millisecond)
			end := start.Add(500 * time.Millisecond)

			err = bundle.Sessions.StoreToolResult(taskID, sessionID, "tc-uuid-1", "browser_click", `{"selector":".btn"}`, `{"ok":true}`, start, end)
			Expect(err).NotTo(HaveOccurred())

			results, err := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))

			tr := results[0]
			Expect(tr.ID).NotTo(BeEmpty())
			Expect(tr.TaskID).To(Equal(taskID))
			Expect(tr.SessionID).To(Equal(sessionID))
			Expect(tr.ToolCallId).To(Equal("tc-uuid-1"))
			Expect(tr.ToolName).To(Equal("browser_click"))
			Expect(tr.InputParams).To(Equal(`{"selector":".btn"}`))
			Expect(tr.RawData).To(Equal(`{"ok":true}`))
		})

		It("returns empty toolCallId for legacy rows without it", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, err := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)
			Expect(err).NotTo(HaveOccurred())

			// Store with empty toolCallId (simulates legacy data)
			err = bundle.Sessions.StoreToolResult(taskID, sessionID, "", "some_tool", "{}", "{}", time.Now(), time.Now())
			Expect(err).NotTo(HaveOccurred())

			results, err := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].ToolCallId).To(Equal(""))
		})

		It("returns multiple results ordered by start time", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "a", "m", nil)

			t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
			t2 := time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC)
			t3 := time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC)

			// Insert out of order
			Expect(bundle.Sessions.StoreToolResult(taskID, sessionID, "tc-3", "tool_c", "{}", "{}", t3, t3)).To(Succeed())
			Expect(bundle.Sessions.StoreToolResult(taskID, sessionID, "tc-1", "tool_a", "{}", "{}", t1, t1)).To(Succeed())
			Expect(bundle.Sessions.StoreToolResult(taskID, sessionID, "tc-2", "tool_b", "{}", "{}", t2, t2)).To(Succeed())

			results, err := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(3))
			Expect(results[0].ToolCallId).To(Equal("tc-1"))
			Expect(results[1].ToolCallId).To(Equal("tc-2"))
			Expect(results[2].ToolCallId).To(Equal("tc-3"))
		})

		It("isolates results by task ID", func() {
			missionID, taskID1 := seedMissionAndTask(bundle)
			taskID2, err := bundle.Missions.CreateTask(missionID, "other-task", "{}")
			Expect(err).NotTo(HaveOccurred())

			sess1, _ := bundle.Sessions.CreateSession(taskID1, "agent", "a", "m", nil)
			sess2, _ := bundle.Sessions.CreateSession(taskID2, "agent", "a", "m", nil)

			now := time.Now()
			Expect(bundle.Sessions.StoreToolResult(taskID1, sess1, "tc-a", "tool_x", "{}", "{}", now, now)).To(Succeed())
			Expect(bundle.Sessions.StoreToolResult(taskID2, sess2, "tc-b", "tool_y", "{}", "{}", now, now)).To(Succeed())

			r1, _ := bundle.Sessions.GetToolResultsByTask(taskID1)
			r2, _ := bundle.Sessions.GetToolResultsByTask(taskID2)
			Expect(r1).To(HaveLen(1))
			Expect(r1[0].ToolCallId).To(Equal("tc-a"))
			Expect(r2).To(HaveLen(1))
			Expect(r2[0].ToolCallId).To(Equal("tc-b"))
		})
	})

	// =========================================================================
	Describe("Two-phase tool calls (StartToolCall / CompleteToolCall)", func() {
		It("creates a started record then completes it", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, err := bundle.Sessions.CreateSession(taskID, "agent", "test", "claude-3", nil)
			Expect(err).NotTo(HaveOccurred())

			// Phase 1: start
			recordID, err := bundle.Sessions.StartToolCall(taskID, sessionID, "tc-1", "http_get", `{"url":"https://example.com"}`)
			Expect(err).NotTo(HaveOccurred())
			Expect(recordID).NotTo(BeEmpty())

			// Should be visible as 'started'
			results, err := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].Status).To(Equal("started"))
			Expect(results[0].ToolName).To(Equal("http_get"))
			Expect(results[0].InputParams).To(Equal(`{"url":"https://example.com"}`))
			Expect(results[0].RawData).To(BeEmpty())

			// Phase 2: complete
			err = bundle.Sessions.CompleteToolCall(recordID, `{"status":200}`)
			Expect(err).NotTo(HaveOccurred())

			results, err = bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].Status).To(Equal("completed"))
			Expect(results[0].RawData).To(Equal(`{"status":200}`))
		})

		It("leaves status as started if never completed (crash simulation)", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "test", "claude-3", nil)

			_, err := bundle.Sessions.StartToolCall(taskID, sessionID, "tc-crash", "shell_exec", `{"cmd":"sleep 100"}`)
			Expect(err).NotTo(HaveOccurred())

			// Never call CompleteToolCall — simulates crash
			results, _ := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Status).To(Equal("started"))
			Expect(results[0].ToolName).To(Equal("shell_exec"))
		})

		It("StoreToolResult writes completed status directly", func() {
			_, taskID := seedMissionAndTask(bundle)
			sessionID, _ := bundle.Sessions.CreateSession(taskID, "agent", "test", "claude-3", nil)

			now := time.Now()
			err := bundle.Sessions.StoreToolResult(taskID, sessionID, "tc-legacy", "http_post", "{}", `{"ok":true}`, now, now)
			Expect(err).NotTo(HaveOccurred())

			results, _ := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Status).To(Equal("completed"))
		})
	})

	// =========================================================================
	Describe("CAS methods", func() {
		It("UpdateTaskStatusCAS succeeds when status matches", func() {
			_, taskID := seedMissionAndTask(bundle)

			ok, err := bundle.Missions.UpdateTaskStatusCAS(taskID, "pending", "running", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			task, _ := bundle.Missions.GetTask(taskID)
			Expect(task.Status).To(Equal("running"))
		})

		It("UpdateTaskStatusCAS fails when status doesn't match", func() {
			_, taskID := seedMissionAndTask(bundle)

			ok, err := bundle.Missions.UpdateTaskStatusCAS(taskID, "running", "completed", nil, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())

			// Status should remain pending
			task, _ := bundle.Missions.GetTask(taskID)
			Expect(task.Status).To(Equal("pending"))
		})

		It("UpdateMissionStatusCAS succeeds when status matches", func() {
			missionID, _ := seedMissionAndTask(bundle)

			ok, err := bundle.Missions.UpdateMissionStatusCAS(missionID, "running", "completed")
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())

			m, _ := bundle.Missions.GetMission(missionID)
			Expect(m.Status).To(Equal("completed"))
		})

		It("UpdateMissionStatusCAS fails when status doesn't match", func() {
			missionID, _ := seedMissionAndTask(bundle)

			ok, err := bundle.Missions.UpdateMissionStatusCAS(missionID, "stopped", "running")
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeFalse())
		})
	})
})

var _ = Describe("SQLite DatasetStore", func() {
	var (
		bundle    *store.Bundle
		cleanup   func()
		missionID string
	)

	BeforeEach(func() {
		bundle, cleanup = newSQLiteBundle()
		var err error
		missionID, err = bundle.Missions.CreateMission("ds-mission", "{}", "{}")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		cleanup()
	})

	// =========================================================================
	// 21. CreateDataset
	// =========================================================================
	Describe("CreateDataset", func() {
		It("returns a valid ID and stores the dataset", func() {
			id, err := bundle.Datasets.CreateDataset(missionID, "urls", "A list of URLs to scrape")
			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())

			datasets, err := bundle.Datasets.ListDatasets(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(datasets).To(HaveLen(1))
			Expect(datasets[0].ID).To(Equal(id))
			Expect(datasets[0].Name).To(Equal("urls"))
			Expect(datasets[0].Description).To(Equal("A list of URLs to scrape"))
		})
	})

	// =========================================================================
	// 22. AddItems
	// =========================================================================
	Describe("AddItems", func() {
		It("appends items to an existing dataset", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "items", "")

			items1 := []cty.Value{
				cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("alpha")}),
				cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("beta")}),
			}
			Expect(bundle.Datasets.AddItems(dsID, items1)).To(Succeed())

			items2 := []cty.Value{
				cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("gamma")}),
			}
			Expect(bundle.Datasets.AddItems(dsID, items2)).To(Succeed())

			count, err := bundle.Datasets.GetItemCount(dsID)
			Expect(err).NotTo(HaveOccurred())
			Expect(count).To(Equal(3))

			all, err := bundle.Datasets.GetItems(dsID, 0, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(HaveLen(3))
		})
	})

	// =========================================================================
	// 23. SetItems
	// =========================================================================
	Describe("SetItems", func() {
		It("replaces all items in a dataset", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "replaceable", "")

			original := []cty.Value{
				cty.StringVal("old-1"),
				cty.StringVal("old-2"),
				cty.StringVal("old-3"),
			}
			Expect(bundle.Datasets.AddItems(dsID, original)).To(Succeed())

			replacement := []cty.Value{
				cty.StringVal("new-1"),
				cty.StringVal("new-2"),
			}
			Expect(bundle.Datasets.SetItems(dsID, replacement)).To(Succeed())

			count, _ := bundle.Datasets.GetItemCount(dsID)
			Expect(count).To(Equal(2))

			items, _ := bundle.Datasets.GetItems(dsID, 0, 100)
			Expect(items).To(HaveLen(2))
		})
	})

	// =========================================================================
	// 24. GetItems — paginated retrieval
	// =========================================================================
	Describe("GetItems", func() {
		It("returns paginated items in order", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "paged", "")

			items := make([]cty.Value, 10)
			for i := 0; i < 10; i++ {
				items[i] = cty.StringVal(fmt.Sprintf("item-%d", i))
			}
			Expect(bundle.Datasets.AddItems(dsID, items)).To(Succeed())

			// First page
			page1, err := bundle.Datasets.GetItems(dsID, 0, 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(page1).To(HaveLen(3))

			// Second page
			page2, err := bundle.Datasets.GetItems(dsID, 3, 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(page2).To(HaveLen(3))

			// Beyond end
			page4, err := bundle.Datasets.GetItems(dsID, 10, 5)
			Expect(err).NotTo(HaveOccurred())
			Expect(page4).To(BeEmpty())
		})
	})

	// =========================================================================
	// 25. GetItemCount
	// =========================================================================
	Describe("GetItemCount", func() {
		It("returns accurate count after adds and sets", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "counted", "")

			count, _ := bundle.Datasets.GetItemCount(dsID)
			Expect(count).To(Equal(0))

			items := []cty.Value{cty.StringVal("a"), cty.StringVal("b")}
			bundle.Datasets.AddItems(dsID, items)

			count, _ = bundle.Datasets.GetItemCount(dsID)
			Expect(count).To(Equal(2))

			bundle.Datasets.AddItems(dsID, []cty.Value{cty.StringVal("c")})
			count, _ = bundle.Datasets.GetItemCount(dsID)
			Expect(count).To(Equal(3))
		})
	})

	// =========================================================================
	// 26. GetSample
	// =========================================================================
	Describe("GetSample", func() {
		It("returns the requested number of items", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "sampled", "")

			items := make([]cty.Value, 20)
			for i := 0; i < 20; i++ {
				items[i] = cty.StringVal(fmt.Sprintf("item-%d", i))
			}
			bundle.Datasets.AddItems(dsID, items)

			sample, err := bundle.Datasets.GetSample(dsID, 5)
			Expect(err).NotTo(HaveOccurred())
			Expect(sample).To(HaveLen(5))
		})

		It("returns all items when count exceeds total", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "small", "")
			bundle.Datasets.AddItems(dsID, []cty.Value{cty.StringVal("only")})

			sample, err := bundle.Datasets.GetSample(dsID, 100)
			Expect(err).NotTo(HaveOccurred())
			Expect(sample).To(HaveLen(1))
		})

		It("returns empty slice for empty dataset", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "empty", "")

			sample, err := bundle.Datasets.GetSample(dsID, 5)
			Expect(err).NotTo(HaveOccurred())
			Expect(sample).To(BeEmpty())
		})
	})

	// =========================================================================
	// 27. GetDatasetByName
	// =========================================================================
	Describe("GetDatasetByName", func() {
		It("looks up a dataset by mission ID and name", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "lookup-me", "desc")

			foundID, err := bundle.Datasets.GetDatasetByName(missionID, "lookup-me")
			Expect(err).NotTo(HaveOccurred())
			Expect(foundID).To(Equal(dsID))
		})

		It("returns an error for nonexistent name", func() {
			_, err := bundle.Datasets.GetDatasetByName(missionID, "nope")
			Expect(err).To(HaveOccurred())
		})
	})

	// =========================================================================
	// 28. ListDatasets
	// =========================================================================
	Describe("ListDatasets", func() {
		It("lists all datasets for a mission", func() {
			bundle.Datasets.CreateDataset(missionID, "ds-a", "first")
			bundle.Datasets.CreateDataset(missionID, "ds-b", "second")

			// Create a dataset in a different mission to confirm isolation
			otherMission, _ := bundle.Missions.CreateMission("other", "{}", "{}")
			bundle.Datasets.CreateDataset(otherMission, "ds-c", "other mission")

			datasets, err := bundle.Datasets.ListDatasets(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(datasets).To(HaveLen(2))

			names := []string{datasets[0].Name, datasets[1].Name}
			Expect(names).To(ContainElements("ds-a", "ds-b"))
		})

		It("returns empty slice when mission has no datasets", func() {
			datasets, err := bundle.Datasets.ListDatasets(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(datasets).To(BeEmpty())
		})

		It("includes item count in listing", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "with-items", "")
			bundle.Datasets.AddItems(dsID, []cty.Value{cty.StringVal("a"), cty.StringVal("b")})

			datasets, _ := bundle.Datasets.ListDatasets(missionID)
			Expect(datasets).To(HaveLen(1))
			Expect(datasets[0].ItemCount).To(Equal(2))
		})
	})

	// =========================================================================
	// 29. LockDataset / IsDatasetLocked
	// =========================================================================
	Describe("LockDataset and IsDatasetLocked", func() {
		It("starts unlocked and can be locked", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "lockable", "")

			locked, err := bundle.Datasets.IsDatasetLocked(dsID)
			Expect(err).NotTo(HaveOccurred())
			Expect(locked).To(BeFalse())

			Expect(bundle.Datasets.LockDataset(dsID)).To(Succeed())

			locked, err = bundle.Datasets.IsDatasetLocked(dsID)
			Expect(err).NotTo(HaveOccurred())
			Expect(locked).To(BeTrue())
		})

		It("locking twice is idempotent", func() {
			dsID, _ := bundle.Datasets.CreateDataset(missionID, "double-lock", "")

			Expect(bundle.Datasets.LockDataset(dsID)).To(Succeed())
			Expect(bundle.Datasets.LockDataset(dsID)).To(Succeed())

			locked, _ := bundle.Datasets.IsDatasetLocked(dsID)
			Expect(locked).To(BeTrue())
		})
	})
})
