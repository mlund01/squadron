package workflow

import (
	"context"
	"fmt"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"squad/agent"
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

	// Task state management
	mu              sync.RWMutex
	taskResults     map[string]*TaskResult       // Results from completed tasks
	taskSupervisors map[string]*agent.Supervisor // Active supervisors for each task
}

// TaskResult holds the outcome of a completed task
type TaskResult struct {
	TaskName string
	Summary  string
	Success  bool
	Error    error
}

// NewRunner creates a new workflow runner
func NewRunner(cfg *config.Config, configPath string, workflowName string, inputs map[string]string) (*Runner, error) {
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

	return &Runner{
		cfg:             cfg,
		configPath:      configPath,
		workflow:        workflow,
		varsValues:      cfg.ResolvedVars,
		inputValues:     inputValues,
		taskResults:     make(map[string]*TaskResult),
		taskSupervisors: make(map[string]*agent.Supervisor),
	}, nil
}

// Run executes the workflow
func (r *Runner) Run(ctx context.Context, streamer streamers.WorkflowHandler) error {
	streamer.WorkflowStarted(r.workflow.Name, len(r.workflow.Tasks))

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

				// Run the task
				result, err := r.runTask(ctx, task, depSummaries, streamer)
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

	// Clean up supervisors
	r.mu.Lock()
	for _, sup := range r.taskSupervisors {
		sup.Close()
	}
	r.mu.Unlock()

	streamer.WorkflowCompleted(r.workflow.Name)
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

	// Get agents for this task (task-level or workflow-level)
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.workflow.Agents
	}

	// Create supervisor for this task
	sup, err := agent.NewSupervisor(ctx, agent.SupervisorOptions{
		Config:          r.cfg,
		ConfigPath:      r.configPath,
		WorkflowName:    r.workflow.Name,
		TaskName:        task.Name,
		SupervisorModel: r.workflow.SupervisorModel,
		AgentNames:      agents,
		DepSummaries:    depSummaries,
	})
	if err != nil {
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Store supervisor for potential ask_supe calls
	r.mu.Lock()
	r.taskSupervisors[task.Name] = sup
	r.mu.Unlock()

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.SupervisorToolCallbacks{
		OnAgentStart: func(taskName, agentName string) {
			streamer.AgentStarted(taskName, agentName)
		},
		GetAgentHandler: func(taskName, agentName string) streamers.ChatHandler {
			return streamer.AgentHandler(taskName, agentName)
		},
		OnAgentComplete: func(taskName, agentName string) {
			streamer.AgentCompleted(taskName, agentName)
		},
		AskSupervisor: r.handleAskSupe,
	}, depSummaries)

	// Create task-specific streamer adapter
	taskStreamer := &supervisorStreamerAdapter{
		taskName: task.Name,
		streamer: streamer,
	}

	// Execute the task
	summary, err := sup.ExecuteTask(ctx, objective, taskStreamer)
	if err != nil {
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	streamer.TaskCompleted(task.Name, summary)
	return &TaskResult{
		TaskName: task.Name,
		Summary:  summary,
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

// handleAskSupe handles ask_supe tool calls from supervisors
func (r *Runner) handleAskSupe(ctx context.Context, taskName string, question string) (string, error) {
	r.mu.RLock()
	sup, ok := r.taskSupervisors[taskName]
	r.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("supervisor for task '%s' not found or task not yet completed", taskName)
	}

	return sup.AnswerQuestion(ctx, question)
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
