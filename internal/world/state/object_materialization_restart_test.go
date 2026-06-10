package state_test

import (
	"reflect"
	"slices"
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestMaterializedORENCHObjectSurvivesCreateGiveDropSaveRestart(t *testing.T) {
	dbRoot := t.TempDir()
	runtime := state.NewWorld(materializationRestartWorld(t))
	defer runtime.Close()
	runtime.SetDBRoot(dbRoot)

	bagID, err := runtime.CloneObjectToCreatureInventory("prototype:enchanted-bag", "creature:alice")
	if err != nil {
		t.Fatalf("CloneObjectToCreatureInventory() error = %v", err)
	}
	created := requireMaterializedObjectSnapshot(t, runtime, bagID)
	if created.root.Properties["adjustment"] == "" || created.root.Properties["pDice"] == "" {
		t.Fatalf("created ORENCH object lost enchant fields: %+v", created.root.Properties)
	}

	if err := runtime.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("flush after create: %v", err)
	}
	afterCreate := restartWithPlayerSaves(t, dbRoot, "player:alice")
	assertMaterializedObjectSnapshot(t, afterCreate, created)
	assertCreatureContainsObject(t, afterCreate, "creature:alice", bagID)

	if err := runtime.MoveObject(bagID, model.ObjectLocation{CreatureID: "creature:bob", Slot: "inventory"}); err != nil {
		t.Fatalf("give/move object to bob: %v", err)
	}
	given := requireMaterializedObjectSnapshot(t, runtime, bagID)
	if err := runtime.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("flush after give: %v", err)
	}
	afterGive := restartWithPlayerSaves(t, dbRoot, "player:alice", "player:bob")
	assertMaterializedObjectSnapshot(t, afterGive, given)
	assertCreatureDoesNotContainObject(t, afterGive, "creature:alice", bagID)
	assertCreatureContainsObject(t, afterGive, "creature:bob", bagID)

	if err := runtime.MoveObject(bagID, model.ObjectLocation{RoomID: "room:plaza"}); err != nil {
		t.Fatalf("drop/move object to room: %v", err)
	}
	dropped := requireMaterializedObjectSnapshot(t, runtime, bagID)
	if err := runtime.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("flush players after drop: %v", err)
	}
	if err := runtime.FlushDirtyRoomObjects(0); err != nil {
		t.Fatalf("flush room after drop: %v", err)
	}
	afterDrop := restartWithPlayerSaves(t, dbRoot, "player:bob")
	roomSave, ok, err := state.LoadRoomObjects(dbRoot, "room:plaza")
	if err != nil || !ok {
		t.Fatalf("LoadRoomObjects after drop = ok %v err %v", ok, err)
	}
	if err := afterDrop.MergeRoomObjectsSaveIntoWorld(roomSave); err != nil {
		t.Fatalf("MergeRoomObjectsSaveIntoWorld after drop: %v", err)
	}
	assertMaterializedObjectSnapshot(t, afterDrop, dropped)
	assertCreatureDoesNotContainObject(t, afterDrop, "creature:bob", bagID)
	assertRoomContainsObject(t, afterDrop, "room:plaza", bagID)
}

type materializedObjectSnapshot struct {
	root  model.ObjectInstance
	child model.ObjectInstance
}

func materializationRestartWorld(t *testing.T) *worldload.World {
	t.Helper()

	loaded := worldload.NewWorld()
	mustAddRoom(t, loaded, model.Room{ID: "room:plaza", DisplayName: "Plaza"})
	mustAddRoom(t, loaded, model.Room{ID: "room:templates", DisplayName: "Templates"})
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
		Inventory:   model.ObjectRefList{},
		Equipment:   map[string]model.ObjectInstanceID{},
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:bob",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Bob",
		PlayerID:    "player:bob",
		RoomID:      "room:plaza",
		Inventory:   model.ObjectRefList{},
		Equipment:   map[string]model.ObjectInstanceID{},
	})
	mustAddObjectPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:enchanted-bag",
		Kind:        model.ObjectKindContainer,
		DisplayName: "Enchanted Bag",
		Properties:  map[string]string{"adjustment": "2", "pDice": "1"},
		Metadata: model.Metadata{
			Tags: []string{"ORENCH"},
			PrototypeResolution: &model.PrototypeResolutionMetadata{
				MaterializedFromObjectInstanceID: "object:template-bag",
			},
		},
	})
	mustAddObjectPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:gem",
		Kind:        model.ObjectKindMisc,
		DisplayName: "Gem",
	})
	mustAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:template-bag",
		PrototypeID: "prototype:enchanted-bag",
		Quantity:    1,
		Location:    model.ObjectLocation{RoomID: "room:templates"},
		Contents:    model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:template-gem"}},
		Properties:  map[string]string{"adjustment": "2", "pDice": "1"},
		Metadata:    model.Metadata{Tags: []string{"ORENCH"}},
	})
	mustAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:template-gem",
		PrototypeID: "prototype:gem",
		Quantity:    1,
		Location:    model.ObjectLocation{ContainerID: "object:template-bag"},
		Properties:  map[string]string{"value": "7"},
	})
	return loaded
}

