package wsbridge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/aitools"
	"squadron/humaninput"
	missionruntime "squadron/mission/runtime"
	"squadron/store"
)

// humanInputListeners routes a resolution back to the goroutine in
// AskHuman that's blocked waiting for it. Keyed by tool_call_id.
type humanInputListeners struct {
	mu sync.Mutex
	m  map[string]chan<- protocol.ResolveHumanInputPayload
}

func newHumanInputListeners() *humanInputListeners {
	return &humanInputListeners{m: make(map[string]chan<- protocol.ResolveHumanInputPayload)}
}

func (l *humanInputListeners) register(toolCallID string, ch chan<- protocol.ResolveHumanInputPayload) func() {
	l.mu.Lock()
	l.m[toolCallID] = ch
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		delete(l.m, toolCallID)
		l.mu.Unlock()
	}
}

func (l *humanInputListeners) deliver(resp protocol.ResolveHumanInputPayload) bool {
	l.mu.Lock()
	ch, ok := l.m[resp.ToolCallID]
	l.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- resp:
		return true
	default:
		return false
	}
}

// DeliverResolution implements humaninput.Listener so wsbridge,
// gateways, and any future surface all wake the same blocking
// AskHuman call regardless of which surface accepted the answer.
func (l *humanInputListeners) DeliverResolution(toolCallID, response, responderUserID string) {
	l.deliver(protocol.ResolveHumanInputPayload{
		ToolCallID:      toolCallID,
		Response:        response,
		ResponderUserID: responderUserID,
	})
}

// AskHuman implements aitools.HumanInputBridge. The open record is
// written before blocking so a squadron crash between send and reply
// can be reconstructed on resume.
func (c *Client) AskHuman(ctx context.Context, req aitools.HumanInputRequest) (string, error) {
	if req.ToolCallID == "" {
		return "", fmt.Errorf("ask: tool_call_id required")
	}
	if c.stores == nil || c.stores.HumanInputs == nil {
		return "", fmt.Errorf("ask: no store configured")
	}

	record := &store.HumanInputRequestRecord{
		MissionID:         req.MissionID,
		TaskID:            req.TaskID,
		ToolCallID:        req.ToolCallID,
		Question:          req.Question,
		ShortSummary:      req.ShortSummary,
		AdditionalContext: req.AdditionalContext,
		Choices:           req.Choices,
		MultiSelect:       req.MultiSelect,
		DeadlineAt:        req.DeadlineAt,
	}
	if err := c.stores.HumanInputs.CreateRequest(record); err != nil {
		return "", fmt.Errorf("ask: persist open request: %w", err)
	}

	// Register before reading the canonical row. This closes the race where a
	// response arrives after the read but before the waiter becomes visible.
	ch := make(chan protocol.ResolveHumanInputPayload, 1)
	cancel := c.humanInputs.register(req.ToolCallID, ch)
	defer cancel()
	stored, err := c.stores.HumanInputs.GetByToolCallID(req.ToolCallID)
	if err != nil {
		return "", fmt.Errorf("ask: load persisted request: %w", err)
	}
	if stored.State == store.HumanInputStateResolved && stored.Response != nil {
		return *stored.Response, nil
	}
	if stored.DeadlineAt != nil {
		stored, err = c.stores.HumanInputs.ExpireRequest(req.ToolCallID, time.Now().UTC())
		if err != nil {
			return "", fmt.Errorf("ask: expire persisted request: %w", err)
		}
		if stored.State == store.HumanInputStateExpired {
			return "", context.DeadlineExceeded
		}
		if stored.State == store.HumanInputStateResolved && stored.Response != nil {
			return *stored.Response, nil
		}
	}

	c.emitHumanInputRequested(req)

	// Re-read so the in-process notifier carries the canonical row
	// (ID, RequestedAt) rather than just the input fields. Best-effort —
	// the wire path already fired and gateways can still catch up via
	// SquadronAPI.ListHumanInputs.
	if c.humanInputNotifier != nil {
		humaninput.PublishCreated(c.humanInputNotifier, *stored)
	}

	select {
	case resp := <-ch:
		return resp.Response, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && stored.DeadlineAt != nil {
			closed, err := c.stores.HumanInputs.ExpireRequest(req.ToolCallID, time.Now().UTC())
			if err != nil {
				return "", fmt.Errorf("ask: expire request after deadline: %w", err)
			}
			if closed.State == store.HumanInputStateResolved && closed.Response != nil {
				return *closed.Response, nil
			}
		}
		return "", ctx.Err()
	}
}

