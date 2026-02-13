package mission

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"squadron/agent"
	"squadron/aitools"
	"squadron/config"
	"squadron/store"
	"squadron/streamers"
)

// Runner executes a mission by orchestrating commanders for each task
type Runner struct {
	cfg        *config.Config
	configPath string
	mission   *config.Mission

	// Input values for objective resolution
	varsValues  map[string]cty.Value
	inputValues map[string]cty.Value

	// Resolved secrets for tool call injection
	secretValues map[string]string    // secret name → actual value
	secretInfos  []agent.SecretInfo   // name + description for prompts

	// Resolved datasets for iteration
	resolvedDatasets map[string][]cty.Value
	datasetIDs       map[string]string // dataset name → store ID
	missionID        string

	// Task state management
	mu                   sync.RWMutex
	taskResults          map[string]*TaskResult                   // Results from completed tasks
	taskCommanders      map[string]*agent.Commander             // Commanders for completed tasks (kept for agent inheritance)
	iterationCommanders map[string]map[int]*agent.Commander     // Commanders for iterated tasks: taskName -> index -> commander
	taskAgents           map[string]map[string]*agent.Agent       // Agents from each task (for inheritance)

	// Knowledge store for structured task outputs
	knowledgeStore *MemoryKnowledgeStore

	// Persistent store bundle (missions, sessions, datasets, questions)
	stores *store.Bundle

	// Debug logging
	debugLogger *DebugLogger

	// Shared store for ask_commander questions across iterations
	askCommanderStore *askCommanderStore
}

// askCommanderStore holds questions and answers shared across parallel iterations
type askCommanderStore struct {
	mu        sync.Mutex
	questions map[string][]*questionEntry // Map: targetTask -> []questionEntry
}

// questionEntry represents a question asked to a dependency commander
type questionEntry struct {
	Question string
	Answer   string
	Ready    chan struct{} // Closed when answer is ready
}

// RunnerOption is a functional option for configuring the Runner
type RunnerOption func(*Runner)

// WithDebugLogger sets the debug logger for the runner
func WithDebugLogger(logger *DebugLogger) RunnerOption {
	return func(r *Runner) {
		r.debugLogger = logger
	}
}

// TaskResult holds the outcome of a completed task
type TaskResult struct {
	TaskName string
	Summary  string
	Success  bool
	Error    error
}

// IterationResult holds the outcome of a single iteration
type IterationResult struct {
	Index     int
	ItemID    string
	Summary   string
	Output    map[string]any
	Learnings map[string]any
	Success   bool
	Error     error
}

// IteratedTaskResult holds the outcome of an iterated task
type IteratedTaskResult struct {
	TaskName       string
	WorkingSummary string
	Iterations     []IterationResult
	AllSuccess     bool
}

// NewRunner creates a new mission runner
func NewRunner(cfg *config.Config, configPath string, missionName string, inputs map[string]string, opts ...RunnerOption) (*Runner, error) {
	// Find the mission
	var mission *config.Mission
	for i := range cfg.Missions {
		if cfg.Missions[i].Name == missionName {
			mission = &cfg.Missions[i]
			break
		}
	}
	if mission == nil {
		return nil, fmt.Errorf("mission '%s' not found", missionName)
	}

	// Resolve and validate input values
	inputValues, err := mission.ResolveInputValues(inputs)
	if err != nil {
		return nil, fmt.Errorf("mission '%s': %w", missionName, err)
	}

	// Resolve datasets
	resolvedDatasets, err := resolveDatasets(mission, inputValues)
	if err != nil {
		return nil, fmt.Errorf("mission '%s': %w", missionName, err)
	}

	// Create store bundle
	stores, err := store.NewBundle(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("mission '%s': init stores: %w", missionName, err)
	}

	// Resolve secrets from inputs with secret=true
	secretValues := make(map[string]string)
	var secretInfos []agent.SecretInfo
	for _, input := range mission.Inputs {
		if !input.Secret {
			continue
		}
		if input.Value != nil && input.Value.Type() == cty.String {
			secretValues[input.Name] = input.Value.AsString()
		}
		secretInfos = append(secretInfos, agent.SecretInfo{
			Name:        input.Name,
			Description: input.Description,
		})
	}

	r := &Runner{
		cfg:                  cfg,
		configPath:           configPath,
		mission:             mission,
		varsValues:           cfg.ResolvedVars,
		inputValues:          inputValues,
		secretValues:         secretValues,
		secretInfos:          secretInfos,
		resolvedDatasets:     resolvedDatasets,
		datasetIDs:           make(map[string]string),
		taskResults:          make(map[string]*TaskResult),
		taskCommanders:      make(map[string]*agent.Commander),
		iterationCommanders: make(map[string]map[int]*agent.Commander),
		taskAgents:           make(map[string]map[string]*agent.Agent),
		knowledgeStore:       NewMemoryKnowledgeStore(),
		stores:               stores,
		askCommanderStore: &askCommanderStore{
			questions: make(map[string][]*questionEntry),
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// resolveDatasets resolves all datasets to their actual values
func resolveDatasets(mission *config.Mission, inputValues map[string]cty.Value) (map[string][]cty.Value, error) {
	resolved := make(map[string][]cty.Value)

	for _, ds := range mission.Datasets {
		var items []cty.Value

		// Check if bound to input
		if ds.BindTo != "" {
			inputVal, ok := inputValues[ds.BindTo]
			if !ok {
				return nil, fmt.Errorf("dataset '%s': bound input '%s' not found", ds.Name, ds.BindTo)
			}

			// Extract items from list/tuple
			if inputVal.Type().IsTupleType() || inputVal.Type().IsListType() {
				for it := inputVal.ElementIterator(); it.Next(); {
					_, v := it.Element()
					items = append(items, v)
				}
			} else {
				return nil, fmt.Errorf("dataset '%s': bound input '%s' is not a list", ds.Name, ds.BindTo)
			}
		} else if len(ds.Items) > 0 {
			// Use inline items
			items = ds.Items
		}

		// Validate items against schema if present
		for i, item := range items {
			if err := ds.ValidateItem(item); err != nil {
				return nil, fmt.Errorf("dataset '%s' item %d: %w", ds.Name, i, err)
			}
		}

		resolved[ds.Name] = items
	}

	return resolved, nil
}

// Run executes the mission
func (r *Runner) Run(ctx context.Context, streamer streamers.MissionHandler) error {
	defer r.stores.Close()

	// Create mission record in store
	inputsJSON, _ := json.Marshal(r.inputValues)
	configJSON, _ := json.Marshal(r.missionSnapshot())
	missionID, err := r.stores.Missions.CreateMission(r.mission.Name, string(inputsJSON), string(configJSON))
	if err != nil {
		return fmt.Errorf("create mission record: %w", err)
	}
	r.missionID = missionID

	// Persist datasets to store
	for _, ds := range r.mission.Datasets {
		dsID, err := r.stores.Datasets.CreateDataset(missionID, ds.Name, ds.Description)
		if err != nil {
			return fmt.Errorf("create dataset '%s': %w", ds.Name, err)
		}
		r.datasetIDs[ds.Name] = dsID

		// Persist any pre-populated items (inline or bound-to-input)
		if items, ok := r.resolvedDatasets[ds.Name]; ok && len(items) > 0 {
			if err := r.stores.Datasets.AddItems(dsID, items); err != nil {
				return fmt.Errorf("add items to dataset '%s': %w", ds.Name, err)
			}
		}
	}

	streamer.MissionStarted(r.mission.Name, len(r.mission.Tasks))

	// Log mission start event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventMissionStarted, map[string]any{
			"mission":   r.mission.Name,
			"task_count": len(r.mission.Tasks),
		})
	}

	// Get tasks in topological order
	sortedTasks := r.mission.TopologicalSort()

	// Track completed tasks and in-flight tasks
	completed := make(map[string]bool)
	var inFlightMu sync.Mutex
	inFlight := make(map[string]bool)

	// Create a wait group for all tasks
	var wg sync.WaitGroup

	// Error channel to collect errors from goroutines
	errChan := make(chan error, len(sortedTasks))

	// Process tasks, launching parallel tasks when their dependencies are met
	for len(completed) < len(sortedTasks) {
		select {
		case <-ctx.Done():
			r.stores.Missions.UpdateMissionStatus(missionID, "failed")
			return ctx.Err()
		default:
		}

		// Find tasks that are ready to run (all dependencies completed)
		var readyTasks []config.Task
		for _, task := range sortedTasks {
			if completed[task.Name] {
				continue
			}

			inFlightMu.Lock()
			isInFlight := inFlight[task.Name]
			inFlightMu.Unlock()

			if isInFlight {
				continue
			}

			// Check if all dependencies are completed
			depsReady := true
			for _, dep := range task.DependsOn {
				if !completed[dep] {
					depsReady = false
					break
				}
			}

			if depsReady {
				readyTasks = append(readyTasks, task)
			}
		}

		if len(readyTasks) == 0 {
			// Wait for any in-flight task to complete
			select {
			case err := <-errChan:
				if err != nil {
					r.stores.Missions.UpdateMissionStatus(missionID, "failed")
					return err
				}
			case <-ctx.Done():
				r.stores.Missions.UpdateMissionStatus(missionID, "failed")
				return ctx.Err()
			}
			continue
		}

		// Launch all ready tasks in parallel
		for _, task := range readyTasks {
			task := task // capture for goroutine

			inFlightMu.Lock()
			inFlight[task.Name] = true
			inFlightMu.Unlock()

			wg.Add(1)
			go func() {
				defer wg.Done()

				// Run the task (regular or iterated)
				// Each task queries its ancestors internally using the pull model
				var result *TaskResult
				var err error

				if task.Iterator != nil {
					result, err = r.runIteratedTask(ctx, task, missionID, streamer)
				} else {
					result, err = r.runTask(ctx, task, missionID, streamer)
				}

				if err != nil {
					errChan <- fmt.Errorf("task '%s' failed: %w", task.Name, err)
					return
				}

				// Store result
				r.mu.Lock()
				r.taskResults[task.Name] = result
				r.mu.Unlock()

				// Mark as completed
				inFlightMu.Lock()
				delete(inFlight, task.Name)
				inFlightMu.Unlock()

				completed[task.Name] = true
				errChan <- nil
			}()
		}
	}

	// Wait for all tasks to complete
	wg.Wait()

	// Drain any remaining errors
	close(errChan)
	for err := range errChan {
		if err != nil {
			r.stores.Missions.UpdateMissionStatus(missionID, "failed")
			return err
		}
	}

	// Cleanup iteration commanders now that all tasks are complete
	r.cleanupIterationCommanders()

	r.stores.Missions.UpdateMissionStatus(missionID, "completed")
	streamer.MissionCompleted(r.mission.Name)

	// Log mission completed event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventMissionCompleted, map[string]any{
			"mission": r.mission.Name,
		})
	}

	return nil
}

