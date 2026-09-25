package mission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"squadron/config"
	"squadron/llm"
	missionruntime "squadron/mission/runtime"
	"squadron/store"
	"squadron/streamers"
)

// runMissionScoped is the primary execution path for declarative executor
// missions. MissionStore receives only the run directory record needed by
// existing product discovery; RuntimeStore is the sole state authority.
func (r *Runner) runMissionScoped(ctx context.Context, streamer streamers.MissionHandler) error {
	if err := r.restoreRuntimeInputs(); err != nil {
		return err
	}
	definitions, err := RuntimeTaskDefinitionsWithMissions(r.mission, r.cfg.Missions)
	if err != nil {
		return err
	}
	configuration, err := json.Marshal(r.mission)
	if err != nil {
		return fmt.Errorf("encode mission configuration: %w", err)
	}
	digestBytes := sha256.Sum256(configuration)
	configurationVersion := "sha256:" + hex.EncodeToString(digestBytes[:])
	visibleInputs := r.visibleRuntimeInputs()
	inputsJSON, err := json.Marshal(visibleInputs)
	if err != nil {
		return fmt.Errorf("encode mission inputs: %w", err)
	}

	var runID string
	var harness *missionruntime.DurableHarness
	if r.resumeMissionID != "" {
		runID = r.resumeMissionID
		harness, _, err = missionruntime.RestoreDurableHarness(r.stores.Runtime, runID)
	} else {
		rawInputsJSON, _ := json.Marshal(r.rawInputs)
		runID, err = r.createMissionRecord(string(rawInputsJSON), string(configuration))
		if err == nil {
			harness, err = missionruntime.CreateDurableHarnessWithMaxParallel(r.stores.Runtime, runID, runID+":commander", definitions, r.mission.MaxParallel)
		}
	}
	if err != nil {
		return fmt.Errorf("initialize mission runtime: %w", err)
	}
	r.missionID = runID
	if err := r.initializeRuntimeDatasets(harness.Snapshot().RunState == missionruntime.RunPending); err != nil {
		return fmt.Errorf("initialize runtime datasets: %w", err)
	}

	commanderProvider, commanderModel, err := r.runtimeProvider(ctx, r.mission.Commander.Model)
	if err != nil {
		return fmt.Errorf("create commander provider: %w", err)
	}
	commander, commanderSession, err := missionruntime.NewModelCommanderWithProvider(commanderProvider, commanderModel, missionruntime.CommanderBootstrap{
		MissionName: r.mission.Name, Directive: r.mission.Directive, Tasks: definitions,
		VisibleInputs: visibleInputs, ConfigurationVersion: configurationVersion,
	}, r.mission.Commander.Reasoning, harness.CommanderMessages())
	if err != nil {
		return err
	}
	defer commanderSession.Close()

	grantResolver, err := NewRuntimeGrantResolver(r.mission, ExecutionGrantContext{
		RunAs: "user:local", InputsJSON: string(inputsJSON), ConfigurationVersion: configurationVersion,
	})
	if err != nil {
		return err
	}
	objectives := make([]missionruntime.TaskObjective, len(definitions))
	for index, definition := range definitions {
		objectives[index] = missionruntime.TaskObjective{Name: definition.Name, Objective: definition.Objective, Routes: definition.Routes, ResultPolicy: definition.ResultPolicy}
	}
	sessionFactory, err := missionruntime.NewModelTaskSessionFactory(&runtimeTaskSessionBuilder{runner: r}, objectives)
	if err != nil {
		return err
	}
	toolRefs := append([]string(nil), r.mission.Tools...)
	for _, task := range r.mission.Tasks {
		toolRefs = append(toolRefs, task.Tools...)
	}
	registry := config.BuildToolsMap(toolRefs, r.cfg.CustomTools, r.cfg.LoadedPlugins, r.cfg.LoadedMCPClients, r, r.humanBridge, r.gatewayBridge)
	// The shared registry keeps legacy dataset aliases for older callers. The
	// mission executor grants only declared capability names, so retain the
	// canonical references explicitly requested by this mission or task.
	for _, ref := range toolRefs {
		if strings.HasPrefix(ref, "builtins.dataset.") {
			if tool := config.GetBuiltinTool(ref, r, r.humanBridge, r.gatewayBridge); tool != nil {
				registry[ref] = tool
			}
		}
	}
	executor, err := missionruntime.NewBoundedExecutor(registry, sessionFactory, &runtimeCredentialBroker{secrets: r.secretValues})
	if err != nil {
		return err
	}
	coordinator, err := missionruntime.NewCoordinator(harness, commander, executor, grantResolver)
	if err != nil {
		return err
	}
	coordinator.SetExecutionObserver(&runtimeExecutionObserver{objectives: definitions, streamer: streamer})
	iterationTasks := make([]string, 0)
	for _, definition := range definitions {
		if definition.Iterator != nil {
			iterationTasks = append(iterationTasks, definition.Name)
		}
	}
	coordinator.SetIterationPlanResolver(&runtimeIterationPlanResolver{runner: r, definitions: definitions}, iterationTasks)

	streamer.MissionStarted(r.mission.Name, runID, len(definitions))
	runErr := coordinator.Run(ctx)
	telemetryErr := r.projectRuntimeModelTurns(harness.Snapshot())
	if runErr != nil {
		status := "failed"
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			status = "stopped"
		} else {
			var waiting *missionruntime.MissionRecoveryWaitingError
			if errors.As(runErr, &waiting) {
				status = "waiting"
			}
		}
		_ = r.stores.Missions.UpdateMissionStatus(runID, status)
		if telemetryErr != nil {
			return errors.Join(runErr, fmt.Errorf("project runtime model telemetry: %w", telemetryErr))
		}
		return runErr
	}
	if telemetryErr != nil {
		return fmt.Errorf("project runtime model telemetry: %w", telemetryErr)
	}
	for _, launch := range harness.Snapshot().CrossMissionLaunches {
		if launch.State != missionruntime.CrossMissionLaunchPending {
			continue
		}
		if r.nextMission != "" && r.nextMission != launch.Target {
			return fmt.Errorf("mission selected multiple cross-mission targets: %q and %q", r.nextMission, launch.Target)
		}
		r.nextMission = launch.Target
		r.nextMissionLaunchID = launch.ID
		r.nextMissionInputs = make(map[string]string, len(launch.Inputs))
		for name, value := range launch.Inputs {
			r.nextMissionInputs[name] = value
		}
	}
	if err := r.stores.Missions.UpdateMissionStatus(runID, "completed"); err != nil {
		return fmt.Errorf("project completed mission: %w", err)
	}
	streamer.MissionCompleted(r.mission.Name)
	return nil
}

