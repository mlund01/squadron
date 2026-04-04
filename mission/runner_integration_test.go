package mission

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/zclconf/go-cty/cty"

	"squadron/config"
	"squadron/llm"
)

var _ = Describe("Runner Integration", func() {

	// shared helper: run a mission with a mock provider
	runMission := func(cfg *config.Config, missionName string, provider *mockProvider, inputs map[string]string) (*mockMissionStreamer, error) {
		streamer := newMockMissionStreamer()
		factory := func() llm.Provider { return provider }

		runner, err := NewRunner(cfg, "", missionName, inputs, WithProviderFactory(factory))
		if err != nil {
			return streamer, err
		}
		defer runner.CloseStores()

		err = runner.Run(context.Background(), streamer)
		return streamer, err
	}

	// -----------------------------------------------------------------------
	// a) Single task, single agent
	// -----------------------------------------------------------------------
	Describe("single task with one agent", func() {
		It("completes the mission end-to-end", func() {
			mission := testMission("test_single", []config.Task{
				testTask("do_work", "Do something useful"),
			})
			cfg := buildTestConfig(mission, testAgent("worker"))

			// Commander: call agent → observe result → task_complete
			// Agent: immediately returns an answer
			provider := newMockProvider(
				// Turn 1 (commander): call the agent
				mockResponse{Content: cmdCallAgent("worker", "Do the work")},
				// Turn 1 (agent): answer
				mockResponse{Content: agentAnswer("Work is done.")},
				// Turn 2 (commander): complete
				mockResponse{Content: cmdTaskComplete()},
			)

			streamer, err := runMission(cfg, "test_single", provider, nil)
			Expect(err).NotTo(HaveOccurred())

			events := streamer.getEvents()
			Expect(streamer.hasEvent("mission_started")).To(BeTrue())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())
			Expect(streamer.hasEvent("task_started")).To(BeTrue())
			Expect(streamer.hasEvent("task_completed")).To(BeTrue())
			Expect(streamer.hasEvent("agent_started")).To(BeTrue())
			Expect(streamer.hasEvent("agent_completed")).To(BeTrue())

			// Verify task name in events
			for _, e := range events {
				if e.Type == "task_started" {
					Expect(e.Data["task"]).To(Equal("do_work"))
				}
			}
		})
	})

	// -----------------------------------------------------------------------
	// b) Two tasks with depends_on
	// -----------------------------------------------------------------------
	Describe("two tasks with depends_on", func() {
		It("runs tasks in order with context passing", func() {
			fetchTask := testTask("fetch", "Fetch data from API")
			processTask := testTask("process", "Process the fetched data")
			processTask.DependsOn = []string{"fetch"}

			mission := testMission("test_deps", []config.Task{fetchTask, processTask})
			cfg := buildTestConfig(mission, testAgent("worker"))

			provider := newMockProvider(
				// Task "fetch" — commander calls agent, agent answers, commander completes
				mockResponse{Content: cmdCallAgent("worker", "Fetch from API")},
				mockResponse{Content: agentAnswer("Fetched 42 records.")},
				mockResponse{Content: cmdTaskComplete()},
				// Task "process" — same flow
				mockResponse{Content: cmdCallAgent("worker", "Process records")},
				mockResponse{Content: agentAnswer("Processed all records.")},
				mockResponse{Content: cmdTaskComplete()},
			)

			streamer, err := runMission(cfg, "test_deps", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())

			// Both tasks completed
			Expect(streamer.eventCount("task_completed")).To(Equal(2))

			// Verify ordering: fetch started before process
			events := streamer.getEvents()
			fetchStartIdx := -1
			processStartIdx := -1
			for i, e := range events {
				if e.Type == "task_started" && e.Data["task"] == "fetch" {
					fetchStartIdx = i
				}
				if e.Type == "task_started" && e.Data["task"] == "process" {
					processStartIdx = i
				}
			}
			Expect(fetchStartIdx).To(BeNumerically("<", processStartIdx))
		})
	})

	// -----------------------------------------------------------------------
	// c) Structured output via submit_output
	// -----------------------------------------------------------------------
	Describe("task with structured output", func() {
		It("persists output via submit_output and task_complete", func() {
			task := testTask("extract", "Extract key data")
			task.Output = &config.OutputSchema{
				Fields: []config.OutputField{
					{Name: "title", Type: "string", Description: "The title", Required: true},
					{Name: "count", Type: "integer", Description: "Item count", Required: true},
				},
			}

			mission := testMission("test_output", []config.Task{task})
			cfg := buildTestConfig(mission, testAgent("worker"))

			provider := newMockProvider(
				// Commander: call agent
				mockResponse{Content: cmdCallAgent("worker", "Extract the data")},
				// Agent: answer
				mockResponse{Content: agentAnswer("Title is Test, count is 5.")},
				// Commander: submit output
				mockResponse{Content: cmdSubmitOutput(map[string]interface{}{
					"title": "Test",
					"count": 5,
				})},
				// Commander: complete
				mockResponse{Content: cmdTaskComplete()},
			)

			streamer, err := runMission(cfg, "test_output", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())
			Expect(streamer.hasEvent("task_completed")).To(BeTrue())
		})
	})

	// -----------------------------------------------------------------------
	// d) Router-based branching
	// -----------------------------------------------------------------------
	Describe("router-based branching", func() {
		It("activates the chosen route target", func() {
			classifyTask := testTask("classify", "Classify the input")
			classifyTask.Router = &config.TaskRouter{
				Routes: []config.TaskRoute{
					{Target: "handle_a", Condition: "Input is type A"},
					{Target: "handle_b", Condition: "Input is type B"},
				},
			}

			mission := testMission("test_router", []config.Task{
				classifyTask,
				testTask("handle_a", "Handle type A"),
				testTask("handle_b", "Handle type B"),
			})
			cfg := buildTestConfig(mission, testAgent("worker"))

			provider := newMockProvider(
				// Task "classify": agent work → task_complete (enters routing) → choose route
				mockResponse{Content: cmdCallAgent("worker", "Classify input")},
				mockResponse{Content: agentAnswer("This is type B.")},
				mockResponse{Content: cmdTaskComplete()}, // triggers routing phase
				mockResponse{Content: cmdTaskCompleteRoute("handle_b")}, // choose route
				// Task "handle_b": execute
				mockResponse{Content: cmdCallAgent("worker", "Handle B")},
				mockResponse{Content: agentAnswer("B handled.")},
				mockResponse{Content: cmdTaskComplete()},
			)

			streamer, err := runMission(cfg, "test_router", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())

			// Route was chosen
			Expect(streamer.hasEvent("route_chosen")).To(BeTrue())
			events := streamer.getEvents()
			for _, e := range events {
				if e.Type == "route_chosen" && e.Data["target"] != "none" {
					Expect(e.Data["target"]).To(Equal("handle_b"))
				}
			}

			// handle_a should NOT have started
			for _, e := range events {
				if e.Type == "task_started" {
					Expect(e.Data["task"]).NotTo(Equal("handle_a"))
				}
			}
		})
	})

	// -----------------------------------------------------------------------
	// e) send_to fan-out
	// -----------------------------------------------------------------------
	Describe("send_to fan-out", func() {
		It("activates both targets after source completes", func() {
			triggerTask := testTask("trigger", "Trigger downstream work")
			triggerTask.SendTo = []string{"branch_a", "branch_b"}

			mission := testMission("test_sendto", []config.Task{
				triggerTask,
				testTask("branch_a", "Do branch A work"),
				testTask("branch_b", "Do branch B work"),
			})
			cfg := buildTestConfig(mission, testAgent("worker"))

			provider := newMockProvider(
				// Task "trigger"
				mockResponse{Content: cmdCallAgent("worker", "Do trigger work")},
				mockResponse{Content: agentAnswer("Triggered.")},
				mockResponse{Content: cmdTaskComplete()},
				// Task "branch_a"
				mockResponse{Content: cmdCallAgent("worker", "Branch A work")},
				mockResponse{Content: agentAnswer("A done.")},
				mockResponse{Content: cmdTaskComplete()},
				// Task "branch_b"
				mockResponse{Content: cmdCallAgent("worker", "Branch B work")},
				mockResponse{Content: agentAnswer("B done.")},
				mockResponse{Content: cmdTaskComplete()},
			)

			streamer, err := runMission(cfg, "test_sendto", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())

			// All three tasks completed
			completedTasks := map[string]bool{}
			for _, e := range streamer.getEvents() {
				if e.Type == "task_completed" {
					completedTasks[e.Data["task"]] = true
				}
			}
			Expect(completedTasks).To(HaveKey("trigger"))
			Expect(completedTasks).To(HaveKey("branch_a"))
			Expect(completedTasks).To(HaveKey("branch_b"))
		})
	})

	// -----------------------------------------------------------------------
	// f) Task failure handling
	// -----------------------------------------------------------------------
	Describe("task failure", func() {
		It("propagates failure and marks mission as failed", func() {
			mission := testMission("test_fail", []config.Task{
				testTask("broken", "This will fail"),
			})
			cfg := buildTestConfig(mission, testAgent("worker"))

			provider := newMockProvider(
				// Commander: call agent
				mockResponse{Content: cmdCallAgent("worker", "Try something")},
				// Agent: answer
				mockResponse{Content: agentAnswer("I tried but it didn't work.")},
				// Commander: fail the task
				mockResponse{Content: cmdTaskCompleteFail("Could not complete the work")},
			)

			streamer, err := runMission(cfg, "test_fail", provider, nil)
			Expect(err).To(HaveOccurred())
			Expect(streamer.hasEvent("task_failed")).To(BeTrue())
		})
	})

	// -----------------------------------------------------------------------
	// g) Parallel iterated task
	// -----------------------------------------------------------------------
	Describe("parallel iterated task", func() {
		It("runs iterations in parallel and collects outputs", func() {
			task := testTask("process", "Process item")
			task.Iterator = &config.TaskIterator{
				Dataset:  "items",
				Parallel: true,
			}
			task.Output = &config.OutputSchema{
				Fields: []config.OutputField{
					{Name: "result", Type: "string", Description: "Processing result", Required: true},
				},
			}

			mission := testMission("test_parallel_iter", []config.Task{task})
			mission.Datasets = []config.Dataset{
				{
					Name:        "items",
					Description: "Test items",
					Items: []cty.Value{
						cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("alpha")}),
						cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("beta")}),
						cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("gamma")}),
					},
				},
			}
			cfg := buildTestConfig(mission, testAgent("worker"))

			// Parallel iterations interleave unpredictably.
			// Use matchers to route responses to the correct session type.
			isAgentSession := func(req *llm.ChatRequest) bool {
				for _, m := range req.Messages {
					if m.Role == llm.RoleSystem && strings.Contains(m.Content, "Test agent") {
						return true
					}
				}
				return false
			}
			isObservation := func(substr string) func(*llm.ChatRequest) bool {
				return func(req *llm.ChatRequest) bool {
					if isAgentSession(req) {
						return false
					}
					last := ""
					for _, m := range req.Messages {
						if m.Role == llm.RoleUser {
							last = m.Content
						}
					}
					return strings.Contains(last, substr)
				}
			}
			provider := newMockProvider()
			for i := 0; i < 3; i++ {
				provider.addResponses(
					// Commander turn 1: call agent (matches any commander first turn)
					mockResponse{Content: cmdCallAgent("worker", "Process this item")},
					// Agent: answer (matches agent sessions)
					mockResponse{Content: agentAnswer("Item processed."), Match: isAgentSession},
					// Commander turn 2: submit output (matches when observation contains agent answer)
					mockResponse{Content: cmdSubmitOutput(map[string]interface{}{"result": fmt.Sprintf("done_%d", i)}), Match: isObservation("Item processed.")},
					// Commander turn 3: complete (matches when observation contains submit ok)
					mockResponse{Content: cmdTaskComplete(), Match: isObservation(`"status": "ok"`)},
				)
			}

			streamer, err := runMission(cfg, "test_parallel_iter", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())
			Expect(streamer.hasEvent("task_iteration_started")).To(BeTrue())
			Expect(streamer.eventCount("iteration_completed")).To(Equal(3))
		})
	})

	// -----------------------------------------------------------------------
	// h) Sequential iterated task
	// -----------------------------------------------------------------------
	Describe("sequential iterated task", func() {
		It("processes items one by one using dataset_next/submit_output", func() {
			task := testTask("seq_process", "Process items sequentially")
			task.Iterator = &config.TaskIterator{
				Dataset:  "items",
				Parallel: false,
			}
			task.Output = &config.OutputSchema{
				Fields: []config.OutputField{
					{Name: "processed", Type: "string", Description: "Result", Required: true},
				},
			}

			mission := testMission("test_seq_iter", []config.Task{task})
			mission.Datasets = []config.Dataset{
				{
					Name:        "items",
					Description: "Sequential items",
					Items: []cty.Value{
						cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("first")}),
						cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("second")}),
						cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("third")}),
					},
				},
			}
			cfg := buildTestConfig(mission, testAgent("worker"))

			// Sequential iteration uses a single commander with dataset_next/submit_output loop
			provider := newMockProvider(
				// Item 1: dataset_next → call_agent → submit_output
				mockResponse{Content: cmdDatasetNext()},
				mockResponse{Content: cmdCallAgent("worker", "Process first")},
				mockResponse{Content: agentAnswer("First done.")},
				mockResponse{Content: cmdSubmitOutput(map[string]interface{}{"processed": "first_done"})},
				// Item 2
				mockResponse{Content: cmdDatasetNext()},
				mockResponse{Content: cmdCallAgent("worker", "Process second")},
				mockResponse{Content: agentAnswer("Second done.")},
				mockResponse{Content: cmdSubmitOutput(map[string]interface{}{"processed": "second_done"})},
				// Item 3
				mockResponse{Content: cmdDatasetNext()},
				mockResponse{Content: cmdCallAgent("worker", "Process third")},
				mockResponse{Content: agentAnswer("Third done.")},
				mockResponse{Content: cmdSubmitOutput(map[string]interface{}{"processed": "third_done"})},
				// Final dataset_next returns exhausted, then task_complete
				mockResponse{Content: cmdDatasetNext()},
				mockResponse{Content: cmdTaskComplete()},
			)

			streamer, err := runMission(cfg, "test_seq_iter", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())
		})
	})

	// -----------------------------------------------------------------------
	// i) Dataset creation and consumption
	// -----------------------------------------------------------------------
	Describe("dataset creation and consumption", func() {
		It("creates a dataset in task A and iterates over it in task B", func() {
			createTask := testTask("create_data", "Create the dataset")

			consumeTask := testTask("consume_data", "Process item")
			consumeTask.DependsOn = []string{"create_data"}
			consumeTask.Iterator = &config.TaskIterator{
				Dataset:  "dynamic_items",
				Parallel: true,
			}
			consumeTask.Output = &config.OutputSchema{
				Fields: []config.OutputField{
					{Name: "status", Type: "string", Description: "Status", Required: true},
				},
			}

			mission := testMission("test_dataset", []config.Task{createTask, consumeTask})
			mission.Datasets = []config.Dataset{
				{
					Name:        "dynamic_items",
					Description: "Dynamically created items",
				},
			}
			cfg := buildTestConfig(mission, testAgent("worker"))

			isAgentSession := func(req *llm.ChatRequest) bool {
				for _, m := range req.Messages {
					if m.Role == llm.RoleSystem && strings.Contains(m.Content, "Test agent") {
						return true
					}
				}
				return false
			}
			isObservation := func(substr string) func(*llm.ChatRequest) bool {
				return func(req *llm.ChatRequest) bool {
					if isAgentSession(req) {
						return false
					}
					last := ""
					for _, m := range req.Messages {
						if m.Role == llm.RoleUser {
							last = m.Content
						}
					}
					return strings.Contains(last, substr)
				}
			}

			provider := newMockProvider(
				// Task "create_data": commander uses set_dataset to populate items
				mockResponse{Content: cmdSetDataset("dynamic_items", []map[string]interface{}{
					{"label": "x"},
					{"label": "y"},
				})},
				mockResponse{Content: cmdTaskComplete()},
				// Dependency query: "create_data" commander clone answers context question
				mockResponse{Content: "I created a dataset with items x and y.", Match: matchLastUserContains("dependent task needs your help")},
			)
			// Task "consume_data": 2 parallel iterations using matchers
			for i := 0; i < 2; i++ {
				provider.addResponses(
					mockResponse{Content: cmdCallAgent("worker", "Process item")},
					mockResponse{Content: agentAnswer("Item done."), Match: isAgentSession},
					mockResponse{Content: cmdSubmitOutput(map[string]interface{}{"status": fmt.Sprintf("ok_%d", i)}), Match: isObservation("Item done.")},
					mockResponse{Content: cmdTaskComplete(), Match: isObservation(`"status": "ok"`)},
				)
			}

			streamer, err := runMission(cfg, "test_dataset", provider, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(streamer.hasEvent("mission_completed")).To(BeTrue())
			Expect(streamer.eventCount("iteration_completed")).To(Equal(2))
		})
	})
})
