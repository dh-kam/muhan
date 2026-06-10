package game

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/session"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestFamilyBankInventoryHandlerListsRoomSpecialContainer(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	out := ctx.OutputString()
	for _, want := range []string{
		"패거리 창고의 보관품입니다.\n",
		"보관품의 목록 : 청동검, 치료약.\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for _, reject := range []string{"숨은 물건", "위치가 어긋난 물건"} {
		if strings.Contains(out, reject) {
			t.Fatalf("output included %q:\n%s", reject, out)
		}
	}
}

func TestFamilyBankInventoryHandlerCreatesMissingListContainer(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	delete(loaded.Banks, "bank:family:무영문_3")
	delete(loaded.Objects, "object:family-storage-root")
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "기증된 물건이 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:family:무영문_3")
	if !ok {
		t.Fatal("family bank listing did not create missing bank account")
	}
	if len(account.Objects.ObjectIDs) != 1 {
		t.Fatalf("created family bank root refs = %+v, want one root", account.Objects.ObjectIDs)
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created family bank root %q not found", account.Objects.ObjectIDs[0])
	}
	if root.Properties["value"] != "0" || root.Properties["shotsMax"] != "200" {
		t.Fatalf("created family bank root props = %+v", root.Properties)
	}
}

func TestFamilyBankInventoryHandlerListsEmptyExistingContainerLikeLegacy(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	root := loaded.Objects["object:family-storage-root"]
	root.Contents.ObjectIDs = nil
	root.Properties["shotsCurrent"] = "0"
	loaded.Objects[root.ID] = root
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	want := "패거리 창고의 보관품입니다.\n패거리에 기증된 물건이 없습니다."
	if status != enginecmd.StatusDefault || ctx.OutputString() != want {
		t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), want)
	}
}

func TestFamilyBankInventoryHandlerListRequiresDetectInvisible(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	if got := ctx.OutputString(); strings.Contains(got, "은신검") {
		t.Fatalf("invisible family bank object leaked without PDINVI:\n%s", got)
	}

	loaded = familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDINVI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	ctx = &enginecmd.Context{ActorID: "Alice"}

	status, err = NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	got := ctx.OutputString()
	if status != enginecmd.StatusDefault || !strings.Contains(got, "은신검") {
		t.Fatalf("status/output = %d/%q, want invisible family bank object listed with PDINVI", status, got)
	}
	if strings.Contains(got, "숨은 물건") {
		t.Fatalf("hidden family bank object listed with PDINVI:\n%s", got)
	}
}

func TestFamilyBankObjectHasFlagExpandsLegacyAliases(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	protoID := model.PrototypeID("proto:family-alias")
	familyBankTestAddPrototype(t, loaded, model.ObjectPrototype{
		ID:          protoID,
		DisplayName: "별칭물건",
		Metadata: model.Metadata{
			Tags: []string{"scene"},
		},
		Properties: map[string]string{
			"inventoryPermanent": "yes",
			"flags":              "weightless",
		},
	})
	world := state.NewWorld(loaded)

	tests := []struct {
		name  string
		obj   model.ObjectInstance
		flags []string
		want  bool
	}{
		{
			name: "object metadata canonical alias matches legacy OPERM2",
			obj: model.ObjectInstance{
				Metadata: model.Metadata{Tags: []string{"inventoryPermanent"}},
			},
			flags: []string{"OPERM2"},
			want:  true,
		},
		{
			name: "object property canonical alias matches legacy OTEMPP",
			obj: model.ObjectInstance{
				Properties: map[string]string{"tempPermanent": "true"},
			},
			flags: []string{"OTEMPP"},
			want:  true,
		},
		{
			name: "object flags container token matches legacy OTEMPP",
			obj: model.ObjectInstance{
				Properties: map[string]string{"flags": "tempPermanent|hidden"},
			},
			flags: []string{"OTEMPP"},
			want:  true,
		},
		{
			name: "prototype metadata canonical alias matches legacy OSCENE",
			obj: model.ObjectInstance{
				PrototypeID: protoID,
			},
			flags: []string{"OSCENE"},
			want:  true,
		},
		{
			name: "prototype property canonical alias matches legacy OPERM2",
			obj: model.ObjectInstance{
				PrototypeID: protoID,
			},
			flags: []string{"OPERM2"},
			want:  true,
		},
		{
			name: "prototype flags container token matches legacy OWTLES",
			obj: model.ObjectInstance{
				PrototypeID: protoID,
			},
			flags: []string{"OWTLES"},
			want:  true,
		},
		{
			name: "disabled canonical property does not match",
			obj: model.ObjectInstance{
				Properties: map[string]string{"inventoryPermanent": "false"},
			},
			flags: []string{"OPERM2"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := familyBankObjectHasFlag(world, tt.obj, tt.flags...); got != tt.want {
				t.Fatalf("familyBankObjectHasFlag(%+v, %+v) = %t, want %t", tt.obj, tt.flags, got, tt.want)
			}
		})
	}
}

func TestFamilyBankInventoryHandlerListHidesFlagsContainerSceneryLikeLegacy(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObjectWithProperties(t, loaded, "object:hidden-by-flags", "proto:hidden-by-flags", "숨은 물건", nil, map[string]string{"flags": "hidden"})
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	if got := ctx.OutputString(); strings.Contains(got, "숨은 물건") {
		t.Fatalf("hidden family bank object listed from flags container:\n%s", got)
	}
}

