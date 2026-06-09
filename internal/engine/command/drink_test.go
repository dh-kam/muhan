package command

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"muhan/internal/commandspec"
	"muhan/internal/persist/cbin"
	"muhan/internal/persist/legacykr"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestDrinkHandlerConsumesPotionChargeAndClearsHidden(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "2", "1")
	player := loaded.Players["player:alice"]
	player.Metadata.Tags = []string{"hidden", "PHIDDN"}
	loaded.Players[player.ID] = player
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = []string{"hidden", "PHIDDN"}
	creature.Stats["PHIDDN"] = 1
	loaded.Creatures[creature.ID] = creature
	runtime := state.NewWorld(loaded)

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	got := ctx.OutputString()
	for _, part := range []string{"당신의 체력이 향상되었습니다.", "몸이 따뜻해진다.", "당신은 치료약을 먹었습니다."} {
		if !strings.Contains(got, part) {
			t.Errorf("output %q does not contain %q", got, part)
		}
	}
	potion, ok := runtime.Object("object:potion")
	if !ok {
		t.Fatal("potion deleted, want retained at one charge")
	}
	if potion.Properties["shotsCurrent"] != "1" {
		t.Fatalf("shotsCurrent = %q, want 1", potion.Properties["shotsCurrent"])
	}
	updatedCreature, _ := runtime.Creature("creature:alice")
	if hasAnyNormalizedFlag(updatedCreature.Metadata.Tags, "hidden", "phiddn") {
		t.Fatalf("creature tags = %+v, want hidden cleared", updatedCreature.Metadata.Tags)
	}
	if updatedCreature.Stats["PHIDDN"] != 0 {
		t.Fatalf("creature PHIDDN = %d, want 0", updatedCreature.Stats["PHIDDN"])
	}
	updatedPlayer, _ := runtime.Player("player:alice")
	if hasAnyNormalizedFlag(updatedPlayer.Metadata.Tags, "hidden", "phiddn") {
		t.Fatalf("player tags = %+v, want hidden cleared", updatedPlayer.Metadata.Tags)
	}
}

func TestDrinkHandlerBroadcastsPotionConsumption(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "2", "1")
	runtime := state.NewWorld(loaded)

	var roomBroadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("player:alice", "session:alice", &roomBroadcasts)
	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	if !strings.Contains(ctx.OutputString(), "당신은 치료약을 먹었습니다.") {
		t.Fatalf("output = %q, want potion consumption text", ctx.OutputString())
	}
	if len(roomBroadcasts) != 1 {
		t.Fatalf("len(roomBroadcasts) = %d, want 1", len(roomBroadcasts))
	}
	want := roomBroadcastRecord{
		RoomID:  "room:tavern",
		Exclude: "session:alice",
		Text:    "\nAlice가 치료약을 먹었습니다.",
	}
	if roomBroadcasts[0] != want {
		t.Fatalf("roomBroadcast = %+v, want %+v", roomBroadcasts[0], want)
	}
}

func TestDrinkHandlerDeletesPotionWhenLastChargeIsConsumed(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "1", "1")
	runtime := state.NewWorld(loaded)

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료약"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || !strings.Contains(ctx.OutputString(), "당신은 치료약을 먹었습니다.") {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if _, ok := runtime.Object("object:potion"); ok {
		t.Fatal("potion still exists after last charge")
	}
	creature, _ := runtime.Creature("creature:alice")
	for _, id := range creature.Inventory.ObjectIDs {
		if id == "object:potion" {
			t.Fatalf("inventory still contains deleted potion: %+v", creature.Inventory.ObjectIDs)
		}
	}
}

func TestDrinkHandlerRejectsMissingNonPotionAndEmptyPotion(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		shots      string
		magicPower string
		want       string
	}{
		{name: "missing target", want: "\n무엇을 먹습니까?\n"},
		{name: "missing object", args: []string{"없는"}, shots: "1", magicPower: "1", want: "\n그런것은 존재하지 않습니다.\n"},
		{name: "non potion", args: []string{"돌"}, shots: "1", magicPower: "1", want: "\n이것은 먹는 물건이 아닙니다.\n"},
		{name: "empty shots", args: []string{"치료"}, shots: "0", magicPower: "1", want: "\n아무것도 들어있지 않습니다.\n\n"},
		{name: "empty magic", args: []string{"치료"}, shots: "1", magicPower: "0", want: "\n아무것도 들어있지 않습니다.\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := drinkWorld(t, "room:tavern", tt.shots, tt.magicPower)
			runtime := state.NewWorld(loaded)
			ctx := &Context{ActorID: "player:alice"}
			status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: tt.args})
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if status != StatusDefault || ctx.OutputString() != tt.want {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.want)
			}
		})
	}
}

