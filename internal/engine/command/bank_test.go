package command

import (
	"fmt"
	"strings"
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestBankBalanceHandler(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankBalanceHandler(world)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신의 잔고는 12345냥입니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankInventoryHandler(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	root := loaded.Objects["object:bank-root"]
	root.Contents.ObjectIDs = append(root.Contents.ObjectIDs, "object:stale")
	loaded.Objects[root.ID] = root
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{ID: "proto:stale", DisplayName: "위치가 어긋난 물건"})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:stale",
		PrototypeID: "proto:stale",
		Location:    model.ObjectLocation{RoomID: "room:bank"},
	})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	got := ctx.OutputString()
	for _, want := range []string{
		"당신의 이름이 새겨진 보관함입니다.\n",
		"보관품의 목록 : 청동검, 치료약.\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "숨은 물건") {
		t.Fatalf("hidden bank object leaked:\n%s", got)
	}
	if strings.Contains(got, "위치가 어긋난 물건") {
		t.Fatalf("stale bank object ref leaked:\n%s", got)
	}
}

func TestBankInventoryHandlerListRequiresDetectInvisible(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	if got := ctx.OutputString(); strings.Contains(got, "은신검") {
		t.Fatalf("invisible bank object leaked without PDINVI:\n%s", got)
	}

	loaded = bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDINVI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	defer world.Close()
	ctx = &Context{ActorID: "Alice"}

	status, err = NewBankInventoryHandler(world)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	got := ctx.OutputString()
	if status != StatusDefault || !strings.Contains(got, "은신검") {
		t.Fatalf("status/output = %d/%q, want invisible bank object listed with PDINVI", status, got)
	}
	if strings.Contains(got, "숨은 물건") {
		t.Fatalf("hidden bank object listed with PDINVI:\n%s", got)
	}
}

func TestBankInventoryHandlerListUsesDetectMagicGrouping(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-one", "proto:magic-sword-one", "청동검", nil, map[string]string{"adjustment": "1"})
	bankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-two", "proto:magic-sword-two", "청동검", nil, map[string]string{"adjustment": "2"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	got := ctx.OutputString()
	if status != StatusDefault || !strings.Contains(got, "(x2) 청동검") {
		t.Fatalf("status/output = %d/%q, want adjusted bank objects grouped without PDMAGI", status, got)
	}
	if strings.Contains(got, "청동검(+1)") || strings.Contains(got, "청동검(+2)") {
		t.Fatalf("bank list revealed magic adjustment without PDMAGI:\n%s", got)
	}

	loaded = bankTestWorld(t, true, true)
	bankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-one", "proto:magic-sword-one", "청동검", nil, map[string]string{"adjustment": "1"})
	bankTestAddStoredObjectWithProperties(t, loaded, "object:sword-plus-two", "proto:magic-sword-two", "청동검", nil, map[string]string{"adjustment": "2"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDMAGI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	defer world.Close()
	ctx = &Context{ActorID: "Alice"}

	status, err = NewBankInventoryHandler(world)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler with PDMAGI error: %v", err)
	}
	got = ctx.OutputString()
	for _, want := range []string{"청동검(+1)", "청동검(+2)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bank list with PDMAGI missing %q:\n%s", want, got)
		}
	}
	if status != StatusDefault || strings.Contains(got, "(x2) 청동검") {
		t.Fatalf("status/output = %d/%q, want adjusted bank objects split with PDMAGI", status, got)
	}
}