func TestFamilyBankInventoryHandlerListUsesDetectMagicGrouping(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-one", "proto:magic-sword-one", "청동검", nil, map[string]string{"adjustment": "1"})
	familyBankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-two", "proto:magic-sword-two", "청동검", nil, map[string]string{"adjustment": "2"})
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got := ctx.OutputString()
	if status != enginecmd.StatusDefault || !strings.Contains(got, "(x2) 청동검") {
		t.Fatalf("status/output = %d/%q, want adjusted family bank objects grouped without PDMAGI", status, got)
	}
	if strings.Contains(got, "청동검(+1)") || strings.Contains(got, "청동검(+2)") {
		t.Fatalf("family bank list revealed magic adjustment without PDMAGI:\n%s", got)
	}

	loaded = familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-one", "proto:magic-sword-one", "청동검", nil, map[string]string{"adjustment": "1"})
	familyBankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-two", "proto:magic-sword-two", "청동검", nil, map[string]string{"adjustment": "2"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDMAGI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	ctx = &enginecmd.Context{ActorID: "Alice"}

	status, err = NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler with PDMAGI error: %v", err)
	}
	got = ctx.OutputString()
	for _, want := range []string{"청동검(+1)", "청동검(+2)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("family bank list with PDMAGI missing %q:\n%s", want, got)
		}
	}
	if status != enginecmd.StatusDefault || strings.Contains(got, "(x2) 청동검") {
		t.Fatalf("status/output = %d/%q, want adjusted family bank objects split with PDMAGI", status, got)
	}
}

