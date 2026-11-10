package gateway

import (
	"fmt"

	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/store"
)

// Separate types intentional: store schema can evolve without
// rippling through every gateway implementation.

func storeRecordToSDK(r store.HumanInputRequestRecord, stores *store.Bundle) (gwsdk.HumanInputRecord, error) {
	out := gwsdk.HumanInputRecord{
		ID:                r.ID,
		MissionID:         r.MissionID,
		TaskID:            r.TaskID,
		ToolCallID:        r.ToolCallID,
		Question:          r.Question,
		ShortSummary:      r.ShortSummary,
		AdditionalContext: r.AdditionalContext,
		Choices:           append([]string(nil), r.Choices...),
		MultiSelect:       r.MultiSelect,
		State:             gwsdk.HumanInputState(r.State),
		RequestedAt:       r.RequestedAt.UTC(),
		Response:          deref(r.Response),
		ResponderUserID:   deref(r.ResponderUserID),
	}
	if r.ResolvedAt != nil {
		out.ResolvedAt = r.ResolvedAt.UTC()
	}

	if r.MissionID != "" && stores != nil && stores.Missions != nil {
		mission, err := stores.Missions.GetMission(r.MissionID)
		if err != nil {
			return out, fmt.Errorf("load question mission thread: %w", err)
		}
		out.ThreadID = mission.ThreadID
		out.MissionName = mission.MissionName
	}
	return out, nil
}

func filterFromSDK(f gwsdk.HumanInputFilter) store.HumanInputFilter {
	return store.HumanInputFilter{
		State:       string(f.State),
		MissionID:   f.MissionID,
		OldestFirst: f.OldestFirst,
		Limit:       f.Limit,
		Offset:      f.Offset,
		// Since is filtered post-query in the store today; this is
		// the integration point if the store later gains a native
		// Since column.
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