func TestDrinkHandlerRoomRestrictionsDoNotConsumePotion(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{name: "no potion", tags: []string{"noPotion"}, want: "\n그것을 먹기전에 인조가 나타나 훔쳐가 버렸습니다.\n 잘먹겠다... 낄낄낄... \n"},
		{name: "survival", tags: []string{"survival"}, want: "\n대련장에서는 아무 것도 먹을 수 없습니다.\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := drinkWorld(t, "room:tavern", "2", "1")
			room := loaded.Rooms["room:tavern"]
			room.Metadata.Tags = tt.tags
			loaded.Rooms[room.ID] = room
			runtime := state.NewWorld(loaded)

			ctx := &Context{ActorID: "player:alice"}
			status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if status != StatusDefault || ctx.OutputString() != tt.want {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.want)
			}
			potion, _ := runtime.Object("object:potion")
			if potion.Properties["shotsCurrent"] != "2" {
				t.Fatalf("shotsCurrent = %q, want unchanged 2", potion.Properties["shotsCurrent"])
			}
		})
	}
}

func TestDrinkHandlerDoesNotConsumeWhenEffectFails(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "2", "1")
	runtime := state.NewWorld(loaded)
	effect := func(*Context, DrinkWorld, model.Creature, model.ObjectInstance, ResolvedCommand) (bool, error) {
		return false, nil
	}

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, effect)(ctx, ResolvedCommand{Args: []string{"치료"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "" {
		t.Fatalf("status/output = %d/%q, want no success output", status, ctx.OutputString())
	}
	potion, _ := runtime.Object("object:potion")
	if potion.Properties["shotsCurrent"] != "2" {
		t.Fatalf("shotsCurrent = %q, want unchanged 2", potion.Properties["shotsCurrent"])
	}
}

func TestDrinkHandlerUsesOnlyFirstArgumentForPotionLookupLikeLegacy(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "2", "1")
	runtime := state.NewWorld(loaded)

	called := false
	var gotObject model.ObjectInstanceID
	var gotArgs []string
	effect := func(_ *Context, _ DrinkWorld, _ model.Creature, object model.ObjectInstance, resolved ResolvedCommand) (bool, error) {
		called = true
		gotObject = object.ID
		gotArgs = append([]string(nil), resolved.Args...)
		return false, nil
	}

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, effect)(ctx, ResolvedCommand{Args: []string{"치료", "Bob"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "" {
		t.Fatalf("status/output = %d/%q, want no failure output", status, ctx.OutputString())
	}
	if !called {
		t.Fatal("effect was not called; drink lookup likely used the full argument string")
	}
	if gotObject != "object:potion" {
		t.Fatalf("effect object = %q, want object:potion", gotObject)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "치료" || gotArgs[1] != "Bob" {
		t.Fatalf("effect args = %+v, want [치료 Bob]", gotArgs)
	}
}

func TestDrinkHandlerPassesTargetArgumentToPotionEffectLikeLegacy(t *testing.T) {
	useSpellFailRoll(t, 0)
	loaded := drinkWorld(t, "room:tavern", "2", "50")
	runtime := state.NewWorld(loaded)

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료", "Bob"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	if got, want := ctx.OutputString(), "이 물건은 자신에게만 사용할수 있습니다."; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	potion, ok := runtime.Object("object:potion")
	if !ok {
		t.Fatal("potion deleted despite rejected target")
	}
	if got := potion.Properties["shotsCurrent"]; got != "2" {
		t.Fatalf("shotsCurrent = %q, want unchanged 2", got)
	}
}

func TestDrinkHandlerAppliesMagicItemRestrictions(t *testing.T) {
	tests := []struct {
		name          string
		creatureStats map[string]int
		objectTags    []string
		protoProps    map[string]string
		want          string
		wantDropped   bool
	}{
		{
			name:          "good only rejects evil actor and drops potion",
			creatureStats: map[string]int{"alignment": -101, "class": legacyClassFighter},
			objectTags:    []string{"goodOnly"},
			want:          "치료약이 당신의 손안에서 타버립니다.\n",
			wantDropped:   true,
		},
		{
			name:          "class selective rejects unlisted class",
			creatureStats: map[string]int{"class": legacyClassFighter},
			protoProps:    map[string]string{"classSelective": "1", "classMage": "1"},
			want:          "\n당신직업상 그 물건을 금하고 있기 때문에 먹을 수 없습니다.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := drinkWorld(t, "room:tavern", "2", "1")
			creature := loaded.Creatures["creature:alice"]
			creature.Stats = tt.creatureStats
			loaded.Creatures[creature.ID] = creature
			proto := loaded.ObjectPrototypes["prototype:potion"]
			proto.Properties = tt.protoProps
			loaded.ObjectPrototypes[proto.ID] = proto
			potion := loaded.Objects["object:potion"]
			potion.Metadata.Tags = tt.objectTags
			loaded.Objects[potion.ID] = potion
			runtime := state.NewWorld(loaded)

			called := false
			ctx := &Context{ActorID: "player:alice"}
			status, err := NewDrinkHandler(runtime, func(*Context, DrinkWorld, model.Creature, model.ObjectInstance, ResolvedCommand) (bool, error) {
				called = true
				return true, nil
			})(ctx, ResolvedCommand{Args: []string{"치료"}})
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if status != StatusDefault || ctx.OutputString() != tt.want {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.want)
			}
			if called {
				t.Fatal("effect was called despite restriction")
			}
			potion, ok := runtime.Object("object:potion")
			if !ok {
				t.Fatal("potion was deleted despite restriction")
			}
			if potion.Properties["shotsCurrent"] != "2" {
				t.Fatalf("shotsCurrent = %q, want unchanged 2", potion.Properties["shotsCurrent"])
			}
			if tt.wantDropped {
				if potion.Location.RoomID != "room:tavern" {
					t.Fatalf("potion location = %+v, want room:tavern", potion.Location)
				}
				creature, _ := runtime.Creature("creature:alice")
				for _, id := range creature.Inventory.ObjectIDs {
					if id == "object:potion" {
						t.Fatalf("inventory still contains dropped potion: %+v", creature.Inventory.ObjectIDs)
					}
				}
			} else if potion.Location.CreatureID != "creature:alice" {
				t.Fatalf("potion location = %+v, want creature inventory", potion.Location)
			}
		})
	}
}

func TestDrinkHandlerSpecialPotionAppliesSafePDiceEffectsAndDestroysPotion(t *testing.T) {
	tests := []struct {
		name              string
		pDice             string
		stats             map[string]int
		creatureTags      []string
		playerTags        []string
		objectProperties  map[string]string
		addRoom           model.RoomID
		wantStats         map[string]int
		wantCreatureTags  []string
		rejectCreatureTag []string
		wantPlayerTags    []string
		rejectPlayerTags  []string
		wantRoomID        model.RoomID
	}{
		{
			name:             "case 1 skips legacy play time mutation",
			pDice:            "1",
			stats:            map[string]int{"strength": 10, "intelligence": 10, "piety": 10, "constitution": 10, "dexterity": 10},
			wantStats:        map[string]int{"strength": 10, "intelligence": 10, "piety": 10, "constitution": 10, "dexterity": 10},
			wantRoomID:       "room:tavern",
			rejectPlayerTags: []string{"chaos", "pchaos", "poison", "ppoisn"},
		},
		{
			name:             "case 2 toggles chaos on",
			pDice:            "2",
			wantStats:        map[string]int{"PCHAOS": 1},
			wantCreatureTags: []string{"chaos", "pchaos"},
			wantPlayerTags:   []string{"chaos", "pchaos"},
			wantRoomID:       "room:tavern",
		},
		{
			name:              "case 2 toggles chaos off",
			pDice:             "2",
			creatureTags:      []string{"PCHAOS"},
			playerTags:        []string{"PCHAOS"},
			wantStats:         map[string]int{"PCHAOS": 0},
			rejectCreatureTag: []string{"chaos", "pchaos"},
			rejectPlayerTags:  []string{"chaos", "pchaos"},
			wantRoomID:        "room:tavern",
		},
		{
			name:              "case 2 clears stat-backed legacy chaos",
			pDice:             "2",
			stats:             map[string]int{"PCHAOS": 1},
			wantStats:         map[string]int{"PCHAOS": 0},
			rejectCreatureTag: []string{"chaos", "pchaos"},
			rejectPlayerTags:  []string{"chaos", "pchaos"},
			wantRoomID:        "room:tavern",
		},
		{
			name:       "case 3 skips legacy play time mutation",
			pDice:      "3",
			wantRoomID: "room:tavern",
		},
		{
			name:  "case 4 increases base stats within legacy cap",
			pDice: "4",
			stats: map[string]int{
				"class": legacyClassFighter, "level": 8,
				"strength": 10, "intelligence": 10, "piety": 10, "constitution": 10, "dexterity": 10,
			},
			wantStats:  map[string]int{"strength": 11, "intelligence": 11, "piety": 11, "constitution": 11, "dexterity": 11},
			wantRoomID: "room:tavern",
		},
		{
			name:  "case 4 leaves high stats unchanged",
			pDice: "4",
			stats: map[string]int{
				"class": legacyClassFighter, "level": 1,
				"strength": 20, "intelligence": 20, "piety": 20, "constitution": 20, "dexterity": 20,
			},
			wantStats:  map[string]int{"strength": 20, "intelligence": 20, "piety": 20, "constitution": 20, "dexterity": 20},
			wantRoomID: "room:tavern",
		},
		{
			name:             "case 5 moves to explicit room id",
			pDice:            "5",
			objectProperties: map[string]string{"roomID": "room:special"},
			addRoom:          "room:special",
			wantRoomID:       "room:special",
		},
		{
			name:             "case 5 moves to numeric value room id",
			pDice:            "5",
			objectProperties: map[string]string{"value": "42"},
			addRoom:          "room:00042",
			wantRoomID:       "room:00042",
		},
		{
			name:             "case 6 poisons actor",
			pDice:            "6",
			wantStats:        map[string]int{"PPOISN": 1},
			wantCreatureTags: []string{"poison", "ppoisn"},
			wantPlayerTags:   []string{"poison", "ppoisn"},
			wantRoomID:       "room:tavern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := drinkWorld(t, "room:tavern", "0", "0")
			if !tt.addRoom.IsZero() {
				mustAddLookRoom(t, loaded, model.Room{ID: tt.addRoom, DisplayName: "특별한 방"})
			}
			player := loaded.Players["player:alice"]
			player.Metadata.Tags = tt.playerTags
			loaded.Players[player.ID] = player
			creature := loaded.Creatures["creature:alice"]
			creature.Metadata.Tags = tt.creatureTags
			creature.Stats = tt.stats
			loaded.Creatures[creature.ID] = creature
			potion := loaded.Objects["object:potion"]
			potion.Metadata.Tags = []string{"specialItem"}
			potion.Properties["pDice"] = tt.pDice
			for key, value := range tt.objectProperties {
				potion.Properties[key] = value
			}
			loaded.Objects[potion.ID] = potion
			tempDir := t.TempDir()
			runtime := state.NewWorld(loaded)
			runtime.SetDBRoot(tempDir)

			ctx := &Context{ActorID: "player:alice"}
			status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			wantOutput := "몸이 따뜻해진다.\n당신은 치료약을 먹었습니다.\n"
			if status != StatusDefault || ctx.OutputString() != wantOutput {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), wantOutput)
			}
			if _, ok := runtime.Object("object:potion"); ok {
				t.Fatal("special potion still exists after use")
			}
			updatedCreature, _ := runtime.Creature("creature:alice")
			for key, want := range tt.wantStats {
				if got := updatedCreature.Stats[key]; got != want {
					t.Fatalf("stat %s = %d, want %d", key, got, want)
				}
			}
			for _, tag := range tt.wantCreatureTags {
				if !hasAnyNormalizedFlag(updatedCreature.Metadata.Tags, tag) {
					t.Fatalf("creature tags = %+v, want %q", updatedCreature.Metadata.Tags, tag)
				}
			}
			for _, tag := range tt.rejectCreatureTag {
				if hasAnyNormalizedFlag(updatedCreature.Metadata.Tags, tag) {
					t.Fatalf("creature tags = %+v, did not want %q", updatedCreature.Metadata.Tags, tag)
				}
			}
			updatedPlayer, _ := runtime.Player("player:alice")
			for _, tag := range tt.wantPlayerTags {
				if !hasAnyNormalizedFlag(updatedPlayer.Metadata.Tags, tag) {
					t.Fatalf("player tags = %+v, want %q", updatedPlayer.Metadata.Tags, tag)
				}
			}
			for _, tag := range tt.rejectPlayerTags {
				if hasAnyNormalizedFlag(updatedPlayer.Metadata.Tags, tag) {
					t.Fatalf("player tags = %+v, did not want %q", updatedPlayer.Metadata.Tags, tag)
				}
			}
			if updatedPlayer.RoomID != tt.wantRoomID {
				t.Fatalf("player room = %q, want %q", updatedPlayer.RoomID, tt.wantRoomID)
			}
			if updatedCreature.RoomID != tt.wantRoomID {
				t.Fatalf("creature room = %q, want %q", updatedCreature.RoomID, tt.wantRoomID)
			}
		})
	}
}

