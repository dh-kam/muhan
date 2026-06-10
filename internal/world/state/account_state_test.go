package state_test

import (
	"slices"
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestSetCreatureDescriptionUpdatesCanonicalField(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		Description: "old description",
		Properties: map[string]string{
			"description":        "stale property",
			"legacyPasswordHash": "old-hash",
		},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	updated, err := runtime.SetCreatureDescription("creature:alice", "new description ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "new description " {
		t.Fatalf("Description = %q, want new description", updated.Description)
	}
	if _, ok := updated.Properties["description"]; ok {
		t.Fatalf("description property was not cleared: %+v", updated.Properties)
	}
	if updated.Properties["legacyPasswordHash"] != "old-hash" {
		t.Fatalf("unrelated property changed: %+v", updated.Properties)
	}

	updated, err = runtime.SetCreatureDescription("creature:alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != "" {
		t.Fatalf("cleared Description = %q, want empty", updated.Description)
	}
}

func TestSetCreaturePasswordHashUsesCanonicalProperty(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	updated, err := runtime.SetCreaturePasswordHash("creature:alice", " hash-value ")
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Properties["legacyPasswordHash"]; got != "hash-value" {
		t.Fatalf("legacyPasswordHash = %q, want hash-value", got)
	}

	updated, err = runtime.SetCreaturePasswordHash("creature:alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.Properties["legacyPasswordHash"]; ok {
		t.Fatalf("legacyPasswordHash was not removed: %+v", updated.Properties)
	}
}

func TestPreparePlayerSuicideMarksStateWithoutDeleting(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		Metadata:    model.Metadata{Tags: []string{"existingPlayerTag"}},
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		Metadata:    model.Metadata{Tags: []string{"existingCreatureTag"}},
		Properties:  map[string]string{"legacyPasswordHash": "hash"},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	player, creature, err := runtime.PreparePlayerSuicide("player:alice", 1234)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(player.Metadata.Tags, "suicidePending") {
		t.Fatalf("player tags = %v, want suicidePending", player.Metadata.Tags)
	}
	if !slices.Contains(creature.Metadata.Tags, "suicidePending") {
		t.Fatalf("creature tags = %v, want suicidePending", creature.Metadata.Tags)
	}
	if got := creature.Properties["suicideRequestedAt"]; got != "1234" {
		t.Fatalf("suicideRequestedAt = %q, want 1234", got)
	}
	if _, ok := runtime.Player("player:alice"); !ok {
		t.Fatal("player was deleted")
	}
	if _, ok := runtime.Creature("creature:alice"); !ok {
		t.Fatal("creature was deleted")
	}
}

func TestDustPlayerRemovesRuntimeOccupants(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddRoom(t, loaded, model.Room{
		ID:          "room:plaza",
		DisplayName: "광장",
		PlayerIDs:   []model.PlayerID{"player:alice", "player:bob"},
		CreatureIDs: []model.CreatureID{"creature:alice", "creature:bob"},
	})
	mustAddPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:plaza",
	})
	mustAddPlayer(t, loaded, model.Player{
		ID:          "player:bob",
		DisplayName: "Bob",
		CreatureID:  "creature:bob",
		RoomID:      "room:plaza",
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		RoomID:      "room:plaza",
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:bob",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Bob",
		PlayerID:    "player:bob",
		RoomID:      "room:plaza",
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	if err := runtime.SetCreatureCooldown("creature:alice", "attack", 1000, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AddEnemy("creature:alice", "creature:bob"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DustPlayer("player:alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Player("player:alice"); ok {
		t.Fatal("player still exists after DustPlayer")
	}
	if _, ok := runtime.Creature("creature:alice"); ok {
		t.Fatal("creature still exists after DustPlayer")
	}
	room, ok := runtime.Room("room:plaza")
	if !ok {
		t.Fatal("missing room:plaza")
	}
	if slices.Contains(room.PlayerIDs, model.PlayerID("player:alice")) {
		t.Fatalf("room player ids = %v, want alice removed", room.PlayerIDs)
	}
	if slices.Contains(room.CreatureIDs, model.CreatureID("creature:alice")) {
		t.Fatalf("room creature ids = %v, want alice removed", room.CreatureIDs)
	}
	if !slices.Contains(room.PlayerIDs, model.PlayerID("player:bob")) {
		t.Fatalf("room player ids = %v, want bob preserved", room.PlayerIDs)
	}
	if !slices.Contains(room.CreatureIDs, model.CreatureID("creature:bob")) {
		t.Fatalf("room creature ids = %v, want bob preserved", room.CreatureIDs)
	}
	if _, ok, err := runtime.CreatureCooldownExpires("creature:alice", "attack"); err == nil || ok {
		t.Fatalf("CreatureCooldownExpires after DustPlayer = ok %v err %v, want creature missing", ok, err)
	}
	enemies, err := runtime.CreatureEnemies("creature:alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(enemies) != 0 {
		t.Fatalf("enemies after DustPlayer = %v, want none", enemies)
	}
}