func TestFamilyBankInventoryHandlerStoresInventoryObject(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 기념패를 기증했습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}

	root, _ := world.Object("object:family-storage-root")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:keepsake") || root.Properties["shotsCurrent"] != "4" {
		t.Fatalf("storage root contents/properties = %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if familyBankTestContains(creature.Inventory.ObjectIDs, "object:keepsake") {
		t.Fatalf("inventory still contains keepsake: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankInventoryHandlerRejectsObjectIDTargetLikeLegacyFindObj(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"object:keepsake"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 그런것을 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want object ID target rejected", status, ctx.OutputString())
	}

	root, _ := world.Object("object:family-storage-root")
	creature, _ := world.Creature("creature:alice")
	if familyBankTestContains(root.Contents.ObjectIDs, "object:keepsake") ||
		!familyBankTestContains(creature.Inventory.ObjectIDs, "object:keepsake") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("object ID target changed state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestFamilyBankInventoryHandlerUsesLegacyPrefixOrderInsteadOfExactFirst(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddPrototype(t, loaded, model.ObjectPrototype{ID: "proto:keepsake-fragment", DisplayName: "기념패조각"})
	familyBankTestAddObject(t, loaded, model.ObjectInstance{
		ID:                  "object:keepsake-fragment",
		PrototypeID:         "proto:keepsake-fragment",
		DisplayNameOverride: "기념패조각",
		Location:            model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
	})
	creature := loaded.Creatures["creature:alice"]
	creature.Inventory.ObjectIDs = append([]model.ObjectInstanceID{"object:keepsake-fragment"}, creature.Inventory.ObjectIDs...)
	loaded.Creatures[creature.ID] = creature
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"기념패"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 기념패조각을 기증했습니다.\n" {
		t.Fatalf("status/output = %d/%q, want earlier prefix match selected", status, ctx.OutputString())
	}

	root, _ := world.Object("object:family-storage-root")
	creature, _ = world.Creature("creature:alice")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:keepsake-fragment") ||
		familyBankTestContains(root.Contents.ObjectIDs, "object:keepsake") ||
		!familyBankTestContains(creature.Inventory.ObjectIDs, "object:keepsake") {
		t.Fatalf("prefix order state = root:%+v inv:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankInventoryHandlerStoresObjectWithMissingBank(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	delete(loaded.Banks, "bank:family:무영문_3")
	delete(loaded.Objects, "object:family-storage-root")
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 기념패를 기증했습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:family:무영문_3")
	if !ok {
		t.Fatal("family bank account was not created")
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created family bank root %q not found", account.Objects.ObjectIDs[0])
	}
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:keepsake") || root.Properties["shotsCurrent"] != "1" {
		t.Fatalf("created family bank root contents/properties = %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if familyBankTestContains(creature.Inventory.ObjectIDs, "object:keepsake") {
		t.Fatalf("inventory still contains keepsake: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankInventoryHandlerRequiresDetectInvisibleForSingleStore(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddInventoryObject(t, loaded, "object:invisible-keepsake", "proto:invisible-keepsake", "은신패", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 그런것을 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want invisible inventory object hidden", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if !familyBankTestContains(creature.Inventory.ObjectIDs, "object:invisible-keepsake") {
		t.Fatalf("inventory lost invisible object after rejected store: %+v", creature.Inventory.ObjectIDs)
	}

	loaded = familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddInventoryObject(t, loaded, "object:invisible-keepsake", "proto:invisible-keepsake", "은신패", []string{"OINVIS"})
	creatureLoaded := loaded.Creatures["creature:alice"]
	creatureLoaded.Metadata.Tags = append(creatureLoaded.Metadata.Tags, "PDINVI")
	loaded.Creatures[creatureLoaded.ID] = creatureLoaded
	world = state.NewWorld(loaded)
	ctx = &enginecmd.Context{ActorID: "Alice"}

	status, err = NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 은신패를 기증했습니다.\n" {
		t.Fatalf("PDINVI status/output = %d/%q, want family bank store confirmation", status, ctx.OutputString())
	}
	root, _ := world.Object("object:family-storage-root")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:invisible-keepsake") {
		t.Fatalf("family bank root missing invisible object after PDINVI store: %+v", root.Contents.ObjectIDs)
	}
	creature, _ = world.Creature("creature:alice")
	if familyBankTestContains(creature.Inventory.ObjectIDs, "object:invisible-keepsake") {
		t.Fatalf("inventory still contains invisible object after PDINVI store: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankInventoryHandlerQueuesSaveAfterObjectStore(t *testing.T) {
	rootDir := t.TempDir()
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	world.SetDBRoot(rootDir)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	world.FlushSaveQueue()

	playerSave := waitForFamilyBankPlayerSave(t, world, rootDir, "Alice")
	if playerSave.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if familyBankTestContains(playerSave.Creature.Inventory.ObjectIDs, "object:keepsake") {
		t.Fatalf("saved player inventory still has keepsake: %+v", playerSave.Creature.Inventory.ObjectIDs)
	}
	bankSave := waitForFamilyBankSave(t, world, "bank:family:무영문_3")
	root, ok := familyBankSaveObject(bankSave, "object:family-storage-root")
	if !ok {
		t.Fatalf("saved family bank root missing from bundle: %+v", bankSave.Objects)
	}
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:keepsake") {
		t.Fatalf("saved family bank root missing keepsake: %+v", root.Contents.ObjectIDs)
	}
}

func TestFamilyBankInventoryHandlerBroadcastsStoreLikeLegacy(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	var broadcasts []struct {
		roomID  model.RoomID
		exclude string
		text    string
	}
	ctx := &enginecmd.Context{
		SessionID: "s1",
		ActorID:   "Alice",
		Values: map[string]any{
			enginecmd.ContextRoomBroadcastKey: enginecmd.RoomBroadcastFunc(func(roomID model.RoomID, excludeSessionID string, text string) error {
				broadcasts = append(broadcasts, struct {
					roomID  model.RoomID
					exclude string
					text    string
				}{roomID: roomID, exclude: excludeSessionID, text: text})
				return errors.New("session closed")
			}),
		},
	}

	status, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"기념"}})
	if err != nil {
		t.Fatalf("handler error = %v, want nil", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 기념패를 기증했습니다.\n" {
		t.Fatalf("status/output = %d/%q, want donation confirmation", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts = %+v, want one room broadcast", broadcasts)
	}
	if got := broadcasts[0]; got.roomID != "room:family-bank" || got.exclude != "s1" || got.text != "\nAlice이 기념패를 기증했습니다." {
		t.Fatalf("broadcast = %+v, want legacy family bank donation broadcast", got)
	}
}

func TestFamilyBankOutputHandlerTakesContainerObject(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"청동"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 청동검을 반출했습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}

	root, _ := world.Object("object:family-storage-root")
	if familyBankTestContains(root.Contents.ObjectIDs, "object:sword") || root.Properties["shotsCurrent"] != "2" {
		t.Fatalf("storage root contents/properties = %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if !familyBankTestContains(creature.Inventory.ObjectIDs, "object:sword") {
		t.Fatalf("inventory missing sword: %+v", creature.Inventory.ObjectIDs)
	}
	sword, _ := world.Object("object:sword")
	if sword.Location.CreatureID != "creature:alice" || sword.Location.Slot != "inventory" {
		t.Fatalf("sword location = %+v, want creature inventory", sword.Location)
	}
}

func TestFamilyBankOutputHandlerBroadcastsOutputLikeLegacy(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	var broadcasts []struct {
		roomID  model.RoomID
		exclude string
		text    string
	}
	ctx := &enginecmd.Context{
		SessionID: "s1",
		ActorID:   "Alice",
		Values: map[string]any{
			enginecmd.ContextRoomBroadcastKey: enginecmd.RoomBroadcastFunc(func(roomID model.RoomID, excludeSessionID string, text string) error {
				broadcasts = append(broadcasts, struct {
					roomID  model.RoomID
					exclude string
					text    string
				}{roomID: roomID, exclude: excludeSessionID, text: text})
				return errors.New("session closed")
			}),
		},
	}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"청동"}})
	if err != nil {
		t.Fatalf("handler error = %v, want nil", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 청동검을 반출했습니다." {
		t.Fatalf("status/output = %d/%q, want output confirmation", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 {
		t.Fatalf("broadcasts = %+v, want one room broadcast", broadcasts)
	}
	if got := broadcasts[0]; got.roomID != "room:family-bank" || got.exclude != "s1" || got.text != "\n패거리 창고에서 Alice이 청동검을 꺼냈습니다." {
		t.Fatalf("broadcast = %+v, want legacy family bank output broadcast", got)
	}
}

func TestFamilyBankOutputHandlerRejectsMissingTargetLikeLegacy(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "패거리 창고에서 무엇을 꺼내시려고요?" {
		t.Fatalf("status/output = %d/%q, want legacy missing target", status, ctx.OutputString())
	}
}

func TestFamilyBankOutputHandlerRejectsObjectIDTargetLikeLegacyFindObj(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"object:sword"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "그 안에 그런것은 없어요." {
		t.Fatalf("status/output = %d/%q, want object ID target rejected", status, ctx.OutputString())
	}

	root, _ := world.Object("object:family-storage-root")
	creature, _ := world.Creature("creature:alice")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:sword") ||
		familyBankTestContains(creature.Inventory.ObjectIDs, "object:sword") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("object ID target changed state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestFamilyBankOutputHandlerUsesLegacyPrefixOrderInsteadOfExactFirst(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddPrototype(t, loaded, model.ObjectPrototype{ID: "proto:sword-sheath", DisplayName: "청동검집"})
	familyBankTestAddObject(t, loaded, model.ObjectInstance{
		ID:                  "object:sword-sheath",
		PrototypeID:         "proto:sword-sheath",
		DisplayNameOverride: "청동검집",
		Location:            model.ObjectLocation{ContainerID: "object:family-storage-root"},
	})
	root := loaded.Objects["object:family-storage-root"]
	root.Contents.ObjectIDs = append([]model.ObjectInstanceID{"object:sword-sheath"}, root.Contents.ObjectIDs...)
	root.Properties["shotsCurrent"] = fmt.Sprintf("%d", len(root.Contents.ObjectIDs))
	loaded.Objects[root.ID] = root
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"청동검"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 청동검집을 반출했습니다." {
		t.Fatalf("status/output = %d/%q, want earlier prefix match selected", status, ctx.OutputString())
	}

	root, _ = world.Object("object:family-storage-root")
	creature, _ := world.Creature("creature:alice")
	if familyBankTestContains(root.Contents.ObjectIDs, "object:sword-sheath") ||
		!familyBankTestContains(root.Contents.ObjectIDs, "object:sword") ||
		!familyBankTestContains(creature.Inventory.ObjectIDs, "object:sword-sheath") {
		t.Fatalf("prefix order state = root:%+v inv:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankOutputHandlerRequiresDetectInvisibleForSingleObject(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "그 안에 그런것은 없어요." {
		t.Fatalf("status/output = %d/%q, want invisible family bank object hidden", status, ctx.OutputString())
	}
	root, _ := world.Object("object:family-storage-root")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:invisible-sword") {
		t.Fatalf("family bank root lost invisible object after rejected output: %+v", root.Contents.ObjectIDs)
	}

	loaded = familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDINVI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	ctx = &enginecmd.Context{ActorID: "Alice"}

	status, err = NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 은신검을 반출했습니다." {
		t.Fatalf("PDINVI status/output = %d/%q, want family bank output confirmation", status, ctx.OutputString())
	}
	root, _ = world.Object("object:family-storage-root")
	if familyBankTestContains(root.Contents.ObjectIDs, "object:invisible-sword") {
		t.Fatalf("family bank root still contains invisible object: %+v", root.Contents.ObjectIDs)
	}
	creature, _ = world.Creature("creature:alice")
	if !familyBankTestContains(creature.Inventory.ObjectIDs, "object:invisible-sword") {
		t.Fatalf("inventory missing invisible object after PDINVI output: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankOutputHandlerSingleFindObjAllowsHiddenObject(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"숨은"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 숨은 물건을 반출했습니다." {
		t.Fatalf("status/output = %d/%q, want C find_obj-style hidden output", status, ctx.OutputString())
	}
	root, _ := world.Object("object:family-storage-root")
	if familyBankTestContains(root.Contents.ObjectIDs, "object:hidden") {
		t.Fatalf("family bank root still contains hidden object: %+v", root.Contents.ObjectIDs)
	}
	creature, _ := world.Creature("creature:alice")
	if !familyBankTestContains(creature.Inventory.ObjectIDs, "object:hidden") {
		t.Fatalf("inventory missing hidden object after output: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestFamilyBankOutputHandlerRejectsWhenInventoryTooFull(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	familyBankTestAddInventoryFillers(t, loaded, 142)
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"청동"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "당신은 더이상 가질 수 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:family-storage-root")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:sword") || root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("storage root changed after rejected output: %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
}

func TestFamilyBankOutputHandlerDoesNotTreatAllAsBulkTarget(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"depot", "family"}, 3, true)
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	want := "그 안에 그런것은 없어요."
	if status != enginecmd.StatusDefault || ctx.OutputString() != want {
		t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), want)
	}
	root, _ := world.Object("object:family-storage-root")
	if !familyBankTestContains(root.Contents.ObjectIDs, "object:sword") ||
		!familyBankTestContains(root.Contents.ObjectIDs, "object:potion") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("output state changed for C non-bulk target: %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
}

func TestFamilyBankOutputHandlerQueuesSaveAfterObjectTake(t *testing.T) {
	rootDir := t.TempDir()
	world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, 3, true))
	world.SetDBRoot(rootDir)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyBankOutputHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"청동"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	world.FlushSaveQueue()

	playerSave := waitForFamilyBankPlayerSave(t, world, rootDir, "Alice")
	if playerSave.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if !familyBankTestContains(playerSave.Creature.Inventory.ObjectIDs, "object:sword") {
		t.Fatalf("saved player inventory missing sword: %+v", playerSave.Creature.Inventory.ObjectIDs)
	}
	bankSave := waitForFamilyBankSave(t, world, "bank:family:무영문_3")
	root, ok := familyBankSaveObject(bankSave, "object:family-storage-root")
	if !ok {
		t.Fatalf("saved family bank root missing from bundle: %+v", bankSave.Objects)
	}
	if familyBankTestContains(root.Contents.ObjectIDs, "object:sword") {
		t.Fatalf("saved family bank root still has sword: %+v", root.Contents.ObjectIDs)
	}
}