func TestDrinkHandlerSpecialPotionClearsPropertyBackedChaosLikeCFlag(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "0", "0")
	creature := loaded.Creatures["creature:alice"]
	creature.Properties = map[string]string{"PCHAOS": "true"}
	loaded.Creatures[creature.ID] = creature
	potion := loaded.Objects["object:potion"]
	potion.Metadata.Tags = []string{"specialItem"}
	potion.Properties["pDice"] = "2"
	loaded.Objects[potion.ID] = potion
	runtime := state.NewWorld(loaded)
	runtime.SetDBRoot(t.TempDir())

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %v, want StatusDefault", status)
	}
	updated, _ := runtime.Creature("creature:alice")
	if updated.Stats["PCHAOS"] != 0 {
		t.Fatalf("PCHAOS stat = %d, want 0", updated.Stats["PCHAOS"])
	}
	if _, ok := updated.Properties["PCHAOS"]; ok {
		t.Fatalf("PCHAOS property still present after C-style F_CLR: %+v", updated.Properties)
	}
	if hasAnyNormalizedFlag(updated.Metadata.Tags, "chaos", "pchaos", "PCHAOS") {
		t.Fatalf("tags = %+v, want chaos cleared", updated.Metadata.Tags)
	}
}

func TestDrinkHandlerDispatchesLegacyAliases(t *testing.T) {
	runtime := state.NewWorld(drinkWorld(t, "room:tavern", "2", "1"))
	registry := mustRegistry(t, []commandspec.CommandSpec{
		{Name: "마셔", Number: 58, Handler: "drink"},
		{Name: "먹어", Number: 58, Handler: "drink"},
	})
	dispatcher := Dispatcher{Registry: registry, Handlers: map[string]Handler{"drink": NewDrinkHandler(runtime, nil)}}

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "치료약 마셔")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDefault || !strings.Contains(ctx.OutputString(), "치료약을 먹었습니다.") {
		t.Fatalf("first status/output = %d/%q", status, ctx.OutputString())
	}

	ctx = &Context{ActorID: "player:alice"}
	status, err = dispatcher.DispatchLine(ctx, "치료약 먹어")
	if err != nil {
		t.Fatalf("DispatchLine() second error = %v", err)
	}
	if status != StatusDefault || !strings.Contains(ctx.OutputString(), "치료약을 먹었습니다.") {
		t.Fatalf("second status/output = %d/%q", status, ctx.OutputString())
	}
}