// cleanupIterationCommanders closes all stored iteration commanders
func (r *Runner) cleanupIterationCommanders() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for taskName, iterSups := range r.iterationCommanders {
		for idx, sup := range iterSups {
			if sup != nil {
				sup.Close()
			}
			delete(iterSups, idx)
		}
		delete(r.iterationCommanders, taskName)
	}
}

// runTask executes a single task with its commander
func (r *Runner) runTask(ctx context.Context, task config.Task, missionID string, streamer streamers.MissionHandler) (*TaskResult, error) {
	// Resolve the objective with vars and inputs
	objective, err := task.ResolvedObjective(r.varsValues, r.inputValues)
	if err != nil {
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Create task record in store with resolved objective
	taskConfigJSON, _ := json.Marshal(taskSnapshot(task, objective))
	taskID, _ := r.stores.Missions.CreateTask(missionID, task.Name, string(taskConfigJSON))
	r.stores.Missions.UpdateTaskStatus(taskID, "running", nil, nil)

	// Helper to update task status on completion/failure
	updateTaskDone := func(success bool, outputJSON *string, errMsg *string) {
		if success {
			r.stores.Missions.UpdateTaskStatus(taskID, "completed", outputJSON, nil)
		} else {
			r.stores.Missions.UpdateTaskStatus(taskID, "failed", nil, errMsg)
		}
	}

	// Query ancestors for targeted context based on our objective
	depSummaries, err := r.queryAncestorsForContext(ctx, task.Name, objective)
	if err != nil {
		errStr := err.Error()
		updateTaskDone(false, nil, &errStr)
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	streamer.TaskStarted(task.Name, objective)

	// Log task start event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventTaskStarted, map[string]any{
			"task":      task.Name,
			"objective": objective,
		})
	}

	// Get agents for this task (task-level or mission-level)
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.mission.Agents
	}

	// Build inherited agents from all dependency tasks in the lineage
	inheritedAgents := r.collectInheritedAgents(task.Name)

	// Collect dependency output schemas for the commander
	depOutputSchemas := r.collectDepOutputSchemas(task.Name)

	// Get task's own output schema if defined
	taskOutputSchema := r.getTaskOutputSchema(task)

	// Get debug file for commander if debug mode is enabled
	var debugFile string
	if r.debugLogger != nil {
		debugFile = r.debugLogger.GetMessageFile("commander", task.Name)
	}

	// Create commander for this task (non-iterated)
	sup, err := agent.NewCommander(ctx, agent.CommanderOptions{
		Config:           r.cfg,
		ConfigPath:       r.configPath,
		MissionName:     r.mission.Name,
		TaskName:         task.Name,
		Commander:  r.mission.Commander,
		AgentNames:       agents,
		DepSummaries:     depSummaries,
		DepOutputSchemas: depOutputSchemas,
		TaskOutputSchema: taskOutputSchema,
		InheritedAgents:  inheritedAgents,
		SecretInfos:      r.secretInfos,
		SecretValues:     r.secretValues,
		IsIteration:      false, // Not an iterated task
		DebugFile:        debugFile,
	})
	if err != nil {
		errStr := err.Error()
		updateTaskDone(false, nil, &errStr)
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName string) {
			streamer.AgentStarted(taskName, agentName)
			if r.debugLogger != nil {
				r.debugLogger.LogEvent(EventAgentStarted, map[string]any{
					"task":  taskName,
					"agent": agentName,
				})
			}
		},
		GetAgentHandler: func(taskName, agentName string) streamers.ChatHandler {
			return streamer.AgentHandler(taskName, agentName)
		},
		OnAgentComplete: func(taskName, agentName string) {
			streamer.AgentCompleted(taskName, agentName)
			if r.debugLogger != nil {
				r.debugLogger.LogEvent(EventAgentCompleted, map[string]any{
					"task":  taskName,
					"agent": agentName,
				})
			}
		},
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLogger,
		GetCommanderForQuery: func(taskName string, iterationIndex int) (*agent.Commander, error) {
			return r.getCommanderForQuery(taskName, iterationIndex, task.Name)
		},
		// Shared question store callbacks (also available for regular tasks)
		ListCommanderQuestions: func(depTaskName string) []string {
			return r.listCommanderQuestions(depTaskName)
		},
		GetCommanderAnswer: func(depTaskName string, index int) (string, error) {
			return r.getCommanderAnswer(depTaskName, index)
		},
		AskCommanderWithCache: func(targetTask string, iterationIndex int, question string) (string, error) {
			return r.askCommanderWithCache(ctx, targetTask, iterationIndex, task.Name, question)
		},
		SessionLogger: r.stores.Sessions,
		TaskID:        taskID,
	}, depSummaries)

	// Create task-specific streamer adapter
	taskStreamer := &commanderStreamerAdapter{
		taskName: task.Name,
		streamer: streamer,
	}

	// Execute the task
	summary, err := sup.ExecuteTask(ctx, objective, taskStreamer)
	if err != nil {
		errStr := err.Error()
		updateTaskDone(false, nil, &errStr)
		sup.Close()
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Store commander's completed agents for inheritance by dependent tasks
	r.mu.Lock()
	r.taskCommanders[task.Name] = sup
	r.taskAgents[task.Name] = sup.GetCompletedAgents()
	r.mu.Unlock()

	// Parse OUTPUT block if present
	output, cleanSummary := parseOutput(summary)

	// Store in knowledge store
	r.knowledgeStore.StoreTaskOutput(TaskOutput{
		TaskName:   task.Name,
		Status:     "success",
		Summary:    cleanSummary,
		Timestamp:  time.Now(),
		Output:     output,
		IsIterated: false,
	})

	// Update task status to completed
	outputJSON, _ := json.Marshal(output)
	outputStr := string(outputJSON)
	updateTaskDone(true, &outputStr, nil)

	// Persist task output to store
	r.stores.Missions.StoreTaskOutput(taskID, nil, nil, outputStr)

	streamer.TaskCompleted(task.Name, cleanSummary)
	return &TaskResult{
		TaskName: task.Name,
		Summary:  cleanSummary,
		Success:  true,
	}, nil
}

