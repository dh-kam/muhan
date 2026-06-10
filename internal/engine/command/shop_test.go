package command

import (
	"fmt"
	"strings"
	"testing"

	"muhan/internal/commandspec"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestShopListHandlerRendersNextRoomStock(t *testing.T) {
	world := state.NewWorld(shopWorld(t, true))
	defer world.Close()
	dispatcher := Dispatcher{
		Registry: mustRegistry(t, []commandspec.CommandSpec{
			{Name: "품목", Number: 41, Handler: "list"},
		}),
		Handlers: map[string]Handler{
			"list": NewShopListHandler(world),
		},
	}

	for _, line := range []string{"품목", "묘고부 품목"} {
		t.Run(line, func(t *testing.T) {
			var broadcasts []roomBroadcastRecord
			ctx := contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)
			ctx.Values[ContextShopSellBonusKey] = false
			status, err := dispatcher.DispatchLine(ctx, line)
			if err != nil {
				t.Fatalf("DispatchLine() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			out := ctx.OutputString()
			for _, want := range []string{"상품들:", "목검", "가격: 50000", "기념패", "가격: 10000"} {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "##") || strings.Contains(out, "|") || strings.HasSuffix(out, "\n") {
				t.Fatalf("output should use C plain shop list without trailing newline:\n%q", out)
			}
		})
	}
}

func TestShopListHandlerIgnoresANSIForLegacyPlainText(t *testing.T) {
	world := state.NewWorld(shopWorld(t, true))
	defer world.Close()
	handler := NewShopListHandler(world)
	ctx := &Context{
		ActorID: "player:alice",
		Values:  map[string]any{ContextANSIKey: true},
	}

	status, err := handler(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	out := ctx.OutputString()
	for _, want := range []string{"상품들:", "목검", "가격: 50000", "기념패", "가격: 10000"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") || strings.Contains(out, "##") || strings.Contains(out, "|") || strings.HasSuffix(out, "\n") {
		t.Fatalf("ANSI shop list should remain C plain text:\n%q", out)
	}
}

func TestShopBuyHandlerBuysStockIntoInventory(t *testing.T) {
	world := state.NewWorld(shopBuyWorld(t, true, 60000))
	defer world.Close()
	dispatcher := Dispatcher{
		Registry: mustRegistry(t, []commandspec.CommandSpec{
			{Name: "사", Number: 42, Handler: "buy"},
		}),
		Handlers: map[string]Handler{
			"buy": NewShopBuyHandler(world),
		},
	}

	for _, line := range []string{"목검 사", "사 기념패"} {
		t.Run(line, func(t *testing.T) {
			world := state.NewWorld(shopBuyWorld(t, true, 60000))
			defer world.Close()
			dispatcher.Handlers["buy"] = NewShopBuyHandler(world)
			ctx := &Context{ActorID: "player:alice"}
			status, err := dispatcher.DispatchLine(ctx, line)
			if err != nil {
				t.Fatalf("DispatchLine() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			out := ctx.OutputString()
			if !strings.Contains(out, "당신은 ") || !strings.Contains(out, " 샀습니다") {
				t.Fatalf("unexpected output:\n%s", out)
			}

			player, ok := world.Player("player:alice")
			if !ok {
				t.Fatal("missing player")
			}
			creature, ok := world.Creature(player.CreatureID)
			if !ok {
				t.Fatal("missing creature")
			}
			if len(creature.Inventory.ObjectIDs) != 1 {
				t.Fatalf("inventory ids = %+v, want one purchased object", creature.Inventory.ObjectIDs)
			}
			purchased, ok := world.Object(creature.Inventory.ObjectIDs[0])
			if !ok {
				t.Fatal("missing purchased object")
			}
			if purchased.ID == "object:sword" || purchased.ID == "object:plaque" {
				t.Fatalf("purchased object should be a clone, got stock id %q", purchased.ID)
			}
			if purchased.Location.CreatureID != "creature:alice" || purchased.Location.Slot != "inventory" {
				t.Fatalf("purchased location = %+v, want inventory", purchased.Location)
			}

			stockRoom, ok := world.Room("room:01072")
			if !ok {
				t.Fatal("missing stock room")
			}
			for _, stockID := range []model.ObjectInstanceID{"object:sword", "object:plaque"} {
				if !containsObjectID(stockRoom.Objects.ObjectIDs, stockID) {
					t.Fatalf("stock room lost %q: %+v", stockID, stockRoom.Objects.ObjectIDs)
				}
			}
		})
	}
}

func TestShopBuyHandlerDebitsGold(t *testing.T) {
	world := state.NewWorld(shopBuyWorld(t, true, 60000))
	defer world.Close()
	handler := NewShopBuyHandler(world)
	ctx := shopSellTestContext()

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := creature.Stats["gold"], 10000; got != want {
		t.Fatalf("gold = %d, want %d", got, want)
	}
	if got, want := ctx.OutputString(), "당신은 목검을 샀습니다"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestShopBuyHandlerClearsHiddenAndBroadcastsLikeLegacy(t *testing.T) {
	world := state.NewWorld(shopBuyWorld(t, true, 60000))
	defer world.Close()
	if _, err := world.UpdateCreatureTags("creature:alice", []string{"hidden", "PHIDDN"}, nil); err != nil {
		t.Fatalf("UpdateCreatureTags() error = %v", err)
	}
	if _, err := world.UpdatePlayerTags("player:alice", []string{"hidden", "PHIDDN"}, nil); err != nil {
		t.Fatalf("UpdatePlayerTags() error = %v", err)
	}
	if err := world.SetCreatureStat("creature:alice", "PHIDDN", 1); err != nil {
		t.Fatalf("SetCreatureStat() error = %v", err)
	}

	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)
	status, err := NewShopBuyHandler(world)(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 목검을 샀습니다" {
		t.Fatalf("status/output = %d/%q, want buy confirmation", status, ctx.OutputString())
	}
	if len(broadcasts) != 1 ||
		broadcasts[0].RoomID != "room:01071" ||
		broadcasts[0].Text != "\nAlice이 목검을 샀습니다." {
		t.Fatalf("broadcasts = %+v, want C buy room broadcast", broadcasts)
	}
	creature, _ := world.Creature("creature:alice")
	if hasAnyNormalizedFlag(creature.Metadata.Tags, "hidden", "phiddn") || creature.Stats["PHIDDN"] != 0 {
		t.Fatalf("creature hidden state = tags:%+v stats:%+v", creature.Metadata.Tags, creature.Stats)
	}
	player, _ := world.Player("player:alice")
	if hasAnyNormalizedFlag(player.Metadata.Tags, "hidden", "phiddn") {
		t.Fatalf("player hidden tags = %+v", player.Metadata.Tags)
	}
}

func TestShopBuyHandlerQueuesPlayerSave(t *testing.T) {
	root := t.TempDir()
	world := state.NewWorld(shopBuyWorld(t, true, 60000))
	defer world.Close()
	world.SetDBRoot(root)
	handler := NewShopBuyHandler(world)
	ctx := shopSellTestContext()

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	world.FlushSaveQueue()

	save := waitForShopPlayerSave(t, world, root, "player:alice")
	if save.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if got, want := save.Creature.Stats["gold"], 10000; got != want {
		t.Fatalf("saved gold = %d, want %d", got, want)
	}
	if len(save.Creature.Inventory.ObjectIDs) != 1 {
		t.Fatalf("saved inventory = %+v, want one purchased object", save.Creature.Inventory.ObjectIDs)
	}
	if len(save.Objects) != 1 {
		t.Fatalf("saved objects = %+v, want purchased object clone", save.Objects)
	}
}

func TestShopHandlersUseOnlyFirstArgumentLikeLegacy(t *testing.T) {
	t.Run("buy", func(t *testing.T) {
		world := state.NewWorld(shopBuyWorld(t, true, 60000))
		defer world.Close()
		handler := NewShopBuyHandler(world)
		ctx := shopSellTestContext()

		status, err := handler(ctx, ResolvedCommand{Args: []string{"목검", "무시"}, Values: []int64{1, 1}})
		if err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if status != StatusDefault || !strings.Contains(ctx.OutputString(), "당신은 목검을") {
			t.Fatalf("status/output = %d/%q, want first-argument buy success", status, ctx.OutputString())
		}
		_, creature, err := CurrentInventoryCreature(world, "player:alice")
		if err != nil {
			t.Fatal(err)
		}
		if len(creature.Inventory.ObjectIDs) != 1 {
			t.Fatalf("inventory ids = %+v, want one purchased object", creature.Inventory.ObjectIDs)
		}
	})

	t.Run("sell", func(t *testing.T) {
		world := state.NewWorld(shopSellWorld(t, "pawnShop", "50000", 1000))
		defer world.Close()
		handler := NewShopSellHandler(world)
		ctx := &Context{ActorID: "player:alice", Values: map[string]any{ContextShopSellBonusKey: false}}

		status, err := handler(ctx, ResolvedCommand{Args: []string{"목검", "무시"}, Values: []int64{1, 1}})
		if err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if status != StatusDefault || ctx.OutputString() != "제가 목검을 사죠.\n전당포주인이 당신에게 25000냥을 줍니다." {
			t.Fatalf("status/output = %d/%q, want first-argument sale success", status, ctx.OutputString())
		}
		if _, ok := world.Object("object:sword"); ok {
			t.Fatal("sold object still exists in world")
		}
	})

	t.Run("value", func(t *testing.T) {
		world := state.NewWorld(shopValueWorld(t, "pawnShop", "50000"))
		defer world.Close()
		handler := NewShopValueHandler(world)
		ctx := &Context{ActorID: "player:alice"}

		status, err := handler(ctx, ResolvedCommand{Args: []string{"목검", "무시"}, Values: []int64{1, 1}})
		if err != nil {
			t.Fatalf("handler() error = %v", err)
		}
		if status != StatusDefault || !strings.Contains(ctx.OutputString(), "25000냥") {
			t.Fatalf("status/output = %d/%q, want first-argument value success", status, ctx.OutputString())
		}
	})
}

func TestShopBuyHandlerRejectsInvalidPurchases(t *testing.T) {
	tests := []struct {
		name  string
		world *worldload.World
		cmd   ResolvedCommand
		want  string
	}{
		{
			name:  "non shop",
			world: shopBuyWorld(t, false, 60000),
			cmd:   ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}},
			want:  "여기는 상점이 아닙니다.",
		},
		{
			name:  "missing target",
			world: shopBuyWorld(t, true, 60000),
			cmd:   ResolvedCommand{},
			want:  "무엇을 사시려구요?",
		},
		{
			name:  "unknown stock",
			world: shopBuyWorld(t, true, 60000),
			cmd:   ResolvedCommand{Args: []string{"없는물건"}, Values: []int64{1}},
			want:  "그런 물건은 팔지 않습니다.",
		},
		{
			name:  "insufficient gold",
			world: shopBuyWorld(t, true, 49999),
			cmd:   ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}},
			want:  "돈도 없으면서... 외상사절!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := state.NewWorld(tt.world)
			defer world.Close()
			handler := NewShopBuyHandler(world)
			ctx := shopSellTestContext()
			status, err := handler(ctx, tt.cmd)
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			if got := ctx.OutputString(); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
			_, creature, err := CurrentInventoryCreature(world, "player:alice")
			if err != nil {
				t.Fatal(err)
			}
			if len(creature.Inventory.ObjectIDs) != 0 {
				t.Fatalf("inventory mutated after rejected purchase: %+v", creature.Inventory.ObjectIDs)
			}
		})
	}
}