// BeginAskHuman is the nonblocking mission-runtime path. The persisted
// request is created before the task lease is released; reconnecting to the
// same tool-call ID does not publish a second prompt.
func (c *Client) BeginAskHuman(ctx context.Context, req aitools.HumanInputRequest) (aitools.DeferredHumanInputResult, error) {
	if err := ctx.Err(); err != nil {
		return aitools.DeferredHumanInputResult{}, err
	}
	if req.ToolCallID == "" || req.Question == "" {
		return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: tool call ID and question are required")
	}
	if c.stores == nil || c.stores.HumanInputs == nil {
		return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: no store configured")
	}
	prior, err := c.stores.HumanInputs.GetByToolCallID(req.ToolCallID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: load request: %w", err)
	}
	created := errors.Is(err, sql.ErrNoRows)
	if created {
		record := &store.HumanInputRequestRecord{
			MissionID: req.MissionID, TaskID: req.TaskID, ToolCallID: req.ToolCallID,
			Question: req.Question, ShortSummary: req.ShortSummary, AdditionalContext: req.AdditionalContext,
			Choices: req.Choices, MultiSelect: req.MultiSelect, DeadlineAt: req.DeadlineAt,
		}
		if err := c.stores.HumanInputs.CreateRequest(record); err != nil {
			return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: persist request: %w", err)
		}
		prior, err = c.stores.HumanInputs.GetByToolCallID(req.ToolCallID)
		if err != nil {
			return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: reload request: %w", err)
		}
	}
	if prior.Question != req.Question || prior.MissionID != req.MissionID || prior.TaskID != req.TaskID || !sameHumanInputDeadline(prior.DeadlineAt, req.DeadlineAt) {
		return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: tool call %q conflicts with its persisted request", req.ToolCallID)
	}
	if prior.DeadlineAt != nil && prior.State == store.HumanInputStateOpen {
		prior, err = c.stores.HumanInputs.ExpireRequest(req.ToolCallID, time.Now().UTC())
		if err != nil {
			return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: expire request: %w", err)
		}
	}
	if created && prior.State == store.HumanInputStateOpen {
		c.emitHumanInputRequested(req)
		if c.humanInputNotifier != nil {
			humaninput.PublishCreated(c.humanInputNotifier, *prior)
		}
	}
	switch prior.State {
	case store.HumanInputStateOpen:
		return aitools.DeferredHumanInputResult{Pending: true, RequestedAt: prior.RequestedAt}, nil
	case store.HumanInputStateResolved:
		if prior.Response == nil {
			return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: resolved request has no response")
		}
		return aitools.DeferredHumanInputResult{Response: *prior.Response, RequestedAt: prior.RequestedAt}, nil
	case store.HumanInputStateExpired:
		return aitools.DeferredHumanInputResult{}, context.DeadlineExceeded
	default:
		return aitools.DeferredHumanInputResult{}, fmt.Errorf("ask: unsupported request state %q", prior.State)
	}
}