func TestBankInventoryHandlerStoresObjectWithArg(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("Alice", "s1", &broadcasts)

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"기념패"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 기념패를 보관시켰습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 || broadcasts[0] != (roomBroadcastRecord{RoomID: "room:bank", Exclude: "s1", Text: "\nAlice가 기념패를 보관시켰습니다."}) {
		t.Fatalf("broadcasts = %+v, want legacy bank store broadcast", broadcasts)
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") || root.Properties["shotsCurrent"] != "4" {
		t.Fatalf("bank root contents/properties = %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") {
		t.Fatalf("inventory still contains keepsake: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestBankInventoryHandlerStoresObjectWithMissingAccount(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	delete(loaded.Banks, "bank:player:Alice")
	delete(loaded.Objects, "object:bank-root")
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"기념패"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 기념패를 보관시켰습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:player:Alice")
	if !ok {
		t.Fatal("bank account was not created")
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created root %q not found", account.Objects.ObjectIDs[0])
	}
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") || root.Properties["shotsCurrent"] != "1" {
		t.Fatalf("created bank root contents/properties = %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") {
		t.Fatalf("inventory still contains keepsake: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestBankInventoryHandlerRequiresDetectInvisibleForSingleStore(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddInventoryObject(t, loaded, "object:invisible-keepsake", "proto:invisible-keepsake", "은신패", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 그런것을 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want invisible inventory object hidden", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if !strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:invisible-keepsake") {
		t.Fatalf("inventory lost invisible object after rejected store: %+v", creature.Inventory.ObjectIDs)
	}

	loaded = bankTestWorld(t, true, true)
	bankTestAddInventoryObject(t, loaded, "object:invisible-keepsake", "proto:invisible-keepsake", "은신패", []string{"OINVIS"})
	creatureLoaded := loaded.Creatures["creature:alice"]
	creatureLoaded.Metadata.Tags = append(creatureLoaded.Metadata.Tags, "PDINVI")
	loaded.Creatures[creatureLoaded.ID] = creatureLoaded
	world = state.NewWorld(loaded)
	defer world.Close()
	ctx = &Context{ActorID: "Alice"}

	status, err = NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 은신패를 보관시켰습니다.\n" {
		t.Fatalf("PDINVI status/output = %d/%q, want bank store confirmation", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-keepsake") {
		t.Fatalf("bank root missing invisible object after PDINVI store: %+v", root.Contents.ObjectIDs)
	}
	creature, _ = world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:invisible-keepsake") {
		t.Fatalf("inventory still contains invisible object after PDINVI store: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestBankInventoryHandlerQueuesSaveAfterObjectStore(t *testing.T) {
	rootDir := t.TempDir()
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	world.SetDBRoot(rootDir)
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"기념패"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	world.FlushSaveQueue()

	playerSave := waitForBankPlayerSave(t, world, rootDir, "Alice")
	if playerSave.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if strings.Contains(strings.Join(objectIDStrings(playerSave.Creature.Inventory.ObjectIDs), ","), "object:keepsake") {
		t.Fatalf("saved player inventory still has keepsake: %+v", playerSave.Creature.Inventory.ObjectIDs)
	}
	bankSave := waitForBankSave(t, world, "bank:player:Alice")
	root, ok := bankSaveObject(bankSave, "object:bank-root")
	if !ok {
		t.Fatalf("saved bank root missing from bundle: %+v", bankSave.Objects)
	}
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") {
		t.Fatalf("saved bank root missing keepsake: %+v", root.Contents.ObjectIDs)
	}
}

func TestBankInventoryHandlerStoresAllMatchingObjects(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("Alice", "s1", &broadcasts)

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 기념패를 보관시켰습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 || broadcasts[0] != (roomBroadcastRecord{RoomID: "room:bank", Exclude: "s1", Text: "\nAlice가 기념패를 보관시켰습니다."}) {
		t.Fatalf("broadcasts = %+v, want legacy bank bulk store broadcast", broadcasts)
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") ||
		strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") ||
		root.Properties["shotsCurrent"] != "4" {
		t.Fatalf("bulk store state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerStoreAllRejectsObjectIDFilterLikeLegacyFindObj(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든object:keepsake"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 보관시킬 물건을 아무것도 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want object ID filter rejected", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("object ID filter changed state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerStoresAllWithMissingAccount(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	delete(loaded.Banks, "bank:player:Alice")
	delete(loaded.Objects, "object:bank-root")
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 기념패를 보관시켰습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:player:Alice")
	if !ok {
		t.Fatal("bank account was not created")
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created root %q not found", account.Objects.ObjectIDs[0])
	}
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") || root.Properties["shotsCurrent"] != "1" {
		t.Fatalf("created bank root contents/properties = %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerStoresAllGroupsMatchingObjects(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddInventoryObject(t, loaded, "object:keepsake-two", "proto:keepsake-two", "기념패", nil)
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 (x2) 기념패를 보관시켰습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") ||
		!strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake-two") ||
		root.Properties["shotsCurrent"] != "5" {
		t.Fatalf("bulk grouped store state = root:%+v props:%+v", root.Contents.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerStoreAllQuestItemsRequireDMLikeLegacy(t *testing.T) {
	tests := []struct {
		name       string
		class      int
		wantOutput string
		wantStored bool
	}{
		{
			name:       "invincible still skips quest item",
			class:      model.ClassInvincible,
			wantOutput: "당신은 보관시킬 물건을 아무것도 갖고 있지 않습니다.",
		},
		{
			name:       "DM can bulk store quest item",
			class:      model.ClassDM,
			wantOutput: "당신은 성물을 보관시켰습니다.\n",
			wantStored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := bankTestWorld(t, true, true)
			bankTestAddInventoryObject(t, loaded, "object:relic", "proto:relic", "성물", nil)
			relic := loaded.Objects["object:relic"]
			relic.Properties = map[string]string{"questNumber": "1"}
			loaded.Objects[relic.ID] = relic
			creature := loaded.Creatures["creature:alice"]
			creature.Stats["class"] = tt.class
			loaded.Creatures[creature.ID] = creature
			world := state.NewWorld(loaded)
			defer world.Close()
			ctx := &Context{ActorID: "Alice"}

			status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든성물"}})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if status != StatusDefault || ctx.OutputString() != tt.wantOutput {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.wantOutput)
			}
			root, _ := world.Object("object:bank-root")
			stored := strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:relic")
			if stored != tt.wantStored {
				t.Fatalf("quest item stored = %v, want %v; root=%+v", stored, tt.wantStored, root.Contents.ObjectIDs)
			}
			creature, _ = world.Creature("creature:alice")
			inInventory := strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:relic")
			if inInventory == tt.wantStored {
				t.Fatalf("quest item inventory presence = %v, want opposite of stored=%v; inv=%+v", inInventory, tt.wantStored, creature.Inventory.ObjectIDs)
			}
		})
	}
}

func TestBankInventoryHandlerStoreAllRequiresDetectInvisible(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddInventoryObject(t, loaded, "object:invisible-keepsake", "proto:invisible-keepsake", "은신패", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든은신"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 보관시킬 물건을 아무것도 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want invisible bulk store hidden", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-keepsake") {
		t.Fatalf("bank root gained invisible object without PDINVI: %+v", root.Contents.ObjectIDs)
	}

	loaded = bankTestWorld(t, true, true)
	bankTestAddInventoryObject(t, loaded, "object:invisible-keepsake", "proto:invisible-keepsake", "은신패", []string{"OINVIS"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDINVI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	defer world.Close()
	ctx = &Context{ActorID: "Alice"}

	status, err = NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든은신"}})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 은신패를 보관시켰습니다.\n" {
		t.Fatalf("PDINVI status/output = %d/%q, want bulk store confirmation", status, ctx.OutputString())
	}
	root, _ = world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-keepsake") {
		t.Fatalf("bank root missing invisible object after PDINVI bulk store: %+v", root.Contents.ObjectIDs)
	}
}

func TestBankInventoryHandlerStoreAllDevouringRootConsumesMatchingObjects(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	root := loaded.Objects["object:bank-root"]
	root.Metadata.Tags = append(root.Metadata.Tags, "containerDevours")
	root.Properties["shotsCurrent"] = "200"
	root.Properties["shotsMax"] = "200"
	loaded.Objects[root.ID] = root
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든기념"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 보관시킬 물건을 아무것도 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if _, ok := world.Object("object:keepsake"); ok {
		t.Fatal("devoured keepsake still exists")
	}
	root, _ = world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") ||
		strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") ||
		root.Properties["shotsCurrent"] != "200" {
		t.Fatalf("devouring store state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerStoreAllSkipsContainers(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "더이상 물건을 보관시킬 수 없습니다.당신은 기념패를 보관시켰습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:bag") ||
		root.Properties["shotsCurrent"] != "4" {
		t.Fatalf("store all state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerStoreAllRejectsNoMatches(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"모든없는"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 보관시킬 물건을 아무것도 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankInventoryHandlerRejectsContainerStore(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"가방"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "보따리 종류는 보관할 수 없습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:bag") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:bag") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("state changed after rejected container store: root=%+v inv=%+v props=%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankInventoryHandlerRejectsFullBank(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	root := loaded.Objects["object:bank-root"]
	root.Properties["shotsCurrent"] = "3"
	root.Properties["shotsMax"] = "3"
	loaded.Objects[root.ID] = root
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{Args: []string{"기념패"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "보관함 안에 더이상 넣을 수 없습니다.\n" {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ = world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:keepsake") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:keepsake") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("state changed after full bank: root=%+v inv=%+v props=%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankDepositHandler(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"5,000냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 5냥을 입금했습니다.\n은행의 잔고가 12350냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if got := creature.Stats["gold"]; got != 19995 {
		t.Fatalf("gold = %d, want 19995", got)
	}
	root, _ := world.Object("object:bank-root")
	if got := root.Properties["value"]; got != "12350" {
		t.Fatalf("bank value = %q, want 12350", got)
	}
}

func TestBankDepositHandlerCreatesMissingAccount(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, false))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"5000냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 5000냥을 입금했습니다.\n은행의 잔고가 5000냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:player:Alice")
	if !ok {
		t.Fatal("bank account was not created")
	}
	if len(account.Objects.ObjectIDs) != 1 {
		t.Fatalf("bank root refs = %+v, want one root", account.Objects.ObjectIDs)
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created root %q not found", account.Objects.ObjectIDs[0])
	}
	if root.Location.BankID != account.ID || root.Properties["value"] != "5000" || root.Properties["shotsMax"] != "200" {
		t.Fatalf("created root = location:%+v props:%+v", root.Location, root.Properties)
	}
	creature, _ := world.Creature("creature:alice")
	if got := creature.Stats["gold"]; got != 15000 {
		t.Fatalf("gold = %d, want 15000", got)
	}
}

func TestBankDepositHandlerSavesResolvedAccountID(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	player := loaded.Players["Alice"]
	player.AccountName = "VaultAlice"
	loaded.Players[player.ID] = player
	bank := loaded.Banks["bank:player:Alice"]
	delete(loaded.Banks, "bank:player:Alice")
	bank.ID = "bank:player:VaultAlice"
	bank.OwnerName = "VaultAlice"
	loaded.Banks[bank.ID] = bank
	root := loaded.Objects["object:bank-root"]
	root.Location.BankID = bank.ID
	loaded.Objects[root.ID] = root

	rootDir := t.TempDir()
	world := state.NewWorld(loaded)
	defer world.Close()
	world.SetDBRoot(rootDir)
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"1냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	world.FlushSaveQueue()
	bankSave := waitForBankSave(t, world, "bank:player:VaultAlice")
	rootSave, ok := bankSaveObject(bankSave, "object:bank-root")
	if !ok {
		t.Fatalf("saved bank root missing from bundle: %+v", bankSave.Objects)
	}
	if got := rootSave.Properties["value"]; got != "12346" {
		t.Fatalf("saved bank value = %q, want 12346", got)
	}
}

func TestBankDepositAllAllowsZero(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	creature := loaded.Creatures["creature:alice"]
	creature.Stats["gold"] = 0
	loaded.Creatures[creature.ID] = creature
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 0냥을 입금했습니다.\n은행의 잔고가 12345냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankDepositRejectsInsufficientGold(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"30000냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 그만큼의 돈을 가지고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	root, _ := world.Object("object:bank-root")
	if creature.Stats["gold"] != 20000 || root.Properties["value"] != "12345" {
		t.Fatalf("state changed after rejected deposit: gold=%d value=%s", creature.Stats["gold"], root.Properties["value"])
	}
}

func TestBankDepositRejectsUsageAndNegativeAmountLikeLegacy(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing amount", want: "얼마를 입금하시려고요?"},
		{name: "bad suffix", args: []string{"12"}, want: "사용법 : 몇냥 입금"},
		{name: "negative", args: []string{"-1냥"}, want: "돈의 단위는 음수가 될수 없습니다."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := state.NewWorld(bankTestWorld(t, true, true))
			defer world.Close()
			ctx := &Context{ActorID: "Alice"}

			status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: tt.args})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if status != StatusDefault || ctx.OutputString() != tt.want {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.want)
			}
		})
	}
}

func TestBankDepositHandlerUsesLegacyAtolPrefix(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"12abc냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 12냥을 입금했습니다.\n은행의 잔고가 12357냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankWithdrawHandler(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankWithdrawHandler(world)(ctx, ResolvedCommand{Args: []string{"2,345냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 2냥을 출금했습니다.\n은행의 잔고가 12343냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if got := creature.Stats["gold"]; got != 20002 {
		t.Fatalf("gold = %d, want 20002", got)
	}
	root, _ := world.Object("object:bank-root")
	if got := root.Properties["value"]; got != "12343" {
		t.Fatalf("bank value = %q, want 12343", got)
	}
}

func TestBankWithdrawHandlerUsesLegacyAtolPrefix(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankWithdrawHandler(world)(ctx, ResolvedCommand{Args: []string{"12abc냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 12냥을 출금했습니다.\n은행의 잔고가 12333냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankWithdrawAllCreatesMissingAccount(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, false))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankWithdrawHandler(world)(ctx, ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 0냥을 출금했습니다.\n은행의 잔고가 0냥이 되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	account, ok := world.Bank("bank:player:Alice")
	if !ok {
		t.Fatal("bank account was not created")
	}
	if len(account.Objects.ObjectIDs) != 1 {
		t.Fatalf("bank root refs = %+v, want one root", account.Objects.ObjectIDs)
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created root %q not found", account.Objects.ObjectIDs[0])
	}
	if root.Location.BankID != account.ID || root.Properties["value"] != "0" || root.Properties["shotsMax"] != "200" {
		t.Fatalf("created root = location:%+v props:%+v", root.Location, root.Properties)
	}
}

func TestBankWithdrawRejectsInsufficientBalance(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankWithdrawHandler(world)(ctx, ResolvedCommand{Args: []string{"30000냥"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 그만큼의 돈을 저금해두지 않았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	root, _ := world.Object("object:bank-root")
	if creature.Stats["gold"] != 20000 || root.Properties["value"] != "12345" {
		t.Fatalf("state changed after rejected withdraw: gold=%d value=%s", creature.Stats["gold"], root.Properties["value"])
	}
}

func TestBankWithdrawRejectsUsageAndNegativeAmountLikeLegacy(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing amount", want: "얼마를 출금하시려고요?"},
		{name: "bad suffix", args: []string{"12"}, want: "사용법 : 몇냥 출금"},
		{name: "negative", args: []string{"-1냥"}, want: "돈의 단위는 음수가 될수 없습니다."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := state.NewWorld(bankTestWorld(t, true, true))
			defer world.Close()
			ctx := &Context{ActorID: "Alice"}

			status, err := NewBankWithdrawHandler(world)(ctx, ResolvedCommand{Args: tt.args})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if status != StatusDefault || ctx.OutputString() != tt.want {
				t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), tt.want)
			}
		})
	}
}

func TestBankOutputHandler(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("Alice", "s1", &broadcasts)

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"청동검"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 청동검을 받았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 || broadcasts[0] != (roomBroadcastRecord{RoomID: "room:bank", Exclude: "s1", Text: "\nAlice이 청동검을 받았습니다."}) {
		t.Fatalf("broadcasts = %+v, want legacy bank output broadcast", broadcasts)
	}
	root, _ := world.Object("object:bank-root")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") {
		t.Fatalf("bank root still contains sword: %+v", root.Contents.ObjectIDs)
	}
	if root.Properties["shotsCurrent"] != "2" {
		t.Fatalf("bank root shotsCurrent = %q, want 2", root.Properties["shotsCurrent"])
	}
	creature, _ := world.Creature("creature:alice")
	if !strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:sword") {
		t.Fatalf("inventory missing sword: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestBankOutputHandlerRequiresDetectInvisibleForSingleObject(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "그 안에 그런것은 없어요." {
		t.Fatalf("status/output = %d/%q, want invisible bank object hidden", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-sword") {
		t.Fatalf("bank root lost invisible object after rejected output: %+v", root.Contents.ObjectIDs)
	}

	loaded = bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDINVI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	defer world.Close()
	ctx = &Context{ActorID: "Alice"}

	status, err = NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"은신"}})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 은신검을 받았습니다." {
		t.Fatalf("PDINVI status/output = %d/%q, want bank output confirmation", status, ctx.OutputString())
	}
	root, _ = world.Object("object:bank-root")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-sword") {
		t.Fatalf("bank root still contains invisible object: %+v", root.Contents.ObjectIDs)
	}
	creature, _ = world.Creature("creature:alice")
	if !strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:invisible-sword") {
		t.Fatalf("inventory missing invisible object after PDINVI output: %+v", creature.Inventory.ObjectIDs)
	}
}

func TestBankOutputHandlerSingleConvertsRoomPermanentObject(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObjectWithProperties(t, loaded, "object:permanent-sword", "proto:permanent-sword", "영구검", []string{"OPERMT"}, map[string]string{"OPERMT": "1"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"영구"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 영구검을 받았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	object, ok := world.Object("object:permanent-sword")
	if !ok {
		t.Fatal("permanent object missing after bank output")
	}
	if hasAnyNormalizedFlag(object.Metadata.Tags, "OPERMT", "permanent") || object.Properties["OPERMT"] != "" {
		t.Fatalf("object tags/properties = %+v/%+v, want OPERMT cleared", object.Metadata.Tags, object.Properties)
	}
	if !hasAnyNormalizedFlag(object.Metadata.Tags, "OPERM2") || object.Properties["OPERM2"] != "1" {
		t.Fatalf("object tags/properties = %+v/%+v, want OPERM2 set", object.Metadata.Tags, object.Properties)
	}
}

func TestBankOutputHandlerRejectsWhenInventoryTooFull(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddInventoryFillers(t, loaded, 150)
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"청동검"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 더이상 가질 수 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("bank root changed after rejected output: %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
}

func TestBankOutputHandlerCountsInventoryContainerContents(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bag := loaded.Objects["object:bag"]
	bag.Properties = map[string]string{"shotsCurrent": "151"}
	loaded.Objects[bag.ID] = bag
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"청동검"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 더이상 가질 수 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("bank root changed after rejected output: %+v/%+v", root.Contents.ObjectIDs, root.Properties)
	}
}

func TestBankOutputHandlerTakeAllSkipsTooHeavyObjects(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	sword := loaded.Objects["object:sword"]
	sword.Properties = map[string]string{"weight": "25"}
	loaded.Objects[sword.ID] = sword
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	want := "가지고 있는 물건이 너무 무거워 들 수가 없습니다.\n당신은 치료약을 받았습니다."
	if status != StatusDefault || ctx.OutputString() != want {
		t.Fatalf("status/output = %d/%q, want %q", status, ctx.OutputString(), want)
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") ||
		strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:potion") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:potion") ||
		root.Properties["shotsCurrent"] != "2" {
		t.Fatalf("take all state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankOutputHandlerTakeAllMoneyCreditsGold(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "proto:money",
		Kind:        model.ObjectKindMoney,
		DisplayName: "돈",
	})
	root := loaded.Objects["object:bank-root"]
	root.Contents.ObjectIDs = []model.ObjectInstanceID{"object:bank-money"}
	root.Properties["shotsCurrent"] = "1"
	loaded.Objects[root.ID] = root
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:                  "object:bank-money",
		PrototypeID:         "proto:money",
		DisplayNameOverride: "777냥",
		Location:            model.ObjectLocation{ContainerID: "object:bank-root"},
		Properties:          map[string]string{"value": "777", "type": "10"},
	})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "\n당신은 이제 20777냥을 가지고 있습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if creature.Stats["gold"] != 20777 ||
		strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:bank-money") {
		t.Fatalf("creature after money output = gold:%d inv:%+v", creature.Stats["gold"], creature.Inventory.ObjectIDs)
	}
	if _, ok := world.Object("object:bank-money"); ok {
		t.Fatal("money object survived after bank take all")
	}
	rootAfter, _ := world.Object("object:bank-root")
	if rootAfter.Properties["shotsCurrent"] != "0" || len(rootAfter.Contents.ObjectIDs) != 0 {
		t.Fatalf("root after money output = contents:%+v props:%+v", rootAfter.Contents.ObjectIDs, rootAfter.Properties)
	}
}

func TestBankOutputHandlerTakeAllRequiresDetectInvisible(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든은신"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "보관품이 아무것도 없습니다." {
		t.Fatalf("status/output = %d/%q, want invisible bulk output hidden", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-sword") {
		t.Fatalf("bank root lost invisible object after rejected bulk output: %+v", root.Contents.ObjectIDs)
	}

	loaded = bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:invisible-sword", "proto:invisible-sword", "은신검", []string{"OINVIS"})
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = append(creature.Metadata.Tags, "PDINVI")
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	defer world.Close()
	ctx = &Context{ActorID: "Alice"}

	status, err = NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든은신"}})
	if err != nil {
		t.Fatalf("handler with PDINVI error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 은신검을 받았습니다." {
		t.Fatalf("PDINVI status/output = %d/%q, want bulk output confirmation", status, ctx.OutputString())
	}
	root, _ = world.Object("object:bank-root")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:invisible-sword") {
		t.Fatalf("bank root still contains invisible object after PDINVI bulk output: %+v", root.Contents.ObjectIDs)
	}
}

func TestBankOutputHandlerTakeAllClearsTemporaryPermanentObject(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObjectWithProperties(t, loaded, "object:temporary-sword", "proto:temporary-sword", "임시검", []string{"OTEMPP", "OPERM2"}, map[string]string{"OTEMPP": "1", "OPERM2": "1"})
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든임시"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 임시검을 받았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	object, ok := world.Object("object:temporary-sword")
	if !ok {
		t.Fatal("temporary object missing after bank bulk output")
	}
	if hasAnyNormalizedFlag(object.Metadata.Tags, "OTEMPP", "OPERM2") ||
		object.Properties["OTEMPP"] != "" || object.Properties["OPERM2"] != "" {
		t.Fatalf("object tags/properties = %+v/%+v, want temporary permanent flags cleared", object.Metadata.Tags, object.Properties)
	}
}

func TestBankOutputHandlerQueuesSaveAfterObjectTake(t *testing.T) {
	rootDir := t.TempDir()
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	world.SetDBRoot(rootDir)
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"청동검"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want StatusDefault", status)
	}
	world.FlushSaveQueue()

	playerSave := waitForBankPlayerSave(t, world, rootDir, "Alice")
	if playerSave.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if !strings.Contains(strings.Join(objectIDStrings(playerSave.Creature.Inventory.ObjectIDs), ","), "object:sword") {
		t.Fatalf("saved player inventory missing sword: %+v", playerSave.Creature.Inventory.ObjectIDs)
	}
	bankSave := waitForBankSave(t, world, "bank:player:Alice")
	root, ok := bankSaveObject(bankSave, "object:bank-root")
	if !ok {
		t.Fatalf("saved bank root missing from bundle: %+v", bankSave.Objects)
	}
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") {
		t.Fatalf("saved bank root still has sword: %+v", root.Contents.ObjectIDs)
	}
}

func TestBankOutputHandlerTakesAllMatchingObjects(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든치"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 치료약을 받았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:potion") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:potion") ||
		root.Properties["shotsCurrent"] != "2" {
		t.Fatalf("bulk take state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankOutputHandlerTakeAllRejectsObjectIDFilterLikeLegacyFindObj(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든object:potion"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "보관품이 아무것도 없습니다." {
		t.Fatalf("status/output = %d/%q, want object ID filter rejected", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if !strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:potion") ||
		strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:potion") ||
		root.Properties["shotsCurrent"] != "3" {
		t.Fatalf("object ID filter changed state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankOutputHandlerTakesAllGroupsMatchingObjects(t *testing.T) {
	loaded := bankTestWorld(t, true, true)
	bankTestAddStoredObject(t, loaded, "object:sword-two", "proto:sword-two", "청동검", nil)
	world := state.NewWorld(loaded)
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든청동"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 (x2) 청동검을 받았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") ||
		strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword-two") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:sword") ||
		!strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:sword-two") {
		t.Fatalf("bulk grouped take state = root:%+v inv:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs)
	}
}

func TestBankOutputHandlerTakesAllVisibleObjects(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("Alice", "s1", &broadcasts)

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모두"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 청동검, 치료약을 받았습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 || broadcasts[0] != (roomBroadcastRecord{RoomID: "room:bank", Exclude: "s1", Text: "\nAlice이 청동검, 치료약을 받았습니다."}) {
		t.Fatalf("broadcasts = %+v, want legacy bank bulk output broadcast", broadcasts)
	}
	root, _ := world.Object("object:bank-root")
	creature, _ := world.Creature("creature:alice")
	if strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:sword") ||
		strings.Contains(strings.Join(objectIDStrings(root.Contents.ObjectIDs), ","), "object:potion") ||
		strings.Contains(strings.Join(objectIDStrings(creature.Inventory.ObjectIDs), ","), "object:hidden") ||
		root.Properties["shotsCurrent"] != "1" {
		t.Fatalf("take all state = root:%+v inv:%+v props:%+v", root.Contents.ObjectIDs, creature.Inventory.ObjectIDs, root.Properties)
	}
}

func TestBankOutputHandlerTakeAllRejectsNoMatches(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"모든없는"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "보관품이 아무것도 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankOutputHandlerRejectsMissingObject(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, true))
	defer world.Close()
	ctx := &Context{ActorID: "Alice"}

	status, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"없는물건"}})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "그 안에 그런것은 없어요." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
}

func TestBankHandlersRequireBankRoom(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, false, true))
	defer world.Close()

	ctx := &Context{ActorID: "Alice"}
	if _, err := NewBankBalanceHandler(world)(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("balance handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "은행에서만 가능합니다." {
		t.Fatalf("balance output = %q", got)
	}

	ctx = &Context{ActorID: "Alice"}
	if _, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("inventory handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "은행에서만 가능합니다." {
		t.Fatalf("inventory output = %q", got)
	}

	ctx = &Context{ActorID: "Alice"}
	if _, err := NewBankDepositHandler(world)(ctx, ResolvedCommand{Args: []string{"1냥"}}); err != nil {
		t.Fatalf("deposit handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "은행에서만 가능합니다." {
		t.Fatalf("deposit output = %q", got)
	}

	ctx = &Context{ActorID: "Alice"}
	if _, err := NewBankWithdrawHandler(world)(ctx, ResolvedCommand{Args: []string{"1냥"}}); err != nil {
		t.Fatalf("withdraw handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "은행에서만 가능합니다." {
		t.Fatalf("withdraw output = %q", got)
	}

	ctx = &Context{ActorID: "Alice"}
	if _, err := NewBankOutputHandler(world)(ctx, ResolvedCommand{Args: []string{"청동검"}}); err != nil {
		t.Fatalf("output handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "은행에서만 가능합니다." {
		t.Fatalf("output output = %q", got)
	}
}

func TestBankHandlersHandleMissingAccount(t *testing.T) {
	world := state.NewWorld(bankTestWorld(t, true, false))
	defer world.Close()

	ctx := &Context{ActorID: "Alice"}
	if _, err := NewBankBalanceHandler(world)(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("balance handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "당신의 잔고는 0냥입니다." {
		t.Fatalf("balance output = %q", got)
	}

	ctx = &Context{ActorID: "Alice"}
	if _, err := NewBankInventoryHandler(world)(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("inventory handler error: %v", err)
	}
	if got := ctx.OutputString(); got != "보관하고 있는 물건이 없습니다." {
		t.Fatalf("inventory output = %q", got)
	}
	account, ok := world.Bank("bank:player:Alice")
	if !ok {
		t.Fatal("inventory listing did not create missing bank account")
	}
	if len(account.Objects.ObjectIDs) != 1 {
		t.Fatalf("created bank root refs = %+v, want one root", account.Objects.ObjectIDs)
	}
	root, ok := world.Object(account.Objects.ObjectIDs[0])
	if !ok {
		t.Fatalf("created bank root %q not found", account.Objects.ObjectIDs[0])
	}
	if root.Properties["value"] != "0" || root.Properties["shotsMax"] != "200" {
		t.Fatalf("created bank root props = %+v", root.Properties)
	}
}

func bankTestWorld(t *testing.T, bankRoom bool, account bool) *worldload.World {
	t.Helper()
	loaded := worldload.NewWorld()
	room := model.Room{
		ID:          "room:bank",
		DisplayName: "은행",
	}
	if bankRoom {
		room.Metadata = model.Metadata{Tags: []string{"bank"}}
	}
	mustAddLookRoom(t, loaded, room)
	mustAddLookPlayer(t, loaded, model.Player{
		ID:          "Alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:bank",
	})
	mustAddLookCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "Alice",
		RoomID:      "room:bank",
		Stats:       map[string]int{"gold": 20000},
		Inventory: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{
			"object:keepsake",
			"object:bag",
		}},
	})
	if !account {
		return loaded
	}

	for _, proto := range []model.ObjectPrototype{
		{ID: "proto:bank-root", DisplayName: "보관함"},
		{ID: "proto:sword", DisplayName: "청동검"},
		{ID: "proto:hidden", DisplayName: "숨은 물건"},
		{ID: "proto:potion", DisplayName: "치료약"},
		{ID: "proto:keepsake", DisplayName: "기념패"},
		{ID: "proto:bag", Kind: model.ObjectKindContainer, DisplayName: "가방"},
	} {
		mustAddLookPrototype(t, loaded, proto)
	}
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:bank-root",
		PrototypeID: "proto:bank-root",
		Location:    model.ObjectLocation{BankID: "bank:player:Alice", Slot: "bank"},
		Properties: map[string]string{
			"value":        "12345",
			"shotsCurrent": "3",
			"shotsMax":     "200",
		},
		Contents: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{
			"object:sword",
			"object:hidden",
			"object:potion",
		}},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:                  "object:sword",
		PrototypeID:         "proto:sword",
		DisplayNameOverride: "청동검",
		Location:            model.ObjectLocation{ContainerID: "object:bank-root"},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:                  "object:hidden",
		PrototypeID:         "proto:hidden",
		DisplayNameOverride: "숨은 물건",
		Location:            model.ObjectLocation{ContainerID: "object:bank-root"},
		Metadata:            model.Metadata{Tags: []string{"hidden"}},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:                  "object:potion",
		PrototypeID:         "proto:potion",
		DisplayNameOverride: "치료약",
		Location:            model.ObjectLocation{ContainerID: "object:bank-root"},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:                  "object:keepsake",
		PrototypeID:         "proto:keepsake",
		DisplayNameOverride: "기념패",
		Location:            model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:bag",
		PrototypeID: "proto:bag",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
	})
	if err := loaded.AddBank(model.BankAccount{
		ID:            "bank:player:Alice",
		Kind:          "player",
		OwnerName:     "Alice",
		OwnerPlayerID: "Alice",
		Objects:       model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:bank-root"}},
	}); err != nil {
		t.Fatal(err)
	}
	return loaded
}