func TestShopBuyHandlerRejectsTooHeavyPurchase(t *testing.T) {
	loaded := shopBuyWorld(t, true, 60000)
	proto := loaded.ObjectPrototypes["prototype:sword"]
	proto.Properties = map[string]string{"value": "50000", "weight": "25"}
	loaded.ObjectPrototypes[proto.ID] = proto
	world := state.NewWorld(loaded)
	defer world.Close()
	handler := NewShopBuyHandler(world)
	ctx := shopSellTestContext()

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 더이상 가질 수 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if creature.Stats["gold"] != 60000 || len(creature.Inventory.ObjectIDs) != 0 {
		t.Fatalf("state mutated after heavy purchase: gold=%d inventory=%+v", creature.Stats["gold"], creature.Inventory.ObjectIDs)
	}
}

func TestShopBuyHandlerRejectsTooManyHeldItems(t *testing.T) {
	loaded := shopBuyWorld(t, true, 60000)
	shopTestFillInventoryAndEquip(t, loaded, 200)
	world := state.NewWorld(loaded)
	defer world.Close()
	handler := NewShopBuyHandler(world)
	ctx := shopSellTestContext()

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 더이상 가질 수 없습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if creature.Stats["gold"] != 60000 || len(creature.Inventory.ObjectIDs) != 200 {
		t.Fatalf("state mutated after full purchase: gold=%d inventory=%d", creature.Stats["gold"], len(creature.Inventory.ObjectIDs))
	}
}