// getDependencyChain returns all tasks this task depends on (including transitive dependencies)
func (r *Runner) getDependencyChain(taskName string) []string {
	task := r.mission.GetTaskByName(taskName)
	if task == nil {
		return nil
	}

	// BFS to get all dependencies in order
	visited := make(map[string]bool)
	var result []string
	queue := make([]string, len(task.DependsOn))
	copy(queue, task.DependsOn)

	for len(queue) > 0 {
		dep := queue[0]
		queue = queue[1:]

		if visited[dep] {
			continue
		}
		visited[dep] = true

		depTask := r.mission.GetTaskByName(dep)
		if depTask != nil {
			// Add this task's dependencies to the queue
			queue = append(queue, depTask.DependsOn...)
		}

		result = append(result, dep)
	}

	return result
}

// collectInheritedAgents gathers all completed agents from dependency tasks in the lineage
func (r *Runner) collectInheritedAgents(taskName string) map[string]*agent.Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*agent.Agent)
	for _, depTaskName := range r.getDependencyChain(taskName) {
		if agents, ok := r.taskAgents[depTaskName]; ok {
			for id, a := range agents {
				result[id] = a
			}
		}
	}
	return result
}

// getTaskOutputSchema converts a task's output schema to agent.OutputFieldSchema slice
func (r *Runner) getTaskOutputSchema(task config.Task) []agent.OutputFieldSchema {
	if task.Output == nil {
		return nil
	}

	var result []agent.OutputFieldSchema
	for _, field := range task.Output.Fields {
		result = append(result, agent.OutputFieldSchema{
			Name:        field.Name,
			Type:        field.Type,
			Description: field.Description,
			Required:    field.Required,
		})
	}
	return result
}

// collectDepOutputSchemas gathers output schema info from dependency tasks
func (r *Runner) collectDepOutputSchemas(taskName string) []agent.DependencyOutputSchema {
	var result []agent.DependencyOutputSchema

	for _, depTaskName := range r.getDependencyChain(taskName) {
		task := r.mission.GetTaskByName(depTaskName)
		if task == nil {
			continue
		}

		// Get task output from knowledge store to check if it exists
		output, ok := r.knowledgeStore.GetTaskOutput(depTaskName)
		if !ok {
			continue
		}

		schema := agent.DependencyOutputSchema{
			TaskName:   depTaskName,
			IsIterated: output.IsIterated,
			ItemCount:  output.TotalIterations,
		}

		// Include output schema if defined
		if task.Output != nil {
			for _, field := range task.Output.Fields {
				schema.OutputFields = append(schema.OutputFields, agent.OutputFieldSchema{
					Name:        field.Name,
					Type:        field.Type,
					Description: field.Description,
					Required:    field.Required,
				})
			}
		}

		result = append(result, schema)
	}

	return result
}

// commanderStreamerAdapter adapts MissionHandler to agent.CommanderStreamer
type commanderStreamerAdapter struct {
	taskName string
	streamer streamers.MissionHandler
}

func (s *commanderStreamerAdapter) Reasoning(content string) {
	s.streamer.CommanderReasoning(s.taskName, content)
}

func (s *commanderStreamerAdapter) Answer(content string) {
	s.streamer.CommanderAnswer(s.taskName, content)
}

func (s *commanderStreamerAdapter) CallingTool(name, input string) {
	s.streamer.CommanderCallingTool(s.taskName, name, input)
}

func (s *commanderStreamerAdapter) ToolComplete(name string) {
	s.streamer.CommanderToolComplete(s.taskName, name)
}

// missionSnapshot returns a JSON-friendly representation of the mission config.
func (r *Runner) missionSnapshot() map[string]any {
	snap := map[string]any{
		"name":      r.mission.Name,
		"commander": r.mission.Commander,
		"agents":    r.mission.Agents,
	}

	if len(r.mission.Inputs) > 0 {
		var inputs []map[string]any
		for _, input := range r.mission.Inputs {
			m := map[string]any{
				"name": input.Name,
				"type": input.Type,
			}
			if input.Description != "" {
				m["description"] = input.Description
			}
			if input.Secret {
				m["secret"] = true
			}
			inputs = append(inputs, m)
		}
		snap["inputs"] = inputs
	}

	if len(r.mission.Datasets) > 0 {
		var datasets []map[string]any
		for _, ds := range r.mission.Datasets {
			m := map[string]any{
				"name": ds.Name,
			}
			if ds.Description != "" {
				m["description"] = ds.Description
			}
			if ds.BindTo != "" {
				m["bindTo"] = ds.BindTo
			}
			if ds.Schema != nil {
				m["schema"] = ds.Schema
			}
			datasets = append(datasets, m)
		}
		snap["datasets"] = datasets
	}

	var tasks []map[string]any
	for _, task := range r.mission.Tasks {
		objective, _ := task.ResolvedObjective(r.varsValues, r.inputValues)
		tasks = append(tasks, taskSnapshot(task, objective))
	}
	snap["tasks"] = tasks

	return snap
}

// taskSnapshot returns a JSON-friendly representation of a task config with the resolved objective.
func taskSnapshot(task config.Task, resolvedObjective string) map[string]any {
	snap := map[string]any{
		"name":      task.Name,
		"objective": resolvedObjective,
	}
	if len(task.Agents) > 0 {
		snap["agents"] = task.Agents
	}
	if len(task.DependsOn) > 0 {
		snap["dependsOn"] = task.DependsOn
	}
	if task.Iterator != nil {
		snap["iterator"] = task.Iterator
	}
	if task.Output != nil {
		snap["output"] = task.Output
	}
	return snap
}

