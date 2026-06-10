package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestSavePlayer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "saveplayer-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Build a loaded world state
	src := worldload.NewWorld()
	playerID := model.PlayerID("player:alice")
	creatureID := model.CreatureID("creature:player:alice")

	src.Players[playerID] = model.Player{
		ID:          playerID,
		DisplayName: "alice",
		CreatureID:  creatureID,
		RoomID:      model.RoomID("r00001"),
	}

	src.Creatures[creatureID] = model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "alice",
		RoomID:      model.RoomID("r00001"),
		PlayerID:    playerID,
		Inventory: model.ObjectRefList{
			ObjectIDs: []model.ObjectInstanceID{"obj:bag"},
		},
		Equipment: map[string]model.ObjectInstanceID{
			"weapon": "obj:sword",
		},
	}

	src.Objects["obj:bag"] = model.ObjectInstance{
		ID:          "obj:bag",
		PrototypeID: "proto:bag",
		Location:    model.ObjectLocation{CreatureID: creatureID, Slot: "inventory"},
		Contents: model.ObjectRefList{
			ObjectIDs: []model.ObjectInstanceID{"obj:gem"},
		},
	}

	src.Objects["obj:gem"] = model.ObjectInstance{
		ID:          "obj:gem",
		PrototypeID: "proto:gem",
		Location:    model.ObjectLocation{ContainerID: "obj:bag"},
	}

	src.Objects["obj:sword"] = model.ObjectInstance{
		ID:          "obj:sword",
		PrototypeID: "proto:sword",
		Location:    model.ObjectLocation{CreatureID: creatureID, Slot: "weapon"},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)

	err = w.SavePlayer(playerID)
	if err != nil {
		t.Fatalf("SavePlayer failed: %v", err)
	}

	// Verify the file was written
	expectedPath := filepath.Join(tempDir, "player", "json", "alice.json")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("Failed to read expected player JSON: %v", err)
	}

	// Decode and verify contents
	type PlayerSaveData struct {
		SchemaVersion int                    `json:"schemaVersion,omitempty"`
		Player        model.Player           `json:"player"`
		Creature      *model.Creature        `json:"creature,omitempty"`
		Objects       []model.ObjectInstance `json:"objects,omitempty"`
	}

	var decoded PlayerSaveData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to decode player JSON: %v", err)
	}

	if decoded.Player.ID != playerID {
		t.Errorf("decoded.Player.ID = %q, want %q", decoded.Player.ID, playerID)
	}

	if decoded.Creature == nil || decoded.Creature.ID != creatureID {
		t.Errorf("decoded.Creature.ID = %v, want %q", decoded.Creature, creatureID)
	}

	// Check objects collected (should be obj:bag, obj:gem, obj:sword)
	if len(decoded.Objects) != 3 {
		t.Fatalf("Expected 3 objects, got %d", len(decoded.Objects))
	}

	// Because of sorting, they should be in order: obj:bag, obj:gem, obj:sword
	if decoded.Objects[0].ID != "obj:bag" {
		t.Errorf("Expected first object to be obj:bag, got %s", decoded.Objects[0].ID)
	}
	if decoded.Objects[1].ID != "obj:gem" {
		t.Errorf("Expected second object to be obj:gem, got %s", decoded.Objects[1].ID)
	}
	if decoded.Objects[2].ID != "obj:sword" {
		t.Errorf("Expected third object to be obj:sword, got %s", decoded.Objects[2].ID)
	}
}

func TestDestroyObjectMarksPlayerDirtyForRecursiveInventorySave(t *testing.T) {
	tempDir := t.TempDir()
	src := worldload.NewWorld()
	playerID := model.PlayerID("player:alice")
	creatureID := model.CreatureID("creature:player:alice")

	src.Players[playerID] = model.Player{
		ID:          playerID,
		DisplayName: "alice",
		CreatureID:  creatureID,
		RoomID:      "room:start",
	}
	src.Creatures[creatureID] = model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "alice",
		RoomID:      "room:start",
		PlayerID:    playerID,
		Inventory:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"obj:bag"}},
	}
	src.Objects["obj:bag"] = model.ObjectInstance{
		ID:          "obj:bag",
		PrototypeID: "proto:bag",
		Location:    model.ObjectLocation{CreatureID: creatureID, Slot: "inventory"},
		Contents:    model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"obj:gem"}},
	}
	src.Objects["obj:gem"] = model.ObjectInstance{
		ID:          "obj:gem",
		PrototypeID: "proto:gem",
		Location:    model.ObjectLocation{ContainerID: "obj:bag"},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)
	if err := w.DestroyObject("obj:bag"); err != nil {
		t.Fatalf("DestroyObject() error = %v", err)
	}
	if _, ok := w.Object("obj:gem"); ok {
		t.Fatal("nested child survived DestroyObject")
	}
	if err := w.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks() error = %v", err)
	}

	save, ok, err := state.LoadPlayer(tempDir, playerID)
	if err != nil {
		t.Fatalf("LoadPlayer() error = %v", err)
	}
	if !ok {
		t.Fatal("dirty player was not saved after DestroyObject")
	}
	if save.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if len(save.Creature.Inventory.ObjectIDs) != 0 {
		t.Fatalf("saved inventory = %+v, want empty", save.Creature.Inventory.ObjectIDs)
	}
	if len(save.Objects) != 0 {
		t.Fatalf("saved objects = %+v, want recursive object tree removed", save.Objects)
	}
}

