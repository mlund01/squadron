package mission

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/mlund01/squadron-wire/protocol"
	"github.com/zclconf/go-cty/cty"

	"squadron/agent"
	"squadron/aitools"
	"squadron/config"
	"squadron/llm"
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

	// Pricing overrides from config (API model name → pricing)
	pricingOverrides map[string]*llm.ModelPricing

	// Resolved datasets for iteration
	resolvedDatasets map[string][]cty.Value
	datasetIDs       map[string]string // dataset name → store ID
	missionID        string

	// Task state management
	mu                   sync.RWMutex
	taskCommanders      map[string]*agent.Commander             // Commanders for completed tasks (for ask_commander queries)
	iterationCommanders map[string]map[int]*agent.Commander     // Commanders for iterated tasks: taskName -> index -> commander
	taskSummaries       map[string]string                       // Push summaries from completed tasks (taskName -> summary)

	// Knowledge store for structured task outputs (reads from MissionStore)
	knowledgeStore KnowledgeStore

	// Persistent store bundle (missions, sessions, datasets, questions)
	stores *store.Bundle

	// Debug logging
	debugLogger *DebugLogger

	// Shared store for ask_commander questions across iterations
	askCommanderStore *askCommanderStore

	// Resume support
	resumeMissionID string            // Non-empty when resuming a prior mission
	rawInputs       map[string]string // Raw input strings for persistence/resume

	// Folder access for mission
	folderStore aitools.FolderStore

	// Conditional routing state
	routerPending []routerActivation   // queue of tasks activated by routers
	routerParents map[string]string    // taskName → routerTaskName that activated it

	// Cross-mission routing result (set when a router chooses a mission target)
	nextMission       string            // mission name to launch, or ""
	nextMissionInputs map[string]string // inputs for the next mission

	// Provider factory for testing — when set, commanders and agents use this instead of creating real providers
	providerFactory func() llm.Provider

	// Task state manager — single authority for task lifecycle
	stateMgr *TaskStateManager

	// Drain signal — closed when mission should gracefully stop
	drainCh   chan struct{}
	drainOnce sync.Once
}