func (r *Runner) restoreRuntimeInputs() error {
	if r.resumeMissionID == "" {
		return nil
	}
	record, err := r.stores.Missions.GetMission(r.resumeMissionID)
	if err != nil {
		return fmt.Errorf("load resumed mission %q: %w", r.resumeMissionID, err)
	}
	if record.MissionName != r.mission.Name {
		return fmt.Errorf("resumed mission name mismatch: stored %q, requested %q", record.MissionName, r.mission.Name)
	}
	var rawInputs map[string]string
	if err := json.Unmarshal([]byte(record.InputValuesJSON), &rawInputs); err != nil {
		return fmt.Errorf("decode resumed mission inputs: %w", err)
	}
	values, err := r.mission.ResolveInputValues(rawInputs)
	if err != nil {
		return fmt.Errorf("resolve resumed mission inputs: %w", err)
	}
	r.rawInputs = rawInputs
	r.inputValues = values
	datasets, err := resolveDatasets(r.mission, values)
	if err != nil {
		return fmt.Errorf("resolve resumed datasets: %w", err)
	}
	r.resolvedDatasets = datasets
	r.secretValues = make(map[string]string)
	for _, input := range r.mission.Inputs {
		if input.Protected && input.Value != nil && input.Value.Type() == cty.String {
			r.secretValues[input.Name] = input.Value.AsString()
		}
	}
	return nil
}