// runIteratedTask executes a task that iterates over a dataset
func (r *Runner) runIteratedTask(ctx context.Context, task config.Task, missionID string, streamer streamers.MissionHandler) (*TaskResult, error) {
	// Create task record in store with representative objective
	representativeObj, _ := r.resolveIterationObjective(task, r.resolvedDatasets[task.Iterator.Dataset][0])
	taskConfigJSON, _ := json.Marshal(taskSnapshot(task, representativeObj))
	taskID, _ := r.stores.Missions.CreateTask(missionID, task.Name, string(taskConfigJSON))
	r.stores.Missions.UpdateTaskStatus(taskID, "running", nil, nil)

	updateTaskDone := func(success bool, outputJSON *string, errMsg *string) {
		if success {
			r.stores.Missions.UpdateTaskStatus(taskID, "completed", outputJSON, nil)
		} else {
			r.stores.Missions.UpdateTaskStatus(taskID, "failed", nil, errMsg)
		}
	}

	datasetName := task.Iterator.Dataset
	items, ok := r.resolvedDatasets[datasetName]
	if !ok {
		errStr := fmt.Sprintf("dataset '%s' not found", datasetName)
		updateTaskDone(false, nil, &errStr)
		return nil, fmt.Errorf("%s", errStr)
	}

	if len(items) == 0 {
		// No items to iterate - return success with empty summary
		streamer.TaskStarted(task.Name, fmt.Sprintf("(0 iterations over %s)", datasetName))
		streamer.TaskCompleted(task.Name, "No items to process")

		// Store empty task output
		r.knowledgeStore.StoreTaskOutput(TaskOutput{
			TaskName:        task.Name,
			Status:          "success",
			Summary:         "No items to process",
			Timestamp:       time.Now(),
			IsIterated:      true,
			TotalIterations: 0,
			Iterations:      nil,
		})

		updateTaskDone(true, nil, nil)
		return &TaskResult{
			TaskName: task.Name,
			Summary:  "No items to process",
			Success:  true,
		}, nil
	}

	// Query ancestors ONCE with first item's objective for targeted context
	var depSummaries []agent.DependencySummary
	representativeObjective, err := r.resolveIterationObjective(task, items[0])
	if err != nil {
		errStr := err.Error()
		updateTaskDone(false, nil, &errStr)
		return nil, fmt.Errorf("resolving representative objective: %w", err)
	}
	depSummaries, err = r.queryAncestorsForContext(ctx, task.Name, representativeObjective)
	if err != nil {
		errStr := err.Error()
		updateTaskDone(false, nil, &errStr)
		return nil, fmt.Errorf("querying ancestors: %w", err)
	}

	// Notify mission handler about iteration start
	streamer.TaskIterationStarted(task.Name, len(items), task.Iterator.Parallel)

	var iterations []IterationResult

	if task.Iterator.Parallel {
		// Parallel execution with fail-fast
		iterations = r.runParallelIterations(ctx, task, items, taskID, depSummaries, streamer)
	} else {
		// Sequential execution (no more rolling aggregation)
		iterations = r.runSequentialIterations(ctx, task, items, taskID, depSummaries, streamer)
	}

	// Check for failures
	var firstError error
	allSuccess := true
	successCount := 0
	for _, iter := range iterations {
		if !iter.Success {
			allSuccess = false
			if firstError == nil {
				firstError = iter.Error
			}
		} else {
			successCount++
		}
	}

	if !allSuccess {
		errStr := firstError.Error()
		updateTaskDone(false, nil, &errStr)
		streamer.TaskFailed(task.Name, firstError)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    firstError,
		}, firstError
	}

	// Convert IterationResults to IterationOutputs for storage
	iterOutputs := make([]IterationOutput, len(iterations))
	for i, iter := range iterations {
		iterOutputs[i] = IterationOutput{
			Index:     iter.Index,
			ItemID:    iter.ItemID,
			Status:    "success",
			Summary:   iter.Summary,
			Output:    iter.Output,
			Timestamp: time.Now(),
		}
	}

	// Create a simple summary (no LLM aggregation)
	summary := fmt.Sprintf("Completed %d iterations over %s", len(iterations), datasetName)

	// Store in knowledge store
	r.knowledgeStore.StoreTaskOutput(TaskOutput{
		TaskName:        task.Name,
		Status:          "success",
		Summary:         summary,
		Timestamp:       time.Now(),
		IsIterated:      true,
		TotalIterations: len(iterations),
		Iterations:      iterOutputs,
	})

	// Update task status to completed
	outputJSON, _ := json.Marshal(iterOutputs)
	outputStr := string(outputJSON)
	updateTaskDone(true, &outputStr, nil)

	// Persist individual iteration outputs to store
	for _, iterOut := range iterOutputs {
		iterOutJSON, _ := json.Marshal(iterOut.Output)
		idx := iterOut.Index
		r.stores.Missions.StoreTaskOutput(taskID, &datasetName, &idx, string(iterOutJSON))
	}

	streamer.TaskIterationCompleted(task.Name, len(iterations), summary)
	streamer.TaskCompleted(task.Name, summary)

	return &TaskResult{
		TaskName: task.Name,
		Summary:  summary,
		Success:  true,
	}, nil
}

// runSequentialIterations runs all iterations in a single commander session with agent reuse
func (r *Runner) runSequentialIterations(ctx context.Context, task config.Task, items []cty.Value, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) []IterationResult {
	// Get agents for this task
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.mission.Agents
	}

	// Build inherited agents from all dependency tasks in the lineage
	inheritedAgents := r.collectInheritedAgents(task.Name)

	// Collect dependency output schemas for the commander
	depOutputSchemas := r.collectDepOutputSchemas(task.Name)

	// Get task's own output schema if defined
	taskOutputSchema := r.getTaskOutputSchema(task)

	// Get debug file for commander if debug mode is enabled
	var debugFile string
	if r.debugLogger != nil {
		debugFile = r.debugLogger.GetMessageFile("commander", task.Name)
	}

	// Build objective for sequential dataset processing
	// Use the first item to resolve a representative objective
	representativeObjective, err := r.resolveIterationObjective(task, items[0])
	if err != nil {
		return []IterationResult{{
			Index:   0,
			Success: false,
			Error:   fmt.Errorf("resolving objective: %w", err),
		}}
	}

	objective := fmt.Sprintf(`Process the following task for each of %d items in the dataset.

Task objective (example for first item): %s

Use dataset_next to get each item. Process it completely, then call dataset_item_complete with the output.
Continue until dataset_next returns "exhausted".`, len(items), representativeObjective)

	// Create single commander with all items
	sup, err := agent.NewCommander(ctx, agent.CommanderOptions{
		Config:            r.cfg,
		ConfigPath:        r.configPath,
		MissionName:      r.mission.Name,
		TaskName:          task.Name,
		Commander:   r.mission.Commander,
		AgentNames:        agents,
		DepSummaries:      depSummaries,
		DepOutputSchemas:  depOutputSchemas,
		TaskOutputSchema:  taskOutputSchema,
		InheritedAgents:   inheritedAgents,
		SecretInfos:       r.secretInfos,
		SecretValues:      r.secretValues,
		IsIteration:       true,
		IsParallel:        false,
		DebugFile:         debugFile,
		SequentialDataset: items, // Pass all items for sequential processing
	})
	if err != nil {
		return []IterationResult{{
			Index:   0,
			Success: false,
			Error:   err,
		}}
	}

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName string) {
			streamer.AgentStarted(taskName, agentName)
			if r.debugLogger != nil {
				r.debugLogger.LogEvent(EventAgentStarted, map[string]any{
					"task":  taskName,
					"agent": agentName,
				})
			}
		},
		GetAgentHandler: func(taskName, agentName string) streamers.ChatHandler {
			return streamer.AgentHandler(taskName, agentName)
		},
		OnAgentComplete: func(taskName, agentName string) {
			streamer.AgentCompleted(taskName, agentName)
			if r.debugLogger != nil {
				r.debugLogger.LogEvent(EventAgentCompleted, map[string]any{
					"task":  taskName,
					"agent": agentName,
				})
			}
		},
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLogger,
		GetCommanderForQuery: func(depTaskName string, iterationIndex int) (*agent.Commander, error) {
			return r.getCommanderForQuery(depTaskName, iterationIndex, task.Name)
		},
		ListCommanderQuestions: func(taskName string) []string {
			return r.listCommanderQuestions(taskName)
		},
		GetCommanderAnswer: func(taskName string, index int) (string, error) {
			return r.getCommanderAnswer(taskName, index)
		},
		AskCommanderWithCache: func(targetTask string, iterationIndex int, question string) (string, error) {
			return r.askCommanderWithCache(ctx, targetTask, iterationIndex, task.Name, question)
		},
		SessionLogger: r.stores.Sessions,
		TaskID:        taskID,
	}, depSummaries)

	// Create streamer adapter for the commander
	seqStreamer := &iterationStreamerAdapter{
		taskName: task.Name,
		index:    0, // Use 0 as we're handling all items in one session
		streamer: streamer,
	}

	// Execute the task - commander handles all items internally
	_, err = sup.ExecuteTask(ctx, objective, seqStreamer)

	// Get results from the commander's dataset cursor
	results := sup.GetDatasetResults()
	if results == nil || len(results) == 0 {
		if err != nil {
			return []IterationResult{{
				Index:   0,
				Success: false,
				Error:   err,
			}}
		}
		// No results but no error - something went wrong
		return []IterationResult{{
			Index:   0,
			Success: false,
			Error:   fmt.Errorf("no results from sequential dataset processing"),
		}}
	}

	// Convert DatasetItemResult to IterationResult
	iterations := make([]IterationResult, len(results))
	for i, r := range results {
		itemID := ""
		if r.Index < len(items) {
			itemID = getItemID(items[r.Index], r.Index)
		}
		iterations[i] = IterationResult{
			Index:   r.Index,
			ItemID:  itemID,
			Summary: r.Summary,
			Output:  r.Output,
			Success: r.Success,
		}
	}

	// Store the commander for ask_commander queries from dependent tasks
	r.mu.Lock()
	if r.iterationCommanders[task.Name] == nil {
		r.iterationCommanders[task.Name] = make(map[int]*agent.Commander)
	}
	// Store as iteration 0 since it's a single commander handling all items
	r.iterationCommanders[task.Name][0] = sup
	r.mu.Unlock()

	return iterations
}