func sameHumanInputDeadline(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// resumeReadyHumanInputMission is driven by an accepted answer or a persisted
// human/retry deadline. It checks runtime waits before resuming, so
// agent-session questions and unrelated inbox rows cannot start a mission.
func (c *Client) resumeReadyHumanInputMission(missionID string) {
	if c.ctx != nil && c.ctx.Err() != nil {
		return
	}
	if missionID == "" || c.stores == nil || c.stores.Runtime == nil || c.stores.Missions == nil {
		return
	}
	record, err := c.stores.Missions.GetMission(missionID)
	if err != nil || record.Status != "waiting" {
		return
	}
	harness, _, err := missionruntime.RestoreDurableHarness(c.stores.Runtime, missionID)
	if err != nil {
		log.Printf("human input resume: restore mission %q: %v", missionID, err)
		return
	}
	snapshot := harness.Snapshot()
	ready := false
	for _, deadline := range snapshot.RetryDeadlines {
		if !deadline.After(time.Now().UTC()) {
			ready = true
			break
		}
	}
	for _, wait := range snapshot.HumanInputWaits {
		if ready {
			break
		}
		if c.stores.HumanInputs == nil {
			return
		}
		input, err := c.stores.HumanInputs.GetByToolCallID(wait.ToolCallID)
		if err != nil {
			log.Printf("human input resume: load request %q: %v", wait.ToolCallID, err)
			return
		}
		if input.State == store.HumanInputStateResolved || input.State == store.HumanInputStateExpired ||
			(input.State == store.HumanInputStateOpen && input.DeadlineAt != nil && !input.DeadlineAt.After(time.Now().UTC())) {
			ready = true
			break
		}
	}
	if !ready {
		return
	}
	c.missionMu.Lock()
	if c.resumingWaits == nil {
		c.resumingWaits = make(map[string]bool)
	}
	if c.resumingWaits[missionID] || c.runningMissions[missionID] != nil {
		c.missionMu.Unlock()
		return
	}
	c.resumingWaits[missionID] = true
	c.missionMu.Unlock()
	defer func() {
		c.missionMu.Lock()
		delete(c.resumingWaits, missionID)
		c.missionMu.Unlock()
	}()
	request, err := protocol.NewRequest(protocol.TypeResumeMission, &protocol.ResumeMissionPayload{
		MissionID: missionID, MissionName: record.MissionName,
	})
	if err != nil {
		log.Printf("human input resume: create request for %q: %v", missionID, err)
		return
	}
	ack, err := c.handleResumeMission(request)
	if err != nil {
		log.Printf("human input resume: mission %q: %v", missionID, err)
		return
	}
	var result protocol.ResumeMissionAckPayload
	if err := protocol.DecodePayload(ack, &result); err != nil || !result.Accepted {
		log.Printf("human input resume: mission %q was not accepted: %v %s", missionID, err, result.Reason)
	}
}

func (c *Client) scheduleWaitingHumanInputMission(missionID string) {
	if missionID == "" || c.stores == nil || c.stores.Runtime == nil {
		return
	}
	harness, _, err := missionruntime.RestoreDurableHarness(c.stores.Runtime, missionID)
	if err != nil {
		return
	}
	var earliest *time.Time
	snapshot := harness.Snapshot()
	for _, deadline := range snapshot.RetryDeadlines {
		if earliest == nil || deadline.Before(*earliest) {
			copy := deadline
			earliest = &copy
		}
	}
	for _, wait := range snapshot.HumanInputWaits {
		if wait.DeadlineAt != nil && (earliest == nil || wait.DeadlineAt.Before(*earliest)) {
			deadline := *wait.DeadlineAt
			earliest = &deadline
		}
	}
	if earliest != nil {
		delay := time.Until(*earliest)
		if delay < 0 {
			delay = 0
		}
		// Wake just after the stored deadline so a sub-millisecond clock
		// difference cannot turn this into an early no-op.
		if delay <= time.Duration(1<<63-1)-time.Millisecond {
			delay += time.Millisecond
		}
		time.AfterFunc(delay, func() { c.resumeReadyHumanInputMission(missionID) })
	}
}

// HumanInputReplaySafe declares the persisted tool-call ID contract used by
// the mission runtime after restart.
func (c *Client) HumanInputReplaySafe() bool {
	return c != nil && c.stores != nil && c.stores.HumanInputs != nil
}

// handleGetHumanInputs is a list passthrough — squadron is the source
// of truth, commander only reads.
func (c *Client) handleGetHumanInputs(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.GetHumanInputsPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode get_human_inputs: %w", err)
	}
	if c.stores == nil || c.stores.HumanInputs == nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeGetHumanInputsResult, &protocol.GetHumanInputsResultPayload{})
	}

	state := payload.State
	if state == "" {
		state = store.HumanInputStateOpen
	}
	rows, total, err := c.stores.HumanInputs.ListRequests(store.HumanInputFilter{
		State:       state,
		MissionID:   payload.MissionID,
		OldestFirst: payload.OldestFirst,
		Limit:       payload.Limit,
		Offset:      payload.Offset,
	})
	if err != nil {
		return nil, err
	}

	// Resolve mission/task names once per id so the Inbox renders names
	// not bare ULIDs. Best-effort: a missing lookup leaves the field blank.
	missionNames := map[string]string{}
	taskNames := map[string]string{}
	if c.stores.Missions != nil {
		for _, r := range rows {
			if r.MissionID != "" {
				if _, ok := missionNames[r.MissionID]; !ok {
					if m, err := c.stores.Missions.GetMission(r.MissionID); err == nil && m != nil {
						missionNames[r.MissionID] = m.MissionName
					} else {
						missionNames[r.MissionID] = ""
					}
				}
			}
			if r.TaskID != "" {
				if _, ok := taskNames[r.TaskID]; !ok {
					if t, err := c.stores.Missions.GetTask(r.TaskID); err == nil && t != nil {
						taskNames[r.TaskID] = t.TaskName
					} else {
						taskNames[r.TaskID] = ""
					}
				}
			}
		}
	}

	result := &protocol.GetHumanInputsResultPayload{
		HumanInputs: make([]protocol.HumanInputRecord, 0, len(rows)),
		Total:       total,
	}
	for _, r := range rows {
		out := recordToProtocol(r)
		out.MissionName = missionNames[r.MissionID]
		out.TaskName = taskNames[r.TaskID]
		result.HumanInputs = append(result.HumanInputs, out)
	}
	return protocol.NewResponse(env.RequestID, protocol.TypeGetHumanInputsResult, result)
}