func TestShopSellHandlerSellsInventoryAtPawnShop(t *testing.T) {
	for _, line := range []string{"목검 팔아", "팔아 목검"} {
		t.Run(line, func(t *testing.T) {
			world := state.NewWorld(shopSellWorld(t, "pawnShop", "50000", 1000))
			defer world.Close()
			dispatcher := Dispatcher{
				Registry: mustRegistry(t, []commandspec.CommandSpec{
					{Name: "팔아", Number: 43, Handler: "sell"},
				}),
				Handlers: map[string]Handler{
					"sell": NewShopSellHandler(world),
				},
			}

			var broadcasts []roomBroadcastRecord
			ctx := contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)
			ctx.Values[ContextShopSellBonusKey] = false
			status, err := dispatcher.DispatchLine(ctx, line)
			if err != nil {
				t.Fatalf("DispatchLine() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			if got, want := ctx.OutputString(), "제가 목검을 사죠.\n전당포주인이 당신에게 25000냥을 줍니다."; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
			if len(broadcasts) != 1 ||
				broadcasts[0].RoomID != "room:pawn" ||
				broadcasts[0].Text != "\nAlice이 전당포주인에게 목검을 팝니다." {
				t.Fatalf("broadcasts = %+v, want C sell room broadcast", broadcasts)
			}

			_, creature, err := CurrentInventoryCreature(world, "player:alice")
			if err != nil {
				t.Fatal(err)
			}
			if got, want := creature.Stats["gold"], 26000; got != want {
				t.Fatalf("gold = %d, want %d", got, want)
			}
			if len(creature.Inventory.ObjectIDs) != 0 {
				t.Fatalf("inventory ids = %+v, want empty after sale", creature.Inventory.ObjectIDs)
			}
			if _, ok := world.Object("object:sword"); ok {
				t.Fatal("sold object still exists in world")
			}
		})
	}
}

func TestShopSellHandlerCapsPawnShopSalePrice(t *testing.T) {
	world := state.NewWorld(shopSellWorld(t, "pawnShop", "500000", 0))
	defer world.Close()
	handler := NewShopSellHandler(world)
	ctx := shopSellTestContext()

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got, want := ctx.OutputString(), "제가 목검을 사죠.\n전당포주인이 당신에게 100000냥을 줍니다."; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := creature.Stats["gold"], 100000; got != want {
		t.Fatalf("gold = %d, want %d", got, want)
	}
}

func TestShopSellHandlerAppliesLegacyBonusPayout(t *testing.T) {
	world := state.NewWorld(shopSellWorld(t, "pawnShop", "50000", 0))
	defer world.Close()
	handler := NewShopSellHandler(world)
	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)
	ctx.Values[ContextShopSellBonusKey] = true

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got, want := ctx.OutputString(), "제가 목검을 사죠.\n 오늘은 기분이 좋으니 두배로 드리죠.\n 전당포주인이 당신에게 50000냥을 줍니다."; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if len(broadcasts) != 1 ||
		broadcasts[0].RoomID != "room:pawn" ||
		broadcasts[0].Text != "\nAlice이 전당포주인에게  목검을 팝니다." {
		t.Fatalf("broadcasts = %+v, want C bonus sell room broadcast with double spacing", broadcasts)
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := creature.Stats["gold"], 50000; got != want {
		t.Fatalf("gold = %d, want %d", got, want)
	}
}

func TestShopSellHandlerQueuesPlayerSave(t *testing.T) {
	root := t.TempDir()
	world := state.NewWorld(shopSellWorld(t, "pawnShop", "50000", 1000))
	defer world.Close()
	world.SetDBRoot(root)
	handler := NewShopSellHandler(world)
	ctx := shopSellTestContext()

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	world.FlushSaveQueue()

	save := waitForShopPlayerSave(t, world, root, "player:alice")
	if save.Creature == nil {
		t.Fatal("saved creature is nil")
	}
	if got, want := save.Creature.Stats["gold"], 26000; got != want {
		t.Fatalf("saved gold = %d, want %d", got, want)
	}
	if len(save.Creature.Inventory.ObjectIDs) != 0 {
		t.Fatalf("saved inventory = %+v, want empty after sale", save.Creature.Inventory.ObjectIDs)
	}
	if len(save.Objects) != 0 {
		t.Fatalf("saved objects = %+v, want sold object omitted", save.Objects)
	}
}

func TestShopSellHandlerRejectsObjectIDTargetLikeLegacyFindObj(t *testing.T) {
	world := state.NewWorld(shopSellWorld(t, "pawnShop", "50000", 1000))
	defer world.Close()
	handler := NewShopSellHandler(world)
	ctx := &Context{ActorID: "player:alice"}

	status, err := handler(ctx, ResolvedCommand{Args: []string{"object:sword"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 그런 물건을 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want missing object", status, ctx.OutputString())
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if creature.Stats["gold"] != 1000 || !containsObjectID(creature.Inventory.ObjectIDs, "object:sword") {
		t.Fatalf("state mutated after object-id sale rejection: gold=%d inv=%+v", creature.Stats["gold"], creature.Inventory.ObjectIDs)
	}
}

func TestShopSellHandlerUsesLegacyPrefixOrderInsteadOfExactFirst(t *testing.T) {
	loaded := shopSellWorld(t, "pawnShop", "50000", 1000)
	proto := loaded.ObjectPrototypes["prototype:sword"]
	proto.DisplayName = "목검 조각"
	proto.Properties = map[string]string{"name": "목검 조각", "value": "50000"}
	loaded.ObjectPrototypes[proto.ID] = proto
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:sword-exact",
		DisplayName: "목검",
		Properties:  map[string]string{"name": "목검", "value": "50000"},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:sword-exact",
		PrototypeID: "prototype:sword-exact",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
	})
	alice := loaded.Creatures["creature:alice"]
	alice.Inventory.ObjectIDs = []model.ObjectInstanceID{"object:sword", "object:sword-exact"}
	loaded.Creatures[alice.ID] = alice
	world := state.NewWorld(loaded)
	defer world.Close()
	handler := NewShopSellHandler(world)
	ctx := &Context{ActorID: "player:alice", Values: map[string]any{ContextShopSellBonusKey: false}}

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "제가 목검 조각을 사죠.\n전당포주인이 당신에게 25000냥을 줍니다." {
		t.Fatalf("status/output = %d/%q, want first prefix object sold", status, ctx.OutputString())
	}
	if _, ok := world.Object("object:sword"); ok {
		t.Fatal("first prefix object still exists after sale")
	}
	if _, ok := world.Object("object:sword-exact"); !ok {
		t.Fatal("later exact object was sold instead of first prefix object")
	}
	_, creature, err := CurrentInventoryCreature(world, "player:alice")
	if err != nil {
		t.Fatal(err)
	}
	if creature.Stats["gold"] != 26000 || containsObjectID(creature.Inventory.ObjectIDs, "object:sword") || !containsObjectID(creature.Inventory.ObjectIDs, "object:sword-exact") {
		t.Fatalf("creature after prefix-order sale = gold:%d inv:%+v", creature.Stats["gold"], creature.Inventory.ObjectIDs)
	}
}

func TestShopSellHandlerFindObjVisibilityUsesPDINVILikeLegacy(t *testing.T) {
	loaded := shopSellWorld(t, "pawnShop", "50000", 1000)
	object := loaded.Objects["object:sword"]
	object.Properties = map[string]string{"OINVIS": "1"}
	loaded.Objects[object.ID] = object
	world := state.NewWorld(loaded)
	defer world.Close()
	handler := NewShopSellHandler(world)
	ctx := &Context{ActorID: "player:alice"}

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 그런 물건을 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want invisible object hidden", status, ctx.OutputString())
	}

	loaded = shopSellWorld(t, "pawnShop", "50000", 1000)
	object = loaded.Objects["object:sword"]
	object.Properties = map[string]string{"OINVIS": "1"}
	loaded.Objects[object.ID] = object
	alice := loaded.Creatures["creature:alice"]
	alice.Metadata.Tags = []string{"PDINVI"}
	loaded.Creatures[alice.ID] = alice
	world = state.NewWorld(loaded)
	defer world.Close()
	handler = NewShopSellHandler(world)
	ctx = &Context{ActorID: "player:alice", Values: map[string]any{ContextShopSellBonusKey: false}}

	status, err = handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler with PDINVI error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "제가 목검을 사죠.\n전당포주인이 당신에게 25000냥을 줍니다." {
		t.Fatalf("PDINVI status/output = %d/%q, want sale success", status, ctx.OutputString())
	}
}

func TestShopSellHandlerRejectsUnsupportedRoomsAndMissingTargets(t *testing.T) {
	tests := []struct {
		name string
		room string
		cmd  ResolvedCommand
		want string
	}{
		{
			name: "non pawn",
			room: "repair",
			cmd:  ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}},
			want: "여기는 전당포가 아닙니다.",
		},
		{
			name: "missing target",
			room: "pawnShop",
			cmd:  ResolvedCommand{},
			want: "무엇을 파시려구요?",
		},
		{
			name: "unknown target",
			room: "pawnShop",
			cmd:  ResolvedCommand{Args: []string{"없는물건"}, Values: []int64{1}},
			want: "당신은 그런 물건을 갖고 있지 않습니다.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := state.NewWorld(shopSellWorld(t, tt.room, "50000", 1000))
			defer world.Close()
			handler := NewShopSellHandler(world)
			ctx := &Context{ActorID: "player:alice"}
			status, err := handler(ctx, tt.cmd)
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			if got := ctx.OutputString(); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
			_, creature, err := CurrentInventoryCreature(world, "player:alice")
			if err != nil {
				t.Fatal(err)
			}
			if got, want := creature.Stats["gold"], 1000; got != want {
				t.Fatalf("gold mutated after rejected sale: %d, want %d", got, want)
			}
			if !containsObjectID(creature.Inventory.ObjectIDs, "object:sword") {
				t.Fatalf("inventory lost object after rejected sale: %+v", creature.Inventory.ObjectIDs)
			}
			if _, ok := world.Object("object:sword"); !ok {
				t.Fatal("object was deleted after rejected sale")
			}
		})
	}
}