// runParallelIterations runs iterations in parallel with concurrency limit and optional staggered starts
func (r *Runner) runParallelIterations(ctx context.Context, task config.Task, items []cty.Value, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) []IterationResult {
	iterations := make([]IterationResult, len(items))
	maxRetries := 0
	if task.Iterator != nil {
		maxRetries = task.Iterator.MaxRetries
	}

	// Get concurrency limit (default 5)
	concurrencyLimit := 5
	if task.Iterator != nil && task.Iterator.ConcurrencyLimit > 0 {
		concurrencyLimit = task.Iterator.ConcurrencyLimit
	}

	// Get start delay (default 0 - no staggering)
	startDelay := 0
	if task.Iterator != nil && task.Iterator.StartDelay > 0 {
		startDelay = task.Iterator.StartDelay
	}

	// Check smoketest mode
	smoketest := false
	if task.Iterator != nil {
		smoketest = task.Iterator.Smoketest
	}

	// If smoketest is enabled, run first iteration completely before starting others
	if smoketest && len(items) > 0 {
		// Run first iteration synchronously
		var firstResult IterationResult
		for attempt := 0; attempt <= maxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return []IterationResult{{
					Index:   0,
					ItemID:  getItemID(items[0], 0),
					Success: false,
					Error:   ctx.Err(),
				}}
			default:
			}

			firstResult = r.runSingleIteration(ctx, task, 0, items[0], nil, nil, taskID, depSummaries, streamer)
			if firstResult.Success {
				break
			}

			if attempt < maxRetries {
				streamer.IterationRetrying(task.Name, 0, attempt+1, maxRetries, firstResult.Error)
			}
		}

		iterations[0] = firstResult

		// If smoketest failed, don't start other iterations
		if !firstResult.Success {
			return iterations[:1] // Return only the failed first iteration
		}

		// Continue with remaining items (index 1+)
		items = items[1:]
		if len(items) == 0 {
			return iterations[:1]
		}

		// Run remaining iterations in parallel
		remainingIterations := r.runParallelIterationsCore(ctx, task, items, 1, maxRetries, concurrencyLimit, startDelay, taskID, depSummaries, streamer)
		for i, result := range remainingIterations {
			iterations[i+1] = result
		}
		return iterations
	}

	// No smoketest - run all iterations in parallel
	return r.runParallelIterationsCore(ctx, task, items, 0, maxRetries, concurrencyLimit, startDelay, taskID, depSummaries, streamer)
}

// runParallelIterationsCore is the core parallel execution logic
func (r *Runner) runParallelIterationsCore(ctx context.Context, task config.Task, items []cty.Value, indexOffset int, maxRetries int, concurrencyLimit int, startDelay int, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) []IterationResult {
	iterations := make([]IterationResult, len(items))

	// Semaphore to limit concurrent iterations
	sem := make(chan struct{}, concurrencyLimit)
	var wg sync.WaitGroup

	for i, item := range items {
		i, item := i, item // capture
		actualIndex := i + indexOffset

		// Stagger starts for the first batch to allow cache population
		if startDelay > 0 && i > 0 && i < concurrencyLimit {
			time.Sleep(time.Duration(startDelay) * time.Millisecond)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			// Acquire semaphore slot (blocks if at concurrency limit)
			sem <- struct{}{}
			defer func() { <-sem }()

			// Run with retries
			var result IterationResult
			for attempt := 0; attempt <= maxRetries; attempt++ {
				select {
				case <-ctx.Done():
					iterations[i] = IterationResult{
						Index:   actualIndex,
						ItemID:  getItemID(item, actualIndex),
						Success: false,
						Error:   ctx.Err(),
					}
					return
				default:
				}

				// Pass nil for prevOutput and prevLearnings in parallel iterations (no meaningful ordering)
				result = r.runSingleIteration(ctx, task, actualIndex, item, nil, nil, taskID, depSummaries, streamer)
				if result.Success {
					break
				}

				// If we have retries remaining, log and retry
				if attempt < maxRetries {
					streamer.IterationRetrying(task.Name, actualIndex, attempt+1, maxRetries, result.Error)
				}
			}

			iterations[i] = result
		}()
	}

	wg.Wait()
	return iterations
}

