package store_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/zclconf/go-cty/cty"

	"squadron/store"
)

var _ = Describe("PurgeExpiredMissions", func() {
	runPurgeTests := func(newBundle func() (*store.Bundle, func())) {
		var (
			bundle  *store.Bundle
			cleanup func()
		)

		BeforeEach(func() {
			bundle, cleanup = newBundle()
		})

		AfterEach(func() {
			cleanup()
		})

		futureCutoff := func() time.Time {
			return time.Now().UTC().Add(time.Hour)
		}

		seedTree := func(name string, complete bool) (missionID, taskID, sessionID, toolCallID string) {
			var err error
			missionID, err = bundle.Missions.CreateMission(name, `{"k":"v"}`, `{}`)
			Expect(err).NotTo(HaveOccurred())
			taskID, err = bundle.Missions.CreateTask(missionID, "task-a", `{}`)
			Expect(err).NotTo(HaveOccurred())
			sessionID, err = bundle.Sessions.CreateSession(taskID, "agent", "worker", "gpt-4", nil)
			Expect(err).NotTo(HaveOccurred())

			now := time.Now().UTC()
			Expect(bundle.Sessions.AppendStructuredMessage(sessionID, "assistant", "hello", []store.MessagePart{
				{Type: "text", Text: "hello"},
			}, now, now)).To(Succeed())
			Expect(bundle.Sessions.StoreToolResult(taskID, sessionID, "tc-"+name, "http_get", `{}`, `{"ok":true}`, now, now)).To(Succeed())
			Expect(bundle.Missions.StoreTaskOutput(taskID, nil, nil, nil, `{"out":true}`)).To(Succeed())
			Expect(bundle.Missions.StoreTaskInput(taskID, nil, "do the thing")).To(Succeed())
			Expect(bundle.Missions.SetSubtasks(taskID, sessionID, nil, []string{"step 1"})).To(Succeed())

			dsID, err := bundle.Datasets.CreateDataset(missionID, "items", "desc")
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Datasets.AddItems(dsID, []cty.Value{cty.StringVal("one")})).To(Succeed())

			Expect(bundle.Missions.StoreRouteDecision(missionID, "task-a", "task-b", "always")).To(Succeed())
			Expect(bundle.Events.StoreEvents([]store.MissionEvent{{
				ID:        "evt-" + name,
				MissionID: missionID,
				TaskID:    &taskID,
				EventType: "mission_started",
				DataJSON:  `{}`,
				CreatedAt: now,
			}})).To(Succeed())
			Expect(bundle.Costs.StoreTurnCost(store.TurnCostRecord{
				MissionID:   missionID,
				TaskID:      taskID,
				SessionID:   sessionID,
				MissionName: name,
				TaskName:    "task-a",
				Entity:      "agent",
				Model:       "gpt-4",
			})).To(Succeed())

			toolCallID = "ask-" + name
			Expect(bundle.HumanInputs.CreateRequest(&store.HumanInputRequestRecord{
				MissionID:  missionID,
				TaskID:     taskID,
				ToolCallID: toolCallID,
				Question:   "confirm?",
			})).To(Succeed())

			if complete {
				Expect(bundle.Missions.UpdateMissionStatus(missionID, "completed")).To(Succeed())
			}
			return
		}

		It("returns 0 when nothing is expired", func() {
			_, _, _, _ = seedTree("fresh", true)
			purged, err := bundle.Missions.PurgeExpiredMissions(time.Now().UTC().Add(-24 * time.Hour))
			Expect(err).NotTo(HaveOccurred())
			Expect(purged).To(Equal(0))

			missions, total, err := bundle.Missions.ListMissions(10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(1))
			Expect(missions).To(HaveLen(1))
		})

		It("deletes an expired completed mission and every related row", func() {
			missionID, taskID, sessionID, toolCallID := seedTree("old-run", true)

			purged, err := bundle.Missions.PurgeExpiredMissions(futureCutoff())
			Expect(err).NotTo(HaveOccurred())
			Expect(purged).To(Equal(1))

			_, err = bundle.Missions.GetMission(missionID)
			Expect(err).To(HaveOccurred())

			tasks, err := bundle.Missions.GetTasksByMission(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(tasks).To(BeEmpty())

			sessions, err := bundle.Sessions.GetSessionsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(sessions).To(BeEmpty())

			_, err = bundle.Sessions.GetMessages(sessionID)
			Expect(err).NotTo(HaveOccurred())

			results, err := bundle.Sessions.GetToolResultsByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(BeEmpty())

			outputs, err := bundle.Missions.GetTaskOutputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(outputs).To(BeEmpty())

			inputs, err := bundle.Missions.GetTaskInputs(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(inputs).To(BeEmpty())

			subtasks, err := bundle.Missions.GetSubtasksByTask(taskID)
			Expect(err).NotTo(HaveOccurred())
			Expect(subtasks).To(BeEmpty())

			datasets, err := bundle.Datasets.ListDatasets(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(datasets).To(BeEmpty())

			routes, err := bundle.Missions.GetRouteDecisions(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(routes).To(BeEmpty())

			events, err := bundle.Events.GetEventsByMission(missionID, 100, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(events).To(BeEmpty())

			costs, err := bundle.Costs.GetCostsByMission(missionID)
			Expect(err).NotTo(HaveOccurred())
			Expect(costs).To(BeEmpty())

			_, err = bundle.HumanInputs.GetByToolCallID(toolCallID)
			Expect(err).To(HaveOccurred())
		})

		It("leaves running missions even when they would otherwise be expired", func() {
			runningID, _, _, _ := seedTree("still-going", false)
			expiredID, _, _, _ := seedTree("done", true)

			purged, err := bundle.Missions.PurgeExpiredMissions(futureCutoff())
			Expect(err).NotTo(HaveOccurred())
			Expect(purged).To(Equal(1))

			_, err = bundle.Missions.GetMission(runningID)
			Expect(err).NotTo(HaveOccurred())
			_, err = bundle.Missions.GetMission(expiredID)
			Expect(err).To(HaveOccurred())
		})

		It("leaves chat sessions that are not tied to a mission task", func() {
			chatID, err := bundle.Sessions.CreateChatSession("my-agent", "gpt-4")
			Expect(err).NotTo(HaveOccurred())
			_, _, _, _ = seedTree("to-purge", true)

			purged, err := bundle.Missions.PurgeExpiredMissions(futureCutoff())
			Expect(err).NotTo(HaveOccurred())
			Expect(purged).To(Equal(1))

			sessions, total, err := bundle.Sessions.ListChatSessions("my-agent", 10, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(1))
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal(chatID))
		})

		It("leaves a recent completed mission when the cutoff is in the past", func() {
			missionID, _, _, _ := seedTree("recent", true)

			purged, err := bundle.Missions.PurgeExpiredMissions(time.Now().UTC().Add(-24 * time.Hour))
			Expect(err).NotTo(HaveOccurred())
			Expect(purged).To(Equal(0))

			_, err = bundle.Missions.GetMission(missionID)
			Expect(err).NotTo(HaveOccurred())
		})

		It("purges stopped missions that have no finished_at, using started_at", func() {
			missionID, err := bundle.Missions.CreateMission("stopped-run", "{}", "{}")
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Missions.UpdateMissionStatus(missionID, "stopped")).To(Succeed())

			purged, err := bundle.Missions.PurgeExpiredMissions(futureCutoff())
			Expect(err).NotTo(HaveOccurred())
			Expect(purged).To(Equal(1))

			_, err = bundle.Missions.GetMission(missionID)
			Expect(err).To(HaveOccurred())
		})
	}

	Context("SQLite backend", func() {
		runPurgeTests(func() (*store.Bundle, func()) {
			dir, err := os.MkdirTemp("", "store-purge-*")
			Expect(err).NotTo(HaveOccurred())

			dbPath := filepath.Join(dir, "test.db")
			bundle, err := store.NewSQLiteBundle(dbPath)
			Expect(err).NotTo(HaveOccurred())

			return bundle, func() {
				bundle.Close()
				os.RemoveAll(dir)
			}
		})
	})

	Context("Postgres backend", func() {
		connStr := os.Getenv("SQUADRON_TEST_POSTGRES_URL")
		if connStr == "" {
			return
		}

		runPurgeTests(func() (*store.Bundle, func()) {
			bundle, err := store.NewPostgresBundle(connStr)
			Expect(err).NotTo(HaveOccurred())

			return bundle, func() {
				db, _ := sql.Open("pgx", connStr)
				_, _ = db.Exec(`
					DELETE FROM session_message_parts;
					DELETE FROM session_messages;
					DELETE FROM tool_results;
					DELETE FROM turn_costs;
					DELETE FROM mission_events;
					DELETE FROM mission_task_subtasks;
					DELETE FROM task_inputs;
					DELETE FROM task_outputs;
					DELETE FROM dataset_items;
					DELETE FROM datasets;
					DELETE FROM route_decisions;
					DELETE FROM human_input_requests;
					DELETE FROM sessions;
					DELETE FROM mission_tasks;
					DELETE FROM missions;
				`)
				db.Close()
				bundle.Close()
			}
		})
	})
})
