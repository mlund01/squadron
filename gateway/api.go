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
		out = append(out, storeRecordToSDK(r))
	}
	return out, total, nil
}

func (a *squadronAPI) ResolveHumanInput(ctx context.Context, toolCallID, response, responderUserID string) (gwsdk.ResolveResult, error) {
	out, err := humaninput.Resolve(a.stores, a.listener, a.notifier, toolCallID, response, responderUserID)
	if err != nil {
		return gwsdk.ResolveResult{}, err
	}
	return gwsdk.ResolveResult{
		Record:          storeRecordToSDK(out.Record),
		AlreadyResolved: out.AlreadyResolved,
		NotFound:        out.NotFound,
	}, nil
}