func drinkWorld(t *testing.T, roomID model.RoomID, shots string, magicPower string) *worldload.World {
	t.Helper()

	loaded := emptyInventoryWorld(t)
	mustAddLookRoom(t, loaded, model.Room{ID: roomID, DisplayName: "주점"})
	player := loaded.Players["player:alice"]
	player.RoomID = roomID
	loaded.Players[player.ID] = player
	creature := loaded.Creatures["creature:alice"]
	creature.RoomID = roomID
	creature.Inventory = model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:potion", "object:stone"}}
	creature.Stats = map[string]int{
		"class":     legacyClassCleric,
		"hpCurrent": 50,
		"hpMax":     100,
		"mpCurrent": 100,
		"mpMax":     100,
	}
	loaded.Creatures[creature.ID] = creature
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:potion",
		Kind:        model.ObjectKindPotion,
		DisplayName: "치료약",
	})
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:stone",
		Kind:        model.ObjectKindMisc,
		DisplayName: "돌",
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:potion",
		PrototypeID: "prototype:potion",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
		Properties: map[string]string{
			"type":         "6",
			"shotsCurrent": shots,
			"magicPower":   magicPower,
			"useOutput":    "몸이 따뜻해진다.",
		},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:stone",
		PrototypeID: "prototype:stone",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
	})
	return loaded
}