func TestFamilyDepositHandlerCreditsFamilyBankValue(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"2만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 2만냥을 기부했습니다.\n패거리 금고의 총액이 12만냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if got := creature.Stats["gold"]; got != 30000 {
		t.Fatalf("gold = %d, want 30000", got)
	}
	root, _ := world.Object("object:family-money-root")
	if got := root.Properties["value"]; got != "12" {
		t.Fatalf("family bank value = %q, want 12", got)
	}
}

func TestFamilyDepositHandlerUsesLegacyAtolPrefix(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"1,000만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 1만냥을 기부했습니다.\n패거리 금고의 총액이 11만냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestFamilyDepositHandlerCreatesMissingMoneyBank(t *testing.T) {
	loaded := familyBankTestWorld(t, []string{"bank", "family"}, 3, true)
	delete(loaded.Banks, "bank:family:무영문_0")
	delete(loaded.Objects, "object:family-money-root")
	world := state.NewWorld(loaded)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"2만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 2만냥을 기부했습니다.\n패거리 금고의 총액이 2만냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:family:무영문_0")
	if !ok {
		t.Fatal("family money bank account was not created")
	}
	if len(account.Objects.ObjectIDs) != 1 {
		t.Fatalf("family money bank root refs = %+v, want one root", account.Objects.ObjectIDs)
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created family money root %q not found", account.Objects.ObjectIDs[0])
	}
	if root.Location.BankID != account.ID || root.Properties["value"] != "2" || root.Properties["shotsMax"] != "200" {
		t.Fatalf("created family money root = location:%+v props:%+v", root.Location, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if got := creature.Stats["gold"]; got != 30000 {
		t.Fatalf("gold = %d, want 30000", got)
	}
}

func TestFamilyDepositHandlerSavesResolvedFamilyBankID(t *testing.T) {
	rootDir := t.TempDir()
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	world.SetDBRoot(rootDir)
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"2만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	world.FlushSaveQueue()
	bankSave := waitForFamilyBankSave(t, world, "bank:family:무영문_0")
	root, ok := familyBankSaveObject(bankSave, "object:family-money-root")
	if !ok {
		t.Fatalf("saved family money root missing from bundle: %+v", bankSave.Objects)
	}
	if got := root.Properties["value"]; got != "12" {
		t.Fatalf("saved family bank value = %q, want 12", got)
	}
}

func TestFamilyWithdrawHandlerRequiresBossAndDebitsFamilyBankValue(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, false))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyWithdrawHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"2만냥"}})
	if err != nil {
		t.Fatalf("non-boss handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "패거리의 문주만이 가능합니다." {
		t.Fatalf("non-boss status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	root, _ := world.Object("object:family-money-root")
	if creature.Stats["gold"] != 50000 || root.Properties["value"] != "10" {
		t.Fatalf("state changed after non-boss withdraw: gold=%d value=%s", creature.Stats["gold"], root.Properties["value"])
	}

	ctx = &enginecmd.Context{ActorID: "Alice"}
	status, err = NewFamilyWithdrawHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if err != nil {
		t.Fatalf("non-boss missing-amount handler error: %v", err)
	}
	if status != enginecmd.StatusDefault || ctx.OutputString() != "패거리의 문주만이 가능합니다." {
		t.Fatalf("non-boss missing-amount status/output = %d/%q, want boss gate first", status, ctx.OutputString())
	}

	world = state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	ctx = &enginecmd.Context{ActorID: "Alice"}
	status, err = NewFamilyWithdrawHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"2만냥"}})
	if err != nil {
		t.Fatalf("boss handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 2만냥을 인출했습니다.\n패거리 금고가 8만냥이 되었습니다." {
		t.Fatalf("boss status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ = world.Creature("creature:alice")
	if got := creature.Stats["gold"]; got != 70000 {
		t.Fatalf("gold = %d, want 70000", got)
	}
	root, _ = world.Object("object:family-money-root")
	if got := root.Properties["value"]; got != "8" {
		t.Fatalf("family bank value = %q, want 8", got)
	}
}

func TestFamilyWithdrawHandlerUsesLegacyAtolPrefix(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	ctx := &enginecmd.Context{ActorID: "Alice"}

	status, err := NewFamilyWithdrawHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"1,000만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 1만냥을 인출했습니다.\n패거리 금고가 9만냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestFamilyWithdrawHandlerDoesNotBroadcastLargeWithdrawal(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	_ = world.UpdateObjectProperty("object:family-money-root", "value", "200")
	broadcastCalled := false

	ctx := &enginecmd.Context{
		ActorID: "Alice",
		Values: map[string]any{
			ContextBroadcastKey: func(cmd session.Command) error {
				broadcastCalled = true
				return nil
			},
		},
	}
	status, err := NewFamilyWithdrawHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"150만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 150만냥을 인출했습니다.\n패거리 금고가 50만냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if broadcastCalled {
		t.Fatal("family_withdraw broadcasted a large withdrawal; C only broadcasts large deposits")
	}
}

func TestFamilyWithdrawHandlerLimits(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing amount", want: "얼마를 인출하시려고요?"},
		{name: "bad suffix", args: []string{"500냥"}, want: "사용법 : 몇만냥 인출"},
		{name: "negative", args: []string{"-5만냥"}, want: "돈의 단위는 음수가 될수 없습니다."},
		{name: "insufficient", args: []string{"11만냥"}, want: "패거리 금고에 그만큼의 돈이 없습니다."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
			ctx := &enginecmd.Context{ActorID: "Alice"}

			status, err := NewFamilyWithdrawHandler(world)(ctx, enginecmd.ResolvedCommand{Args: tt.args})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if status != enginecmd.StatusDefault || ctx.OutputString() != tt.want {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.want)
			}
		})
	}
}

