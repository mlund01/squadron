package workflow

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

	"squad/agent"
	"squad/aitools"
	"squad/config"
	"squad/streamers"
)

// Runner executes a workflow by orchestrating supervisors for each task
type Runner struct {
	cfg        *config.Config
	configPath string
	workflow   *config.Workflow

	// Input values for objective resolution
	varsValues  map[string]cty.Value
	inputValues map[string]cty.Value

	// Resolved datasets for iteration
	resolvedDatasets map[string][]cty.Value

	// Task state management
	mu               sync.RWMutex
	taskResults      map[string]*TaskResult             // Results from completed tasks
	taskSupervisors  map[string]*agent.Supervisor       // Supervisors for completed tasks (kept for agent inheritance)
	taskAgents       map[string]map[string]*agent.Agent // Agents from each task (for inheritance)

	// Knowledge store for structured task outputs
	knowledgeStore *MemoryKnowledgeStore

	// Debug logging
	debugLogger *DebugLogger
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

// NewRunner creates a new workflow runner
func NewRunner(cfg *config.Config, configPath string, workflowName string, inputs map[string]string, opts ...RunnerOption) (*Runner, error) {
	// Find the workflow
	var workflow *config.Workflow
	for i := range cfg.Workflows {
		if cfg.Workflows[i].Name == workflowName {
			workflow = &cfg.Workflows[i]
			break
		}
	}
	if workflow == nil {
		return nil, fmt.Errorf("workflow '%s' not found", workflowName)
	}

	// Resolve and validate input values
	inputValues, err := workflow.ResolveInputValues(inputs)
	if err != nil {
		return nil, fmt.Errorf("workflow '%s': %w", workflowName, err)
	}

	// Resolve datasets
	resolvedDatasets, err := resolveDatasets(workflow, inputValues)
	if err != nil {
		return nil, fmt.Errorf("workflow '%s': %w", workflowName, err)
	}

	r := &Runner{
		cfg:              cfg,
		configPath:       configPath,
		workflow:         workflow,
		varsValues:       cfg.ResolvedVars,
		inputValues:      inputValues,
		resolvedDatasets: resolvedDatasets,
		taskResults:      make(map[string]*TaskResult),
		taskSupervisors:  make(map[string]*agent.Supervisor),
		taskAgents:       make(map[string]map[string]*agent.Agent),
		knowledgeStore:   NewMemoryKnowledgeStore(),
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// resolveDatasets resolves all datasets to their actual values
func resolveDatasets(workflow *config.Workflow, inputValues map[string]cty.Value) (map[string][]cty.Value, error) {
	resolved := make(map[string][]cty.Value)

	for _, ds := range workflow.Datasets {
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
		} else if len(ds.Default) > 0 {
			// Use default values
			items = ds.Default
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

// Run executes the workflow
func (r *Runner) Run(ctx context.Context, streamer streamers.WorkflowHandler) error {
	streamer.WorkflowStarted(r.workflow.Name, len(r.workflow.Tasks))

	// Log workflow start event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventWorkflowStarted, map[string]any{
			"workflow":   r.workflow.Name,
			"task_count": len(r.workflow.Tasks),
		})
	}

	// Get tasks in topological order
	sortedTasks := r.workflow.TopologicalSort()

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
					return err
				}
			case <-ctx.Done():
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

				// Get dependency summaries for this task
				r.mu.RLock()
				var depSummaries []agent.DependencySummary
				for _, dep := range r.getDependencyChain(task.Name) {
					if result, ok := r.taskResults[dep]; ok && result.Success {
						depSummaries = append(depSummaries, agent.DependencySummary{
							TaskName: dep,
							Summary:  result.Summary,
						})
					}
				}
				r.mu.RUnlock()

				// Run the task (regular or iterated)
				var result *TaskResult
				var err error

				if task.Iterator != nil {
					result, err = r.runIteratedTask(ctx, task, depSummaries, streamer)
				} else {
					result, err = r.runTask(ctx, task, depSummaries, streamer)
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
			return err
		}
	}

	streamer.WorkflowCompleted(r.workflow.Name)

	// Log workflow completed event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventWorkflowCompleted, map[string]any{
			"workflow": r.workflow.Name,
		})
	}

	return nil
}