func TestDrinkHandlerSpecialPotionPlayTimeMutations(t *testing.T) {
	// Case 1: Subtracts 1 day (86400 seconds) from playtime
	t.Run("case 1 subtracts 1 day", func(t *testing.T) {
		tempDir := t.TempDir()
		loaded := drinkWorld(t, "room:tavern", "0", "0")
		player := loaded.Players["player:alice"]
		loaded.Players[player.ID] = player
		creature := loaded.Creatures["creature:alice"]
		creature.Stats = map[string]int{
			"legacyHoursInterval": 200000,
			"legacyAgeYears":      20,
		}
		loaded.Creatures[creature.ID] = creature
		potion := loaded.Objects["object:potion"]
		potion.Metadata.Tags = []string{"specialItem"}
		potion.Properties["pDice"] = "1"
		loaded.Objects[potion.ID] = potion

		runtime := state.NewWorld(loaded)
		runtime.SetDBRoot(tempDir)
		ctx := &Context{ActorID: "player:alice"}
		status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
		if err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if status != StatusDefault {
			t.Fatalf("status = %v, want StatusDefault", status)
		}

		updatedCreature, _ := runtime.Creature("creature:alice")
		gotInterval := updatedCreature.Stats["legacyHoursInterval"]
		wantInterval := 200000 - 86400
		if gotInterval != wantInterval {
			t.Errorf("legacyHoursInterval = %d, want %d", gotInterval, wantInterval)
		}
		gotAge := updatedCreature.Stats["legacyAgeYears"]
		wantAge := 18 + wantInterval/86400
		if gotAge != wantAge {
			t.Errorf("legacyAgeYears = %d, want %d", gotAge, wantAge)
		}

		// Verify save file content
		verifyPlayerSaveFile(t, tempDir, "alice", wantInterval, wantAge)
	})

	t.Run("case 1 caps at 0", func(t *testing.T) {
		tempDir := t.TempDir()
		loaded := drinkWorld(t, "room:tavern", "0", "0")
		player := loaded.Players["player:alice"]
		loaded.Players[player.ID] = player
		creature := loaded.Creatures["creature:alice"]
		creature.Stats = map[string]int{
			"legacyHoursInterval": 50000,
			"legacyAgeYears":      18,
		}
		loaded.Creatures[creature.ID] = creature
		potion := loaded.Objects["object:potion"]
		potion.Metadata.Tags = []string{"specialItem"}
		potion.Properties["pDice"] = "1"
		loaded.Objects[potion.ID] = potion

		runtime := state.NewWorld(loaded)
		runtime.SetDBRoot(tempDir)
		ctx := &Context{ActorID: "player:alice"}
		status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
		if err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if status != StatusDefault {
			t.Fatalf("status = %v, want StatusDefault", status)
		}

		updatedCreature, _ := runtime.Creature("creature:alice")
		gotInterval := updatedCreature.Stats["legacyHoursInterval"]
		if gotInterval != 0 {
			t.Errorf("legacyHoursInterval = %d, want 0", gotInterval)
		}
		gotAge := updatedCreature.Stats["legacyAgeYears"]
		if gotAge != 18 {
			t.Errorf("legacyAgeYears = %d, want 18", gotAge)
		}

		// Verify save file content
		verifyPlayerSaveFile(t, tempDir, "alice", 0, 18)
	})

	// Case 3: Adds 1 day (86400 seconds) to playtime
	t.Run("case 3 adds 1 day", func(t *testing.T) {
		tempDir := t.TempDir()
		loaded := drinkWorld(t, "room:tavern", "0", "0")
		player := loaded.Players["player:alice"]
		loaded.Players[player.ID] = player
		creature := loaded.Creatures["creature:alice"]
		creature.Stats = map[string]int{
			"legacyHoursInterval": 100000,
			"legacyAgeYears":      19,
		}
		loaded.Creatures[creature.ID] = creature
		potion := loaded.Objects["object:potion"]
		potion.Metadata.Tags = []string{"specialItem"}
		potion.Properties["pDice"] = "3"
		loaded.Objects[potion.ID] = potion

		runtime := state.NewWorld(loaded)
		runtime.SetDBRoot(tempDir)
		ctx := &Context{ActorID: "player:alice"}
		status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
		if err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if status != StatusDefault {
			t.Fatalf("status = %v, want StatusDefault", status)
		}

		updatedCreature, _ := runtime.Creature("creature:alice")
		gotInterval := updatedCreature.Stats["legacyHoursInterval"]
		wantInterval := 100000 + 86400
		if gotInterval != wantInterval {
			t.Errorf("legacyHoursInterval = %d, want %d", gotInterval, wantInterval)
		}
		gotAge := updatedCreature.Stats["legacyAgeYears"]
		wantAge := 18 + wantInterval/86400
		if gotAge != wantAge {
			t.Errorf("legacyAgeYears = %d, want %d", gotAge, wantAge)
		}

		// Verify save file content
		verifyPlayerSaveFile(t, tempDir, "alice", wantInterval, wantAge)
	})
}