func (r *Runner) initializeRuntimeDatasets(beforeStart bool) error {
	for _, dataset := range r.mission.Datasets {
		id, err := r.stores.Datasets.GetDatasetByName(r.missionID, dataset.Name)
		if err != nil {
			if !beforeStart || !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("restore dataset %q: %w", dataset.Name, err)
			}
			id, err = r.stores.Datasets.CreateDataset(r.missionID, dataset.Name, dataset.Description)
			if err != nil {
				return fmt.Errorf("create dataset %q: %w", dataset.Name, err)
			}
		}
		if beforeStart && len(r.resolvedDatasets[dataset.Name]) > 0 {
			count, err := r.stores.Datasets.GetItemCount(id)
			if err != nil {
				return fmt.Errorf("count initial dataset %q: %w", dataset.Name, err)
			}
			if count == 0 {
				if err := r.stores.Datasets.AddItems(id, r.resolvedDatasets[dataset.Name]); err != nil {
					return fmt.Errorf("populate dataset %q: %w", dataset.Name, err)
				}
			}
		}
		r.datasetIDs[dataset.Name] = id
	}
	r.resolvedDatasets = nil
	return nil
}

type runtimeIterationPlanResolver struct {
	runner      *Runner
	definitions []missionruntime.TaskDefinition
}

func (r *runtimeIterationPlanResolver) ResolveIterationPlan(ctx context.Context, taskName string) (missionruntime.IterationPlan, error) {
	var policy *missionruntime.IterationPolicy
	for _, definition := range r.definitions {
		if definition.Name == taskName {
			policy = definition.Iterator
			break
		}
	}
	if policy == nil {
		return missionruntime.IterationPlan{}, fmt.Errorf("task %q has no iterator", taskName)
	}
	id, ok := r.runner.datasetIDs[policy.Dataset]
	if !ok {
		return missionruntime.IterationPlan{}, fmt.Errorf("dataset %q is not initialized", policy.Dataset)
	}
	if err := ctx.Err(); err != nil {
		return missionruntime.IterationPlan{}, err
	}
	// Lock before reading: once the first child can run, the dataset's ordered
	// contents must remain stable for retries and process restarts.
	if err := r.runner.stores.Datasets.LockDataset(id); err != nil {
		return missionruntime.IterationPlan{}, fmt.Errorf("lock dataset %q: %w", policy.Dataset, err)
	}
	count, err := r.runner.stores.Datasets.GetItemCount(id)
	if err != nil {
		return missionruntime.IterationPlan{}, fmt.Errorf("count dataset %q: %w", policy.Dataset, err)
	}
	raw, err := r.runner.stores.Datasets.GetItemsRaw(id, 0, count)
	if err != nil {
		return missionruntime.IterationPlan{}, fmt.Errorf("read dataset %q: %w", policy.Dataset, err)
	}
	if len(raw) != count {
		return missionruntime.IterationPlan{}, fmt.Errorf("dataset %q changed while being read", policy.Dataset)
	}
	items := make([]json.RawMessage, len(raw))
	for index, item := range raw {
		items[index] = json.RawMessage(item)
	}
	return missionruntime.BuildIterationPlan(taskName, *policy, items)
}

// projectRuntimeModelTurns materializes the runtime's durable, provider-neutral
// turn ledger into the reporting store. Stable IDs and store-level upserts make
// this safe after replay, including when a process died after a provider
// response was checkpointed but before reporting was projected.
func (r *Runner) projectRuntimeModelTurns(snapshot missionruntime.Snapshot) error {
	ids := make([]string, 0, len(snapshot.ModelTurns))
	for id := range snapshot.ModelTurns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		turn := snapshot.ModelTurns[id]
		model := r.runtimeAPIModel(turn.Model)
		cost := llm.ComputeTurnCost(
			llm.GetPricing(model, r.pricingOverrides),
			turn.InputTokens,
			turn.OutputTokens,
			turn.CacheReadTokens,
			turn.CacheWriteTokens,
		)
		if err := r.stores.Costs.StoreTurnCost(store.TurnCostRecord{
			ID:               r.missionID + ":" + turn.ID,
			MissionID:        r.missionID,
			SessionID:        turn.SessionID,
			MissionName:      r.mission.Name,
			TaskName:         turn.TaskName,
			Entity:           turn.Entity,
			Model:            model,
			InputTokens:      turn.InputTokens,
			OutputTokens:     turn.OutputTokens,
			CacheWriteTokens: turn.CacheWriteTokens,
			CacheReadTokens:  turn.CacheReadTokens,
			InputCost:        cost.InputCost,
			OutputCost:       cost.OutputCost,
			CacheReadCost:    cost.CacheReadCost,
			CacheWriteCost:   cost.CacheWriteCost,
			TotalCost:        cost.TotalCost,
		}); err != nil {
			return fmt.Errorf("store turn %q: %w", turn.ID, err)
		}
	}
	return nil
}