func objectIDStrings(ids []model.ObjectInstanceID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

func bankTestAddInventoryFillers(t *testing.T, loaded *worldload.World, count int) {
	t.Helper()
	protoID := model.PrototypeID("proto:filler")
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{ID: protoID, DisplayName: "더미"})
	creature := loaded.Creatures["creature:alice"]
	for i := 0; i < count; i++ {
		id := model.ObjectInstanceID(fmt.Sprintf("object:filler:%03d", i))
		mustAddLookObject(t, loaded, model.ObjectInstance{
			ID:          id,
			PrototypeID: protoID,
			Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
		})
		creature.Inventory.ObjectIDs = append(creature.Inventory.ObjectIDs, id)
	}
	loaded.Creatures[creature.ID] = creature
}

func bankTestAddStoredObject(t *testing.T, loaded *worldload.World, objectID model.ObjectInstanceID, protoID model.PrototypeID, name string, tags []string) {
	t.Helper()
	bankTestAddStoredObjectWithProperties(t, loaded, objectID, protoID, name, tags, nil)
}

func bankTestAddStoredObjectWithProperties(t *testing.T, loaded *worldload.World, objectID model.ObjectInstanceID, protoID model.PrototypeID, name string, tags []string, properties map[string]string) {
	t.Helper()
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{ID: protoID, DisplayName: name})
	objectProperties := map[string]string{}
	for key, value := range properties {
		objectProperties[key] = value
	}
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:                  objectID,
		PrototypeID:         protoID,
		DisplayNameOverride: name,
		Location:            model.ObjectLocation{ContainerID: "object:bank-root"},
		Metadata:            model.Metadata{Tags: tags},
		Properties:          objectProperties,
	})
	root := loaded.Objects["object:bank-root"]
	root.Contents.ObjectIDs = append(root.Contents.ObjectIDs, objectID)
	if root.Properties == nil {
		root.Properties = map[string]string{}
	}
	root.Properties["shotsCurrent"] = fmt.Sprintf("%d", len(root.Contents.ObjectIDs))
	loaded.Objects[root.ID] = root
}

func bankTestAddInventoryObject(t *testing.T, loaded *worldload.World, objectID model.ObjectInstanceID, protoID model.PrototypeID, name string, tags []string) {
	t.Helper()
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{ID: protoID, DisplayName: name})
	mustAddLookObject(t, loaded, model.ObjectInstance{
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

func waitForBankPlayerSave(t *testing.T, world *state.World, root string, playerID model.PlayerID) state.PlayerSaveData {
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

func waitForBankSave(t *testing.T, world *state.World, bankID model.BankID) model.BankSaveBundle {
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

func bankSaveObject(bundle model.BankSaveBundle, id model.ObjectInstanceID) (model.ObjectInstance, bool) {
	for _, object := range bundle.Objects {
		if object.ID == id {
			return object, true
		}
	}
	return model.ObjectInstance{}, false
}