func verifyPlayerSaveFile(t *testing.T, dbRoot string, playerName string, wantInterval int, wantAge int) {
	t.Helper()
	path := filepath.Join(dbRoot, "player", "json", playerName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read player save file: %v", err)
	}

	var saved state.PlayerSaveData
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("failed to unmarshal player save data: %v", err)
	}

	if saved.Creature == nil {
		t.Fatal("saved creature is nil")
	}

	gotInterval := saved.Creature.Stats["legacyHoursInterval"]
	if gotInterval != wantInterval {
		t.Errorf("saved legacyHoursInterval = %d, want %d", gotInterval, wantInterval)
	}

	gotAge := saved.Creature.Stats["legacyAgeYears"]
	if gotAge != wantAge {
		t.Errorf("saved legacyAgeYears = %d, want %d", gotAge, wantAge)
	}
}

func TestDrinkHandlerSpecialPotionTeleportBroadcast(t *testing.T) {
	loaded := drinkWorld(t, "room:tavern", "0", "0")
	// Target room
	mustAddLookRoom(t, loaded, model.Room{ID: "room:special", DisplayName: "특별한 방"})

	player := loaded.Players["player:alice"]
	loaded.Players[player.ID] = player

	creature := loaded.Creatures["creature:alice"]
	loaded.Creatures[creature.ID] = creature

	potion := loaded.Objects["object:potion"]
	potion.Metadata.Tags = []string{"specialItem"}
	potion.Properties["pDice"] = "5"
	potion.Properties["roomID"] = "room:special"
	loaded.Objects[potion.ID] = potion

	runtime := state.NewWorld(loaded)

	// Capture global broadcasts
	var globalBroadcasts []string
	runtime.BroadcastAllFunc = func(msg string) error {
		globalBroadcasts = append(globalBroadcasts, msg)
		return nil
	}

	// Capture room broadcasts
	var roomBroadcasts []roomBroadcastRecord
	ctx := &Context{
		ActorID:   "player:alice",
		SessionID: "session:alice",
		Values: map[string]interface{}{
			ContextRoomBroadcastKey: RoomBroadcastFunc(func(roomID model.RoomID, excludeSessionID string, text string) error {
				roomBroadcasts = append(roomBroadcasts, roomBroadcastRecord{
					RoomID:  string(roomID),
					Exclude: excludeSessionID,
					Text:    text,
				})
				return nil
			}),
		},
	}

	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %v, want StatusDefault", status)
	}

	// Verify player moved
	updatedPlayer, _ := runtime.Player("player:alice")
	if updatedPlayer.RoomID != "room:special" {
		t.Errorf("player room = %q, want room:special", updatedPlayer.RoomID)
	}

	// Verify room broadcasts (C add_ply_rom arrival, then C broadcast_rom consumption message).
	if len(roomBroadcasts) != 2 {
		t.Fatalf("len(roomBroadcasts) = %d, want 2", len(roomBroadcasts))
	}

	// Arrival broadcast
	if roomBroadcasts[0].RoomID != "room:special" {
		t.Errorf("first roomBroadcast roomID = %q, want room:special", roomBroadcasts[0].RoomID)
	}
	wantArriveMsg := "\nAlice가 도착하였습니다."
	if roomBroadcasts[0].Text != wantArriveMsg {
		t.Errorf("first roomBroadcast text = %q, want %q", roomBroadcasts[0].Text, wantArriveMsg)
	}

	// Consumption broadcast
	wantConsume := roomBroadcastRecord{
		RoomID:  "room:special",
		Exclude: "session:alice",
		Text:    "\nAlice가 치료약을 먹었습니다.",
	}
	if roomBroadcasts[1] != wantConsume {
		t.Errorf("second roomBroadcast = %+v, want %+v", roomBroadcasts[1], wantConsume)
	}

	// C magic1.c pdice 5 does not broadcast a global spatial-move line.
	if len(globalBroadcasts) != 0 {
		t.Fatalf("len(globalBroadcasts) = %d, want 0", len(globalBroadcasts))
	}

}