func TestFamilyDepositHandlerLimits(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))

	// 1. Missing amount
	ctx := &enginecmd.Context{ActorID: "Alice"}
	_, _ = NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{})
	if ctx.OutputString() != "얼마를 기부하시려고요?" {
		t.Fatalf("expected missing amount prompt, got %q", ctx.OutputString())
	}

	// 2. Invalid syntax (no "만냥" suffix)
	ctx = &enginecmd.Context{ActorID: "Alice"}
	_, _ = NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"500냥"}})
	if ctx.OutputString() != "사용법 : 몇만냥 기부" {
		t.Fatalf("expected syntax error, got %q", ctx.OutputString())
	}

	// 3. Negative amount
	ctx = &enginecmd.Context{ActorID: "Alice"}
	_, _ = NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"-5만냥"}})
	if ctx.OutputString() != "돈의 단위는 음수가 될수 없습니다." {
		t.Fatalf("expected negative error, got %q", ctx.OutputString())
	}

	// 4. Deposit > 1000만냥
	ctx = &enginecmd.Context{ActorID: "Alice"}
	_, _ = NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"1001만냥"}})
	if ctx.OutputString() != "기부는 1000만냥 이하만 가능합니다.\n" {
		t.Fatalf("expected limit error, got %q", ctx.OutputString())
	}

	// 5. Player doesn't have enough gold
	ctx = &enginecmd.Context{ActorID: "Alice"}
	_, _ = NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"6만냥"}})
	if ctx.OutputString() != "당신은 그만큼의 돈을 가지고 있지 않습니다.\n" {
		t.Fatalf("expected insufficient gold error, got %q", ctx.OutputString())
	}

	// 6. Total balance after deposit > 10억
	// Legacy family bank value is stored in 만냥 units; C still checks
	// currentValue + requestedGold against 10억.
	_ = world.UpdateObjectProperty("object:family-money-root", "value", "999990000")

	ctx = &enginecmd.Context{ActorID: "Alice"}
	_, _ = NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"2만냥"}})
	if ctx.OutputString() != "패거리 금고의 총액은 10억 이상 될수 없습니다. \n" {
		t.Fatalf("expected max balance error, got %q", ctx.OutputString())
	}
}