// routerActivation represents a task activated by a router
type routerActivation struct {
	TaskName    string
	ActivatedBy string
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

// WithResume configures the runner to resume a previously failed mission
func WithResume(missionID string) RunnerOption {
	return func(r *Runner) {
		r.resumeMissionID = missionID
	}
}

// WithProviderFactory sets a factory function that creates LLM providers for commanders and agents.
// Used in tests to inject mock providers. The factory is called once per commander/agent.
func WithProviderFactory(factory func() llm.Provider) RunnerOption {
	return func(r *Runner) {
		r.providerFactory = factory
	}
}

// testProvider returns a provider from the factory if set, or nil (letting the commander/agent create its own).
func (r *Runner) testProvider() llm.Provider {
	if r.providerFactory != nil {
		return r.providerFactory()
	}
	return nil
}

// debugLoggerInterface returns the debug logger as an agent.DebugLogger interface,
// or nil if no logger is set. This avoids the Go pitfall where a typed nil pointer
// assigned to an interface creates a non-nil interface value.
func (r *Runner) debugLoggerInterface() agent.DebugLogger {
	if r.debugLogger == nil {
		return nil
	}
	return r.debugLogger
}

// TaskResult holds the outcome of a completed task
type TaskResult struct {
	TaskName       string
	Success        bool
	Error          error
	ChosenRoute    string            // non-empty if a route was chosen (task name or mission name)
	IsMissionRoute bool              // true if ChosenRoute is a mission name
	MissionInputs  map[string]string // inputs for the mission route (nil for task routes)
}

// IterationResult holds the outcome of a single iteration
type IterationResult struct {
	Index   int
	ItemID  string
	Output  map[string]any
	Success bool
	Error   error
}

// IteratedTaskResult holds the outcome of an iterated task
type IteratedTaskResult struct {
	TaskName   string
	Iterations []IterationResult
	AllSuccess bool
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

	// Create store bundle
	stores, err := store.NewBundle(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("mission '%s': init stores: %w", missionName, err)
	}

	r := &Runner{
		cfg:                  cfg,
		configPath:           configPath,
		mission:             mission,
		varsValues:           cfg.ResolvedVars,
		rawInputs:            inputs,
		datasetIDs:           make(map[string]string),
		taskCommanders:      make(map[string]*agent.Commander),
		taskSummaries:       make(map[string]string),
		iterationCommanders: make(map[string]map[int]*agent.Commander),
		stores:               stores,
		askCommanderStore: &askCommanderStore{
			questions: make(map[string][]*questionEntry),
		},
		routerParents: make(map[string]string),
		drainCh:       make(chan struct{}),
	}

	// Apply options (must happen before input/dataset resolution so resumeMissionID is set)
	for _, opt := range opts {
		opt(r)
	}

	// Build pricing overrides from model config
	configOverrides := config.BuildPricingOverrides(cfg.Models)
	if len(configOverrides) > 0 {
		r.pricingOverrides = make(map[string]*llm.ModelPricing, len(configOverrides))
		for apiName, pc := range configOverrides {
			r.pricingOverrides[apiName] = &llm.ModelPricing{
				Input:      pc.Input,
				Output:     pc.Output,
				CacheRead:  pc.CacheRead,
				CacheWrite: pc.CacheWrite,
			}
		}
	}

	// When resuming, skip input/dataset resolution — they'll be loaded from the store in Run()
	if r.resumeMissionID == "" {
		// Resolve and validate input values
		inputValues, err := mission.ResolveInputValues(inputs)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': %w", missionName, err)
		}
		r.inputValues = inputValues

		// Resolve datasets
		resolvedDatasets, err := resolveDatasets(mission, inputValues)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': %w", missionName, err)
		}
		r.resolvedDatasets = resolvedDatasets

		// Resolve secrets from inputs with protected=true
		secretValues := make(map[string]string)
		var secretInfos []agent.SecretInfo
		for _, input := range mission.Inputs {
			if !input.Protected {
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
		r.secretValues = secretValues
		r.secretInfos = secretInfos

		// Build folder store from mission config
		folderStore, err := buildFolderStore(mission, cfg.SharedFolders)
		if err != nil {
			return nil, fmt.Errorf("mission '%s': %w", missionName, err)
		}
		r.folderStore = folderStore
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

// EventStore returns the runner's event store for use by decorators like StoringMissionHandler.
func (r *Runner) EventStore() store.EventStore {
	return r.stores.Events
}

// CostStore returns the runner's cost store for turn cost persistence.
func (r *Runner) CostStore() store.CostStore {
	return r.stores.Costs
}

// CloseStores closes the underlying data stores. Call after Run returns and all events are flushed.
func (r *Runner) CloseStores() {
	r.stores.Close()
}

// Drain signals the mission to stop gracefully. Running tasks finish their current
// atomic operation (LLM call or tool call) and then transition to Stopped.
func (r *Runner) Drain() {
	r.drainOnce.Do(func() { close(r.drainCh) })
}

// IsDraining returns true if a drain signal has been sent.
func (r *Runner) IsDraining() bool {
	select {
	case <-r.drainCh:
		return true
	default:
		return false
	}
}

// DrainCh returns the drain signal channel for select statements.
func (r *Runner) DrainCh() <-chan struct{} {
	return r.drainCh
}

// NextMission returns the mission name to launch as a result of cross-mission routing, or "".
func (r *Runner) NextMission() string {
	return r.nextMission
}

// NextMissionInputs returns the inputs for the next mission to launch, or nil.
func (r *Runner) NextMissionInputs() map[string]string {
	return r.nextMissionInputs
}

// Run executes the mission.
// The caller is responsible for closing r.stores after Run returns and all events are flushed.
func (r *Runner) Run(ctx context.Context, streamer streamers.MissionHandler) error {

	var missionID string
	stateStore := newTaskStateStore(r.stores.Missions)

	// Track existing task IDs from prior run (for resume)
	existingTaskIDs := make(map[string]string) // taskName → taskID

	var stateMgr *TaskStateManager

	if r.resumeMissionID != "" {
		// === RESUME PATH ===
		missionID = r.resumeMissionID
		r.missionID = missionID
		stateMgr = NewTaskStateManager(missionID, stateStore)
		r.stateMgr = stateMgr

		// Validate mission exists and matches
		record, err := r.stores.Missions.GetMission(missionID)
		if err != nil {
			return fmt.Errorf("resume: mission '%s' not found in store: %w", missionID, err)
		}
		if record.MissionName != r.mission.Name {
			return fmt.Errorf("resume: mission name mismatch: store has '%s', config has '%s'", record.MissionName, r.mission.Name)
		}
		if record.Status == "completed" {
			return fmt.Errorf("resume: mission '%s' is already completed", missionID)
		}

		// Load raw inputs from store and re-resolve
		var rawInputs map[string]string
		if err := json.Unmarshal([]byte(record.InputValuesJSON), &rawInputs); err != nil {
			return fmt.Errorf("resume: parsing stored inputs: %w", err)
		}
		inputValues, err := r.mission.ResolveInputValues(rawInputs)
		if err != nil {
			return fmt.Errorf("resume: resolving inputs: %w", err)
		}
		r.inputValues = inputValues

		// Re-resolve secrets
		r.secretValues = make(map[string]string)
		for _, input := range r.mission.Inputs {
			if !input.Protected {
				continue
			}
			if input.Value != nil && input.Value.Type() == cty.String {
				r.secretValues[input.Name] = input.Value.AsString()
			}
			r.secretInfos = append(r.secretInfos, agent.SecretInfo{
				Name:        input.Name,
				Description: input.Description,
			})
		}

		// Initialize store-backed knowledge store
		r.knowledgeStore = &PersistentKnowledgeStore{MissionID: missionID, Store: r.stores.Missions}

		// Load dataset IDs from store
		for _, ds := range r.mission.Datasets {
			dsID, err := r.stores.Datasets.GetDatasetByName(missionID, ds.Name)
			if err != nil {
				return fmt.Errorf("resume: dataset '%s' not found in store: %w", ds.Name, err)
			}
			r.datasetIDs[ds.Name] = dsID
		}

		// Identify completed and interrupted tasks
		tasks, err := r.stores.Missions.GetTasksByMission(missionID)
		if err != nil {
			return fmt.Errorf("resume: loading tasks: %w", err)
		}
		for _, t := range tasks {
			existingTaskIDs[t.TaskName] = t.ID
			// Register with actual DB status so CAS transitions match
			switch t.Status {
			case "completed":
				stateMgr.RegisterTask(t.TaskName, t.ID, TaskCompleted)
			case "stopped":
				stateMgr.RegisterTask(t.TaskName, t.ID, TaskStopped)
			case "failed":
				stateMgr.RegisterTask(t.TaskName, t.ID, TaskFailed)
			case "running":
				// Was running when process died — treat as stopped
				stateMgr.RegisterTask(t.TaskName, t.ID, TaskStopped)
				r.stores.Missions.UpdateTaskStatus(t.ID, "stopped", nil, nil)
			default:
				stateMgr.RegisterTask(t.TaskName, t.ID, TaskPending)
			}
		}

		// Load route decisions to reconstruct router state
		routeDecisions, err := r.stores.Missions.GetRouteDecisions(missionID)
		if err != nil {
			return fmt.Errorf("resume: loading route decisions: %w", err)
		}
		for _, rd := range routeDecisions {
			r.routerParents[rd.TargetTask] = rd.RouterTask
			// If the routed-to task hasn't completed yet, re-queue it
			if !stateMgr.IsCompleted(rd.TargetTask) {
				r.routerPending = append(r.routerPending, routerActivation{
					TaskName:    rd.TargetTask,
					ActivatedBy: rd.RouterTask,
				})
			}
		}

		// Resaturate commanders for completed tasks (topological order)
		sortedTasks := r.mission.TopologicalSort()
		var completedNames []string
		for _, t := range sortedTasks {
			if stateMgr.IsCompleted(t.Name) {
				completedNames = append(completedNames, t.Name)
			}
		}
		if err := r.resaturateCommanders(ctx, completedNames); err != nil {
			return fmt.Errorf("resume: resaturating commanders: %w", err)
		}

		stateMgr.missionState = MissionStopped // resume from stopped
		_ = stateMgr.TransitionMission(MissionRunning)
	} else {
		// === FRESH PATH ===
		rawInputsJSON, _ := json.Marshal(r.rawInputs)
		configJSON, _ := json.Marshal(r.missionSnapshot())
		var err error
		missionID, err = r.stores.Missions.CreateMission(r.mission.Name, string(rawInputsJSON), string(configJSON))
		if err != nil {
			return fmt.Errorf("create mission record: %w", err)
		}
		r.missionID = missionID
		stateMgr = NewTaskStateManager(missionID, stateStore)
		stateMgr.missionState = MissionRunning // DB creates missions as 'running'
		r.stateMgr = stateMgr

		// Initialize store-backed knowledge store
		r.knowledgeStore = &PersistentKnowledgeStore{MissionID: missionID, Store: r.stores.Missions}

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

		// Free in-memory datasets — the store is now the source of truth
		r.resolvedDatasets = nil
	}

	streamer.MissionStarted(r.mission.Name, missionID, len(r.mission.Tasks))

	// Log mission start event
	if r.debugLogger != nil {
		r.debugLogger.LogEvent(EventMissionStarted, map[string]any{
			"mission":    r.mission.Name,
			"mission_id": missionID,
			"task_count": len(r.mission.Tasks),
			"resumed":    r.resumeMissionID != "",
		})
	}

	// Get tasks in topological order, excluding router-only tasks
	allSorted := r.mission.TopologicalSort()
	var sortedTasks []config.Task
	for _, t := range allSorted {
		if !r.mission.IsRouterOnlyTask(t.Name) {
			sortedTasks = append(sortedTasks, t)
		}
	}

	// Create a wait group for all tasks
	var wg sync.WaitGroup

	// Error channel to collect errors from goroutines
	errChan := make(chan error, len(sortedTasks))

	// Register static tasks that aren't already registered (fresh run)
	for _, t := range sortedTasks {
		if _, ok := stateMgr.GetTaskState(t.Name); !ok {
			stateMgr.RegisterTask(t.Name, "", TaskPending)
		}
	}
	// Include already-activated router targets (from resume or prior route decisions)
	for _, activation := range r.routerPending {
		if _, ok := stateMgr.GetTaskState(activation.TaskName); !ok {
			stateMgr.RegisterTask(activation.TaskName, "", TaskPending)
		}
	}

	// isLoopDone returns true when all registered tasks are completed and no router-activated tasks are pending
	isLoopDone := func() bool {
		return stateMgr.AllCompleted() && !stateMgr.AnyInFlight() && len(r.routerPending) == 0
	}

	// Process tasks, launching parallel tasks when their dependencies are met
	for !isLoopDone() {
		// Check for drain signal — wait for in-flight tasks then stop gracefully
		select {
		case <-r.drainCh:
			stateMgr.StopAll()
			wg.Wait()
			r.stores.Missions.UpdateMissionStatus(missionID, "stopped")
			stateMgr.missionState = MissionStopped
			return fmt.Errorf("mission stopped")
		case <-ctx.Done():
			stateMgr.StopAll()
			wg.Wait()
			r.stores.Missions.UpdateMissionStatus(missionID, "stopped")
			stateMgr.missionState = MissionStopped
			return ctx.Err()
		default:
		}

		// Find tasks that are ready to run (all dependencies completed)
		var readyTasks []config.Task
		for _, task := range sortedTasks {
			if stateMgr.IsCompleted(task.Name) || stateMgr.IsInFlight(task.Name) {
				continue
			}

			// Check if all dependencies are completed
			depsReady := true
			for _, dep := range task.DependsOn {
				if !stateMgr.IsCompleted(dep) {
					depsReady = false
					break
				}
			}

			if depsReady {
				readyTasks = append(readyTasks, task)
			}
		}

		// Also check router-activated tasks
		var pendingCopy []routerActivation
		pendingCopy = append(pendingCopy, r.routerPending...)
		r.routerPending = nil
		for _, activation := range pendingCopy {
			if stateMgr.IsCompleted(activation.TaskName) || stateMgr.IsInFlight(activation.TaskName) {
				continue
			}
			task := r.mission.GetTaskByName(activation.TaskName)
			if task != nil {
				readyTasks = append(readyTasks, *task)
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
			case <-r.drainCh:
				stateMgr.StopAll()
				wg.Wait()
				r.stores.Missions.UpdateMissionStatus(missionID, "stopped")
				stateMgr.missionState = MissionStopped
				return fmt.Errorf("mission stopped")
			case <-ctx.Done():
				stateMgr.StopAll()
				wg.Wait()
				r.stores.Missions.UpdateMissionStatus(missionID, "stopped")
				stateMgr.missionState = MissionStopped
				return ctx.Err()
			}
			continue
		}

		// Launch all ready tasks in parallel
		for _, task := range readyTasks {
			task := task // capture for goroutine

			// Transition: pending → ready → running
			// If any transition fails (e.g. already running), skip this task
			if err := stateMgr.TransitionTask(task.Name, TaskReady, nil, nil); err != nil {
				continue
			}
			if err := stateMgr.TransitionTask(task.Name, TaskRunning, nil, nil); err != nil {
				continue
			}

			wg.Add(1)
			go func() {
				defer wg.Done()

				// Run the task (regular or iterated)
				// Each task queries its ancestors internally using the pull model
				var result *TaskResult
				var err error

				existingTaskID := existingTaskIDs[task.Name]
				if task.Iterator != nil {
					result, err = r.runIteratedTask(ctx, task, missionID, existingTaskID, streamer)
				} else {
					result, err = r.runTask(ctx, task, missionID, existingTaskID, streamer)
				}

				if err != nil {
					if ctx.Err() != nil {
						// Mission was stopped — mark task as stopped
						stateMgr.ForceState(task.Name, TaskStopped)
						if tid := stateMgr.GetTaskID(task.Name); tid != "" {
							r.stores.Missions.UpdateTaskStatus(tid, "stopped", nil, nil)
						}
						errChan <- ctx.Err()
					} else {
						stateMgr.ForceState(task.Name, TaskFailed)
						errChan <- fmt.Errorf("task '%s' failed: %w", task.Name, err)
					}
					return
				}

				// Handle route activation
				if task.Router != nil && (result.ChosenRoute == "" || result.ChosenRoute == "none") {
					// Router chose "none" — emit event so UI can show terminal state
					streamer.RouteChosen(task.Name, "none", "No route applies", false)
					if r.debugLogger != nil {
						r.debugLogger.LogEvent(EventRouteChosen, map[string]any{
							"router_task": task.Name,
							"target_task": "none",
							"condition":   "No route applies",
						})
					}
				}
				if result.ChosenRoute != "" && result.ChosenRoute != "none" {
					// Find the route condition for the event
					var condition string
					if task.Router != nil {
						for _, route := range task.Router.Routes {
							if route.Target == result.ChosenRoute {
								condition = route.Condition
								break
							}
						}
					}

					streamer.RouteChosen(task.Name, result.ChosenRoute, condition, result.IsMissionRoute)
					if r.debugLogger != nil {
						r.debugLogger.LogEvent(EventRouteChosen, map[string]any{
							"router_task": task.Name,
							"target_task": result.ChosenRoute,
							"condition":   condition,
							"is_mission":  result.IsMissionRoute,
						})
					}

					// Persist the route decision
					r.stores.Missions.StoreRouteDecision(missionID, task.Name, result.ChosenRoute, condition)

					if result.IsMissionRoute {
						// Cross-mission route: store the target for the caller to launch
						r.nextMission = result.ChosenRoute
						r.nextMissionInputs = result.MissionInputs
					} else {
						// Local task route: activate the target task
						// First-one-wins: only activate if target isn't already completed or in-flight
						alreadyActive := stateMgr.IsCompleted(result.ChosenRoute) || stateMgr.IsInFlight(result.ChosenRoute)

						if !alreadyActive {
							// Record the router parent and activate the target task
							r.routerParents[result.ChosenRoute] = task.Name
							r.routerPending = append(r.routerPending, routerActivation{
								TaskName:    result.ChosenRoute,
								ActivatedBy: task.Name,
							})
							if _, ok := stateMgr.GetTaskState(result.ChosenRoute); !ok {
								stateMgr.RegisterTask(result.ChosenRoute, "", TaskPending)
							}
						}
					}
				}

				// Handle send_to activation (unconditional push to targets)
				if len(task.SendTo) > 0 {
					for _, target := range task.SendTo {
						streamer.RouteChosen(task.Name, target, "send_to", false)
						if r.debugLogger != nil {
							r.debugLogger.LogEvent(EventRouteChosen, map[string]any{
								"router_task": task.Name,
								"target_task": target,
								"condition":   "send_to",
							})
						}

						r.stores.Missions.StoreRouteDecision(missionID, task.Name, target, "send_to")

						// First-one-wins: only activate if target isn't already completed or in-flight
						alreadyActive := stateMgr.IsCompleted(target) || stateMgr.IsInFlight(target)

						if !alreadyActive {
							r.routerParents[target] = task.Name
							r.routerPending = append(r.routerPending, routerActivation{
								TaskName:    target,
								ActivatedBy: task.Name,
							})
							if _, ok := stateMgr.GetTaskState(target); !ok {
								stateMgr.RegisterTask(target, "", TaskPending)
							}
						}
					}
				}

				// Update in-memory state (DB already updated by runTask/runIteratedTask)
				stateMgr.ForceState(task.Name, TaskCompleted)
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
			_ = stateMgr.TransitionMission(MissionFailed)
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

// resaturateCommanders rebuilds live commanders from stored session messages for completed tasks.
// This allows resumed missions to have fully functional commanders that can answer queries
// from dependent tasks via ask_commander, CloneForQuery, and agent inheritance.
func (r *Runner) resaturateCommanders(ctx context.Context, completedTaskNames []string) error {
	for _, taskName := range completedTaskNames {
		task := r.mission.GetTaskByName(taskName)
		if task == nil {
			continue
		}

		taskRecord, err := r.stores.Missions.GetTaskByName(r.missionID, taskName)
		if err != nil {
			return fmt.Errorf("loading task record for '%s': %w", taskName, err)
		}

		// Find sessions for this task
		sessions, err := r.stores.Sessions.GetSessionsByTask(taskRecord.ID)
		if err != nil {
			return fmt.Errorf("loading sessions for task '%s': %w", taskName, err)
		}

		// Collect agent sessions for reconstruction
		agentSessions := map[string]*store.SessionInfo{} // agentName → session
		hasCommander := false
		for i, s := range sessions {
			if s.Role == "commander" {
				hasCommander = true
			} else if s.Role == "agent" {
				agentSessions[s.AgentName] = &sessions[i]
			}
		}

		if !hasCommander {
			continue // No session stored — can't resaturate
		}

		// Resolve objective for depSummaries
		objective, err := task.ResolvedObjective(r.varsValues, r.inputValues)
		if err != nil {
			return fmt.Errorf("resolving objective for '%s': %w", taskName, err)
		}

		// Query already-resaturated ancestors for context
		depSummaries, err := r.queryAncestorsForContext(ctx, taskName, objective)
		if err != nil {
			// Fallback: use stored summary if ancestor query fails
			depSummaries = nil
		}

		depOutputSchemas := r.collectDepOutputSchemas(taskName)
		taskOutputSchema := r.getTaskOutputSchema(*task)

		agents := task.Agents
		if len(agents) == 0 {
			agents = r.mission.Agents
		}

		// Determine if this was an iterated task
		isIterated := task.Iterator != nil

		// Create commander with same config (gets correct system prompts, tools, provider)
		sup, err := agent.NewCommander(ctx, agent.CommanderOptions{
			Config:           r.cfg,
			ConfigPath:       r.configPath,
			MissionName:     r.mission.Name,
			TaskName:         taskName,
			Commander:  r.mission.Commander.Model,
			AgentNames:       agents,
			DepSummaries:     depSummaries,
			DepOutputSchemas: depOutputSchemas,
			TaskOutputSchema: taskOutputSchema,
			SecretInfos:      r.secretInfos,
			SecretValues:     r.secretValues,
			IsIteration:         isIterated,
			FolderStore:         r.folderStore,
			Compaction:          r.commanderCompaction(),
			PruneOn:             r.commanderPruneOn(),
			PruneTo:             r.commanderPruneTo(),
			ToolResponseMaxSize: r.mission.Commander.GetToolResponseMaxBytes(),
			PricingOverrides:    r.pricingOverrides,
			MissionLocalAgents:  r.mission.LocalAgents,
			Provider:            r.testProvider(),
		})
		if err != nil {
			return fmt.Errorf("creating commander for resaturation of '%s': %w", taskName, err)
		}

		// Load stored session messages into the commander
		existingSessionID := r.findAndLoadExistingSession(sup, taskRecord.ID, nil)

		// Set up minimal callbacks (needed for ask_agent, ask_commander on the resaturated commander)
		sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
			DatasetStore:   r,
			KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
			GetCommanderForQuery: func(depTaskName string, iterationIndex int) (*agent.Commander, error) {
				return r.getCommanderForQuery(depTaskName, iterationIndex, taskName)
			},
			ListCommanderQuestions: func(depTaskName string) []string {
				return r.listCommanderQuestions(depTaskName)
			},
			GetCommanderAnswer: func(depTaskName string, index int) (string, error) {
				return r.getCommanderAnswer(depTaskName, index)
			},
			AskCommanderWithCache: func(targetTask string, iterationIndex int, question string) (string, error) {
				return r.askCommanderWithCache(ctx, targetTask, iterationIndex, taskName, question)
			},
			SessionLogger:     r.stores.Sessions,
			TaskID:            taskRecord.ID,
			ExistingSessionID: existingSessionID,
		}, depSummaries)

		// Reconstruct completed agents from stored sessions
		for agentName, agentSess := range agentSessions {
			agentMsgs, err := r.stores.Sessions.GetMessages(agentSess.ID)
			if err != nil {
				continue // Non-fatal: skip agent if messages can't be loaded
			}
			var agentLLMMsgs []llm.Message
			for _, m := range agentMsgs {
				agentLLMMsgs = append(agentLLMMsgs, llm.Message{
					Role:    llm.Role(m.Role),
					Content: m.Content,
				})
			}
			restoredAgent, err := agent.RestoreAgent(ctx, agent.Options{
				ConfigPath: r.configPath,
				Config:     r.cfg,
				AgentName:  agentName,
				SecretInfos: r.secretInfos,
				SecretValues: r.secretValues,
			}, agentLLMMsgs)
			if err != nil {
				continue // Non-fatal: skip agent if it can't be restored
			}
			sup.AddRestoredAgent(agentName, restoredAgent)
		}

		// Store in runner maps
		r.mu.Lock()
		if isIterated {
			if r.iterationCommanders[taskName] == nil {
				r.iterationCommanders[taskName] = make(map[int]*agent.Commander)
			}
			r.iterationCommanders[taskName][0] = sup
		} else {
			r.taskCommanders[taskName] = sup
			if summary := sup.TaskSummary(); summary != "" {
				r.taskSummaries[taskName] = summary
			}
		}
		r.mu.Unlock()
	}

	return nil
}

// findAndLoadExistingSession checks the store for a prior commander session matching
// the given taskID and iterationIndex. If found, loads the stored messages into the
// commander's LLM session and returns the session ID for reuse.
// Returns "" if no existing session is found.
func (r *Runner) findAndLoadExistingSession(sup *agent.Commander, taskID string, iterationIndex *int) string {
	sessions, err := r.stores.Sessions.GetSessionsByTask(taskID)
	if err != nil || len(sessions) == 0 {
		return ""
	}
	for _, s := range sessions {
		if s.Role == "commander" && intPtrEqual(s.IterationIndex, iterationIndex) {
			msgs, err := r.stores.Sessions.GetMessages(s.ID)
			if err != nil || len(msgs) == 0 {
				return ""
			}
			var llmMsgs []llm.Message
			for _, m := range msgs {
				llmMsgs = append(llmMsgs, llm.Message{
					Role:    llm.Role(m.Role),
					Content: m.Content,
				})
			}
			sup.LoadSessionMessages(llmMsgs)
			return s.ID
		}
	}
	return ""
}

// restoreAgentSessions finds stored agent sessions for the given taskID and restores
// them into the commander. Completed agents go into completedAgents (for ask_agent),
// running/interrupted agents go into agentSessions (for call_agent to reuse).
// iterationIndex filters to a specific iteration (nil matches sessions with no iteration).
// Must be called AFTER SetToolCallbacks (needs sessionLogger to be wired up).
func (r *Runner) restoreAgentSessions(ctx context.Context, sup *agent.Commander, taskID string, iterationIndex *int) {
	sessions, err := r.stores.Sessions.GetSessionsByTask(taskID)
	if err != nil {
		return
	}
	for _, s := range sessions {
		if s.Role != "agent" || s.AgentName == "" {
			continue
		}
		if !intPtrEqual(s.IterationIndex, iterationIndex) {
			continue
		}
		msgs, err := r.stores.Sessions.GetMessages(s.ID)
		if err != nil || len(msgs) == 0 {
			continue
		}
		var llmMsgs []llm.Message
		for _, m := range msgs {
			llmMsgs = append(llmMsgs, llm.Message{
				Role:    llm.Role(m.Role),
				Content: m.Content,
			})
		}
		// Heal agent messages before loading: if last message is assistant with ACTION,
		// the tool call was interrupted — inject a placeholder observation.
		llmMsgs = agent.HealSessionMessages(llmMsgs)
		mode := config.ModeMission
		restoredAgent, err := agent.RestoreAgent(ctx, agent.Options{
			ConfigPath:   r.configPath,
			Config:       r.cfg,
			AgentName:    s.AgentName,
			Mode:         &mode,
			SecretInfos:  r.secretInfos,
			SecretValues: r.secretValues,
		}, llmMsgs)
		if err != nil {
			continue
		}
		if s.Status == "completed" {
			sup.AddRestoredAgent(s.AgentName, restoredAgent)
		} else {
			// Running or interrupted — add as active so call_agent reuses it
			sup.AddRestoredActiveAgent(s.AgentName, restoredAgent, s.ID)
		}
	}
}

func intPtrEqual(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// runTask executes a single task with its commander
func (r *Runner) runTask(ctx context.Context, task config.Task, missionID string, existingTaskID string, streamer streamers.MissionHandler) (*TaskResult, error) {
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

	// Create or reuse task record in store
	var taskID string
	if existingTaskID != "" {
		// Resume: reuse existing task record
		taskID = existingTaskID
	} else {
		taskConfigJSON, _ := json.Marshal(taskSnapshot(task, objective))
		taskID, _ = r.stores.Missions.CreateTask(missionID, task.Name, string(taskConfigJSON))
	}
	if reg, ok := streamer.(streamers.IDRegistrar); ok {
		reg.SetTaskID(task.Name, taskID)
	}
	if r.stateMgr != nil {
		r.stateMgr.SetTaskID(task.Name, taskID)
	}
	r.stores.Missions.UpdateTaskStatus(taskID, "running", nil, nil)

	// Store resolved task input (non-iterated = single input, no iteration index)
	r.stores.Missions.StoreTaskInput(taskID, nil, objective)

	// Helper to update task status on completion/failure
	updateTaskDone := func(success bool, outputJSON, errMsg *string) {
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
		Config:              r.cfg,
		ConfigPath:          r.configPath,
		MissionName:         r.mission.Name,
		TaskName:            task.Name,
		Commander:            r.mission.Commander.Model,
		AgentNames:          agents,
		DepSummaries:        depSummaries,
		DepOutputSchemas:    depOutputSchemas,
		TaskOutputSchema:    taskOutputSchema,
		SecretInfos:         r.secretInfos,
		SecretValues:        r.secretValues,
		IsIteration:         false,
		DebugFile:           debugFile,
		FolderStore:         r.folderStore,
		Compaction:          r.commanderCompaction(),
		PruneOn:             r.commanderPruneOn(),
		PruneTo:             r.commanderPruneTo(),
		Routes:              r.routeOptionsForTask(task),
		ToolResponseMaxSize: r.mission.Commander.GetToolResponseMaxBytes(),
		PricingOverrides:    r.pricingOverrides,
		MissionLocalAgents:  r.mission.LocalAgents,
		Provider:            r.testProvider(),
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

	// Check for existing session state (finds stored session from prior run if any)
	existingSessionID := r.findAndLoadExistingSession(sup, taskID, nil)

	// Track commander session ID for subtask callbacks
	var cmdSessionID string
	if existingSessionID != "" {
		cmdSessionID = existingSessionID
	}

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName, instruction string) {
			streamer.AgentStarted(taskName, agentName, instruction)
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
		OnAgentCompaction:  agentCompactionCallback(streamer),
		OnAgentSessionTurn: agentSessionTurnCallback(streamer),
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLoggerInterface(),
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
		OnSubmitOutput: func(index int, output map[string]any) {
			outputJSON, _ := json.Marshal(output)
			r.stores.Missions.StoreTaskOutput(taskID, nil, nil, nil, string(outputJSON))
		},
		SessionLogger:     r.stores.Sessions,
		TaskID:            taskID,
		ExistingSessionID: existingSessionID,
		OnSessionCreated: func(taskName, agentName, sessionID string) {
			if agentName == "commander" {
				cmdSessionID = sessionID
			}
			if reg, ok := streamer.(streamers.IDRegistrar); ok {
				reg.SetSessionID(taskName, agentName, sessionID)
			}
		},
		SetSubtasks: func(titles []string) error {
			return r.stores.Missions.SetSubtasks(taskID, cmdSessionID, nil, titles)
		},
		GetSubtasks: func() ([]store.Subtask, error) {
			return r.stores.Missions.GetSubtasks(taskID, cmdSessionID, nil)
		},
		CompleteSubtask: func() error {
			return r.stores.Missions.CompleteSubtask(taskID, cmdSessionID, nil)
		},
	}, depSummaries)

	// Restore any agent sessions from the store (so call_agent reuses them)
	r.restoreAgentSessions(ctx, sup, taskID, nil)

	// Create task-specific streamer adapter
	taskStreamer := &commanderStreamerAdapter{
		taskName: task.Name,
		streamer: streamer,
	}

	// Execute (or resume if stored messages were loaded)
	err = sup.ExecuteOrResume(ctx, objective, taskStreamer)
	if err != nil {
		sup.Close()
		if ctx.Err() != nil {
			// Mission was stopped — don't emit task_failed, just propagate
			return &TaskResult{TaskName: task.Name, Success: false, Error: ctx.Err()}, ctx.Err()
		}
		errStr := err.Error()
		updateTaskDone(false, nil, &errStr)
		streamer.TaskFailed(task.Name, err)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    err,
		}, err
	}

	// Check if task was explicitly marked as failed
	if !sup.IsTaskSucceeded() {
		errStr := "task marked as failed by commander"
		if reason := sup.TaskFailureReason(); reason != "" {
			errStr = reason
		}
		updateTaskDone(false, nil, &errStr)
		sup.Close()
		streamer.TaskFailed(task.Name, fmt.Errorf("%s", errStr))
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    fmt.Errorf("%s", errStr),
		}, fmt.Errorf("%s", errStr)
	}

	// Store commander and summary for dependent tasks
	r.mu.Lock()
	r.taskCommanders[task.Name] = sup
	if summary := sup.TaskSummary(); summary != "" {
		r.taskSummaries[task.Name] = summary
		r.stores.Missions.UpdateTaskSummary(taskID, summary)
	}
	r.mu.Unlock()

	// Get output from submit_output tool
	var output map[string]any
	if results := sup.GetSubmitResults(); len(results) > 0 {
		output = results[0].Output
	}

	// Update task status to completed (output already persisted via OnSubmitOutput)
	outputJSON, _ := json.Marshal(output)
	outputStr := string(outputJSON)
	updateTaskDone(true, &outputStr, nil)

	streamer.TaskCompleted(task.Name)
	return &TaskResult{
		TaskName:       task.Name,
		Success:        true,
		ChosenRoute:    sup.ChosenRoute(),
		IsMissionRoute: sup.IsMissionRoute(),
		MissionInputs:  sup.MissionInputs(),
	}, nil
}

// getDependencyChain returns all tasks this task depends on (including transitive dependencies).
// For router-activated tasks (no depends_on), the routing task is treated as a virtual dependency,
// giving the routed-to task access to the full DAG ancestry of the router.
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

	// Include the router parent as a virtual dependency
	if routerParent, ok := r.routerParents[taskName]; ok {
		queue = append(queue, routerParent)
	}

	for len(queue) > 0 {
		dep := queue[0]
		queue = queue[1:]

		if visited[dep] {
			continue
		}
		visited[dep] = true

		depTask := r.mission.GetTaskByName(dep)
		if depTask != nil {
			queue = append(queue, depTask.DependsOn...)
		}

		// Also follow router parent links
		if routerParent, ok := r.routerParents[dep]; ok {
			queue = append(queue, routerParent)
		}

		result = append(result, dep)
	}

	return result
}

// convertOutputField converts a config.OutputField to agent.OutputFieldSchema recursively
func convertOutputField(field config.OutputField) agent.OutputFieldSchema {
	s := agent.OutputFieldSchema{
		Name:        field.Name,
		Type:        field.Type,
		Description: field.Description,
		Required:    field.Required,
	}
	if field.Items != nil {
		items := convertOutputField(*field.Items)
		s.Items = &items
	}
	for _, prop := range field.Properties {
		s.Properties = append(s.Properties, convertOutputField(prop))
	}
	return s
}

// getTaskOutputSchema converts a task's output schema to agent.OutputFieldSchema slice
func (r *Runner) getTaskOutputSchema(task config.Task) []agent.OutputFieldSchema {
	if task.Output == nil {
		return nil
	}

	var result []agent.OutputFieldSchema
	for _, field := range task.Output.Fields {
		result = append(result, convertOutputField(field))
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

func (s *commanderStreamerAdapter) ReasoningStarted() {
	s.streamer.CommanderReasoningStarted(s.taskName)
}

func (s *commanderStreamerAdapter) ReasoningCompleted(content string) {
	s.streamer.CommanderReasoningCompleted(s.taskName, content)
}

func (s *commanderStreamerAdapter) Answer(content string) {
	s.streamer.CommanderAnswer(s.taskName, content)
}

func (s *commanderStreamerAdapter) CallingTool(toolCallId, name, input string) {
	s.streamer.CommanderCallingTool(s.taskName, toolCallId, name, input)
}

func (s *commanderStreamerAdapter) ToolComplete(toolCallId, name string, result string) {
	s.streamer.CommanderToolComplete(s.taskName, toolCallId, name, result)
}

func (s *commanderStreamerAdapter) Compaction(inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int) {
	s.streamer.Compaction(s.taskName, "commander", inputTokens, tokenLimit, messagesCompacted, turnRetention)
}

func (s *commanderStreamerAdapter) SessionTurn(data protocol.SessionTurnData) {
	data.TaskName = s.taskName
	data.Entity = "commander"
	s.streamer.SessionTurn(data)
}

// agentCompactionCallback returns a callback for agent compaction events that routes to the streamer.
func agentCompactionCallback(streamer streamers.MissionHandler) func(string, string, int, int, int, int) {
	return func(taskName, agentName string, inputTokens, tokenLimit, messagesCompacted, turnRetention int) {
		streamer.Compaction(taskName, agentName, inputTokens, tokenLimit, messagesCompacted, turnRetention)
	}
}

// agentSessionTurnCallback returns a callback for agent session turn telemetry that routes to the streamer.
func agentSessionTurnCallback(streamer streamers.MissionHandler) func(string, string, protocol.SessionTurnData) {
	return func(taskName, agentName string, data protocol.SessionTurnData) {
		data.TaskName = taskName
		data.Entity = agentName
		streamer.SessionTurn(data)
	}
}

// routeOptionsForTask converts a task's router config into RouteOption slice for the commander.
// For mission route targets, it populates IsMission and the target mission's input info.
func (r *Runner) routeOptionsForTask(task config.Task) []aitools.RouteOption {
	if task.Router == nil {
		return nil
	}
	opts := make([]aitools.RouteOption, len(task.Router.Routes))
	for i, route := range task.Router.Routes {
		opts[i] = aitools.RouteOption{
			Target:    route.Target,
			Condition: route.Condition,
			IsMission: route.IsMission,
		}
		if route.IsMission {
			// Look up the target mission's inputs
			for _, m := range r.cfg.Missions {
				if m.Name == route.Target {
					for _, inp := range m.Inputs {
						opts[i].Inputs = append(opts[i].Inputs, aitools.RouteInput{
							Name:        inp.Name,
							Type:        inp.Type,
							Description: inp.Description,
							Required:    inp.Default == nil && !inp.Protected,
						})
					}
					break
				}
			}
		}
	}
	return opts
}

func (r *Runner) commanderCompaction() *agent.CompactionConfig {
	if r.mission.Commander == nil || r.mission.Commander.Compaction == nil {
		return nil
	}
	return &agent.CompactionConfig{
		TokenLimit:    r.mission.Commander.Compaction.TokenLimit,
		TurnRetention: r.mission.Commander.Compaction.TurnRetention,
	}
}

// commanderPruneOn returns the prune_on threshold from mission pruning config, or 0 if not set.
func (r *Runner) commanderPruneOn() int {
	if r.mission.Commander == nil || r.mission.Commander.Pruning == nil {
		return 0
	}
	return r.mission.Commander.Pruning.PruneOn
}

// commanderPruneTo returns the prune_to target from mission pruning config, or 0 if not set.
func (r *Runner) commanderPruneTo() int {
	if r.mission.Commander == nil || r.mission.Commander.Pruning == nil {
		return 0
	}
	return r.mission.Commander.Pruning.PruneTo
}

// missionSnapshot returns a JSON-friendly representation of the mission config.
func (r *Runner) missionSnapshot() map[string]any {
	snap := map[string]any{
		"name":      r.mission.Name,
		"commander": r.mission.Commander.Model,
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
			if input.Protected {
				m["protected"] = true
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
		objective, err := task.ResolvedObjective(r.varsValues, r.inputValues)
		if err != nil || objective == "" {
			objective = task.RawObjective
		}
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
	if task.Router != nil {
		snap["router"] = task.Router
	}
	if len(task.SendTo) > 0 {
		snap["sendTo"] = task.SendTo
	}
	return snap
}

// runIteratedTask executes a task that iterates over a dataset
func (r *Runner) runIteratedTask(ctx context.Context, task config.Task, missionID string, existingTaskID string, streamer streamers.MissionHandler) (*TaskResult, error) {
	// Load dataset items from store
	datasetName := task.Iterator.Dataset
	dsID, ok := r.datasetIDs[datasetName]
	if !ok {
		return nil, fmt.Errorf("dataset '%s' not found", datasetName)
	}
	itemCount, _ := r.stores.Datasets.GetItemCount(dsID)
	items, err := r.stores.Datasets.GetItems(dsID, 0, itemCount)
	if err != nil {
		return nil, fmt.Errorf("load dataset '%s': %w", datasetName, err)
	}

	// Lock the dataset — no mutations allowed after iteration begins
	r.stores.Datasets.LockDataset(dsID)

	// Create or reuse task record in store
	var taskID string
	if existingTaskID != "" {
		taskID = existingTaskID
	} else {
		var representativeObj string
		if len(items) > 0 {
			representativeObj, _ = r.resolveIterationObjective(task, items[0])
		}
		taskConfigJSON, _ := json.Marshal(taskSnapshot(task, representativeObj))
		taskID, _ = r.stores.Missions.CreateTask(missionID, task.Name, string(taskConfigJSON))
	}
	if reg, ok := streamer.(streamers.IDRegistrar); ok {
		reg.SetTaskID(task.Name, taskID)
	}
	if r.stateMgr != nil {
		r.stateMgr.SetTaskID(task.Name, taskID)
	}
	r.stores.Missions.UpdateTaskStatus(taskID, "running", nil, nil)

	updateTaskDone := func(success bool, outputJSON, errMsg *string) {
		if success {
			r.stores.Missions.UpdateTaskStatus(taskID, "completed", outputJSON, nil)
		} else {
			r.stores.Missions.UpdateTaskStatus(taskID, "failed", nil, errMsg)
		}
	}

	if len(items) == 0 {
		// No items to iterate - return success
		streamer.TaskStarted(task.Name, fmt.Sprintf("(0 iterations over %s)", datasetName))
		streamer.TaskCompleted(task.Name)

		updateTaskDone(true, nil, nil)
		return &TaskResult{
			TaskName: task.Name,
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

	// Store resolved task inputs for each iteration
	if existingTaskID == "" {
		for i, item := range items {
			iterObj, _ := r.resolveIterationObjective(task, item)
			idx := i
			r.stores.Missions.StoreTaskInput(taskID, &idx, iterObj)
		}
	}

	// Notify mission handler about iteration start
	streamer.TaskIterationStarted(task.Name, len(items), task.Iterator.Parallel)

	var iterations []IterationResult

	if task.Iterator.Parallel {
		if existingTaskID != "" {
			// Resume: check which iterations already completed
			existingOutputs, _ := r.stores.Missions.GetTaskOutputs(taskID)
			completedIndices := make(map[int]bool)
			for _, o := range existingOutputs {
				if o.DatasetIndex != nil {
					completedIndices[*o.DatasetIndex] = true
				}
			}

			// Build list of remaining items and their original indices
			var remainingItems []cty.Value
			var remainingIndices []int
			for i, item := range items {
				if !completedIndices[i] {
					remainingItems = append(remainingItems, item)
					remainingIndices = append(remainingIndices, i)
				}
			}

			if len(remainingItems) == 0 {
				// All iterations already completed
				iterations = make([]IterationResult, len(items))
				for i := range items {
					iterations[i] = IterationResult{Index: i, Success: true}
				}
			} else {
				// Run only remaining iterations
				partialResults := r.runParallelIterationsWithIndices(ctx, task, remainingItems, remainingIndices, taskID, depSummaries, streamer)
				// Merge with completed
				iterations = make([]IterationResult, len(items))
				for i := range items {
					if completedIndices[i] {
						iterations[i] = IterationResult{Index: i, Success: true}
					}
				}
				for _, result := range partialResults {
					iterations[result.Index] = result
				}
			}
		} else {
			// Fresh: parallel execution with fail-fast
			iterations = r.runParallelIterations(ctx, task, items, taskID, depSummaries, streamer)
		}
	} else {
		// Sequential execution
		if existingTaskID != "" {
			iterations = r.runSequentialIterationsResume(ctx, task, items, taskID, depSummaries, streamer)
		} else {
			iterations = r.runSequentialIterations(ctx, task, items, taskID, depSummaries, streamer)
		}
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
		if ctx.Err() != nil {
			return &TaskResult{TaskName: task.Name, Success: false, Error: ctx.Err()}, ctx.Err()
		}
		errStr := firstError.Error()
		updateTaskDone(false, nil, &errStr)
		streamer.TaskFailed(task.Name, firstError)
		return &TaskResult{
			TaskName: task.Name,
			Success:  false,
			Error:    firstError,
		}, firstError
	}

	// Update task status to completed
	// Individual iteration outputs already persisted via OnSubmitOutput callbacks
	updateTaskDone(true, nil, nil)

	streamer.TaskIterationCompleted(task.Name, len(iterations))
	streamer.TaskCompleted(task.Name)

	// For sequential iterators with a router, get the chosen route from the commander
	var chosenRoute string
	var isMissionRoute bool
	var missionInputs map[string]string
	if task.Router != nil && !task.Iterator.Parallel {
		r.mu.RLock()
		if iterSups, ok := r.iterationCommanders[task.Name]; ok {
			if sup, ok := iterSups[0]; ok {
				chosenRoute = sup.ChosenRoute()
				isMissionRoute = sup.IsMissionRoute()
				missionInputs = sup.MissionInputs()
			}
		}
		r.mu.RUnlock()
	}

	return &TaskResult{
		TaskName:       task.Name,
		Success:        true,
		ChosenRoute:    chosenRoute,
		IsMissionRoute: isMissionRoute,
		MissionInputs:  missionInputs,
	}, nil
}

// runSequentialIterations runs all iterations in a single commander session with agent reuse
func (r *Runner) runSequentialIterations(ctx context.Context, task config.Task, items []cty.Value, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) []IterationResult {
	// Get agents for this task
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.mission.Agents
	}

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
	// Sequential tasks don't use item vars in the objective — the commander gets items via dataset_next
	taskObjective, err := task.ResolvedObjective(r.varsValues, r.inputValues)
	if err != nil {
		return []IterationResult{{
			Index:   0,
			Success: false,
			Error:   fmt.Errorf("resolving objective: %w", err),
		}}
	}

	objective := fmt.Sprintf(`Process the following task for each of %d items in the dataset.

Task objective: %s

Use dataset_next to get each item. Process it completely, then call submit_output with the output.
Continue until dataset_next returns "exhausted".`, len(items), taskObjective)

	// Create single commander with all items
	sup, err := agent.NewCommander(ctx, agent.CommanderOptions{
		Config:            r.cfg,
		ConfigPath:        r.configPath,
		MissionName:      r.mission.Name,
		TaskName:          task.Name,
		Commander:   r.mission.Commander.Model,
		AgentNames:        agents,
		DepSummaries:      depSummaries,
		DepOutputSchemas:  depOutputSchemas,
		TaskOutputSchema:  taskOutputSchema,
		SecretInfos:       r.secretInfos,
		SecretValues:      r.secretValues,
		IsIteration:       true,
		IsParallel:        false,
		DebugFile:         debugFile,
		SequentialDataset:   items,
		FolderStore:         r.folderStore,
		Compaction:          r.commanderCompaction(),
		PruneOn:             r.commanderPruneOn(),
		PruneTo:             r.commanderPruneTo(),
		Routes:              r.routeOptionsForTask(task),
		ToolResponseMaxSize: r.mission.Commander.GetToolResponseMaxBytes(),
		PricingOverrides:    r.pricingOverrides,
		MissionLocalAgents:  r.mission.LocalAgents,
		Provider:            r.testProvider(),
	})
	if err != nil {
		return []IterationResult{{
			Index:   0,
			Success: false,
			Error:   err,
		}}
	}

	// Wire up OnNext to emit iteration_started events
	sup.SetDatasetOnNext(func(index int) {
		streamer.IterationStarted(task.Name, index, taskObjective)
	})

	// Track commander session ID for subtask callbacks
	var seqCmdSessionID string
	var seqSubtaskIterIdx *int

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName, instruction string) {
			streamer.AgentStarted(taskName, agentName, instruction)
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
		OnAgentCompaction:  agentCompactionCallback(streamer),
		OnAgentSessionTurn: agentSessionTurnCallback(streamer),
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLoggerInterface(),
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
		OnSubmitOutput: func(index int, output map[string]any) {
			datasetName := task.Iterator.Dataset
			itemID := ""
			if index < len(items) {
				itemID = getItemID(items[index], index)
			}
			outputJSON, _ := json.Marshal(output)
			r.stores.Missions.StoreTaskOutput(taskID, &datasetName, &index, &itemID, string(outputJSON))
			streamer.IterationCompleted(task.Name, index)
		},
		SessionLogger: r.stores.Sessions,
		TaskID:        taskID,
		OnSessionCreated: func(taskName, agentName, sessionID string) {
			if agentName == "commander" {
				seqCmdSessionID = sessionID
			}
			if reg, ok := streamer.(streamers.IDRegistrar); ok {
				reg.SetSessionID(taskName, agentName, sessionID)
			}
		},
		SetSubtasks: func(titles []string) error {
			iterIdx := sup.GetCurrentDatasetIndex()
			seqSubtaskIterIdx = iterIdx
			return r.stores.Missions.SetSubtasks(taskID, seqCmdSessionID, iterIdx, titles)
		},
		GetSubtasks: func() ([]store.Subtask, error) {
			return r.stores.Missions.GetSubtasks(taskID, seqCmdSessionID, seqSubtaskIterIdx)
		},
		CompleteSubtask: func() error {
			return r.stores.Missions.CompleteSubtask(taskID, seqCmdSessionID, seqSubtaskIterIdx)
		},
	}, depSummaries)

	// Create streamer adapter for the commander with dynamic index
	seqStreamer := &iterationStreamerAdapter{
		taskName: task.Name,
		indexFunc: func() int {
			if idx := sup.GetCurrentDatasetIndex(); idx != nil {
				return *idx
			}
			return 0
		},
		streamer: streamer,
	}

	// Execute the task - commander handles all items internally
	err = sup.ExecuteTask(ctx, objective, seqStreamer)

	// Check if task was explicitly marked as failed
	if err == nil && !sup.IsTaskSucceeded() {
		failMsg := "task marked as failed by commander"
		if reason := sup.TaskFailureReason(); reason != "" {
			failMsg = reason
		}
		sup.Close()
		return []IterationResult{{
			Index:   0,
			Success: false,
			Error:   fmt.Errorf("%s", failMsg),
		}}
	}

	// Get results from submit_output tool
	results := sup.GetSubmitResults()
	if len(results) == 0 {
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

	// Convert SubmitResult to IterationResult
	iterations := make([]IterationResult, len(results))
	for i, r := range results {
		itemID := ""
		if i < len(items) {
			itemID = getItemID(items[i], i)
		}
		iterations[i] = IterationResult{
			Index:   i,
			ItemID:  itemID,
			Output:  r.Output,
			Success: true,
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

			firstResult = r.runSingleIteration(ctx, task, 0, items[0], nil, taskID, depSummaries, streamer)
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

				// Pass nil for prevOutput in parallel iterations (no meaningful ordering)
				result = r.runSingleIteration(ctx, task, actualIndex, item, nil, taskID, depSummaries, streamer)
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

// runParallelIterationsWithIndices runs specific iterations (by index) in parallel.
// Used on resume to only run iterations that didn't complete in the prior run.
func (r *Runner) runParallelIterationsWithIndices(ctx context.Context, task config.Task, items []cty.Value, indices []int, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) []IterationResult {
	maxRetries := 0
	if task.Iterator != nil {
		maxRetries = task.Iterator.MaxRetries
	}
	concurrencyLimit := 5
	if task.Iterator != nil && task.Iterator.ConcurrencyLimit > 0 {
		concurrencyLimit = task.Iterator.ConcurrencyLimit
	}

	results := make([]IterationResult, len(items))
	sem := make(chan struct{}, concurrencyLimit)
	var wg sync.WaitGroup

	for i, item := range items {
		i, item := i, item
		actualIndex := indices[i]

		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var result IterationResult
			for attempt := 0; attempt <= maxRetries; attempt++ {
				select {
				case <-ctx.Done():
					results[i] = IterationResult{
						Index:   actualIndex,
						ItemID:  getItemID(item, actualIndex),
						Success: false,
						Error:   ctx.Err(),
					}
					return
				default:
				}

				result = r.runSingleIteration(ctx, task, actualIndex, item, nil, taskID, depSummaries, streamer)
				if result.Success {
					break
				}
				if attempt < maxRetries {
					streamer.IterationRetrying(task.Name, actualIndex, attempt+1, maxRetries, result.Error)
				}
			}
			results[i] = result
		}()
	}

	wg.Wait()
	return results
}

// runSequentialIterationsResume resumes sequential iterations from where they left off.
// It counts completed outputs in the store and skips those iterations.
func (r *Runner) runSequentialIterationsResume(ctx context.Context, task config.Task, items []cty.Value, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) []IterationResult {
	// Count completed outputs from prior run
	existingOutputs, _ := r.stores.Missions.GetTaskOutputs(taskID)
	completedCount := len(existingOutputs)

	if completedCount >= len(items) {
		// All iterations already completed
		iterations := make([]IterationResult, len(items))
		for i := range items {
			iterations[i] = IterationResult{Index: i, Success: true}
		}
		return iterations
	}

	// Build iterations: completed ones from store + run remaining
	iterations := make([]IterationResult, 0, len(items))
	for i := 0; i < completedCount; i++ {
		iterations = append(iterations, IterationResult{Index: i, Success: true})
	}

	// Run the remaining items with a sequential commander
	remainingItems := items[completedCount:]

	// Get agents for this task
	agents := task.Agents
	if len(agents) == 0 {
		agents = r.mission.Agents
	}
	depOutputSchemas := r.collectDepOutputSchemas(task.Name)
	taskOutputSchema := r.getTaskOutputSchema(task)

	var debugFile string
	if r.debugLogger != nil {
		debugFile = r.debugLogger.GetMessageFile("commander", task.Name)
	}

	taskObjective, err := task.ResolvedObjective(r.varsValues, r.inputValues)
	if err != nil {
		return append(iterations, IterationResult{
			Index:   completedCount,
			Success: false,
			Error:   fmt.Errorf("resolving objective: %w", err),
		})
	}

	objective := fmt.Sprintf(`Process the following task for each of %d items in the dataset.

Task objective: %s

Use dataset_next to get each item. Process it completely, then call submit_output with the output.
Continue until dataset_next returns "exhausted".`, len(remainingItems), taskObjective)

	// Create commander for remaining items
	sup, err := agent.NewCommander(ctx, agent.CommanderOptions{
		Config:            r.cfg,
		ConfigPath:        r.configPath,
		MissionName:      r.mission.Name,
		TaskName:          task.Name,
		Commander:   r.mission.Commander.Model,
		AgentNames:        agents,
		DepSummaries:      depSummaries,
		DepOutputSchemas:  depOutputSchemas,
		TaskOutputSchema:  taskOutputSchema,
		SecretInfos:       r.secretInfos,
		SecretValues:      r.secretValues,
		IsIteration:       true,
		IsParallel:        false,
		DebugFile:         debugFile,
		SequentialDataset: remainingItems,
		FolderStore:         r.folderStore,
		Compaction:          r.commanderCompaction(),
		PruneOn:             r.commanderPruneOn(),
		PruneTo:             r.commanderPruneTo(),
		ToolResponseMaxSize: r.mission.Commander.GetToolResponseMaxBytes(),
		PricingOverrides:    r.pricingOverrides,
		MissionLocalAgents:  r.mission.LocalAgents,
		Provider:            r.testProvider(),
	})
	if err != nil {
		return append(iterations, IterationResult{
			Index:   completedCount,
			Success: false,
			Error:   err,
		})
	}

	// Wire up OnNext to emit iteration_started events
	sup.SetDatasetOnNext(func(index int) {
		actualIndex := index + completedCount
		streamer.IterationStarted(task.Name, actualIndex, taskObjective)
	})

	// Check for existing session state
	existingSessionID := r.findAndLoadExistingSession(sup, taskID, nil)

	// Track commander session ID for subtask callbacks
	var seqResumeCmdSessionID string
	var seqResumeSubtaskIterIdx *int
	if existingSessionID != "" {
		seqResumeCmdSessionID = existingSessionID
	}

	// Set up tool callbacks
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName, instruction string) {
			streamer.AgentStarted(taskName, agentName, instruction)
		},
		GetAgentHandler: func(taskName, agentName string) streamers.ChatHandler {
			return streamer.AgentHandler(taskName, agentName)
		},
		OnAgentComplete: func(taskName, agentName string) {
			streamer.AgentCompleted(taskName, agentName)
		},
		OnAgentCompaction:  agentCompactionCallback(streamer),
		OnAgentSessionTurn: agentSessionTurnCallback(streamer),
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLoggerInterface(),
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
		OnSubmitOutput: func(index int, output map[string]any) {
			// Adjust index to account for already-completed items
			actualIndex := index + completedCount
			datasetName := task.Iterator.Dataset
			itemID := ""
			if actualIndex < len(items) {
				itemID = getItemID(items[actualIndex], actualIndex)
			}
			outputJSON, _ := json.Marshal(output)
			r.stores.Missions.StoreTaskOutput(taskID, &datasetName, &actualIndex, &itemID, string(outputJSON))
			streamer.IterationCompleted(task.Name, actualIndex)
		},
		SessionLogger:     r.stores.Sessions,
		TaskID:            taskID,
		ExistingSessionID: existingSessionID,
		OnSessionCreated: func(taskName, agentName, sessionID string) {
			if agentName == "commander" {
				seqResumeCmdSessionID = sessionID
			}
			if reg, ok := streamer.(streamers.IDRegistrar); ok {
				reg.SetSessionID(taskName, agentName, sessionID)
			}
		},
		SetSubtasks: func(titles []string) error {
			iterIdx := sup.GetCurrentDatasetIndex()
			seqResumeSubtaskIterIdx = iterIdx
			return r.stores.Missions.SetSubtasks(taskID, seqResumeCmdSessionID, iterIdx, titles)
		},
		GetSubtasks: func() ([]store.Subtask, error) {
			return r.stores.Missions.GetSubtasks(taskID, seqResumeCmdSessionID, seqResumeSubtaskIterIdx)
		},
		CompleteSubtask: func() error {
			return r.stores.Missions.CompleteSubtask(taskID, seqResumeCmdSessionID, seqResumeSubtaskIterIdx)
		},
	}, depSummaries)

	// Restore any agent sessions from the store
	r.restoreAgentSessions(ctx, sup, taskID, nil)

	seqStreamer := &iterationStreamerAdapter{
		taskName: task.Name,
		indexFunc: func() int {
			if idx := sup.GetCurrentDatasetIndex(); idx != nil {
				return *idx
			}
			return completedCount
		},
		streamer: streamer,
	}

	// Execute (or resume if stored messages were loaded)
	err = sup.ExecuteOrResume(ctx, objective, seqStreamer)

	// Check if task was explicitly marked as failed
	if err == nil && !sup.IsTaskSucceeded() {
		failMsg := "task marked as failed by commander"
		if reason := sup.TaskFailureReason(); reason != "" {
			failMsg = reason
		}
		sup.Close()
		iterations = append(iterations, IterationResult{
			Index:   completedCount,
			Success: false,
			Error:   fmt.Errorf("%s", failMsg),
		})
		return iterations
	}

	// Get results
	results := sup.GetSubmitResults()
	for i, res := range results {
		actualIndex := i + completedCount
		itemID := ""
		if actualIndex < len(items) {
			itemID = getItemID(items[actualIndex], actualIndex)
		}
		iterations = append(iterations, IterationResult{
			Index:   actualIndex,
			ItemID:  itemID,
			Output:  res.Output,
			Success: true,
		})
	}

	if len(results) == 0 && err != nil {
		iterations = append(iterations, IterationResult{
			Index:   completedCount,
			Success: false,
			Error:   err,
		})
	}

	// Store commander for queries
	r.mu.Lock()
	if r.iterationCommanders[task.Name] == nil {
		r.iterationCommanders[task.Name] = make(map[int]*agent.Commander)
	}
	r.iterationCommanders[task.Name][0] = sup
	r.mu.Unlock()

	return iterations
}

// runSingleIteration executes a single iteration of an iterated task.
// It checks the store for existing session state and resumes automatically if found.
func (r *Runner) runSingleIteration(ctx context.Context, task config.Task, index int, item cty.Value, prevOutput map[string]any, taskID string, depSummaries []agent.DependencySummary, streamer streamers.MissionHandler) IterationResult {
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
		Commander:        r.mission.Commander.Model,
		AgentNames:             agents,
		DepSummaries:           depSummaries,
		DepOutputSchemas:       depOutputSchemas,
		TaskOutputSchema:       taskOutputSchema,
		PrevIterationOutput:    prevOutput,
		SecretInfos:            r.secretInfos,
		SecretValues:           r.secretValues,
		IsIteration:            true,
		IsParallel:             task.Iterator.Parallel,
		DebugFile:              debugFile,
		FolderStore:            r.folderStore,
		Compaction:             r.commanderCompaction(),
		PruneOn:                r.commanderPruneOn(),
		PruneTo:                r.commanderPruneTo(),
		ToolResponseMaxSize:    r.mission.Commander.GetToolResponseMaxBytes(),
		MissionLocalAgents:     r.mission.LocalAgents,
		Provider:               r.testProvider(),
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

	// Check for existing session state (finds stored session from prior run if any)
	iterIdx := index
	existingSessionID := r.findAndLoadExistingSession(sup, taskID, &iterIdx)

	// Track commander session ID for subtask callbacks
	var iterCmdSessionID string
	if existingSessionID != "" {
		iterCmdSessionID = existingSessionID
	}

	// Set up tool callbacks for iteration
	sup.SetToolCallbacks(&agent.CommanderToolCallbacks{
		OnAgentStart: func(taskName, agentName, instruction string) {
			streamer.AgentStarted(taskName, agentName, instruction)
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
		OnAgentCompaction:  agentCompactionCallback(streamer),
		OnAgentSessionTurn: agentSessionTurnCallback(streamer),
		DatasetStore:   r,
		KnowledgeStore: &knowledgeStoreAdapter{store: r.knowledgeStore},
		DebugLogger:    r.debugLoggerInterface(),
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
		OnSubmitOutput: func(idx int, output map[string]any) {
			datasetName := task.Iterator.Dataset
			outputJSON, _ := json.Marshal(output)
			actualIdx := index
			r.stores.Missions.StoreTaskOutput(taskID, &datasetName, &actualIdx, &itemID, string(outputJSON))
		},
		SessionLogger:     r.stores.Sessions,
		TaskID:            taskID,
		IterationIndex:    &iterIdx,
		ExistingSessionID: existingSessionID,
		OnSessionCreated: func(taskName, agentName, sessionID string) {
			if agentName == "commander" {
				iterCmdSessionID = sessionID
			}
			if reg, ok := streamer.(streamers.IDRegistrar); ok {
				reg.SetSessionID(taskName, agentName, sessionID)
			}
		},
		SetSubtasks: func(titles []string) error {
			return r.stores.Missions.SetSubtasks(taskID, iterCmdSessionID, &iterIdx, titles)
		},
		GetSubtasks: func() ([]store.Subtask, error) {
			return r.stores.Missions.GetSubtasks(taskID, iterCmdSessionID, &iterIdx)
		},
		CompleteSubtask: func() error {
			return r.stores.Missions.CompleteSubtask(taskID, iterCmdSessionID, &iterIdx)
		},
	}, depSummaries)

	// Restore any agent sessions from the store
	r.restoreAgentSessions(ctx, sup, taskID, &iterIdx)

	// Create iteration-specific streamer adapter
	iterStreamer := &iterationStreamerAdapter{
		taskName: task.Name,
		index:    index,
		streamer: streamer,
	}

	// Execute (or resume if stored messages were loaded)
	err = sup.ExecuteOrResume(ctx, objective, iterStreamer)
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

	// Check if task was explicitly marked as failed
	if !sup.IsTaskSucceeded() {
		failMsg := "iteration marked as failed by commander"
		if reason := sup.TaskFailureReason(); reason != "" {
			failMsg = reason
		}
		sup.Close()
		failErr := fmt.Errorf("%s", failMsg)
		streamer.IterationFailed(task.Name, index, failErr)
		return IterationResult{
			Index:   index,
			ItemID:  itemID,
			Success: false,
			Error:   failErr,
		}
	}

	// Get output from submit_output tool
	var output map[string]any
	if results := sup.GetSubmitResults(); len(results) > 0 {
		output = results[0].Output
	}

	// Store the iteration commander for ask_commander queries from dependent tasks
	r.mu.Lock()
	if r.iterationCommanders[task.Name] == nil {
		r.iterationCommanders[task.Name] = make(map[int]*agent.Commander)
	}
	r.iterationCommanders[task.Name][index] = sup
	r.mu.Unlock()

	streamer.IterationCompleted(task.Name, index)
	return IterationResult{
		Index:   index,
		ItemID:  itemID,
		Output:  output,
		Success: true,
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
	taskName  string
	index     int        // static index (parallel)
	indexFunc func() int // dynamic index (sequential), takes precedence
	streamer  streamers.MissionHandler
}

func (s *iterationStreamerAdapter) getIndex() int {
	if s.indexFunc != nil {
		return s.indexFunc()
	}
	return s.index
}

func (s *iterationStreamerAdapter) ReasoningStarted() {
	s.streamer.CommanderReasoningStarted(fmt.Sprintf("%s[%d]", s.taskName, s.getIndex()))
}

func (s *iterationStreamerAdapter) ReasoningCompleted(content string) {
	s.streamer.CommanderReasoningCompleted(fmt.Sprintf("%s[%d]", s.taskName, s.getIndex()), content)
}

func (s *iterationStreamerAdapter) Answer(content string) {
	s.streamer.CommanderAnswer(fmt.Sprintf("%s[%d]", s.taskName, s.getIndex()), content)
}

func (s *iterationStreamerAdapter) CallingTool(toolCallId, name, input string) {
	s.streamer.CommanderCallingTool(fmt.Sprintf("%s[%d]", s.taskName, s.getIndex()), toolCallId, name, input)
}

func (s *iterationStreamerAdapter) ToolComplete(toolCallId, name string, result string) {
	s.streamer.CommanderToolComplete(fmt.Sprintf("%s[%d]", s.taskName, s.getIndex()), toolCallId, name, result)
}

func (s *iterationStreamerAdapter) Compaction(inputTokens int, tokenLimit int, messagesCompacted int, turnRetention int) {
	s.streamer.Compaction(fmt.Sprintf("%s[%d]", s.taskName, s.getIndex()), "commander", inputTokens, tokenLimit, messagesCompacted, turnRetention)
}

func (s *iterationStreamerAdapter) SessionTurn(data protocol.SessionTurnData) {
	data.TaskName = fmt.Sprintf("%s[%d]", s.taskName, s.getIndex())
	data.Entity = "commander"
	s.streamer.SessionTurn(data)
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

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, depTaskName := range depChain {
		// Skip iterated tasks — output schema info is injected separately
		if _, isIterated := r.iterationCommanders[depTaskName]; isIterated {
			continue
		}

		// Use the stored push summary from the completed task
		if summary, ok := r.taskSummaries[depTaskName]; ok && summary != "" {
			depSummaries = append(depSummaries, agent.DependencySummary{
				TaskName: depTaskName,
				Summary:  summary,
			})
			continue
		}

		// Fallback: no summary available (task completed without providing one)
		depSummaries = append(depSummaries, agent.DependencySummary{
			TaskName: depTaskName,
			Summary:  "(No summary available — use ask_commander to query this task's commander for details.)",
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

	// Register the question (no dedup — LLM uses list_commander_questions to check existing answers)
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

// SetDataset sets a dataset's values at runtime (replaces all existing items)
func (r *Runner) SetDataset(name string, items []cty.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dsID, ok := r.datasetIDs[name]
	if ok {
		if locked, err := r.stores.Datasets.IsDatasetLocked(dsID); err == nil && locked {
			return fmt.Errorf("dataset '%s' is locked and cannot be modified — a downstream task is already iterating over it. Datasets become immutable once iteration begins. If you need to store new data, use a different dataset", name)
		}
	}

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

	// Write to persistent store
	if !ok {
		return fmt.Errorf("dataset '%s' not initialized", name)
	}
	if err := r.stores.Datasets.SetItems(dsID, items); err != nil {
		return fmt.Errorf("persist dataset '%s': %w", name, err)
	}

	return nil
}

// AppendDataset appends items to a dataset without replacing existing ones
func (r *Runner) AppendDataset(name string, items []cty.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	dsID, ok := r.datasetIDs[name]
	if ok {
		if locked, err := r.stores.Datasets.IsDatasetLocked(dsID); err == nil && locked {
			return fmt.Errorf("dataset '%s' is locked and cannot be modified — a downstream task is already iterating over it. Datasets become immutable once iteration begins. If you need to store new data, use a different dataset", name)
		}
	}

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

	for i, item := range items {
		if err := ds.ValidateItem(item); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}

	if !ok {
		return fmt.Errorf("dataset '%s' not initialized", name)
	}
	if err := r.stores.Datasets.AddItems(dsID, items); err != nil {
		return fmt.Errorf("append to dataset '%s': %w", name, err)
	}

	return nil
}

// GetDatasetSample returns a sample of items from a dataset
func (r *Runner) GetDatasetSample(name string, count int) ([]cty.Value, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dsID, ok := r.datasetIDs[name]
	if !ok {
		return nil, fmt.Errorf("dataset '%s' not found", name)
	}

	if count <= 0 {
		count = 5
	}

	return r.stores.Datasets.GetSample(dsID, count)
}

// GetDatasetCount returns the number of items in a dataset
func (r *Runner) GetDatasetCount(name string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	dsID, ok := r.datasetIDs[name]
	if !ok {
		return 0, fmt.Errorf("dataset '%s' not found", name)
	}

	return r.stores.Datasets.GetItemCount(dsID)
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
		}
		if dsID, ok := r.datasetIDs[ds.Name]; ok {
			dsInfo.ItemCount, _ = r.stores.Datasets.GetItemCount(dsID)
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
// Knowledge Store Adapter - adapts mission.KnowledgeStore to agent.KnowledgeStore
// =============================================================================

// knowledgeStoreAdapter wraps KnowledgeStore to implement agent.KnowledgeStore
type knowledgeStoreAdapter struct {
	store KnowledgeStore
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
		IsIterated:      output.IsIterated,
		TotalIterations: output.TotalIterations,
		Output:          output.Output,
	}

	// Convert iterations
	for _, iter := range output.Iterations {
		info.Iterations = append(info.Iterations, agent.IterationInfo{
			Index:  iter.Index,
			ItemID: iter.ItemID,
			Status: iter.Status,
			Output: iter.Output,
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
			Index:  iter.Index,
			ItemID: iter.ItemID,
			Status: iter.Status,
			Output: iter.Output,
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
			Index:  result.Item.Index,
			ItemID: result.Item.ItemID,
			Status: result.Item.Status,
			Output: result.Item.Output,
		}
	}

	return agentResult
}