func TestDrinkHandlerSpecialPotionDynamicRoomLoad(t *testing.T) {
	tempDir := t.TempDir()
	roomsDir := filepath.Join(tempDir, "rooms", "r12")
	if err := os.MkdirAll(roomsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create room:12345 binary file
	roomData := makeMinimalRoom(12345, "동굴")
	roomFilePath := filepath.Join(roomsDir, "r12345")
	if err := os.WriteFile(roomFilePath, roomData, 0644); err != nil {
		t.Fatal(err)
	}

	loaded := drinkWorld(t, "room:tavern", "0", "0")
	// Make sure the target room is NOT in loaded rooms
	delete(loaded.Rooms, "room:12345")

	potion := loaded.Objects["object:potion"]
	potion.Metadata.Tags = []string{"specialItem"}
	potion.Properties["pDice"] = "5"
	potion.Properties["targetRoomID"] = "room:12345"
	loaded.Objects[potion.ID] = potion

	runtime := state.NewWorld(loaded)
	runtime.SetDBRoot(tempDir) // Set root to our temp directory

	ctx := &Context{ActorID: "player:alice"}
	status, err := NewDrinkHandler(runtime, nil)(ctx, ResolvedCommand{Args: []string{"치료"}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %v, want StatusDefault", status)
	}

	// Verify player moved to room:12345
	player, _ := runtime.Player("player:alice")
	if player.RoomID != "room:12345" {
		t.Errorf("player.RoomID = %q, want room:12345", player.RoomID)
	}

	// Verify room is registered in runtime
	room, ok := runtime.Room("room:12345")
	if !ok {
		t.Fatal("room:12345 not registered in runtime world")
	}
	if room.DisplayName != "동굴" {
		t.Errorf("room.DisplayName = %q, want 동굴. Metadata: %+v. RawFields: %+v", room.DisplayName, room.Metadata, room.Metadata.RawFields)
	}
}

func makeMinimalRoom(number int, name string) []byte {
	data := makeRoomRecord(number, name)
	data = appendInt32(data, 0)
	data = appendInt32(data, 0)
	data = appendInt32(data, 0)
	data = appendDescription(data, nil)
	data = appendDescription(data, nil)
	data = appendDescription(data, nil)
	return data
}

func makeRoomRecord(number int, name string) []byte {
	data := make([]byte, cbin.RoomSize)
	binary.LittleEndian.PutUint16(data, uint16(int16(number)))
	encodedName, err := legacykr.EncodeEUCKR(name)
	if err == nil {
		copy(data[2:], encodedName)
	}
	return data
}

func appendInt32(data []byte, n int) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(int32(n)))
	return append(data, buf[:]...)
}

func appendDescription(data []byte, desc []byte) []byte {
	data = appendInt32(data, len(desc))
	return append(data, desc...)
}