// runTask executes a single task with its supervisor
func (r *Runner) runTask(ctx context.Context, task config.Task, depSummaries []agent.DependencySummary, streamer streamers.WorkflowHandler) (*TaskResult, error) {
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

	streamer.TaskStarted(task.Name, objective)

	// Log task start event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventTaskStarted, map[string]any{
			"task":      task.Name,
			"objective": objective,
		})
	}

	// Get agents for this task (task-level or workflow-level)
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.workflow.Agents
	}

	// Build inherited agents from all dependency tasks in the lineage
	inheritedAgents := r.collectInheritedAgents(task.Name)

	// Collect dependency output schemas for the supervisor
	depOutputSchemas := r.collectDepOutputSchemas(task.Name)

	// Get task's own output schema if defined
	taskOutputSchema := r.getTaskOutputSchema(task)

	// Get debug file for supervisor if debug mode is enabled
	var debugFile string
	if r.debugLogger != nil {
		debugFile = r.debugLogger.GetMessageFile("supervisor", task.Name)
	}

	// Create supervisor for this task
	sup, err := agent.NewSupervisor(ctx, agent.SupervisorOptions{
		Config:           r.cfg,
		ConfigPath:       r.configPath,
		WorkflowName:     r.workflow.Name,
		TaskName:         task.Name,
		SupervisorModel:  r.workflow.SupervisorModel,
		AgentNames:       agents,
		DepSummaries:     depSummaries,
		DepOutputSchemas: depOutputSchemas,
		TaskOutputSchema: taskOutputSchema,
		InheritedAgents:  inheritedAgents,
		DebugFile:        debugFile,
	})
	if err != nil {
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.SupervisorToolCallbacks{
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
	}, depSummaries)

	// Create task-specific streamer adapter
	taskStreamer := &supervisorStreamerAdapter{
		taskName: task.Name,
		streamer: streamer,
	}

	// Execute the task
	summary, err := sup.ExecuteTask(ctx, objective, taskStreamer)
	if err != nil {
		sup.Close()
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Store supervisor's completed agents for inheritance by dependent tasks
	r.mu.Lock()
	r.taskSupervisors[task.Name] = sup
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

	streamer.TaskCompleted(task.Name, cleanSummary)
	return &TaskResult{
		TaskName: task.Name,
		Summary:  cleanSummary,
		Success:  true,
	}, nil
}

