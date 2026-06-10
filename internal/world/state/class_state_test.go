package state_test

import (
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestSetCreatureClassRecalculatesCombatStats(t *testing.T) {
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
		Equipment:   map[string]model.ObjectInstanceID{"body": "object:armor"},
		Stats: map[string]int{
			"class":        4,
			"level":        20,
			"constitution": 20,
			"dexterity":    20,
			"armor":        100,
			"thaco":        20,
		},
	})
	mustAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:armor",
		PrototypeID: "prototype:armor",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "body"},
		Properties:  map[string]string{"armor": "12"},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	updated, err := runtime.SetCreatureClass("creature:alice", 13)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Stats["class"]; got != 13 {
		t.Fatalf("class = %d, want 13", got)
	}
	if got := updated.Stats["armor"]; got != 29 {
		t.Fatalf("armor = %d, want 29", got)
	}
	if got := updated.Stats["thaco"]; got != -59 {
		t.Fatalf("thaco = %d, want -59", got)
	}
}

func TestRecalculateCreatureCombatStatsUsesEquipmentAndFlags(t *testing.T) {
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
		Equipment: map[string]model.ObjectInstanceID{
			"body":  "object:armor",
			"wield": "object:sword",
		},
		Metadata: model.Metadata{Tags: []string{"PBLESS", "PPROTE"}},
		Stats: map[string]int{
			"class":         4,
			"level":         20,
			"constitution":  20,
			"dexterity":     20,
			"proficiency/1": 1024,
		},
	})
	mustAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:armor",
		PrototypeID: "prototype:armor",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "body"},
		Properties:  map[string]string{"armor": "12"},
	})
	mustAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:sword",
		PrototypeID: "prototype:sword",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "wield"},
		Properties: map[string]string{
			"type":       "1",
			"adjustment": "3",
		},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	updated, err := runtime.RecalculateCreatureCombatStats("creature:alice")
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Stats["armor"]; got != 29 {
		t.Fatalf("armor = %d, want 29", got)
	}
	if got := updated.Stats["thaco"]; got != 9 {
		t.Fatalf("thaco = %d, want 9", got)
	}
}

func TestRecalculateCreatureTHACOUsesCSubDMProficiencyTotals(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddPlayer(t, loaded, model.Player{
		ID:          "player:subdm",
		DisplayName: "SubDM",
		CreatureID:  "creature:subdm",
	})
	mustAddCreature(t, loaded, model.Creature{
		ID:          "creature:subdm",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "SubDM",
		PlayerID:    "player:subdm",
		Stats: map[string]int{
			"class":              12,
			"level":              20,
			"proficiencySharp":   1024,
			"proficiencyThrust":  1024,
			"proficiencyBlunt":   1024,
			"proficiencyMissile": 1024,
			"realmEarth":         2048,
			"realmWind":          2048,
			"realmFire":          2048,
		},
		Properties: map[string]string{
			"proficiency/pole": "1024",
		},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	updated, err := runtime.RecalculateCreatureTHACO("creature:subdm")
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Stats["thaco"]; got != -2 {
		t.Fatalf("thaco = %d, want -2", got)
	}
}

func TestUpdateFamilyMemberAfterClassChangeUpdatesMemberClass(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddFamily(t, loaded, model.Family{
		ID:          20,
		Slot:        7,
		DisplayName: "무영문",
		Members: []model.FamilyMember{
			{
				DisplayName: "Alice",
				Class:       4,
				Metadata:    model.Metadata{RawFields: map[string][]byte{"line": []byte("4 Alice")}},
			},
			{
				DisplayName: "Bob",
				Class:       5,
				Metadata:    model.Metadata{RawFields: map[string][]byte{"line": []byte("5 Bob")}},
			},
		},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	if err := runtime.UpdateFamilyMemberAfterClassChange("Alice", 8, 7); err != nil {
		t.Fatal(err)
	}

	family, ok := runtime.Family(20)
	if !ok {
		t.Fatal("family 20 not found")
	}
	if got := family.Members[0].Class; got != 8 {
		t.Fatalf("Alice class = %d, want 8", got)
	}
	if got := string(family.Members[0].Metadata.RawFields["line"]); got != "8 Alice" {
		t.Fatalf("Alice raw line = %q, want %q", got, "8 Alice")
	}
	if got := family.Members[1].Class; got != 5 {
		t.Fatalf("Bob class = %d, want unchanged 5", got)
	}
}

func TestUpdateFamilyMemberAfterClassChangeMissingRowsAreNoop(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddFamily(t, loaded, model.Family{
		ID:          2,
		Slot:        2,
		DisplayName: "무영문",
		Members: []model.FamilyMember{{
			DisplayName: "Alice",
			Class:       4,
			Metadata:    model.Metadata{RawFields: map[string][]byte{"line": []byte("4 Alice")}},
		}},
	})

	runtime := state.NewWorld(loaded)
	defer runtime.Close()
	for _, tc := range []struct {
		name          string
		dailyExpndMax int
	}{
		{name: "Carol", dailyExpndMax: 2},
		{name: "Alice", dailyExpndMax: 99},
		{name: "", dailyExpndMax: 2},
		{name: "Alice", dailyExpndMax: 0},
	} {
		if err := runtime.UpdateFamilyMemberAfterClassChange(tc.name, 8, tc.dailyExpndMax); err != nil {
			t.Fatalf("UpdateFamilyMemberAfterClassChange(%q, 8, %d) error = %v", tc.name, tc.dailyExpndMax, err)
		}
	}

	family, ok := runtime.Family(2)
	if !ok {
		t.Fatal("family 2 not found")
	}
	if got := family.Members[0].Class; got != 4 {
		t.Fatalf("Alice class after no-op calls = %d, want 4", got)
	}
	if got := string(family.Members[0].Metadata.RawFields["line"]); got != "4 Alice" {
		t.Fatalf("Alice raw line after no-op calls = %q, want %q", got, "4 Alice")
	}
}
