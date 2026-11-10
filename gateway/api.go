package gateway

import (
	"context"
	"errors"

	gwsdk "github.com/mlund01/squadron-gateway-sdk"

	"squadron/humaninput"
	"squadron/store"
)

// squadronAPI implements gwsdk.SquadronAPI — gateways call back into
// these methods over the broker stream the host plugin set up.
type squadronAPI struct {
	runURL   func(string) string
	stores   *store.Bundle
	notifier *humaninput.Notifier
	listener humaninput.Listener
}

func (a *squadronAPI) ListHumanInputs(ctx context.Context, filter gwsdk.HumanInputFilter) ([]gwsdk.HumanInputRecord, int, error) {
	if a.stores == nil || a.stores.HumanInputs == nil {
		return nil, 0, errors.New("squadron api: human input store not configured")
	}
	rows, total, err := a.stores.HumanInputs.ListRequests(filterFromSDK(filter))
	if err != nil {
		return nil, 0, err
	}
	out := make([]gwsdk.HumanInputRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := storeRecordToSDK(r, a.stores)
		if err != nil {
			return nil, 0, err
		}
		rec.RunURL = missionURL(a.runURL, rec.MissionID)
		out = append(out, rec)
	}
	return out, total, nil
}

func (a *squadronAPI) ResolveHumanInput(ctx context.Context, toolCallID, response, responderUserID string) (gwsdk.ResolveResult, error) {
	out, err := humaninput.Resolve(a.stores, a.listener, a.notifier, toolCallID, response, responderUserID)
	if err != nil {
		return gwsdk.ResolveResult{}, err
	}
	rec, err := storeRecordToSDK(out.Record, a.stores)
	if err != nil {
		return gwsdk.ResolveResult{}, err
	}
	rec.RunURL = missionURL(a.runURL, rec.MissionID)
	return gwsdk.ResolveResult{
		Record:          rec,
		AlreadyResolved: out.AlreadyResolved,
		NotFound:        out.NotFound,
	}, nil
}

func missionURL(resolve func(string) string, id string) string {
	if resolve == nil || id == "" {
		return ""
	}
	return resolve(id)
}