// handleResolveHumanInput delegates to humaninput.Resolve so
// commander, gateways, and any future surface flow through the same
// store / listener / notifier path. The resolved wire event is fanned
// out by the notifier subscription set up in SetHumanInputNotifier.
func (c *Client) handleResolveHumanInput(env *protocol.Envelope) (*protocol.Envelope, error) {
	var payload protocol.ResolveHumanInputPayload
	if err := protocol.DecodePayload(env, &payload); err != nil {
		return nil, fmt.Errorf("decode resolve_human_input: %w", err)
	}
	if c.stores == nil || c.stores.HumanInputs == nil {
		return protocol.NewResponse(env.RequestID, protocol.TypeResolveHumanInputResult, &protocol.ResolveHumanInputResultPayload{
			Accepted: false, Reason: "no store configured",
		})
	}

	out, err := humaninput.Resolve(c.stores, c.humanInputs, c.humanInputNotifier,
		payload.ToolCallID, payload.Response, payload.ResponderUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.NewResponse(env.RequestID, protocol.TypeResolveHumanInputResult, &protocol.ResolveHumanInputResultPayload{
				Accepted: false, Reason: "not found",
			})
		}
		return nil, err
	}
	if out.NotFound {
		return protocol.NewResponse(env.RequestID, protocol.TypeResolveHumanInputResult, &protocol.ResolveHumanInputResultPayload{
			Accepted: false, Reason: "not found",
		})
	}
	if out.AlreadyExpired {
		return protocol.NewResponse(env.RequestID, protocol.TypeResolveHumanInputResult, &protocol.ResolveHumanInputResultPayload{
			Accepted: false, Reason: "expired", HumanInput: recordToProtocol(out.Record),
		})
	}
	if out.AlreadyResolved {
		log.Printf("resolve_human_input for %s arrived after a prior resolution — replaying existing record", payload.ToolCallID)
	} else if c.humanInputNotifier == nil {
		go c.resumeReadyHumanInputMission(out.Record.MissionID)
	}
	return protocol.NewResponse(env.RequestID, protocol.TypeResolveHumanInputResult, &protocol.ResolveHumanInputResultPayload{
		Accepted:   true,
		HumanInput: recordToProtocol(out.Record),
	})
}

