package state_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestPlayerDeath_PvE(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "playerdeath-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	src := worldload.NewWorld()

	playerID := model.PlayerID("player:bob")
	creatureID := model.CreatureID("creature:player:bob")
	room1ID := model.RoomID("room:1")
	room1008ID := model.RoomID("room:1008")

	src.Rooms[room1ID] = model.Room{
		ID:        room1ID,
		PlayerIDs: []model.PlayerID{playerID},
	}
	src.Rooms[room1008ID] = model.Room{
		ID: room1008ID,
	}

	src.Players[playerID] = model.Player{
		ID:          playerID,
		DisplayName: "bob",
		CreatureID:  creatureID,
		RoomID:      room1ID,
		Metadata: model.Metadata{
			Tags: []string{"PPOISN", "poison"},
		},
	}

	src.Creatures[creatureID] = model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "bob",
		RoomID:      room1ID,
		PlayerID:    playerID,
		Level:       30,
		Stats: map[string]int{
			"experience": 50000,
			"level":      30,
			"hpCurrent":  5,
			"hpMax":      100,
			"mpCurrent":  10,
			"mpMax":      50,
			"alignment":  200,
		},
		Inventory: model.ObjectRefList{
			ObjectIDs: []model.ObjectInstanceID{"item:1"},
		},
		Equipment: map[string]model.ObjectInstanceID{
			"weapon": "item:2",
		},
		Properties: map[string]string{
			"legacyTitle": "초보 용사",
		},
		Metadata: model.Metadata{
			Tags: []string{"PDISEA", "disease"},
		},
	}

	src.Objects["item:1"] = model.ObjectInstance{
		ID:          "item:1",
		PrototypeID: "proto:potion",
		Location:    model.ObjectLocation{CreatureID: creatureID, Slot: "inventory"},
	}

	src.Objects["item:2"] = model.ObjectInstance{
		ID:          "item:2",
		PrototypeID: "proto:sword",
		Location:    model.ObjectLocation{CreatureID: creatureID, Slot: "weapon"},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)

	// Kill bob via PvE (attacker ID is zero/empty)
	err = w.PlayerDeath(playerID, model.CreatureID(""))
	if err != nil {
		t.Fatalf("PlayerDeath failed: %v", err)
	}

	// 1. Verify Bob is now in room:1008
	bobPlayer, _ := w.Player(playerID)
	if bobPlayer.RoomID != room1008ID {
		t.Errorf("bob player room = %s, want %s", bobPlayer.RoomID, room1008ID)
	}
	bobCrt, _ := w.Creature(creatureID)
	if bobCrt.RoomID != room1008ID {
		t.Errorf("bob creature room = %s, want %s", bobCrt.RoomID, room1008ID)
	}

	// 2. Verify Bob keeps ordinary inventory but loses eligible ready equipment.
	if len(bobCrt.Inventory.ObjectIDs) != 1 || bobCrt.Inventory.ObjectIDs[0] != "item:1" {
		t.Errorf("bob inventory = %v, want item:1 preserved", bobCrt.Inventory.ObjectIDs)
	}
	if got := bobCrt.Equipment["weapon"]; got != "" {
		t.Errorf("bob weapon equipment = %s, want dropped", got)
	}

	// 3. Verify Bob's title is reset
	if bobCrt.Properties["legacyTitle"] != "" {
		t.Errorf("bob title not cleared: %q", bobCrt.Properties["legacyTitle"])
	}

	// 4. Verify poison/disease tags are removed
	for _, tag := range bobPlayer.Metadata.Tags {
		if tag == "PPOISN" || tag == "poison" {
			t.Errorf("Bob still has poison tag: %s", tag)
		}
	}
	for _, tag := range bobCrt.Metadata.Tags {
		if tag == "PDISEA" || tag == "disease" {
			t.Errorf("Bob still has disease tag: %s", tag)
		}
	}

	// 5. Verify exp has been deducted and level has been updated accordingly
	// bob started at 50,000 exp and level 30 (level >= 20).
	// PvE formula for level >= 20:
	// experience -= experience / 15 => 50000 - 3333 = 46667
	// Level for 46,667 exp is expToLev(46667) => let's check legacyNeededExperience
	// 40960 (level 30 threshold is index 29 => legacyNeededExperience[29]=40960).
	// index 30 is 49152. So level should drop from 30 to 30 (since 46667 >= 40960).
	// Let's assert exp is 46667 and level is 30.
	if exp := bobCrt.Stats["experience"]; exp != 46667 {
		t.Errorf("expected experience = 46667, got %d", exp)
	}

	// 6. Verify Bob's HP is restored to max and MP is restored to max (since PvE)
	if bobCrt.Stats["hpCurrent"] != 100 {
		t.Errorf("expected HP = 100, got %d", bobCrt.Stats["hpCurrent"])
	}
	if bobCrt.Stats["mpCurrent"] != 50 {
		t.Errorf("expected MP = 50, got %d", bobCrt.Stats["mpCurrent"])
	}

	// 7. Verify C-style ready[] drop: wield/weapon moved to the death room, no corpse container.
	room1, _ := w.Room(room1ID)
	if len(room1.Objects.ObjectIDs) != 1 || room1.Objects.ObjectIDs[0] != "item:2" {
		t.Fatalf("room objects = %+v, want dropped weapon item:2", room1.Objects.ObjectIDs)
	}
	item1, _ := w.Object("item:1")
	if item1.Location.CreatureID != creatureID || item1.Location.Slot != "inventory" {
		t.Errorf("item:1 location = %+v, want creature inventory", item1.Location)
	}
	item2, _ := w.Object("item:2")
	if item2.Location.RoomID != room1ID {
		t.Errorf("item:2 location = %+v, want room:1", item2.Location)
	}
	if !hasTag(item2.Metadata.Tags, "OPERM2") || !hasTag(item2.Metadata.Tags, "OTEMPP") {
		t.Errorf("item:2 tags = %+v, want OPERM2/OTEMPP", item2.Metadata.Tags)
	}

	// 8. Verify SavePlayer wrote correct JSON file
	expectedPath := filepath.Join(tempDir, "player", "json", "bob.json")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("player JSON file not found: %v", err)
	}

	var saved struct {
		Player   model.Player           `json:"player"`
		Creature *model.Creature        `json:"creature"`
		Objects  []model.ObjectInstance `json:"objects"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("failed to parse saved player json: %v", err)
	}
	if saved.Player.ID != playerID {
		t.Errorf("saved player ID = %s, want %s", saved.Player.ID, playerID)
	}
}

func TestPlayerDeathNormalizesStatAndPropertyKeys(t *testing.T) {
	tempDir := t.TempDir()
	src := worldload.NewWorld()

	playerID := model.PlayerID("player:bob")
	creatureID := model.CreatureID("creature:bob")
	roomID := model.RoomID("room:1")
	room1008ID := model.RoomID("room:1008")

	src.Rooms[roomID] = model.Room{ID: roomID, PlayerIDs: []model.PlayerID{playerID}}
	src.Rooms[room1008ID] = model.Room{ID: room1008ID}
	src.Players[playerID] = model.Player{ID: playerID, DisplayName: "bob", CreatureID: creatureID, RoomID: roomID}
	src.Creatures[creatureID] = model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "bob",
		RoomID:      roomID,
		PlayerID:    playerID,
		Stats: map[string]int{
			"EXPERIENCE": 50000,
			"LEVEL":      30,
			"ALIGNMENT":  200,
		},
		Properties: map[string]string{
			"hp-max":     "100",
			"mp-max":     "50",
			"mp-current": "10",
		},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)
	if err := w.PlayerDeath(playerID, ""); err != nil {
		t.Fatalf("PlayerDeath failed: %v", err)
	}

	bobCrt, _ := w.Creature(creatureID)
	if exp := bobCrt.Stats["experience"]; exp != 46667 {
		t.Fatalf("normalized experience deduction = %d, want 46667", exp)
	}
	if hp := bobCrt.Stats["hpCurrent"]; hp != 100 {
		t.Fatalf("normalized hp restore = %d, want 100", hp)
	}
	if mp := bobCrt.Stats["mpCurrent"]; mp != 50 {
		t.Fatalf("normalized mp restore = %d, want 50", mp)
	}
}

func TestPlayerDeathAppliesLegacyDownLevelStatsAndPUPDMGRollback(t *testing.T) {
	tempDir := t.TempDir()
	src := worldload.NewWorld()

	playerID := model.PlayerID("player:bob")
	creatureID := model.CreatureID("creature:bob")
	roomID := model.RoomID("room:1")
	room1008ID := model.RoomID("room:1008")

	src.Rooms[roomID] = model.Room{ID: roomID, PlayerIDs: []model.PlayerID{playerID}}
	src.Rooms[room1008ID] = model.Room{ID: room1008ID}
	src.Players[playerID] = model.Player{
		ID:          playerID,
		DisplayName: "bob",
		CreatureID:  creatureID,
		RoomID:      roomID,
		Metadata:    model.Metadata{Tags: []string{"PUPDMG"}},
	}
	src.Creatures[creatureID] = model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "bob",
		RoomID:      roomID,
		PlayerID:    playerID,
		Level:       4,
		Stats: map[string]int{
			"class":        4,
			"experience":   100,
			"level":        4,
			"hpCurrent":    1,
			"hpMax":        100,
			"mpCurrent":    1,
			"mpMax":        100,
			"pDice":        5,
			"dexterity":    10,
			"constitution": 10,
			"strength":     10,
			"intelligence": 10,
			"piety":        10,
			"PUPDMG":       1,
		},
		Metadata: model.Metadata{Tags: []string{"PUPDMG"}},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)
	if err := w.PlayerDeath(playerID, ""); err != nil {
		t.Fatalf("PlayerDeath failed: %v", err)
	}

	player, _ := w.Player(playerID)
	for _, tag := range player.Metadata.Tags {
		if tag == "PUPDMG" {
			t.Fatalf("player PUPDMG tag was not cleared: %+v", player.Metadata.Tags)
		}
	}
	creature, _ := w.Creature(creatureID)
	if creature.Level != 1 || creature.Stats["level"] != 1 {
		t.Fatalf("level = %d/%d, want 1/1", creature.Level, creature.Stats["level"])
	}
	if got := creature.Stats["hpMax"]; got != 44 {
		t.Fatalf("hpMax = %d, want 44", got)
	}
	if got := creature.Stats["mpMax"]; got != 48 {
		t.Fatalf("mpMax = %d, want 48", got)
	}
	if got := creature.Stats["hpCurrent"]; got != 44 {
		t.Fatalf("hpCurrent = %d, want restored 44", got)
	}
	if got := creature.Stats["mpCurrent"]; got != 48 {
		t.Fatalf("mpCurrent = %d, want restored 48", got)
	}
	if got := creature.Stats["pDice"]; got != 3 {
		t.Fatalf("pDice = %d, want 3", got)
	}
	if got := creature.Stats["dexterity"]; got != 9 {
		t.Fatalf("dexterity = %d, want 9 from fighter down_level cycle", got)
	}
	if got := creature.Stats["PUPDMG"]; got != 0 {
		t.Fatalf("PUPDMG stat = %d, want cleared to 0", got)
	}
	for _, tag := range creature.Metadata.Tags {
		if tag == "PUPDMG" {
			t.Fatalf("creature PUPDMG tag was not cleared: %+v", creature.Metadata.Tags)
		}
	}
}

func TestPlayerDeath_PvP(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "playerdeath-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	src := worldload.NewWorld()

	player1ID := model.PlayerID("player:bob")
	creature1ID := model.CreatureID("creature:player:bob")
	player2ID := model.PlayerID("player:alice")
	creature2ID := model.CreatureID("creature:player:alice")

	room1ID := model.RoomID("room:1")
	room1008ID := model.RoomID("room:1008")

	src.Rooms[room1ID] = model.Room{
		ID:        room1ID,
		PlayerIDs: []model.PlayerID{player1ID, player2ID},
	}
	src.Rooms[room1008ID] = model.Room{
		ID: room1008ID,
	}

	src.Players[player1ID] = model.Player{
		ID:          player1ID,
		DisplayName: "bob",
		CreatureID:  creature1ID,
		RoomID:      room1ID,
	}
	src.Players[player2ID] = model.Player{
		ID:          player2ID,
		DisplayName: "alice",
		CreatureID:  creature2ID,
		RoomID:      room1ID,
	}

	src.Creatures[creature1ID] = model.Creature{
		ID:          creature1ID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "bob",
		RoomID:      room1ID,
		PlayerID:    player1ID,
		Level:       10,
		Stats: map[string]int{
			"experience":         10000,
			"level":              10,
			"hpCurrent":          2,
			"hpMax":              100,
			"mpCurrent":          2,
			"mpMax":              50,
			"alignment":          300,
			"proficiencySharp":   2000,
			"proficiencyThrust":  2000,
			"proficiencyBlunt":   2000,
			"proficiencyPole":    2000,
			"proficiencyMissile": 2000,
			"realmEarth":         2000,
			"realmWind":          2000,
			"realmFire":          2000,
			"realmWater":         2000,
		},
	}

	src.Creatures[creature2ID] = model.Creature{
		ID:          creature2ID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "alice",
		RoomID:      room1ID,
		PlayerID:    player2ID,
		Level:       15,
		Stats: map[string]int{
			"experience": 5000,
			"level":      15,
			"hpCurrent":  100,
			"hpMax":      150,
			"mpCurrent":  50,
			"mpMax":      80,
			"alignment":  100,
		},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)

	// Bob is killed by Alice (PvP)
	err = w.PlayerDeath(player1ID, creature2ID)
	if err != nil {
		t.Fatalf("PlayerDeath failed: %v", err)
	}

	// 1. Verify Bob's experience was NOT deducted (PvP death)
	bobCrt, _ := w.Creature(creature1ID)
	if exp := bobCrt.Stats["experience"]; exp != 10000 {
		t.Errorf("expected Bob's PvP exp to be unchanged (10000), got %d", exp)
	}

	// 2. Verify Bob's MP is restored to max(mpCurrent, mpMax/10) => max(2, 5) = 5
	if bobCrt.Stats["mpCurrent"] != 5 {
		t.Errorf("expected Bob's PvP MP to be 5, got %d", bobCrt.Stats["mpCurrent"])
	}

	// 3. C creature.c:die() applies alignment shifts only in the monster-death
	// reward branch. Player death does not change the player killer alignment.
	aliceCrt, _ := w.Creature(creature2ID)
	if align := aliceCrt.Stats["alignment"]; align != 100 {
		t.Errorf("expected Alice's alignment to remain 100, got %d", align)
	}

	// 4. C still applies the non-survival/check_war proficiency-loss block for
	// player death. With nine 2000-point slots and 10000 exp, profloss is
	// 18000 - 10000 - 1024 = 6976.
	if got := bobCrt.Stats["proficiencySharp"]; got != 1225 {
		t.Errorf("proficiencySharp after PvP death = %d, want 1225", got)
	}
	if got := bobCrt.Stats["realmWater"]; got != 1224 {
		t.Errorf("realmWater after PvP death = %d, want 1224", got)
	}
}

func TestPlayerDeathSurvivalRoomKeepsInventoryAndEquipment(t *testing.T) {
	tempDir := t.TempDir()
	src := worldload.NewWorld()

	player1ID := model.PlayerID("player:bob")
	creature1ID := model.CreatureID("creature:player:bob")
	player2ID := model.PlayerID("player:alice")
	creature2ID := model.CreatureID("creature:player:alice")
	room1ID := model.RoomID("room:1")
	room1008ID := model.RoomID("room:1008")

	src.Rooms[room1ID] = model.Room{
		ID:        room1ID,
		PlayerIDs: []model.PlayerID{player1ID, player2ID},
		Metadata:  model.Metadata{Tags: []string{"RSUVIV"}},
	}
	src.Rooms[room1008ID] = model.Room{ID: room1008ID}
	src.Players[player1ID] = model.Player{
		ID:          player1ID,
		DisplayName: "bob",
		CreatureID:  creature1ID,
		RoomID:      room1ID,
	}
	src.Players[player2ID] = model.Player{
		ID:          player2ID,
		DisplayName: "alice",
		CreatureID:  creature2ID,
		RoomID:      room1ID,
	}
	src.Creatures[creature1ID] = model.Creature{
		ID:          creature1ID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "bob",
		RoomID:      room1ID,
		PlayerID:    player1ID,
		Level:       10,
		Stats: map[string]int{
			"experience": 1000,
			"level":      10,
			"hpCurrent":  2,
			"hpMax":      100,
			"mpCurrent":  2,
			"mpMax":      50,
			"alignment":  300,
		},
		Inventory: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"item:1"}},
		Equipment: map[string]model.ObjectInstanceID{"weapon": "item:2"},
	}
	src.Creatures[creature2ID] = model.Creature{
		ID:          creature2ID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "alice",
		RoomID:      room1ID,
		PlayerID:    player2ID,
		Stats:       map[string]int{"alignment": 100},
	}
	src.Objects["item:1"] = model.ObjectInstance{
		ID:          "item:1",
		PrototypeID: "proto:potion",
		Location:    model.ObjectLocation{CreatureID: creature1ID, Slot: "inventory"},
	}
	src.Objects["item:2"] = model.ObjectInstance{
		ID:          "item:2",
		PrototypeID: "proto:sword",
		Location:    model.ObjectLocation{CreatureID: creature1ID, Slot: "weapon"},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)

	if err := w.PlayerDeath(player1ID, creature2ID); err != nil {
		t.Fatalf("PlayerDeath failed: %v", err)
	}

	bobCrt, _ := w.Creature(creature1ID)
	if len(bobCrt.Inventory.ObjectIDs) != 1 || bobCrt.Inventory.ObjectIDs[0] != "item:1" {
		t.Fatalf("inventory after RSUVIV death = %+v, want item:1 preserved", bobCrt.Inventory.ObjectIDs)
	}
	if got := bobCrt.Equipment["weapon"]; got != "item:2" {
		t.Fatalf("equipment after RSUVIV death = %s, want item:2 preserved", got)
	}
	item1, _ := w.Object("item:1")
	if item1.Location.CreatureID != creature1ID || item1.Location.Slot != "inventory" {
		t.Fatalf("item:1 location = %+v, want creature inventory", item1.Location)
	}
	item2, _ := w.Object("item:2")
	if item2.Location.CreatureID != creature1ID || item2.Location.Slot != "weapon" {
		t.Fatalf("item:2 location = %+v, want equipped slot", item2.Location)
	}
	room1, _ := w.Room(room1ID)
	if len(room1.Objects.ObjectIDs) != 0 {
		t.Fatalf("RSUVIV death room objects = %+v, want no corpse/drop", room1.Objects.ObjectIDs)
	}
	for _, objectID := range []model.ObjectInstanceID{"object:corpse", "object:corpse:1"} {
		if corpse, ok := w.Object(objectID); ok && corpse.DisplayNameOverride == "bob의 시체" {
			t.Fatalf("unexpected corpse object after RSUVIV death: %+v", corpse)
		}
	}
}

func TestPlayerDeathFamilyWarPairKeepsReadyEquipment(t *testing.T) {
	tempDir := t.TempDir()
	src := worldload.NewWorld()

	player1ID := model.PlayerID("player:bob")
	creature1ID := model.CreatureID("creature:player:bob")
	player2ID := model.PlayerID("player:alice")
	creature2ID := model.CreatureID("creature:player:alice")
	room1ID := model.RoomID("room:1")
	room1008ID := model.RoomID("room:1008")

	src.Rooms[room1ID] = model.Room{
		ID:        room1ID,
		PlayerIDs: []model.PlayerID{player1ID, player2ID},
	}
	src.Rooms[room1008ID] = model.Room{ID: room1008ID}
	src.Players[player1ID] = model.Player{
		ID:          player1ID,
		DisplayName: "bob",
		CreatureID:  creature1ID,
		RoomID:      room1ID,
	}
	src.Players[player2ID] = model.Player{
		ID:          player2ID,
		DisplayName: "alice",
		CreatureID:  creature2ID,
		RoomID:      room1ID,
	}
	src.Creatures[creature1ID] = model.Creature{
		ID:          creature1ID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "bob",
		RoomID:      room1ID,
		PlayerID:    player1ID,
		Level:       10,
		Stats: map[string]int{
			"familyID":   5,
			"experience": 1000,
			"level":      10,
			"hpCurrent":  2,
			"hpMax":      100,
			"mpCurrent":  2,
			"mpMax":      50,
		},
		Inventory: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"item:1"}},
		Equipment: map[string]model.ObjectInstanceID{"wield": "item:2"},
	}
	src.Creatures[creature2ID] = model.Creature{
		ID:          creature2ID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: "alice",
		RoomID:      room1ID,
		PlayerID:    player2ID,
		Stats:       map[string]int{"familyID": 2},
	}
	src.Objects["item:1"] = model.ObjectInstance{
		ID:          "item:1",
		PrototypeID: "proto:potion",
		Location:    model.ObjectLocation{CreatureID: creature1ID, Slot: "inventory"},
	}
	src.Objects["item:2"] = model.ObjectInstance{
		ID:          "item:2",
		PrototypeID: "proto:sword",
		Location:    model.ObjectLocation{CreatureID: creature1ID, Slot: "wield"},
	}

	w := state.NewWorld(src)
	defer w.Close()
	w.SetDBRoot(tempDir)
	if _, err := w.RequestFamilyWar(2, 5); err != nil {
		t.Fatalf("RequestFamilyWar: %v", err)
	}
	if _, err := w.AcceptFamilyWar(5, 2); err != nil {
		t.Fatalf("AcceptFamilyWar: %v", err)
	}

	if err := w.PlayerDeath(player1ID, creature2ID); err != nil {
		t.Fatalf("PlayerDeath failed: %v", err)
	}

	bobCrt, _ := w.Creature(creature1ID)
	if len(bobCrt.Inventory.ObjectIDs) != 1 || bobCrt.Inventory.ObjectIDs[0] != "item:1" {
		t.Fatalf("inventory after family-war death = %+v, want item:1 preserved", bobCrt.Inventory.ObjectIDs)
	}
	if got := bobCrt.Equipment["wield"]; got != "item:2" {
		t.Fatalf("equipment after family-war death = %s, want item:2 preserved", got)
	}
	item2, _ := w.Object("item:2")
	if item2.Location.CreatureID != creature1ID || item2.Location.Slot != "wield" {
		t.Fatalf("item:2 location = %+v, want wield slot preserved", item2.Location)
	}
	room1, _ := w.Room(room1ID)
	if len(room1.Objects.ObjectIDs) != 0 {
		t.Fatalf("family-war death room objects = %+v, want no drop", room1.Objects.ObjectIDs)
	}
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