// getDependencyChain returns all tasks this task depends on (including transitive dependencies)
func (r *Runner) getDependencyChain(taskName string) []string {
	task := r.workflow.GetTaskByName(taskName)
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

		depTask := r.workflow.GetTaskByName(dep)
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
		task := r.workflow.GetTaskByName(depTaskName)
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

// supervisorStreamerAdapter adapts WorkflowHandler to agent.SupervisorStreamer
type supervisorStreamerAdapter struct {
	taskName string
	streamer streamers.WorkflowHandler
}

func (s *supervisorStreamerAdapter) Reasoning(content string) {
	s.streamer.SupervisorReasoning(s.taskName, content)
}

func (s *supervisorStreamerAdapter) Answer(content string) {
	s.streamer.SupervisorAnswer(s.taskName, content)
}

func (s *supervisorStreamerAdapter) CallingTool(name, input string) {
	s.streamer.SupervisorCallingTool(s.taskName, name, input)
}

func (s *supervisorStreamerAdapter) ToolComplete(name string) {
	s.streamer.SupervisorToolComplete(s.taskName, name)
}

// runIteratedTask executes a task that iterates over a dataset
func (r *Runner) runIteratedTask(ctx context.Context, task config.Task, depSummaries []agent.DependencySummary, streamer streamers.WorkflowHandler) (*TaskResult, error) {
	datasetName := task.Iterator.Dataset
	items, ok := r.resolvedDatasets[datasetName]
	if !ok {
		return nil, fmt.Errorf("dataset '%s' not found", datasetName)
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

		return &TaskResult{
			TaskName: task.Name,
			Summary:  "No items to process",
			Success:  true,
		}, nil
	}

	// Notify workflow handler about iteration start
	streamer.TaskIterationStarted(task.Name, len(items), task.Iterator.Parallel)

	var iterations []IterationResult

	if task.Iterator.Parallel {
		// Parallel execution with fail-fast
		iterations = r.runParallelIterations(ctx, task, items, depSummaries, streamer)
	} else {
		// Sequential execution (no more rolling aggregation)
		iterations = r.runSequentialIterations(ctx, task, items, depSummaries, streamer)
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

	streamer.TaskIterationCompleted(task.Name, len(iterations), summary)
	streamer.TaskCompleted(task.Name, summary)

	return &TaskResult{
		TaskName: task.Name,
		Summary:  summary,
		Success:  true,
	}, nil
}

// runSequentialIterations runs iterations one at a time with retry support
func (r *Runner) runSequentialIterations(ctx context.Context, task config.Task, items []cty.Value, depSummaries []agent.DependencySummary, streamer streamers.WorkflowHandler) []IterationResult {
	var iterations []IterationResult
	var prevOutput map[string]any    // Track previous iteration's output for context passing
	var prevLearnings map[string]any // Track previous iteration's learnings for context passing
	maxRetries := 0
	if task.Iterator != nil {
		maxRetries = task.Iterator.MaxRetries
	}

	for i, item := range items {
		select {
		case <-ctx.Done():
			iterations = append(iterations, IterationResult{
				Index:   i,
				ItemID:  getItemID(item, i),
				Success: false,
				Error:   ctx.Err(),
			})
			return iterations
		default:
		}

		// Run with retries
		var result IterationResult
		retryLearnings := prevLearnings // Start with learnings from previous iteration
		for attempt := 0; attempt <= maxRetries; attempt++ {
			result = r.runSingleIteration(ctx, task, i, item, prevOutput, retryLearnings, depSummaries, streamer)
			if result.Success {
				break
			}

			// Accumulate learnings from failed attempt for the retry
			if result.Learnings != nil {
				retryLearnings = mergeLearnings(retryLearnings, result.Learnings)
			}

			// If we have retries remaining, log and retry
			if attempt < maxRetries {
				streamer.IterationRetrying(task.Name, i, attempt+1, maxRetries, result.Error)
			}
		}

		iterations = append(iterations, result)

		if !result.Success {
			// Fail-fast: stop on first failure (after all retries exhausted)
			return iterations
		}

		// Update prevOutput and prevLearnings for next iteration (only on success)
		prevOutput = result.Output
		prevLearnings = result.Learnings
	}

	return iterations
}

// runParallelIterations runs all iterations in parallel with retry support
func (r *Runner) runParallelIterations(ctx context.Context, task config.Task, items []cty.Value, depSummaries []agent.DependencySummary, streamer streamers.WorkflowHandler) []IterationResult {
	iterations := make([]IterationResult, len(items))
	maxRetries := 0
	if task.Iterator != nil {
		maxRetries = task.Iterator.MaxRetries
	}

	var wg sync.WaitGroup

	for i, item := range items {
		i, item := i, item // capture
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Run with retries
			var result IterationResult
			for attempt := 0; attempt <= maxRetries; attempt++ {
				select {
				case <-ctx.Done():
					iterations[i] = IterationResult{
						Index:   i,
						ItemID:  getItemID(item, i),
						Success: false,
						Error:   ctx.Err(),
					}
					return
				default:
				}

				// Pass nil for prevOutput and prevLearnings in parallel iterations (no meaningful ordering)
				result = r.runSingleIteration(ctx, task, i, item, nil, nil, depSummaries, streamer)
				if result.Success {
					break
				}

				// If we have retries remaining, log and retry
				if attempt < maxRetries {
					streamer.IterationRetrying(task.Name, i, attempt+1, maxRetries, result.Error)
				}
			}

			iterations[i] = result
		}()
	}

	wg.Wait()
	return iterations
}

// runSingleIteration executes a single iteration of an iterated task
func (r *Runner) runSingleIteration(ctx context.Context, task config.Task, index int, item cty.Value, prevOutput map[string]any, prevLearnings map[string]any, depSummaries []agent.DependencySummary, streamer streamers.WorkflowHandler) IterationResult {
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
		agents = r.workflow.Agents
	}

	// Build inherited agents from all dependency tasks in the lineage
	inheritedAgents := r.collectInheritedAgents(task.Name)

	// Collect dependency output schemas for the supervisor
	depOutputSchemas := r.collectDepOutputSchemas(task.Name)

	// Get task's own output schema if defined
	taskOutputSchema := r.getTaskOutputSchema(task)

	// Get debug file for supervisor if debug mode is enabled
	iterTaskName := fmt.Sprintf("%s[%d]", task.Name, index)
	var debugFile string
	if r.debugLogger != nil {
		debugFile = r.debugLogger.GetMessageFile("supervisor", iterTaskName)
	}

	// Create supervisor for this iteration
	sup, err := agent.NewSupervisor(ctx, agent.SupervisorOptions{
		Config:                 r.cfg,
		ConfigPath:             r.configPath,
		WorkflowName:           r.workflow.Name,
		TaskName:               iterTaskName,
		SupervisorModel:        r.workflow.SupervisorModel,
		AgentNames:             agents,
		DepSummaries:           depSummaries,
		DepOutputSchemas:       depOutputSchemas,
		TaskOutputSchema:       taskOutputSchema,
		InheritedAgents:        inheritedAgents,
		PrevIterationOutput:    prevOutput,
		PrevIterationLearnings: prevLearnings,
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
	defer sup.Close()

	// Set up tool callbacks for iteration
	sup.SetToolCallbacks(&agent.SupervisorToolCallbacks{
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

// iterationStreamerAdapter adapts WorkflowHandler to agent.SupervisorStreamer for iterations
type iterationStreamerAdapter struct {
	taskName string
	index    int
	streamer streamers.WorkflowHandler
}

func (s *iterationStreamerAdapter) Reasoning(content string) {
	s.streamer.IterationReasoning(s.taskName, s.index, content)
}

func (s *iterationStreamerAdapter) Answer(content string) {
	s.streamer.IterationAnswer(s.taskName, s.index, content)
}

func (s *iterationStreamerAdapter) CallingTool(name, input string) {
	s.streamer.SupervisorCallingTool(fmt.Sprintf("%s[%d]", s.taskName, s.index), name, input)
}

func (s *iterationStreamerAdapter) ToolComplete(name string) {
	s.streamer.SupervisorToolComplete(fmt.Sprintf("%s[%d]", s.taskName, s.index), name)
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
	for i := range r.workflow.Datasets {
		if r.workflow.Datasets[i].Name == name {
			ds = &r.workflow.Datasets[i]
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
	for _, ds := range r.workflow.Datasets {
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
	task := r.workflow.GetTaskByName(taskName)
	if task == nil {
		return nil
	}
	return task.Output
}

// GetDependencyOutputInfo returns info about completed dependency task outputs
// for injection into supervisor prompts
func (r *Runner) GetDependencyOutputInfo(taskName string) []DependencyOutputInfo {
	var result []DependencyOutputInfo

	for _, depTaskName := range r.getDependencyChain(taskName) {
		task := r.workflow.GetTaskByName(depTaskName)
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

// DependencyOutputInfo describes a completed dependency task's output for the supervisor
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
// Knowledge Store Adapter - adapts workflow.MemoryKnowledgeStore to agent.KnowledgeStore
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
