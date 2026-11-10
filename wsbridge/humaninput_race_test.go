package wsbridge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"squadron/aitools"
	"squadron/humaninput"
	"squadron/store"
)

type requestCreationHook struct {
	store.HumanInputStore
	create func(*store.HumanInputRequestRecord) error
}

func (s requestCreationHook) CreateRequest(req *store.HumanInputRequestRecord) error {
	return s.create(req)
}

func TestAskHumanReceivesAnswerAsSoonAsQuestionIsStored(t *testing.T) {
	bundle, err := store.NewSQLiteBundle(filepath.Join(t.TempDir(), "questions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	client := &Client{stores: bundle, humanInputs: newHumanInputListeners()}
	original := bundle.HumanInputs
	bundle.HumanInputs = requestCreationHook{HumanInputStore: original, create: func(req *store.HumanInputRequestRecord) error {
		if err := original.CreateRequest(req); err != nil {
			return err
		}
		// Resolve while creation is returning, before AskHuman can publish or wait.
		_, err := humaninput.Resolve(bundle, client.humanInputs, nil, req.ToolCallID, "Proceed", "reviewer")
		return err
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	answer, err := client.AskHuman(ctx, aitools.HumanInputRequest{ToolCallID: "immediate", Question: "Continue?"})
	if err != nil || answer != "Proceed" {
		t.Fatalf("answer = %q, error = %v", answer, err)
	}
	client.humanInputs.mu.Lock()
	defer client.humanInputs.mu.Unlock()
	if len(client.humanInputs.m) != 0 {
		t.Fatal("completed question retained its listener")
	}
}

func TestAskHumanRemovesListenerWhenQuestionCreationFails(t *testing.T) {
	failure := errors.New("cannot save question")
	client := &Client{humanInputs: newHumanInputListeners(), stores: &store.Bundle{
		HumanInputs: requestCreationHook{create: func(*store.HumanInputRequestRecord) error { return failure }},
	}}
	_, err := client.AskHuman(context.Background(), aitools.HumanInputRequest{ToolCallID: "failed", Question: "Continue?"})
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want persistence failure", err)
	}
	client.humanInputs.mu.Lock()
	defer client.humanInputs.mu.Unlock()
	if len(client.humanInputs.m) != 0 {
		t.Fatal("failed question retained its listener")
	}
}