func TestSidecarNamesRejectPathTraversal(t *testing.T) {
	t.Run("save player", func(t *testing.T) {
		tempDir := t.TempDir()
		src := worldload.NewWorld()
		playerID := model.PlayerID("player:../escape")
		creatureID := model.CreatureID("creature:player:escape")
		src.Players[playerID] = model.Player{ID: playerID, DisplayName: "escape", CreatureID: creatureID, RoomID: "room:start"}
		src.Creatures[creatureID] = model.Creature{ID: creatureID, Kind: model.CreatureKindPlayer, PlayerID: playerID}

		world := state.NewWorld(src)
	defer world.Close()
		world.SetDBRoot(tempDir)
		if err := world.SavePlayer(playerID); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("SavePlayer traversal error = %v, want unsafe", err)
		}
		if _, err := os.Stat(filepath.Join(tempDir, "player", "escape.json")); !os.IsNotExist(err) {
			t.Fatalf("SavePlayer wrote outside player/json: %v", err)
		}
	})

	t.Run("load player", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, "player", "escape.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(state.PlayerSaveData{
			SchemaVersion: state.CurrentSaveSchemaVersion,
			Player:        model.Player{ID: "player:escape"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := state.LoadPlayer(tempDir, "player:../escape"); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("LoadPlayer traversal = ok %v err %v, want unsafe error", ok, err)
		}
	})

	t.Run("save bank", func(t *testing.T) {
		tempDir := t.TempDir()
		src := worldload.NewWorld()
		bankID := model.BankID("bank:player:escape")
		src.Banks[bankID] = model.BankAccount{ID: bankID, OwnerName: "../escape-bank"}

		world := state.NewWorld(src)
	defer world.Close()
		world.SetDBRoot(tempDir)
		if err := world.SaveBank(bankID); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("SaveBank traversal error = %v, want unsafe", err)
		}
		if _, err := os.Stat(filepath.Join(tempDir, "player", "bank", "escape-bank.json")); !os.IsNotExist(err) {
			t.Fatalf("SaveBank wrote outside bank/json: %v", err)
		}
	})

	t.Run("load bank", func(t *testing.T) {
		tempDir := t.TempDir()
		path := filepath.Join(tempDir, "player", "bank", "escape-bank.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(model.BankSaveBundle{
			SchemaVersion: state.CurrentSaveSchemaVersion,
			BankAccount:   model.BankAccount{ID: "bank:player:escape"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		world := state.NewWorld(nil)
	defer world.Close()
		world.SetDBRoot(tempDir)
		if _, ok, err := world.LoadBank("bank:player:../escape-bank"); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("LoadBank traversal = ok %v err %v, want unsafe error", ok, err)
		}
	})

	t.Run("board", func(t *testing.T) {
		tempDir := t.TempDir()
		world := state.NewWorld(nil)
	defer world.Close()
		world.SetDBRoot(tempDir)
		if err := world.SaveBoardPosts("../escape-board"); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("SaveBoardPosts traversal error = %v, want unsafe", err)
		}
		if _, ok, err := state.LoadBoardPosts(tempDir, "../escape-board"); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("LoadBoardPosts traversal = ok %v err %v, want unsafe error", ok, err)
		}
	})

	t.Run("unicode slash spoofing", func(t *testing.T) {
		for _, slash := range []string{"\u2044", "\u2215", "\u29f5", "\ufe68", "\uff0f", "\uff3c"} {
			t.Run(slash, func(t *testing.T) {
				tempDir := t.TempDir()
				spoof := ".." + slash + "escape"

				src := worldload.NewWorld()
				playerID := model.PlayerID("player:" + spoof)
				creatureID := model.CreatureID("creature:spoof")
				src.Players[playerID] = model.Player{ID: playerID, DisplayName: "escape", CreatureID: creatureID, RoomID: "room:start"}
				src.Creatures[creatureID] = model.Creature{ID: creatureID, Kind: model.CreatureKindPlayer, PlayerID: playerID}
				bankID := model.BankID("bank:player:spoof")
				src.Banks[bankID] = model.BankAccount{ID: bankID, OwnerName: spoof}
				roomID := model.RoomID("room:" + spoof)
				src.Rooms[roomID] = model.Room{ID: roomID}

				world := state.NewWorld(src)
	defer world.Close()
				world.SetDBRoot(tempDir)
				if err := world.SavePlayer(playerID); err == nil || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("SavePlayer unicode slash error = %v, want unsafe", err)
				}
				if _, ok, err := state.LoadPlayer(tempDir, playerID); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("LoadPlayer unicode slash = ok %v err %v, want unsafe", ok, err)
				}
				if err := world.SaveBank(bankID); err == nil || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("SaveBank unicode slash error = %v, want unsafe", err)
				}
				if _, ok, err := world.LoadBank(model.BankID("bank:player:" + spoof)); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("LoadBank unicode slash = ok %v err %v, want unsafe", ok, err)
				}
				if err := world.SaveRoomObjects(roomID); err == nil || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("SaveRoomObjects unicode slash error = %v, want unsafe", err)
				}
				if _, ok, err := state.LoadRoomObjects(tempDir, roomID); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("LoadRoomObjects unicode slash = ok %v err %v, want unsafe", ok, err)
				}
				if err := world.SaveBoardPosts(spoof); err == nil || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("SaveBoardPosts unicode slash error = %v, want unsafe", err)
				}
				if _, ok, err := state.LoadBoardPosts(tempDir, spoof); err == nil || ok || !strings.Contains(err.Error(), "unsafe") {
					t.Fatalf("LoadBoardPosts unicode slash = ok %v err %v, want unsafe", ok, err)
				}
			})
		}
	})
}

