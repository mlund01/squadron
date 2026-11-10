package store_test

import (
	"path/filepath"
	"testing"

	"squadron/store"
)

func TestSQLiteCostStoreUsesStableTurnIDIdempotently(t *testing.T) {
	bundle, err := store.NewSQLiteBundle(filepath.Join(t.TempDir(), "costs.db"))
	if err != nil {
		t.Fatalf("NewSQLiteBundle() error = %v", err)
	}
	defer bundle.Close()

	missionID, err := bundle.Missions.CreateMission("durable", "test-thread", `{}`, `{}`)
	if err != nil {
		t.Fatalf("CreateMission() error = %v", err)
	}
	first := store.TurnCostRecord{
		ID: "run:commander:turn:1", MissionID: missionID, MissionName: "durable",
		Entity: "commander", Model: "model-a", InputTokens: 11, OutputTokens: 3,
	}
	if err := bundle.Costs.StoreTurnCost(first); err != nil {
		t.Fatalf("first StoreTurnCost() error = %v", err)
	}
	conflictingReplay := first
	conflictingReplay.Model = "model-b"
	conflictingReplay.InputTokens = 999
	if err := bundle.Costs.StoreTurnCost(conflictingReplay); err != nil {
		t.Fatalf("replayed StoreTurnCost() error = %v", err)
	}

	records, err := bundle.Costs.GetCostsByMission(missionID)
	if err != nil {
		t.Fatalf("GetCostsByMission() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want one stable turn", len(records))
	}
	if records[0].ID != first.ID || records[0].Model != first.Model || records[0].InputTokens != first.InputTokens {
		t.Fatalf("stored stable turn = %#v, want original %#v", records[0], first)
	}
}