func TestShopSellHandlerRejectsLegacyUnsupportedObjects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*worldload.World)
		want   string
	}{
		{
			name: "low value",
			mutate: func(loaded *worldload.World) {
				proto := loaded.ObjectPrototypes["prototype:sword"]
				proto.Properties["value"] = "39"
				loaded.ObjectPrototypes[proto.ID] = proto
			},
			want: "전당포주인이 \"그런 쓰레기는 안사요!\"라고 말합니다.",
		},
		{
			name: "event object",
			mutate: func(loaded *worldload.World) {
				object := loaded.Objects["object:sword"]
				object.Properties = map[string]string{"OEVENT": "1"}
				loaded.Objects[object.ID] = object
			},
			want: "전당포주인이 \"그런건 안사요!\"라고 말합니다.",
		},
		{
			name: "event object via flags property token",
			mutate: func(loaded *worldload.World) {
				object := loaded.Objects["object:sword"]
				object.Properties = map[string]string{"flags": "eventItem"}
				loaded.Objects[object.ID] = object
			},
			want: "전당포주인이 \"그런건 안사요!\"라고 말합니다.",
		},
		{
			name: "container with contents",
			mutate: func(loaded *worldload.World) {
				object := loaded.Objects["object:sword"]
				object.Contents = model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:gem"}}
				loaded.Objects[object.ID] = object
			},
			want: "전당포주인이 \"그 안에 뭔가가 들어있군요.\"라고 말합니다.",
		},
		{
			name: "potion",
			mutate: func(loaded *worldload.World) {
				proto := loaded.ObjectPrototypes["prototype:sword"]
				proto.Kind = model.ObjectKindPotion
				loaded.ObjectPrototypes[proto.ID] = proto
			},
			want: "전당포주인이 \"두루마기나 독약같은것은 안사요!\"라고 말합니다.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded := shopSellWorld(t, "pawnShop", "50000", 1000)
			tt.mutate(loaded)
			world := state.NewWorld(loaded)
			defer world.Close()
			handler := NewShopSellHandler(world)
			ctx := &Context{ActorID: "player:alice"}
			status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
			if err != nil {
				t.Fatalf("handler() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			if got := ctx.OutputString(); got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
			_, creature, err := CurrentInventoryCreature(world, "player:alice")
			if err != nil {
				t.Fatal(err)
			}
			if got, want := creature.Stats["gold"], 1000; got != want {
				t.Fatalf("gold mutated after rejected sale: %d, want %d", got, want)
			}
			if !containsObjectID(creature.Inventory.ObjectIDs, "object:sword") {
				t.Fatalf("inventory lost object after rejected sale: %+v", creature.Inventory.ObjectIDs)
			}
		})
	}
}