// runSingleIteration executes a single iteration of an iterated task
func (r *Runner) runSingleIteration(ctx context.Context, task config.Task, index int, item cty.Value, prevOutput map[string]any, prevLearnings map[string]any, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) IterationResult {
	itemID := getItemID(item, index)

	// Resolve the objective with item context
	objective, err := r.resolveIterationObjective(task, item)
	if err != nil {
		streamer.IterationFailed(task.Name, index, err)
		return IterationResult{
			Index:   index,
			ItemID:  itemID,
			Success: false,
			Error:   err,
		}
	}

	streamer.IterationStarted(task.Name, index, objective)

	// Log iteration started event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventIterationStarted, map[string]any{
			"task":      task.Name,
			"index":     index,
			"item_id":   itemID,
			"objective": objective,
		})
	}

	// Get agents for this task
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.mission.Agents
	}

	// Build inherited agents from all dependency tasks in the lineage
	inheritedAgents := r.collectInheritedAgents(task.Name)

	// Collect dependency output schemas for the commander
	depOutputSchemas := r.collectDepOutputSchemas(task.Name)

	// Get task's own output schema if defined
	taskOutputSchema := r.getTaskOutputSchema(task)

	// Get debug file for commander if debug mode is enabled
	iterTaskName := fmt.Sprintf("%s[%d]", task.Name, index)
	var debugFile string
	if r.debugLogger != nil {
		debugFile = r.debugLogger.GetMessageFile("commander", iterTaskName)
	}

	// Create commander for this iteration
	sup, err := agent.NewCommander(ctx, agent.CommanderOptions{
		Config:                 r.cfg,
		ConfigPath:             r.configPath,
		MissionName:           r.mission.Name,
		TaskName:               iterTaskName,
		Commander:        r.mission.Commander,
		AgentNames:             agents,
		DepSummaries:           depSummaries,
		DepOutputSchemas:       depOutputSchemas,
		TaskOutputSchema:       taskOutputSchema,
		InheritedAgents:        inheritedAgents,
		PrevIterationOutput:    prevOutput,
		PrevIterationLearnings: prevLearnings,
		SecretInfos:            r.secretInfos,
		SecretValues:           r.secretValues,
		IsIteration:            true,
		IsParallel:             task.Iterator.Parallel,
		DebugFile:              debugFile,
	})
	if err != nil {
		streamer.IterationFailed(task.Name, index, err)
		return IterationResult{
			Index:   index,
			ItemID:  itemID,
			Success: false,
			Error:   err,
		}
	}
	// Note: Don't close sup here - store it for ask_commander queries from dependent tasks
	// Cleanup happens in cleanupIterationCommanders() after all dependent tasks complete

	// Set up tool callbacks for iteration
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName string) {
			streamer.AgentStarted(taskName, agentName)
			if r.debugLogger != nil {
				r.debugLogger.LogEvent(EventAgentStarted, map[string]any{
					"task":  taskName,
					"agent": agentName,
				})
			}
		},
		GetAgentHandler: func(taskName, agentName string) streamers.ChatHandler {
			return streamer.AgentHandler(taskName, agentName)
		},
		OnAgentComplete: func(taskName, agentName string) {
			streamer.AgentCompleted(taskName, agentName)
			if r.debugLogger != nil {
				r.debugLogger.LogEvent(EventAgentCompleted, map[string]any{
					"task":  taskName,
					"agent": agentName,
				})
			}
		},
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLogger,
		GetCommanderForQuery: func(depTaskName string, iterationIndex int) (*agent.Commander, error) {
			// Use base task name (without iteration index) for dependency validation
			return r.getCommanderForQuery(depTaskName, iterationIndex, task.Name)
		},
		// Iteration-specific callbacks for shared question store
		ListCommanderQuestions: func(taskName string) []string {
			return r.listCommanderQuestions(taskName)
		},
		GetCommanderAnswer: func(taskName string, index int) (string, error) {
			return r.getCommanderAnswer(taskName, index)
		},
		AskCommanderWithCache: func(targetTask string, iterationIndex int, question string) (string, error) {
			return r.askCommanderWithCache(ctx, targetTask, iterationIndex, task.Name, question)
		},
		SessionLogger: r.stores.Sessions,
		TaskID:        taskID,
	}, depSummaries)

	// Create iteration-specific streamer adapter
	iterStreamer := &iterationStreamerAdapter{
		taskName: task.Name,
		index:    index,
		streamer: streamer,
	}

	// Execute the iteration
	summary, err := sup.ExecuteTask(ctx, objective, iterStreamer)
	if err != nil {
		sup.Close() // Close on failure
		streamer.IterationFailed(task.Name, index, err)
		return IterationResult{
			Index:   index,
			ItemID:  itemID,
			Success: false,
			Error:   err,
		}
	}

	// Parse OUTPUT block if present
	output, cleanSummary := parseOutput(summary)

	// Parse LEARNINGS block if present
	learnings, cleanSummary := parseLearnings(cleanSummary)

	// Validate output against schema - if required fields are missing, iteration failed
	if err := validateOutput(output, task.Output); err != nil {
		sup.Close() // Close on failure
		streamer.IterationFailed(task.Name, index, err)
		return IterationResult{
			Index:     index,
			ItemID:    itemID,
			Summary:   cleanSummary,
			Output:    output,
			Learnings: learnings,
			Success:   false,
			Error:     err,
		}
	}

	// Store the iteration commander for ask_commander queries from dependent tasks
	r.mu.Lock()
	if r.iterationCommanders[task.Name] == nil {
		r.iterationCommanders[task.Name] = make(map[int]*agent.Commander)
	}
	r.iterationCommanders[task.Name][index] = sup
	r.mu.Unlock()

	streamer.IterationCompleted(task.Name, index, cleanSummary)
	return IterationResult{
		Index:     index,
		ItemID:    itemID,
		Summary:   cleanSummary,
		Output:    output,
		Learnings: learnings,
		Success:   true,
	}
}

// resolveIterationObjective evaluates the objective with vars, inputs, and item context
func (r *Runner) resolveIterationObjective(task config.Task, item cty.Value) (string, error) {
	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"vars":   cty.ObjectVal(r.varsValues),
			"inputs": cty.ObjectVal(r.inputValues),
			"item":   item,
		},
	}
	val, diags := task.ObjectiveExpr.Value(ctx)
	if diags.HasErrors() {
		return "", fmt.Errorf("evaluating objective: %s", diags.Error())
	}
	return val.AsString(), nil
}

// parseOutput extracts structured output from an answer containing an OUTPUT block
// Returns the parsed output map and the answer with the OUTPUT block removed
func parseOutput(answer string) (map[string]any, string) {
	// Match <OUTPUT>...</OUTPUT> block
	re := regexp.MustCompile(`(?s)<OUTPUT>\s*(.*?)\s*</OUTPUT>`)
	match := re.FindStringSubmatch(answer)

	if match == nil {
		return nil, answer
	}

	// Parse the JSON content
	var output map[string]any
	if err := json.Unmarshal([]byte(match[1]), &output); err != nil {
		// If parsing fails, return nil output but still strip the block
		return nil, re.ReplaceAllString(answer, "")
	}

	// Remove the OUTPUT block from the answer
	cleanAnswer := strings.TrimSpace(re.ReplaceAllString(answer, ""))
	return output, cleanAnswer
}

// validateOutput checks if output satisfies the task's required output schema fields.
// Returns nil if valid, or an error describing which required fields are missing.
func validateOutput(output map[string]any, schema *config.OutputSchema) error {
	if schema == nil {
		// No schema defined - any output (or none) is valid
		return nil
	}

	// Check each required field
	var missingFields []string
	for _, field := range schema.Fields {
		if !field.Required {
			continue
		}

		val, exists := output[field.Name]
		if !exists || val == nil {
			missingFields = append(missingFields, field.Name)
		}
	}

	if len(missingFields) > 0 {
		return fmt.Errorf("missing required output fields: %v", missingFields)
	}

	return nil
}

// parseLearnings extracts learnings from an answer containing a LEARNINGS block
// Returns the parsed learnings map and the answer with the LEARNINGS block removed
func parseLearnings(answer string) (map[string]any, string) {
	// Match <LEARNINGS>...</LEARNINGS> block
	re := regexp.MustCompile(`(?s)<LEARNINGS>\s*(.*?)\s*</LEARNINGS>`)
	match := re.FindStringSubmatch(answer)

	if match == nil {
		return nil, answer
	}

	// Parse the JSON content
	var learnings map[string]any
	if err := json.Unmarshal([]byte(match[1]), &learnings); err != nil {
		// If parsing fails, return nil learnings but still strip the block
		return nil, re.ReplaceAllString(answer, "")
	}

	// Remove the LEARNINGS block from the answer
	cleanAnswer := strings.TrimSpace(re.ReplaceAllString(answer, ""))
	return learnings, cleanAnswer
}

// mergeLearnings combines two learnings maps, appending arrays and overwriting strings
// Used to accumulate learnings from failed retry attempts
func mergeLearnings(base, new map[string]any) map[string]any {
	if base == nil {
		return new
	}
	if new == nil {
		return base
	}

	merged := make(map[string]any)
	// Copy base
	for k, v := range base {
		merged[k] = v
	}

	// Merge new values
	for k, v := range new {
		if existing, ok := merged[k]; ok {
			// If both are slices, append
			if existingSlice, ok := existing.([]any); ok {
				if newSlice, ok := v.([]any); ok {
					merged[k] = append(existingSlice, newSlice...)
					continue
				}
			}
		}
		// Otherwise, overwrite (new value takes precedence for recommendations, etc.)
		merged[k] = v
	}

	return merged
}

// getItemID generates an identifier for an iteration item
func getItemID(item cty.Value, index int) string {
	// Try to get a meaningful ID from the item
	if item.Type().IsObjectType() || item.Type().IsMapType() {
		// Look for common ID fields
		for _, fieldName := range []string{"id", "name", "key"} {
			if item.Type().HasAttribute(fieldName) {
				val := item.GetAttr(fieldName)
				if val.Type() == cty.String && val.IsKnown() && !val.IsNull() {
					return val.AsString()
				}
			}
		}
	}

	// Fall back to index-based ID
	return fmt.Sprintf("item_%d", index)
}

// iterationStreamerAdapter adapts MissionHandler to agent.CommanderStreamer for iterations
type iterationStreamerAdapter struct {
	taskName string
	index    int
	streamer streamers.MissionHandler
}

func (s *iterationStreamerAdapter) Reasoning(content string) {
	s.streamer.IterationReasoning(s.taskName, s.index, content)
}

func (s *iterationStreamerAdapter) Answer(content string) {
	s.streamer.IterationAnswer(s.taskName, s.index, content)
}

func (s *iterationStreamerAdapter) CallingTool(name, input string) {
	s.streamer.CommanderCallingTool(fmt.Sprintf("%s[%d]", s.taskName, s.index), name, input)
}