func TestFamilyDepositHandlerBroadcasting(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	// Give Alice 200만냥 (2000000 gold)
	_ = world.UpdateCreatureStat("creature:alice", "gold", 2000000)
	broadcastCalled := false
	var broadcastCmd session.Command

	ctx := &enginecmd.Context{
		ActorID: "Alice",
		Values: map[string]any{
			ContextBroadcastKey: func(cmd session.Command) error {
				broadcastCalled = true
				broadcastCmd = cmd
				return nil
			},
		},
	}

	status, err := NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"150만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}

	if !broadcastCalled {
		t.Fatal("expected broadcast to be called")
	}
	wantBroadcast := "\n### Alice님이 패거리에 150만냥을 기부하였습니다."
	if broadcastCmd.Write != wantBroadcast {
		t.Fatalf("broadcast message = %q, want %q", broadcastCmd.Write, wantBroadcast)
	}
}

func TestFamilyDepositHandlerBroadcastingUsesLegacyMessageDuringWar(t *testing.T) {
	world := state.NewWorld(familyBankTestWorld(t, []string{"bank", "family"}, 3, true))
	if _, err := world.RequestFamilyWar(2, 5); err != nil {
		t.Fatalf("RequestFamilyWar: %v", err)
	}
	if _, err := world.AcceptFamilyWar(5, 2); err != nil {
		t.Fatalf("AcceptFamilyWar: %v", err)
	}
	_ = world.UpdateCreatureStat("creature:alice", "gold", 2000000)
	var broadcastCmd session.Command

	ctx := &enginecmd.Context{
		ActorID: "Alice",
		Values: map[string]any{
			ContextBroadcastKey: func(cmd session.Command) error {
				broadcastCmd = cmd
				return nil
			},
		},
	}
	status, err := NewFamilyDepositHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{"150만냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != enginecmd.StatusDefault ||
		ctx.OutputString() != "당신은 150만냥을 기부했습니다.\n패거리 금고의 총액이 160만냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	wantBroadcast := "\n### Alice님이 패거리에 150만냥을 기부하였습니다."
	if broadcastCmd.Write != wantBroadcast {
		t.Fatalf("broadcast message = %q, want %q", broadcastCmd.Write, wantBroadcast)
	}
}

func TestFamilyBankInventoryHandlerStorageRestrictions(t *testing.T) {
	tests := []struct {
		name          string
		roomSpecial   int
		itemName      string
		expectedError string
	}{
		{
			name:          "Weapon in non-weapon depot",
			roomSpecial:   2, // Armor warehouse
			itemName:      "청동검",
			expectedError: "\n무기류 창고에서만 가능합니다.",
		},
		{
			name:          "Weapon in weapon depot",
			roomSpecial:   1, // Weapon warehouse
			itemName:      "청동검",
			expectedError: "", // should succeed
		},
		{
			name:          "Armor in non-armor depot",
			roomSpecial:   1, // Weapon warehouse
			itemName:      "가죽갑옷",
			expectedError: "\n방어구류 창고에서만 가능합니다.",
		},
		{
			name:          "Armor in armor depot",
			roomSpecial:   2, // Armor warehouse
			itemName:      "가죽갑옷",
			expectedError: "",
		},
		{
			name:          "Potion in non-potion depot",
			roomSpecial:   1,
			itemName:      "치료약",
			expectedError: "\n주술구류 창고에서만 가능합니다.",
		},
		{
			name:          "Potion in potion depot",
			roomSpecial:   3,
			itemName:      "치료약",
			expectedError: "",
		},
		{
			name:          "Scroll in non-potion depot",
			roomSpecial:   1,
			itemName:      "귀환주문서",
			expectedError: "\n주술구류 창고에서만 가능합니다.",
		},
		{
			name:          "Scroll in potion depot",
			roomSpecial:   3,
			itemName:      "귀환주문서",
			expectedError: "",
		},
		{
			name:          "Wand in non-wand depot",
			roomSpecial:   1,
			itemName:      "나무지팡이",
			expectedError: "\n성구류 창고에서만 가능합니다.",
		},
		{
			name:          "Wand in wand depot",
			roomSpecial:   4,
			itemName:      "나무지팡이",
			expectedError: "",
		},
		{
			name:          "Key in non-key depot",
			roomSpecial:   1,
			itemName:      "철열쇠",
			expectedError: "\n열쇠류 창고에서만 가능합니다.",
		},
		{
			name:          "Key in key depot",
			roomSpecial:   5,
			itemName:      "철열쇠",
			expectedError: "",
		},
		{
			name:          "Light source in non-misc depot",
			roomSpecial:   1,
			itemName:      "횃불",
			expectedError: "\n기타류 창고에서만 가능합니다.",
		},
		{
			name:          "Light source in misc depot",
			roomSpecial:   6,
			itemName:      "횃불",
			expectedError: "",
		},
		{
			name:          "Misc in non-misc depot",
			roomSpecial:   1,
			itemName:      "바위",
			expectedError: "\n기타류 창고에서만 가능합니다.",
		},
		{
			name:          "Misc in misc depot",
			roomSpecial:   6,
			itemName:      "바위",
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := state.NewWorld(familyBankTestWorld(t, []string{"depot", "family"}, tt.roomSpecial, false))
			ctx := &enginecmd.Context{ActorID: "Alice"}

			_, err := NewFamilyBankInventoryHandler(world)(ctx, enginecmd.ResolvedCommand{Args: []string{tt.itemName}})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			out := ctx.OutputString()
			if tt.expectedError != "" {
				if out != tt.expectedError {
					t.Fatalf("output = %q, want error %q", out, tt.expectedError)
				}
			} else {
				if strings.Contains(out, "창고에서만 가능합니다") {
					t.Fatalf("unexpected restriction error in output: %q", out)
				}
				if !strings.Contains(out, "기증했습니다") {
					t.Fatalf("expected success, got output: %q", out)
				}
			}
		})
	}
}