func restartWithPlayerSaves(t *testing.T, dbRoot string, playerIDs ...model.PlayerID) *state.World {
	t.Helper()

	runtime := state.NewWorld(materializationRestartWorld(t))
	defer runtime.Close()
	runtime.SetDBRoot(dbRoot)
	for _, playerID := range playerIDs {
		save, ok, err := state.LoadPlayer(dbRoot, playerID)
		if err != nil || !ok {
			t.Fatalf("LoadPlayer(%s) = ok %v err %v", playerID, ok, err)
		}
		if err := runtime.MergePlayerSaveIntoWorld(save); err != nil {
			t.Fatalf("MergePlayerSaveIntoWorld(%s): %v", playerID, err)
		}
	}
	return runtime
}

func requireMaterializedObjectSnapshot(t *testing.T, runtime *state.World, rootID model.ObjectInstanceID) materializedObjectSnapshot {
	t.Helper()

	root, ok := runtime.Object(rootID)
	if !ok {
		t.Fatalf("missing materialized root %q", rootID)
	}
	if len(root.Contents.ObjectIDs) != 1 {
		t.Fatalf("root %q contents = %+v, want one child", rootID, root.Contents.ObjectIDs)
	}
	childID := root.Contents.ObjectIDs[0]
	child, ok := runtime.Object(childID)
	if !ok {
		t.Fatalf("missing materialized child %q", childID)
	}
	if child.Location.ContainerID != rootID {
		t.Fatalf("child %q location = %+v, want container %q", childID, child.Location, rootID)
	}
	return materializedObjectSnapshot{root: root, child: child}
}

func assertMaterializedObjectSnapshot(t *testing.T, runtime *state.World, want materializedObjectSnapshot) {
	t.Helper()

	gotRoot, ok := runtime.Object(want.root.ID)
	if !ok {
		t.Fatalf("missing restored root %q", want.root.ID)
	}
	gotChild, ok := runtime.Object(want.child.ID)
	if !ok {
		t.Fatalf("missing restored child %q", want.child.ID)
	}
	assertObjectInstanceEqual(t, gotRoot, want.root)
	assertObjectInstanceEqual(t, gotChild, want.child)
}

func assertObjectInstanceEqual(t *testing.T, got, want model.ObjectInstance) {
	t.Helper()

	if got.ID != want.ID ||
		got.PrototypeID != want.PrototypeID ||
		got.DisplayNameOverride != want.DisplayNameOverride ||
		got.Quantity != want.Quantity ||
		got.Location != want.Location ||
		!slices.Equal(got.Contents.ObjectIDs, want.Contents.ObjectIDs) ||
		!reflect.DeepEqual(got.Properties, want.Properties) ||
		!slices.Equal(got.Metadata.Tags, want.Metadata.Tags) {
		t.Fatalf("object mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func assertCreatureContainsObject(t *testing.T, runtime *state.World, creatureID model.CreatureID, objectID model.ObjectInstanceID) {
	t.Helper()

	creature, ok := runtime.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %q", creatureID)
	}
	if !slices.Contains(creature.Inventory.ObjectIDs, objectID) {
		t.Fatalf("creature %q inventory = %+v, want %q", creatureID, creature.Inventory.ObjectIDs, objectID)
	}
}

func assertCreatureDoesNotContainObject(t *testing.T, runtime *state.World, creatureID model.CreatureID, objectID model.ObjectInstanceID) {
	t.Helper()

	creature, ok := runtime.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %q", creatureID)
	}
	if slices.Contains(creature.Inventory.ObjectIDs, objectID) {
		t.Fatalf("creature %q inventory = %+v, did not want %q", creatureID, creature.Inventory.ObjectIDs, objectID)
	}
}

func assertRoomContainsObject(t *testing.T, runtime *state.World, roomID model.RoomID, objectID model.ObjectInstanceID) {
	t.Helper()

	room, ok := runtime.Room(roomID)
	if !ok {
		t.Fatalf("missing room %q", roomID)
	}
	if !slices.Contains(room.Objects.ObjectIDs, objectID) {
		t.Fatalf("room %q objects = %+v, want %q", roomID, room.Objects.ObjectIDs, objectID)
	}
}