func TestRenderShopListUsesLegacyPlainTextRows(t *testing.T) {
	world := state.NewWorld(shopWorld(t, true))
	defer world.Close()
	stockRoom, ok := world.Room("room:01072")
	if !ok {
		t.Fatal("missing stock room")
	}
	got := RenderShopList(&Context{}, world, stockRoom)

	for _, want := range []string{
		"상품들:",
		"\n   목검",
		"가격: 50000",
		"\n   기념패",
		"가격: 10000",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("shop list missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "##") || strings.Contains(got, "|") || strings.HasSuffix(got, "\n") {
		t.Fatalf("shop list still looks like markdown or has trailing newline:\n%q", got)
	}
}

func TestShopValueHandlerValuesInventoryAtPawnShop(t *testing.T) {
	world := state.NewWorld(shopValueWorld(t, "pawnShop", "50000"))
	defer world.Close()
	dispatcher := Dispatcher{
		Registry: mustRegistry(t, []commandspec.CommandSpec{
			{Name: "가치", Number: 44, Handler: "value"},
			{Name: "가격", Number: 44, Handler: "value"},
		}),
		Handlers: map[string]Handler{
			"value": NewShopValueHandler(world),
		},
	}

	for _, line := range []string{"목검 가치", "가치 목검"} {
		t.Run(line, func(t *testing.T) {
			var broadcasts []roomBroadcastRecord
			ctx := contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)
			status, err := dispatcher.DispatchLine(ctx, line)
			if err != nil {
				t.Fatalf("DispatchLine() error = %v", err)
			}
			if status != StatusDefault {
				t.Fatalf("status = %d, want default", status)
			}
			out := ctx.OutputString()
			for _, want := range []string{"상점주인이", "목검이라면", "25000냥"} {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
			if len(broadcasts) != 1 ||
				broadcasts[0].RoomID != "room:pawn" ||
				broadcasts[0].Text != "\nAlice이 목검의 가치를 알아봅니다." {
				t.Fatalf("broadcasts = %+v, want C value room broadcast", broadcasts)
			}
		})
	}
}

func TestShopValueHandlerCapsPawnShopValue(t *testing.T) {
	world := state.NewWorld(shopValueWorld(t, "pawnShop", "500000"))
	defer world.Close()
	handler := NewShopValueHandler(world)
	ctx := &Context{ActorID: "player:alice"}

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "100000냥") {
		t.Fatalf("output missing capped value:\n%s", got)
	}
}