// emitHumanInputRequested fires only when there's a mission to attach
// to and a subscriber for the event.
func (c *Client) emitHumanInputRequested(req aitools.HumanInputRequest) {
	if req.MissionID == "" {
		return
	}
	if !c.subscriptions.ShouldSend(string(protocol.EventHumanInputRequested), req.MissionID) {
		return
	}
	env, err := protocol.NewEvent(protocol.TypeMissionEvent, &protocol.MissionEventPayload{
		MissionID: req.MissionID,
		EventType: protocol.EventHumanInputRequested,
		Data: protocol.HumanInputRequestedData{
			ToolCallID:        req.ToolCallID,
			Question:          req.Question,
			ShortSummary:      req.ShortSummary,
			AdditionalContext: req.AdditionalContext,
			Choices:           req.Choices,
			MultiSelect:       req.MultiSelect,
		},
	})
	if err != nil {
		log.Printf("emit human_input_requested: marshal: %v", err)
		return
	}
	if err := c.SendEvent(env); err != nil {
		log.Printf("emit human_input_requested: send: %v", err)
	}
}

func (c *Client) emitHumanInputResolved(rec store.HumanInputRequestRecord) {
	if rec.MissionID == "" {
		return
	}
	if !c.subscriptions.ShouldSend(string(protocol.EventHumanInputResolved), rec.MissionID) {
		return
	}
	data := protocol.HumanInputResolvedData{ToolCallID: rec.ToolCallID}
	if rec.Response != nil {
		data.Response = *rec.Response
	}
	if rec.ResponderUserID != nil {
		data.ResponderUserID = *rec.ResponderUserID
	}
	env, err := protocol.NewEvent(protocol.TypeMissionEvent, &protocol.MissionEventPayload{
		MissionID: rec.MissionID,
		EventType: protocol.EventHumanInputResolved,
		Data:      data,
	})
	if err != nil {
		log.Printf("emit human_input_resolved: marshal: %v", err)
		return
	}
	if err := c.SendEvent(env); err != nil {
		log.Printf("emit human_input_resolved: send: %v", err)
	}
}

// cancelOpenHumanInputsForMission auto-resolves still-open requests
// for a mission that's failed or stopped, so the Inbox and connected
// gateways don't linger on dead questions. Each row goes through the
// shared Resolve helper with a "system" responder and sentinel
// response — same code path as a real answer.
func (c *Client) cancelOpenHumanInputsForMission(missionID, reason string) {
	if missionID == "" || c.stores == nil || c.stores.HumanInputs == nil {
		return
	}
	rows, _, err := c.stores.HumanInputs.ListRequests(store.HumanInputFilter{
		State:     store.HumanInputStateOpen,
		MissionID: missionID,
	})
	if err != nil {
		log.Printf("cancel open human inputs for mission %s: list: %v", missionID, err)
		return
	}
	for _, r := range rows {
		if _, err := humaninput.Resolve(c.stores, c.humanInputs, c.humanInputNotifier,
			r.ToolCallID, reason, "system"); err != nil {
			log.Printf("cancel open human input %s: %v", r.ToolCallID, err)
		}
	}
}

func recordToProtocol(rec store.HumanInputRequestRecord) protocol.HumanInputRecord {
	out := protocol.HumanInputRecord{
		ID:                rec.ID,
		MissionID:         rec.MissionID,
		TaskID:            rec.TaskID,
		ToolCallID:        rec.ToolCallID,
		Question:          rec.Question,
		ShortSummary:      rec.ShortSummary,
		AdditionalContext: rec.AdditionalContext,
		Choices:           rec.Choices,
		MultiSelect:       rec.MultiSelect,
		State:             rec.State,
		RequestedAt:       rec.RequestedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if rec.ResolvedAt != nil {
		out.ResolvedAt = rec.ResolvedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	if rec.Response != nil {
		out.Response = *rec.Response
	}
	if rec.ResponderUserID != nil {
		out.ResponderUserID = *rec.ResponderUserID
	}
	return out
}