func familyBankTestWorld(t *testing.T, roomTags []string, roomSpecial int, boss bool) *worldload.World {
	t.Helper()

	loaded := worldload.NewWorld()
	familyBankTestAddFamily(t, loaded, model.Family{ID: 2, Slot: 2, DisplayName: "무영문", BossName: "무영풍"})
	familyBankTestAddRoom(t, loaded, model.Room{
		ID:          "room:family-bank",
		DisplayName: "패거리 은행",
		Properties:  map[string]string{"special": strconv.Itoa(roomSpecial)},
		Metadata:    model.Metadata{Tags: append([]string(nil), roomTags...)},
	})

	familyBankTestAddPlayer(t, loaded, model.Player{
		ID:          "Alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:family-bank",
	})
	stats := map[string]int{"gold": 50000, "familyFlag": 1, "familyID": 2}
	if boss {
		stats["PFMBOS"] = 1
	}
	familyBankTestAddCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "Alice",
		RoomID:      "room:family-bank",
		Stats:       stats,
		Inventory: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{
			"object:keepsake",
			"object:bag",
			"object:weapon1",
			"object:armor1",
			"object:potion1",
			"object:scroll1",
			"object:wand1",
			"object:key1",
			"object:light1",
			"object:misc1",
		}},
	})

	for _, proto := range []model.ObjectPrototype{
		{ID: "proto:family-storage-root", DisplayName: "패거리 창고"},
		{ID: "proto:family-money-root", DisplayName: "패거리 금고"},
		{ID: "proto:sword", DisplayName: "청동검"},
		{ID: "proto:potion", DisplayName: "치료약"},
		{ID: "proto:hidden", DisplayName: "숨은 물건"},
		{ID: "proto:stale", DisplayName: "위치가 어긋난 물건"},
		{ID: "proto:keepsake", DisplayName: "기념패"},
		{ID: "proto:bag", Kind: model.ObjectKindContainer, DisplayName: "가방"},
		{ID: "proto:weapon1", Kind: model.ObjectKindWeapon, DisplayName: "청동검"},
		{ID: "proto:armor1", Kind: model.ObjectKindArmor, DisplayName: "가죽갑옷"},
		{ID: "proto:potion1", Kind: model.ObjectKindPotion, DisplayName: "치료약"},
		{ID: "proto:scroll1", Kind: model.ObjectKindScroll, DisplayName: "귀환주문서"},
		{ID: "proto:wand1", Kind: model.ObjectKindWand, DisplayName: "나무지팡이"},
		{ID: "proto:key1", Kind: model.ObjectKindKey, DisplayName: "철열쇠"},
		{ID: "proto:light1", Kind: model.ObjectKindLightSource, DisplayName: "횃불"},
		{ID: "proto:misc1", Kind: model.ObjectKindMisc, DisplayName: "바위"},
	} {
		familyBankTestAddPrototype(t, loaded, proto)
	}

	familyBankTestAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:family-storage-root",
		PrototypeID: "proto:family-storage-root",
		Location:    model.ObjectLocation{BankID: model.BankID("bank:family:무영문_" + strconv.Itoa(roomSpecial)), Slot: "bank"},
		Properties:  map[string]string{"shotsCurrent": "3", "shotsMax": "5"},
		Contents: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{
			"object:sword",
			"object:hidden",
			"object:potion",
			"object:stale",
		}},
	})
	familyBankTestAddObject(t, loaded, model.ObjectInstance{
		ID:          "object:family-money-root",
		PrototypeID: "proto:family-money-root",
		Location:    model.ObjectLocation{BankID: "bank:family:무영문_0", Slot: "bank"},
		Properties:  map[string]string{"value": "10", "shotsCurrent": "0", "shotsMax": "200"},
	})
	for _, object := range []model.ObjectInstance{
		{ID: "object:sword", PrototypeID: "proto:sword", DisplayNameOverride: "청동검", Location: model.ObjectLocation{ContainerID: "object:family-storage-root"}},
		{ID: "object:potion", PrototypeID: "proto:potion", DisplayNameOverride: "치료약", Location: model.ObjectLocation{ContainerID: "object:family-storage-root"}},
		{ID: "object:hidden", PrototypeID: "proto:hidden", DisplayNameOverride: "숨은 물건", Location: model.ObjectLocation{ContainerID: "object:family-storage-root"}, Metadata: model.Metadata{Tags: []string{"hidden"}}},
		{ID: "object:stale", PrototypeID: "proto:stale", DisplayNameOverride: "위치가 어긋난 물건", Location: model.ObjectLocation{RoomID: "room:family-bank"}},
		{ID: "object:keepsake", PrototypeID: "proto:keepsake", DisplayNameOverride: "기념패", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:bag", PrototypeID: "proto:bag", DisplayNameOverride: "가방", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:weapon1", PrototypeID: "proto:weapon1", DisplayNameOverride: "청동검", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:armor1", PrototypeID: "proto:armor1", DisplayNameOverride: "가죽갑옷", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:potion1", PrototypeID: "proto:potion1", DisplayNameOverride: "치료약", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:scroll1", PrototypeID: "proto:scroll1", DisplayNameOverride: "귀환주문서", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:wand1", PrototypeID: "proto:wand1", DisplayNameOverride: "나무지팡이", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:key1", PrototypeID: "proto:key1", DisplayNameOverride: "철열쇠", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:light1", PrototypeID: "proto:light1", DisplayNameOverride: "횃불", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
		{ID: "object:misc1", PrototypeID: "proto:misc1", DisplayNameOverride: "바위", Location: model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}},
	} {
		familyBankTestAddObject(t, loaded, object)
	}

	familyBankTestAddBank(t, loaded, model.BankAccount{
		ID:        model.BankID("bank:family:무영문_" + strconv.Itoa(roomSpecial)),
		Kind:      "family",
		OwnerName: "무영문_" + strconv.Itoa(roomSpecial),
		Objects:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:family-storage-root"}},
	})
	familyBankTestAddBank(t, loaded, model.BankAccount{
		ID:        "bank:family:무영문_0",
		Kind:      "family",
		OwnerName: "무영문_0",
		Objects:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:family-money-root"}},
	})
	return loaded
}

func familyBankTestAddRoom(t *testing.T, world *worldload.World, room model.Room) {
	t.Helper()
	if err := world.AddRoom(room); err != nil {
		t.Fatalf("AddRoom(%q): %v", room.ID, err)
	}
}