func (r *Runner) runtimeAPIModel(modelKey string) string {
	for i := range r.cfg.Models {
		if apiName, ok := r.cfg.Models[i].AvailableModels()[modelKey]; ok {
			return apiName
		}
	}
	return modelKey
}

func (r *Runner) visibleRuntimeInputs() map[string]any {
	protected := make(map[string]bool, len(r.mission.Inputs))
	for _, input := range r.mission.Inputs {
		protected[input.Name] = input.Protected
	}
	result := make(map[string]any)
	for name, value := range r.rawInputs {
		if !protected[name] {
			result[name] = value
		}
	}
	return result
}

func (r *Runner) runtimeProvider(ctx context.Context, modelKey string) (llm.Provider, string, error) {
	for i := range r.cfg.Models {
		model := &r.cfg.Models[i]
		apiName, ok := model.AvailableModels()[modelKey]
		if !ok {
			continue
		}
		if r.providerFactory != nil {
			return r.providerFactory(), apiName, nil
		}
		switch model.Provider {
		case config.ProviderOpenAI:
			return llm.NewOpenAIProvider(model.APIKey, model.BaseURL), apiName, nil
		case config.ProviderAnthropic:
			return llm.NewAnthropicProvider(model.APIKey, model.BaseURL), apiName, nil
		case config.ProviderGemini:
			provider, err := llm.NewGeminiProvider(ctx, model.APIKey, model.BaseURL)
			return provider, apiName, err
		case config.ProviderOpenAICompatible:
			return llm.NewOpenAICompatibleProvider(model.BaseURL), apiName, nil
		default:
			return nil, "", fmt.Errorf("unknown provider %q", model.Provider)
		}
	}
	return nil, "", fmt.Errorf("no model provider allows %q", modelKey)
}

type runtimeTaskSessionBuilder struct{ runner *Runner }

func (b *runtimeTaskSessionBuilder) BuildTaskModelSession(ctx context.Context, dispatch missionruntime.Dispatch) (missionruntime.TaskModelSession, error) {
	provider, model, err := b.runner.runtimeProvider(ctx, dispatch.Grant.Model)
	if err != nil {
		return nil, err
	}
	session := llm.NewSession(provider, model)
	return session, nil
}

type runtimeCredentialBroker struct{ secrets map[string]string }

func (b *runtimeCredentialBroker) AuthorizeToolInput(_ context.Context, request missionruntime.ToolCallAuthorization) (string, error) {
	input := request.InputJSON
	for name, value := range b.secrets {
		input = strings.ReplaceAll(input, "${secrets."+name+"}", value)
	}
	return input, nil
}

type runtimeExecutionObserver struct {
	objectives []missionruntime.TaskDefinition
	streamer   streamers.MissionHandler
}

func (e *runtimeExecutionObserver) TaskStarted(dispatch missionruntime.Dispatch) {
	objective := dispatch.TaskName
	for _, definition := range e.objectives {
		if definition.Name == dispatch.TaskName {
			objective = definition.Objective
			break
		}
	}
	e.streamer.TaskStarted(dispatch.TaskName, objective)
}

func (e *runtimeExecutionObserver) TaskCompleted(dispatch missionruntime.Dispatch) {
	e.streamer.TaskCompleted(dispatch.TaskName)
}

func (e *runtimeExecutionObserver) TaskFailed(dispatch missionruntime.Dispatch, err error, retry bool) {
	if !retry {
		e.streamer.TaskFailed(dispatch.TaskName, err)
	}
}

func (e *runtimeExecutionObserver) RouteSelected(selection missionruntime.RouteSelection) {
	condition := "No route applies"
	isMission := false
	for _, definition := range e.objectives {
		if definition.Name != selection.TaskName {
			continue
		}
		for _, route := range definition.Routes {
			if route.Target == selection.Target {
				condition, isMission = route.Condition, route.IsMission
				break
			}
		}
	}
	e.streamer.RouteChosen(selection.TaskName, selection.Target, condition, isMission)
}