func (s *iterationStreamerAdapter) ToolComplete(name string) {
	s.streamer.CommanderToolComplete(fmt.Sprintf("%s[%d]", s.taskName, s.index), name)
}

// =============================================================================
// Commander Query Support - allows commanders to query previous commanders
// =============================================================================

// queryAncestorsForContext queries each non-iterated ancestor commander with the task's objective
// to get targeted context instead of generic summaries.
// For iterated ancestors, we skip the query (they use ask_commander with specific indices instead).
// Returns error if any ancestor query fails - this is a critical failure.
func (r *Runner) queryAncestorsForContext(ctx context.Context, taskName string, objective string) ([]agent.DependencySummary, error) {
	depChain := r.getDependencyChain(taskName)
	var depSummaries []agent.DependencySummary

	for _, depTaskName := range depChain {
		// Check if this is an iterated task
		r.mu.RLock()
		_, isIterated := r.iterationCommanders[depTaskName]
		sup, hasRegularSup := r.taskCommanders[depTaskName]
		r.mu.RUnlock()

		if isIterated {
			// Skip pull query for iterated tasks
			// Output schema info is injected separately via DepOutputSchemas
			// Task can use ask_commander with index if it needs specific iteration context
			continue
		}

		if !hasRegularSup {
			return nil, fmt.Errorf("commander for dependency '%s' not found", depTaskName)
		}

		// Create a clone for querying
		clone := sup.CloneForQuery()

		question := fmt.Sprintf(
			"A dependent task needs your help. Their objective is:\n\n%s\n\n"+
				"Based on what you learned during your task, what relevant context, "+
				"findings, or information should they know to accomplish their objective?",
			objective,
		)

		answer, err := clone.AnswerQueryIsolated(ctx, question)
		clone.Close()

		if err != nil {
			return nil, fmt.Errorf("failed to query ancestor '%s': %w", depTaskName, err)
		}

		depSummaries = append(depSummaries, agent.DependencySummary{
			TaskName: depTaskName,
			Summary:  answer,
		})
	}

	return depSummaries, nil
}

// getCommanderForQuery returns an isolated clone of a completed commander for querying.
// The requestingTask parameter is used to validate that the requested task is in the
// dependency chain of the requesting task.
// For iterated tasks, pass the iteration index (0+). For regular tasks, pass -1.
func (r *Runner) getCommanderForQuery(taskName string, iterationIndex int, requestingTask string) (*agent.Commander, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Check if the requested task is in the dependency chain of the requesting task
	depChain := r.getDependencyChain(requestingTask)
	found := false
	for _, dep := range depChain {
		if dep == taskName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("task '%s' is not in the dependency chain of '%s'", taskName, requestingTask)
	}

	if iterationIndex >= 0 {
		// Query specific iteration commander
		iterSups, ok := r.iterationCommanders[taskName]
		if !ok {
			return nil, fmt.Errorf("no iteration commanders found for task '%s'", taskName)
		}
		sup, ok := iterSups[iterationIndex]
		if !ok {
			return nil, fmt.Errorf("iteration %d not found for task '%s'", iterationIndex, taskName)
		}
		return sup.CloneForQuery(), nil
	}

	// Query regular task commander
	sup, ok := r.taskCommanders[taskName]
	if !ok {
		// Check if this is an iterated task (has iteration commanders but no regular commander)
		if _, hasIterations := r.iterationCommanders[taskName]; hasIterations {
			return nil, fmt.Errorf("task '%s' is an iterated task - you must provide an 'index' parameter to query a specific iteration", taskName)
		}
		return nil, fmt.Errorf("commander for task '%s' not found (task may not have completed yet)", taskName)
	}

	// Return a cloned copy for isolated querying
	return sup.CloneForQuery(), nil
}

// =============================================================================
// Shared Question Store - deduplicates ask_commander queries across iterations
// =============================================================================

// listCommanderQuestions returns the list of questions asked to a dependency task.
// This allows commanders to see what questions have already been asked by other iterations.
func (r *Runner) listCommanderQuestions(taskName string) []string {
	r.askCommanderStore.mu.Lock()
	defer r.askCommanderStore.mu.Unlock()

	entries := r.askCommanderStore.questions[taskName]
	questions := make([]string, len(entries))
	for i, e := range entries {
		questions[i] = e.Question
	}
	return questions
}

// getCommanderAnswer returns the answer for a question by index.
// If the answer is not ready yet, it blocks until the original asker completes.
func (r *Runner) getCommanderAnswer(taskName string, index int) (string, error) {
	r.askCommanderStore.mu.Lock()
	entries := r.askCommanderStore.questions[taskName]
	if index < 0 || index >= len(entries) {
		r.askCommanderStore.mu.Unlock()
		return "", fmt.Errorf("question index %d out of range (task '%s' has %d questions)", index, taskName, len(entries))
	}
	entry := entries[index]
	r.askCommanderStore.mu.Unlock()

	// Wait for the answer to be ready
	<-entry.Ready

	// Return the answer (no lock needed - answer is immutable once Ready is closed)
	return entry.Answer, nil
}

// askCommanderWithCache checks if an exact question already exists in the cache.
// If yes, it waits for the answer (if pending) and returns it.
// If no, it registers the question, queries the commander, caches the answer, and returns it.
// For iterated tasks, pass the iteration index (0+). For regular tasks, pass -1.
func (r *Runner) askCommanderWithCache(ctx context.Context, targetTask string, iterationIndex int, requestingTask, question string) (string, error) {
	// Validate dependency chain first
	depChain := r.getDependencyChain(requestingTask)
	found := false
	for _, dep := range depChain {
		if dep == targetTask {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("task '%s' is not in the dependency chain of '%s'", targetTask, requestingTask)
	}

	// Cache key includes iteration index for iterated tasks
	cacheKey := targetTask
	if iterationIndex >= 0 {
		cacheKey = fmt.Sprintf("%s[%d]", targetTask, iterationIndex)
	}

	r.askCommanderStore.mu.Lock()

	// Check if exact question already exists
	entries := r.askCommanderStore.questions[cacheKey]
	for _, entry := range entries {
		if entry.Question == question {
			// Question exists - unlock and wait for answer
			r.askCommanderStore.mu.Unlock()
			<-entry.Ready
			return entry.Answer, nil
		}
	}

	// Question doesn't exist - register it with a pending answer
	entry := &questionEntry{
		Question: question,
		Answer:   "",
		Ready:    make(chan struct{}),
	}
	r.askCommanderStore.questions[cacheKey] = append(r.askCommanderStore.questions[cacheKey], entry)
	r.askCommanderStore.mu.Unlock()

	// Query the commander (outside lock)
	var sup *agent.Commander
	var ok bool

	r.mu.RLock()
	if iterationIndex >= 0 {
		// Query specific iteration commander
		if iterSups, exists := r.iterationCommanders[targetTask]; exists {
			sup, ok = iterSups[iterationIndex]
		}
	} else {
		// Query regular task commander
		sup, ok = r.taskCommanders[targetTask]
	}
	r.mu.RUnlock()

	if !ok {
		// Mark as failed and close the channel
		r.askCommanderStore.mu.Lock()
		entry.Answer = "ERROR: commander not found"
		close(entry.Ready)
		r.askCommanderStore.mu.Unlock()
		if iterationIndex >= 0 {
			return "", fmt.Errorf("commander for task '%s' iteration %d not found", targetTask, iterationIndex)
		}
		// Check if this is an iterated task (has iteration commanders but no regular commander)
		if _, hasIterations := r.iterationCommanders[targetTask]; hasIterations {
			return "", fmt.Errorf("task '%s' is an iterated task - you must provide an 'index' parameter to query a specific iteration", targetTask)
		}
		return "", fmt.Errorf("commander for task '%s' not found", targetTask)
	}

	clone := sup.CloneForQuery()
	answer, err := clone.AnswerQueryIsolated(ctx, question)
	if err != nil {
		// Mark as failed and close the channel
		r.askCommanderStore.mu.Lock()
		entry.Answer = fmt.Sprintf("ERROR: %v", err)
		close(entry.Ready)
		r.askCommanderStore.mu.Unlock()
		return "", err
	}

	// Store the answer and signal ready
	r.askCommanderStore.mu.Lock()
	entry.Answer = answer
	close(entry.Ready)
	r.askCommanderStore.mu.Unlock()

	return answer, nil
}

// =============================================================================
// DatasetStore Implementation - provides runtime dataset access for agents
// =============================================================================

// SetDataset sets a dataset's values at runtime
func (r *Runner) SetDataset(name string, items []cty.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find the dataset definition
	var ds *config.Dataset
	for i := range r.mission.Datasets {
		if r.mission.Datasets[i].Name == name {
			ds = &r.mission.Datasets[i]
			break
		}
	}
	if ds == nil {
		return fmt.Errorf("dataset '%s' not found", name)
	}

	// Validate items against schema if present
	for i, item := range items {
		if err := ds.ValidateItem(item); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}

	r.resolvedDatasets[name] = items

	// Write through to persistent store
	if dsID, ok := r.datasetIDs[name]; ok {
		if err := r.stores.Datasets.SetItems(dsID, items); err != nil {
			return fmt.Errorf("persist dataset '%s': %w", name, err)
		}
	}

	return nil
}

// GetDatasetSample returns a sample of items from a dataset
func (r *Runner) GetDatasetSample(name string, count int) ([]cty.Value, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	items, ok := r.resolvedDatasets[name]
	if !ok {
		return nil, fmt.Errorf("dataset '%s' not found", name)
	}

	if count <= 0 {
		count = 5
	}
	if count > len(items) {
		count = len(items)
	}

	return items[:count], nil
}

// GetDatasetCount returns the number of items in a dataset
func (r *Runner) GetDatasetCount(name string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	items, ok := r.resolvedDatasets[name]
	if !ok {
		return 0, fmt.Errorf("dataset '%s' not found", name)
	}

	return len(items), nil
}

// GetDatasetInfo returns information about all available datasets
func (r *Runner) GetDatasetInfo() []aitools.DatasetInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var info []aitools.DatasetInfo
	for _, ds := range r.mission.Datasets {
		dsInfo := aitools.DatasetInfo{
			Name:        ds.Name,
			Description: ds.Description,
			ItemCount:   len(r.resolvedDatasets[ds.Name]),
		}

		// Convert schema if present
		if ds.Schema != nil {
			for _, field := range ds.Schema.Fields {
				dsInfo.Schema = append(dsInfo.Schema, aitools.FieldInfo{
					Name:     field.Name,
					Type:     field.Type,
					Required: field.Required,
				})
			}
		}

		info = append(info, dsInfo)
	}

	return info
}