func familyBankTestAddPlayer(t *testing.T, world *worldload.World, player model.Player) {
	t.Helper()
	if err := world.AddPlayer(player); err != nil {
		t.Fatalf("AddPlayer(%q): %v", player.ID, err)
	}
}

func familyBankTestAddCreature(t *testing.T, world *worldload.World, creature model.Creature) {
	t.Helper()
	if err := world.AddCreature(creature); err != nil {
		t.Fatalf("AddCreature(%q): %v", creature.ID, err)
	}
}

func familyBankTestAddFamily(t *testing.T, world *worldload.World, family model.Family) {
	t.Helper()
	if err := world.AddFamily(family); err != nil {
		t.Fatalf("AddFamily(%d): %v", family.ID, err)
	}
}

func familyBankTestAddBank(t *testing.T, world *worldload.World, bank model.BankAccount) {
	t.Helper()
	if err := world.AddBank(bank); err != nil {
		t.Fatalf("AddBank(%q): %v", bank.ID, err)
	}
}

func familyBankTestAddObject(t *testing.T, world *worldload.World, object model.ObjectInstance) {
	t.Helper()
	if err := world.AddObjectInstance(object); err != nil {
		t.Fatalf("AddObjectInstance(%q): %v", object.ID, err)
	}
}

func familyBankTestAddPrototype(t *testing.T, world *worldload.World, proto model.ObjectPrototype) {
	t.Helper()
	if err := world.AddObjectPrototype(proto); err != nil {
		t.Fatalf("AddObjectPrototype(%q): %v", proto.ID, err)
	}
}

func familyBankTestContains(ids []model.ObjectInstanceID, target model.ObjectInstanceID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func familyBankTestAddInventoryFillers(t *testing.T, loaded *worldload.World, count int) {
	t.Helper()
	protoID := model.PrototypeID("proto:filler")
	if _, ok := loaded.ObjectPrototypes[protoID]; !ok {
		familyBankTestAddPrototype(t, loaded, model.ObjectPrototype{ID: protoID, DisplayName: "더미"})
	}
	creature := loaded.Creatures["creature:alice"]
	for i := 0; i < count; i++ {
		id := model.ObjectInstanceID(fmt.Sprintf("object:filler:%03d", i))
		familyBankTestAddObject(t, loaded, model.ObjectInstance{
			ID:          id,
			PrototypeID: protoID,
			Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
		})
		creature.Inventory.ObjectIDs = append(creature.Inventory.ObjectIDs, id)
	}
	loaded.Creatures[creature.ID] = creature
}

func familyBankTestAddStoredObject(t *testing.T, loaded *worldload.World, objectID model.ObjectInstanceID, protoID model.PrototypeID, name string, tags []string) {
	t.Helper()
	familyBankTestAddStoredObjectWithProperties(t, loaded, objectID, protoID, name, tags, nil)
}

func familyBankTestAddStoredObjectWithProperties(t *testing.T, loaded *worldload.World, objectID model.ObjectInstanceID, protoID model.PrototypeID, name string, tags []string, properties map[string]string) {
	t.Helper()
	familyBankTestAddPrototype(t, loaded, model.ObjectPrototype{ID: protoID, DisplayName: name})
	objectProperties := map[string]string{}
	for key, value := range properties {
		objectProperties[key] = value
	}
	familyBankTestAddObject(t, loaded, model.ObjectInstance{
		ID:                  objectID,
		PrototypeID:         protoID,
		DisplayNameOverride: name,
		Location:            model.ObjectLocation{ContainerID: "object:family-storage-root"},
		Metadata:            model.Metadata{Tags: tags},
		Properties:          objectProperties,
	})
	root := loaded.Objects["object:family-storage-root"]
	root.Contents.ObjectIDs = append(root.Contents.ObjectIDs, objectID)
	if root.Properties == nil {
		root.Properties = map[string]string{}
	}
	root.Properties["shotsCurrent"] = fmt.Sprintf("%d", len(root.Contents.ObjectIDs))
	loaded.Objects[root.ID] = root
}

func familyBankTestAddInventoryObject(t *testing.T, loaded *worldload.World, objectID model.ObjectInstanceID, protoID model.PrototypeID, name string, tags []string) {
	t.Helper()
	familyBankTestAddPrototype(t, loaded, model.ObjectPrototype{ID: protoID, DisplayName: name})
	familyBankTestAddObject(t, loaded, model.ObjectInstance{
		ID:                  objectID,
		PrototypeID:         protoID,
		DisplayNameOverride: name,
		Location:            model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
		Metadata:            model.Metadata{Tags: tags},
	})
	creature := loaded.Creatures["creature:alice"]
	creature.Inventory.ObjectIDs = append(creature.Inventory.ObjectIDs, objectID)
	loaded.Creatures[creature.ID] = creature
}

func waitForFamilyBankPlayerSave(t *testing.T, world *state.World, root string, playerID model.PlayerID) state.PlayerSaveData {
	t.Helper()
	world.FlushSaveQueue()
	save, ok, err := state.LoadPlayer(root, playerID)
	if err != nil {
		t.Fatalf("LoadPlayer(%s) error after flush: %v", playerID, err)
	}
	if !ok {
		t.Fatalf("LoadPlayer(%s) ok=false after flush", playerID)
	}
	return save
}

func waitForFamilyBankSave(t *testing.T, world *state.World, bankID model.BankID) model.BankSaveBundle {
	t.Helper()
	world.FlushSaveQueue()
	save, ok, err := world.LoadBank(bankID)
	if err != nil {
		t.Fatalf("LoadBank(%s) error after flush: %v", bankID, err)
	}
	if !ok {
		t.Fatalf("LoadBank(%s) ok=false after flush", bankID)
	}
	return save
}

func familyBankSaveObject(bundle model.BankSaveBundle, id model.ObjectInstanceID) (model.ObjectInstance, bool) {
	for _, object := range bundle.Objects {
		if object.ID == id {
			return object, true
		}
	}
	return model.ObjectInstance{}, false
}
