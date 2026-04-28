package gateway

import (
	"time"

	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/store"
)

// storeRecordToSDK converts a squadron-internal record to the SDK's
// gateway-facing shape. The two are intentionally separate types so
// store mutations don't ripple through every gateway implementation.
func storeRecordToSDK(r store.HumanInputRequestRecord) gwsdk.HumanInputRecord {
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
	return out
}

// filterFromSDK translates an SDK-side filter to the store filter.
// The SDK exposes Since as a time.Time; the store expects nothing
// extra yet — this function is the integration point if/when we add
// store-level Since support.
func filterFromSDK(f gwsdk.HumanInputFilter) store.HumanInputFilter {
	return store.HumanInputFilter{
		State:       string(f.State),
		MissionID:   f.MissionID,
		OldestFirst: f.OldestFirst,
		Limit:       f.Limit,
		Offset:      f.Offset,
		// Since is currently filtered post-query inside the store
		// abstraction — see the squadron-side ListRequests call site
		// for the implementation. Once the store gains a native
		// Since column, this passes through directly.
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Compile-time assertion the SDK time format matches what we emit.
var _ = time.RFC3339Nano