// GetKnowledgeStore returns the knowledge store for querying task outputs
func (r *Runner) GetKnowledgeStore() KnowledgeStore {
	return r.knowledgeStore
}

// GetTaskOutputSchema returns the output schema for a task by name
func (r *Runner) GetTaskOutputSchema(taskName string) *config.OutputSchema {
	task := r.mission.GetTaskByName(taskName)
	if task == nil {
		return nil
	}
	return task.Output
}

// GetDependencyOutputInfo returns info about completed dependency task outputs
// for injection into commander prompts
func (r *Runner) GetDependencyOutputInfo(taskName string) []DependencyOutputInfo {
	var result []DependencyOutputInfo

	for _, depTaskName := range r.getDependencyChain(taskName) {
		task := r.mission.GetTaskByName(depTaskName)
		if task == nil {
			continue
		}

		// Get task output from knowledge store
		output, ok := r.knowledgeStore.GetTaskOutput(depTaskName)
		if !ok {
			continue
		}

		info := DependencyOutputInfo{
			TaskName:   depTaskName,
			IsIterated: output.IsIterated,
			ItemCount:  output.TotalIterations,
		}

		// Include output schema if defined
		if task.Output != nil {
			for _, field := range task.Output.Fields {
				info.OutputFields = append(info.OutputFields, OutputFieldInfo{
					Name:        field.Name,
					Type:        field.Type,
					Description: field.Description,
					Required:    field.Required,
				})
			}
		}

		result = append(result, info)
	}

	return result
}

// DependencyOutputInfo describes a completed dependency task's output for the commander
type DependencyOutputInfo struct {
	TaskName     string
	IsIterated   bool
	ItemCount    int
	OutputFields []OutputFieldInfo
}

// OutputFieldInfo describes an output field
type OutputFieldInfo struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// =============================================================================
// Knowledge Store Adapter - adapts mission.MemoryKnowledgeStore to agent.KnowledgeStore
// =============================================================================

// knowledgeStoreAdapter wraps MemoryKnowledgeStore to implement agent.KnowledgeStore
type knowledgeStoreAdapter struct {
	store *MemoryKnowledgeStore
}

// GetTaskOutput implements agent.KnowledgeStore
func (a *knowledgeStoreAdapter) GetTaskOutput(taskName string) (*agent.TaskOutputInfo, bool) {
	output, ok := a.store.GetTaskOutput(taskName)
	if !ok {
		return nil, false
	}

	// Convert to agent.TaskOutputInfo
	info := &agent.TaskOutputInfo{
		TaskName:        output.TaskName,
		Status:          output.Status,
		Summary:         output.Summary,
		IsIterated:      output.IsIterated,
		TotalIterations: output.TotalIterations,
		Output:          output.Output,
	}

	// Convert iterations
	for _, iter := range output.Iterations {
		info.Iterations = append(info.Iterations, agent.IterationInfo{
			Index:   iter.Index,
			ItemID:  iter.ItemID,
			Status:  iter.Status,
			Summary: iter.Summary,
			Output:  iter.Output,
		})
	}

	return info, true
}

// Query implements agent.KnowledgeStore
func (a *knowledgeStoreAdapter) Query(taskName string, query agent.TaskQuery) agent.TaskQueryResult {
	// Convert query
	filters := make([]Filter, len(query.Filters))
	for i, f := range query.Filters {
		filters[i] = Filter{
			Field: f.Field,
			Op:    FilterOp(f.Op),
			Value: f.Value,
		}
	}

	result := a.store.Query(taskName, Query{
		Filters: filters,
		Limit:   query.Limit,
		Offset:  query.Offset,
		OrderBy: query.OrderBy,
		Desc:    query.Desc,
	})

	// Convert result
	var iterations []agent.IterationInfo
	for _, iter := range result.Results {
		iterations = append(iterations, agent.IterationInfo{
			Index:   iter.Index,
			ItemID:  iter.ItemID,
			Status:  iter.Status,
			Summary: iter.Summary,
			Output:  iter.Output,
		})
	}

	return agent.TaskQueryResult{
		TotalMatches: result.TotalMatches,
		Results:      iterations,
	}
}

// Aggregate implements agent.KnowledgeStore
func (a *knowledgeStoreAdapter) Aggregate(taskName string, query agent.AggregateQuery) agent.AggregateResult {
	// Convert query
	filters := make([]Filter, len(query.Filters))
	for i, f := range query.Filters {
		filters[i] = Filter{
			Field: f.Field,
			Op:    FilterOp(f.Op),
			Value: f.Value,
		}
	}

	result := a.store.Aggregate(taskName, AggregateQuery{
		Op:      AggregateOp(query.Op),
		Field:   query.Field,
		Filters: filters,
		GroupBy: query.GroupBy,
		GroupOp: AggregateOp(query.GroupOp),
	})

	// Convert result
	agentResult := agent.AggregateResult{
		Value:  result.Value,
		Values: result.Values,
		Groups: result.Groups,
	}

	if result.Item != nil {
		agentResult.Item = &agent.IterationInfo{
			Index:   result.Item.Index,
			ItemID:  result.Item.ItemID,
			Status:  result.Item.Status,
			Summary: result.Item.Summary,
			Output:  result.Item.Output,
		}
	}

	return agentResult
}
