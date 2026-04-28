package wsbridge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/mlund01/squadron-wire/protocol"

	"squadron/aitools"
	"squadron/humaninput"
	"squadron/store"
)

// humanInputListeners is a per-Client map of tool_call_id → listener
// channel, used to route an incoming HumanInputResponse back to the
// blocking AskHuman call that initiated it.
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

// DeliverResolution implements humaninput.Listener — the gateway
// SquadronAPI surface and the wire-protocol resolve handler both fire
// this so the agent's blocking AskHuman call wakes up regardless of
// which surface accepted the answer.
func (l *humanInputListeners) DeliverResolution(toolCallID, response, responderUserID string) {
	l.deliver(protocol.ResolveHumanInputPayload{
		ToolCallID:      toolCallID,
		Response:        response,
		ResponderUserID: responderUserID,
	})
}

// AskHuman implements aitools.AskHumanBridge. Squadron owns the record:
// before blocking, it writes an "open" row to its own store AND emits an
// EventHumanInputRequested mission event. Commander learns about the
// request either through that event stream (live) or by querying the
// store via TypeGetHumanInputs (on-demand). When a human response lands
// via TypeResolveHumanInput, squadron persists the resolution, emits
// EventHumanInputResolved, and delivers to the listener that unblocks
// this call.
func (c *Client) AskHuman(ctx context.Context, req aitools.AskHumanRequest) (string, error) {
	if req.ToolCallID == "" {
		return "", fmt.Errorf("ask_human: tool_call_id required")
	}
	if c.stores == nil || c.stores.HumanInputs == nil {
		return "", fmt.Errorf("ask_human: no store configured")
	}

	// Write the open record first so a squadron crash between send and
	// reply doesn't lose the question — on resume, commander can query
	// and re-surface.
	record := &store.HumanInputRequestRecord{
		MissionID:         req.MissionID,
		TaskID:            req.TaskID,
		ToolCallID:        req.ToolCallID,
		Question:          req.Question,
		ShortSummary:      req.ShortSummary,
		AdditionalContext: req.AdditionalContext,
		Choices:           req.Choices,
		MultiSelect:       req.MultiSelect,
	}
	if err := c.stores.HumanInputs.CreateRequest(record); err != nil {
		return "", fmt.Errorf("ask_human: persist open request: %w", err)
	}

	c.emitHumanInputRequested(req)

	// Re-read the canonical row so the in-process notifier carries the
	// authoritative state (ID, RequestedAt) rather than just the input
	// fields. Errors are non-fatal: the wire-protocol path already
	// fired, the agent is still waiting, and gateway listeners can
	// catch up via SquadronAPI.ListHumanInputs.
	if c.humanInputNotifier != nil {
		if stored, err := c.stores.HumanInputs.GetByToolCallID(req.ToolCallID); err == nil {
			humaninput.PublishCreated(c.humanInputNotifier, *stored)
		}
	}

	ch := make(chan protocol.ResolveHumanInputPayload, 1)
	cancel := c.humanInputs.register(req.ToolCallID, ch)
	defer cancel()

	select {
	case resp := <-ch:
		return resp.Response, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// handleGetHumanInputs lets commander query the store. Squadron is the
// source of truth; commander is a passthrough.
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

	// Resolve mission/task names once per id so the Inbox doesn't have to
	// render bare ULIDs. Best-effort: a missing or errored lookup leaves
	// the name field blank rather than failing the whole list.
	missionNames := map[string]string{}
	taskNames := map[string]string{}
	if c.stores != nil && c.stores.Missions != nil {
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

// handleResolveHumanInput is called when commander forwards a human's
// response. Delegates to the shared humaninput.Resolve helper so
// commander, gateways, and any future surface use exactly the same
// store / listener / notifier flow.
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
	if out.AlreadyResolved {
		log.Printf("resolve_human_input for %s arrived after a prior resolution — replaying existing record", payload.ToolCallID)
	}
	// The wire event for resolved state is fanned out by the notifier
	// subscription in SetHumanInputNotifier — every Resolve path
	// (commander, gateway, mission-failure cancel) publishes there.

	return protocol.NewResponse(env.RequestID, protocol.TypeResolveHumanInputResult, &protocol.ResolveHumanInputResultPayload{
		Accepted:   true,
		HumanInput: recordToProtocol(out.Record),
	})
}

// emitHumanInputRequested fires a mission event if this request is
// attached to a running mission. Standalone invocations (empty mission
// id) skip the event — there's no mission stream to flow through.
func (c *Client) emitHumanInputRequested(req aitools.AskHumanRequest) {
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
	data := protocol.HumanInputResolvedData{
		ToolCallID: rec.ToolCallID,
	}
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

// cancelOpenHumanInputsForMission auto-resolves any still-open human
// input requests attached to a mission that has just failed or
// stopped. Without this, questions linger in the Inbox and on
// connected gateways even though the mission they belong to is gone
// and nobody is listening for the answer anymore.
//
// Each open row is resolved through the shared humaninput.Resolve
// helper with a synthetic "system" responder and a sentinel response,
// so commander UI, gateways, and the in-process notifier all see the
// state transition through the same path as a normal answer.
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
		// Wire event is fanned out by the notifier subscription in
		// SetHumanInputNotifier — Resolve publishes there.
		if _, err := humaninput.Resolve(c.stores, c.humanInputs, c.humanInputNotifier,
			r.ToolCallID, reason, "system"); err != nil {
			log.Printf("cancel open human input %s: %v", r.ToolCallID, err)
		}
	}
}

// recordToProtocol converts a store record to its wire DTO.
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