func TestShopValueHandlerValuesInventoryAtRepairShop(t *testing.T) {
	world := state.NewWorld(shopValueWorld(t, "repair", "50000"))
	defer world.Close()
	handler := NewShopValueHandler(world)
	var broadcasts []roomBroadcastRecord
	ctx := contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	out := ctx.OutputString()
	for _, want := range []string{"상점주인이", "목검을 수리하는데", "12500냥"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if len(broadcasts) != 1 ||
		broadcasts[0].RoomID != "room:pawn" ||
		broadcasts[0].Text != "\nAlice이 목검의 가치를 알아봅니다." {
		t.Fatalf("broadcasts = %+v, want C value room broadcast from repair shop", broadcasts)
	}
}

func TestShopValueHandlerRejectsUnsupportedRoomsAndMissingTargets(t *testing.T) {
	world := state.NewWorld(shopValueWorld(t, "", "50000"))
	defer world.Close()
	handler := NewShopValueHandler(world)
	ctx := &Context{ActorID: "player:alice"}

	status, err := handler(ctx, ResolvedCommand{Args: []string{"목검"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got, want := ctx.OutputString(), "전당포에 가셔서 물건의 가치를 알아보세요."; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	world = state.NewWorld(shopValueWorld(t, "pawnShop", "50000"))
	defer world.Close()
	handler = NewShopValueHandler(world)
	ctx = &Context{ActorID: "player:alice"}
	status, err = handler(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got, want := ctx.OutputString(), "어떤 물건의 가치를 알고 싶으세요?"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}

	ctx = &Context{ActorID: "player:alice"}
	status, err = handler(ctx, ResolvedCommand{Args: []string{"없는물건"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got, want := ctx.OutputString(), "당신은 그런 물건을 갖고 있지 않습니다."; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestShopValueHandlerClearsHiddenBeforeFindObjLikeLegacy(t *testing.T) {
	world := state.NewWorld(shopValueWorld(t, "pawnShop", "50000"))
	defer world.Close()
	if _, err := world.UpdateCreatureTags("creature:alice", []string{"hidden", "PHIDDN"}, nil); err != nil {
		t.Fatalf("UpdateCreatureTags() error = %v", err)
	}
	if _, err := world.UpdatePlayerTags("player:alice", []string{"hidden", "PHIDDN"}, nil); err != nil {
		t.Fatalf("UpdatePlayerTags() error = %v", err)
	}
	if err := world.SetCreatureStat("creature:alice", "PHIDDN", 1); err != nil {
		t.Fatalf("SetCreatureStat() error = %v", err)
	}
	handler := NewShopValueHandler(world)
	ctx := &Context{ActorID: "player:alice"}

	status, err := handler(ctx, ResolvedCommand{Args: []string{"없는물건"}, Values: []int64{1}})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "당신은 그런 물건을 갖고 있지 않습니다." {
		t.Fatalf("status/output = %d/%q, want missing object", status, ctx.OutputString())
	}
	creature, _ := world.Creature("creature:alice")
	if hasAnyNormalizedFlag(creature.Metadata.Tags, "hidden", "phiddn") || creature.Stats["PHIDDN"] != 0 {
		t.Fatalf("creature hidden state = tags:%+v stats:%+v", creature.Metadata.Tags, creature.Stats)
	}
	player, _ := world.Player("player:alice")
	if hasAnyNormalizedFlag(player.Metadata.Tags, "hidden", "phiddn") {
		t.Fatalf("player hidden tags = %+v", player.Metadata.Tags)
	}
}

func TestFormatThousands(t *testing.T) {
	tests := map[int]string{
		0:        "0",
		999:      "999",
		1000:     "1,000",
		10000:    "10,000",
		1234567:  "1,234,567",
		-1234567: "-1,234,567",
	}

	for input, want := range tests {
		if got := formatThousands(input); got != want {
			t.Fatalf("formatThousands(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestShopListHandlerRejectsNonShopRoom(t *testing.T) {
	world := state.NewWorld(shopWorld(t, false))
	defer world.Close()
	handler := NewShopListHandler(world)
	ctx := &Context{ActorID: "player:alice"}

	status, err := handler(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	if got, want := ctx.OutputString(), "여기는 상점이 아닙니다."; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func shopWorld(t *testing.T, shop bool) *worldload.World {
	t.Helper()

	loaded := emptyInventoryWorld(t)
	tags := []string(nil)
	if shop {
		tags = []string{"shoppe"}
	}
	mustAddLookRoom(t, loaded, model.Room{
		ID:          "room:01071",
		DisplayName: "기념품점",
		Metadata:    model.Metadata{Tags: tags},
	})
	mustAddLookRoom(t, loaded, model.Room{
		ID:          "room:01072",
		DisplayName: "기념품점 창고",
		Objects:     model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:sword", "object:plaque"}},
	})
	player := loaded.Players["player:alice"]
	player.RoomID = "room:01071"
	loaded.Players[player.ID] = player
	creature := loaded.Creatures["creature:alice"]
	creature.RoomID = "room:01071"
	loaded.Creatures[creature.ID] = creature

	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:sword",
		DisplayName: "목검",
		Properties:  map[string]string{"value": "50000"},
	})
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:plaque",
		DisplayName: "기념패",
		Properties:  map[string]string{"value": "10000"},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:sword",
		PrototypeID: "prototype:sword",
		Location:    model.ObjectLocation{RoomID: "room:01072"},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:plaque",
		PrototypeID: "prototype:plaque",
		Location:    model.ObjectLocation{RoomID: "room:01072"},
	})
	return loaded
}

func shopBuyWorld(t *testing.T, shop bool, gold int) *worldload.World {
	t.Helper()

	loaded := shopWorld(t, shop)
	creature := loaded.Creatures["creature:alice"]
	creature.Stats = map[string]int{"gold": gold}
	loaded.Creatures[creature.ID] = creature
	return loaded
}

func shopTestFillInventoryAndEquip(t *testing.T, loaded *worldload.World, inventoryCount int) {
	t.Helper()
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{ID: "prototype:filler", DisplayName: "더미"})
	creature := loaded.Creatures["creature:alice"]
	for i := 0; i < inventoryCount; i++ {
		id := model.ObjectInstanceID(fmt.Sprintf("object:filler:%03d", i))
		mustAddLookObject(t, loaded, model.ObjectInstance{
			ID:          id,
			PrototypeID: "prototype:filler",
			Location:    model.ObjectLocation{CreatureID: creature.ID, Slot: "inventory"},
		})
		creature.Inventory.ObjectIDs = append(creature.Inventory.ObjectIDs, id)
	}
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:equipped-filler",
		PrototypeID: "prototype:filler",
		Location:    model.ObjectLocation{CreatureID: creature.ID, Slot: "body"},
	})
	creature.Equipment = map[string]model.ObjectInstanceID{"body": "object:equipped-filler"}
	loaded.Creatures[creature.ID] = creature
}

func containsObjectID(ids []model.ObjectInstanceID, want model.ObjectInstanceID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func shopValueWorld(t *testing.T, roomTag string, value string) *worldload.World {
	t.Helper()

	loaded := emptyInventoryWorld(t)
	tags := []string(nil)
	if roomTag != "" {
		tags = []string{roomTag}
	}
	mustAddLookRoom(t, loaded, model.Room{
		ID:          "room:pawn",
		DisplayName: "전당포",
		Metadata:    model.Metadata{Tags: tags},
	})

	player := loaded.Players["player:alice"]
	player.RoomID = "room:pawn"
	loaded.Players[player.ID] = player
	creature := loaded.Creatures["creature:alice"]
	creature.RoomID = "room:pawn"
	creature.Inventory = model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:sword"}}
	loaded.Creatures[creature.ID] = creature

	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:sword",
		DisplayName: "목검",
		Properties:  map[string]string{"value": value},
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:sword",
		PrototypeID: "prototype:sword",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
	})
	return loaded
}

func shopSellWorld(t *testing.T, roomTag string, value string, gold int) *worldload.World {
	t.Helper()

	loaded := shopValueWorld(t, roomTag, value)
	creature := loaded.Creatures["creature:alice"]
	creature.Stats = map[string]int{"gold": gold}
	loaded.Creatures[creature.ID] = creature
	return loaded
}

func shopSellTestContext() *Context {
	return &Context{
		ActorID: "player:alice",
		Values:  map[string]any{ContextShopSellBonusKey: false},
	}
}

func waitForShopPlayerSave(t *testing.T, world *state.World, root string, playerID model.PlayerID) state.PlayerSaveData {
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
