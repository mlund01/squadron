package mission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"squadron/config"
	"squadron/llm"
	missionruntime "squadron/mission/runtime"
	"squadron/streamers"
)

// runMissionScoped is the primary execution path for declarative executor
// missions. MissionStore receives only the run directory record needed by
// existing product discovery; RuntimeStore is the sole state authority.
func (r *Runner) runMissionScoped(ctx context.Context, streamer streamers.MissionHandler) error {
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
	registry := config.BuildToolsMap(toolRefs, r.cfg.CustomTools, r.cfg.LoadedPlugins, r.cfg.LoadedMCPClients, nil, r.humanBridge, r.gatewayBridge)
	executor, err := missionruntime.NewBoundedExecutor(registry, sessionFactory, &runtimeCredentialBroker{secrets: r.secretValues})
	if err != nil {
		return err
	}
	coordinator, err := missionruntime.NewCoordinator(harness, commander, executor, grantResolver)
	if err != nil {
		return err
	}
	coordinator.SetExecutionObserver(&runtimeExecutionObserver{objectives: definitions, streamer: streamer})

	streamer.MissionStarted(r.mission.Name, runID, len(definitions))
	if err := coordinator.Run(ctx); err != nil {
		status := "failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = "stopped"
		} else {
			var waiting *missionruntime.MissionRecoveryWaitingError
			if errors.As(err, &waiting) {
				status = "waiting"
			}
		}
		_ = r.stores.Missions.UpdateMissionStatus(runID, status)
		return err
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