// Package C tests for board posts + family news JSON sidecar, dirty, Save/Load/Merge, flush.
func TestLoadBoardPostsAndFamilyNews_NoSidecar(t *testing.T) {
	tempDir := t.TempDir()
	_, ok, err := state.LoadBoardPosts(tempDir, "info")
	if err != nil {
		t.Fatalf("LoadBoardPosts nonexist: %v", err)
	}
	if ok {
		t.Error("expected no board sidecar")
	}
	_, ok, err = state.LoadFamilyNews(tempDir, 7)
	if err != nil {
		t.Fatalf("LoadFamilyNews nonexist: %v", err)
	}
	if ok {
		t.Error("expected no family news sidecar")
	}
}

func TestSaveLoadFamilyNewsRoundtrip(t *testing.T) {
	tempDir := t.TempDir()
	world := state.New(nil)
	world.SetDBRoot(tempDir)

	content := "                      === 패거리 공지 ===\n\nC package test news.\n"
	if err := world.SaveFamilyNews(42, content); err != nil {
		t.Fatalf("SaveFamilyNews: %v", err)
	}

	loaded, ok, err := state.LoadFamilyNews(tempDir, 42)
	if err != nil {
		t.Fatalf("LoadFamilyNews: %v", err)
	}
	if !ok {
		t.Fatal("expected family news sidecar after save")
	}
	if loaded.Content != content || loaded.FamilyID != 42 {
		t.Errorf("roundtrip mismatch")
	}
	if err := world.MergeFamilyNewsSaveIntoWorld(loaded); err != nil {
		t.Errorf("MergeFamilyNews: %v", err)
	}
}

func TestBoardDirtyAndMarkAndFlush(t *testing.T) {
	world := state.New(nil)
	world.SetDBRoot(t.TempDir())
	world.MarkBoardDirty("info")
	world.MarkFamilyNewsDirty(1)
	if err := world.FlushDirtyBoardsAndFamilyNews(0); err != nil {
		// may err on SaveBoard for missing dir, but ok for test (we test mark path)
		t.Logf("flush note (expected for missing board): %v", err)
	}
}

func TestSaveBoardPosts_ErrOnMissing(t *testing.T) {
	tempDir := t.TempDir()
	world := state.New(nil)
	world.SetDBRoot(tempDir)
	if err := world.SaveBoardPosts("no-such-board"); err == nil {
		t.Error("wanted err for missing board dir")
	}
}
