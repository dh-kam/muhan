package command

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"muhan/internal/krtext"
	"muhan/internal/world/model"
)

type DrinkWorld interface {
	StatusWorld
	UpdateCreatureTags(model.CreatureID, []string, []string) (model.Creature, error)
	UpdatePlayerTags(model.PlayerID, []string, []string) (model.Player, error)
	ConsumeCreatureObjectCharge(model.ObjectInstanceID, model.CreatureID, bool) (model.ObjectInstance, bool, bool, error)
	DestroyCreatureInventoryObject(model.ObjectInstanceID, model.CreatureID) (bool, error)
	MoveObject(model.ObjectInstanceID, model.ObjectLocation) error
	MovePlayerToRoom(model.PlayerID, model.RoomID) error
	SetCreatureStat(model.CreatureID, string, int) error
	LoadRoom(model.RoomID) error
	SavePlayer(model.PlayerID) error
}

type DrinkEffectFunc func(*Context, DrinkWorld, model.Creature, model.ObjectInstance, ResolvedCommand) (bool, error)

func NewDrinkHandler(world DrinkWorld, effect DrinkEffectFunc) Handler {
	if effect == nil {
		effect = defaultDrinkMagicEffect
	}
	return func(ctx *Context, resolved ResolvedCommand) (Status, error) {
		playerID := InventoryPlayerIDFromContext(ctx)
		if playerID.IsZero() {
			return StatusDefault, ErrInventoryActorRequired
		}
		player, creature, err := CurrentInventoryCreature(world, playerID)
		if err != nil {
			return StatusDefault, err
		}
		target := getArg(resolved, 0)
		if target == "" {
			ctx.WriteString("\n무엇을 먹습니까?\n")
			return StatusDefault, nil
		}

		object, name, ok := findDrinkObject(world, creature, target, firstGetOrdinal(resolved), inventoryViewerDetectsInvisible(player, creature))
		if !ok {
			ctx.WriteString("\n그런것은 존재하지 않습니다.\n")
			return StatusDefault, nil
		}
		if !drinkObjectIsPotion(world, object) {
			ctx.WriteString("\n이것은 먹는 물건이 아닙니다.\n")
			return StatusDefault, nil
		}
		if drinkObjectIsSpecialItem(world, object) {
			if err := drinkSpecialItemPotion(ctx, world, player, creature, object, name); err != nil {
				return StatusDefault, err
			}
			return StatusDefault, nil
		}
		if objectIntPropertyOrZero(world, object, "shotsCurrent") < 1 || drinkMagicPower(world, object) < 1 {
			ctx.WriteString("\n아무것도 들어있지 않습니다.\n\n")
			return StatusDefault, nil
		}
		room, ok := world.Room(player.RoomID)
		if !ok {
			return StatusDefault, fmt.Errorf("drink: room %q not found", player.RoomID)
		}
		if roomHasAnyFlag(room, "noPotion", "rnopot") {
			ctx.WriteString("\n그것을 먹기전에 인조가 나타나 훔쳐가 버렸습니다.\n 잘먹겠다... 낄낄낄... \n")
			return StatusDefault, nil
		}
		if roomHasAnyFlag(room, "survival", "rsuviv") {
			ctx.WriteString("\n대련장에서는 아무 것도 먹을 수 없습니다.\n")
			return StatusDefault, nil
		}
		if magicObjectAlignmentRejected(world, creature, object) {
			if err := world.MoveObject(object.ID, model.ObjectLocation{RoomID: room.ID}); err != nil {
				return StatusDefault, err
			}
			ctx.WriteString(name + krtext.Particle(name, '1') + " 당신의 손안에서 타버립니다.\n")
			return StatusDefault, nil
		}
		if magicObjectClassRestricted(world, creature, object) {
			ctx.WriteString("\n당신직업상 그 물건을 금하고 있기 때문에 먹을 수 없습니다.\n")
			return StatusDefault, nil
		}

		player, creature, err = clearCommandActorHidden(world, player, creature)
		if err != nil {
			return StatusDefault, err
		}
		success, err := effect(ctx, world, creature, object, resolved)
		if err != nil {
			return StatusDefault, err
		}
		if !success {
			return StatusDefault, nil
		}

		if text := drinkObjectUseOutput(world, object); text != "" {
			ctx.WriteString(ensureTrailingNewline(text))
		}
		ctx.WriteString("\n당신은 " + name + krtext.Particle(name, '3') + " 먹었습니다.\n")
		if err := drinkBroadcastConsumption(ctx, world, player, creature, name); err != nil {
			return StatusDefault, err
		}
		_, _, consumed, err := world.ConsumeCreatureObjectCharge(object.ID, creature.ID, true)
		if err != nil {
			return StatusDefault, err
		}
		if !consumed {
			ctx.WriteString("\n아무것도 들어있지 않습니다.\n\n")
		}
		return StatusDefault, nil
	}
}

func findDrinkObject(world InventoryWorld, creature model.Creature, target string, ordinal int64, detectInvisible bool) (model.ObjectInstance, string, bool) {
	if object, name, ok := findEquipInventoryObjectWithVisibility(world, creature, target, ordinal, detectInvisible); ok {
		return object, name, true
	}
	return findEquippedObject(world, creature, target, ordinal)
}

func drinkObjectIsPotion(world InventoryWorld, object model.ObjectInstance) bool {
	return objectLegacyType(world, object) == legacyObjectPotion ||
		objectKindIs(world, object, model.ObjectKindPotion)
}

func drinkMagicPower(world InventoryWorld, object model.ObjectInstance) int {
	if value, ok := objectIntProperty(world, object, "magicPower"); ok {
		return value
	}
	if value, ok := objectIntProperty(world, object, "magicpower"); ok {
		return value
	}
	return 0
}

func drinkObjectUseOutput(world InventoryWorld, object model.ObjectInstance) string {
	if text := object.Properties["useOutput"]; strings.TrimSpace(text) != "" {
		return text
	}
	if object.PrototypeID.IsZero() {
		return ""
	}
	proto, ok := world.ObjectPrototype(object.PrototypeID)
	if !ok {
		return ""
	}
	if text := proto.Properties["useOutput"]; strings.TrimSpace(text) != "" {
		return text
	}
	return ""
}

func drinkObjectIsSpecialItem(world InventoryWorld, object model.ObjectInstance) bool {
	return magicObjectHasFlag(world, object, "specialItem", "ospeci", "OSPECI")
}

func drinkSpecialItemPotion(
	ctx *Context,
	world DrinkWorld,
	player model.Player,
	creature model.Creature,
	object model.ObjectInstance,
	name string,
) error {
	switch drinkPDice(world, object) {
	case 1:
		// C magic1.c: pdice 1 reduces LT_HOURS by one day, capped at zero.
		interval := creatureStat(creature, "legacyHoursInterval")
		if interval < 86400 {
			interval = 0
		} else {
			interval -= 86400
		}
		if err := world.SetCreatureStat(creature.ID, "legacyHoursInterval", interval); err != nil {
			return err
		}
		if err := world.SetCreatureStat(creature.ID, "legacyAgeYears", 18+interval/86400); err != nil {
			return err
		}
		if !player.ID.IsZero() {
			if err := world.SavePlayer(player.ID); err != nil {
				log.Printf("[PERSIST] ERROR drink SavePlayer %s: %v", player.ID, err)
				return err
			}
		}
	case 2:
		if err := drinkToggleCreaturePlayerFlag(world, creature, player, "PCHAOS", []string{"chaos", "pchaos", "PCHAOS"}); err != nil {
			return err
		}
	case 3:
		// C magic1.c: pdice 3 increases LT_HOURS by one day.
		interval := creatureStat(creature, "legacyHoursInterval")
		interval += 86400
		if err := world.SetCreatureStat(creature.ID, "legacyHoursInterval", interval); err != nil {
			return err
		}
		if err := world.SetCreatureStat(creature.ID, "legacyAgeYears", 18+interval/86400); err != nil {
			return err
		}
		if !player.ID.IsZero() {
			if err := world.SavePlayer(player.ID); err != nil {
				log.Printf("[PERSIST] ERROR drink SavePlayer %s: %v", player.ID, err)
				return err
			}
		}
	case 4:
		if err := drinkIncreaseBaseStats(world, creature); err != nil {
			return err
		}
	case 5:
		// C magic1.c:604-608: load_rom(obj_ptr->value); del_ply_rom/add_ply_rom for room teleport side-effect.
		// load_rom side effects (room cache load/evict from disk files in files2.c:64) simulated by existing LoadRoom + Move in drinkResolveTargetRoomID/drinkMovePlayerToSpecialRoom.
		// No user-visible placeholder text (matches C output exactly; broadcast/move already performed inside move func).
		if err := drinkMovePlayerToSpecialRoom(ctx, world, player, creature, object); err != nil {
			return err
		}
	case 6:
		if err := drinkAddCreaturePlayerFlag(world, creature, player, "PPOISN", []string{"poison", "poisoned", "ppoisn", "PPOISN"}); err != nil {
			return err
		}
	}

	if text := drinkObjectUseOutput(world, object); text != "" {
		ctx.WriteString(ensureTrailingNewline(text))
	}
	ctx.WriteString("당신은 " + name + krtext.Particle(name, '3') + " 먹었습니다.\n")
	if err := drinkBroadcastConsumption(ctx, world, player, creature, name); err != nil {
		return err
	}
	return drinkDestroyCarriedPotion(world, creature.ID, object)
}

func drinkBroadcastConsumption(
	ctx *Context,
	world DrinkWorld,
	player model.Player,
	creature model.Creature,
	objectName string,
) error {
	currentPlayer := player
	currentCreature := creature
	if !player.ID.IsZero() {
		if refreshedPlayer, refreshedCreature, err := CurrentInventoryCreature(world, player.ID); err == nil {
			currentPlayer = refreshedPlayer
			currentCreature = refreshedCreature
		}
	}

	roomID := currentPlayer.RoomID
	if roomID.IsZero() {
		roomID = currentCreature.RoomID
	}

	actorName := strings.TrimSpace(currentCreature.DisplayName)
	if actorName == "" {
		actorName = strings.TrimSpace(currentPlayer.DisplayName)
	}
	if actorName == "" && !currentPlayer.ID.IsZero() {
		actorName = string(currentPlayer.ID)
	}
	if actorName == "" && ctx != nil {
		actorName = strings.TrimSpace(ctx.ActorID)
	}
	if actorName == "" {
		actorName = "누군가"
	}

	objectName = strings.TrimSpace(objectName)
	if objectName == "" {
		objectName = "무언가"
	}
	text := "\n" + actorName + krtext.Particle(actorName, '1') + " " + objectName + krtext.Particle(objectName, '3') + " 먹었습니다."
	return roomBroadcast(ctx, roomID, text)
}

func drinkPDice(world InventoryWorld, object model.ObjectInstance) int {
	if value, ok := objectIntProperty(world, object, "pDice"); ok {
		return value
	}
	if value, ok := objectIntProperty(world, object, "pdice"); ok {
		return value
	}
	return 0
}

type drinkCreaturePropertySetter interface {
	SetCreatureProperty(model.CreatureID, string, string) (model.Creature, error)
}

func drinkToggleCreaturePlayerFlag(world DrinkWorld, creature model.Creature, player model.Player, statKey string, tags []string) error {
	if creatureHasAnyFlag(creature, tags...) {
		return drinkRemoveCreaturePlayerFlag(world, creature, player, statKey, tags)
	}
	return drinkAddCreaturePlayerFlag(world, creature, player, statKey, tags)
}

func drinkAddCreaturePlayerFlag(world DrinkWorld, creature model.Creature, player model.Player, statKey string, tags []string) error {
	if err := world.SetCreatureStat(creature.ID, statKey, 1); err != nil {
		return err
	}
	if _, err := world.UpdateCreatureTags(creature.ID, tags, nil); err != nil {
		return err
	}
	if !player.ID.IsZero() {
		if _, err := world.UpdatePlayerTags(player.ID, tags, nil); err != nil {
			return err
		}
	}
	return nil
}

func drinkRemoveCreaturePlayerFlag(world DrinkWorld, creature model.Creature, player model.Player, statKey string, tags []string) error {
	if err := world.SetCreatureStat(creature.ID, statKey, 0); err != nil {
		return err
	}
	if setter, ok := world.(drinkCreaturePropertySetter); ok {
		if _, err := setter.SetCreatureProperty(creature.ID, statKey, ""); err != nil {
			return err
		}
	}
	if _, err := world.UpdateCreatureTags(creature.ID, nil, tags); err != nil {
		return err
	}
	if !player.ID.IsZero() {
		if _, err := world.UpdatePlayerTags(player.ID, nil, tags); err != nil {
			return err
		}
	}
	return nil
}

func drinkIncreaseBaseStats(world DrinkWorld, creature model.Creature) error {
	statKeys := []string{"strength", "intelligence", "piety", "constitution", "dexterity"}
	total := 0
	for _, key := range statKeys {
		total += creatureStat(creature, key)
	}

	limit := 54 + drinkCreatureLevel(creature)/4
	if creatureClass(creature) > legacyClassThief {
		limit += 25
	}
	if creatureHasAnyFlag(creature, "haste", "phaste", "PHASTE") {
		limit += 15
	}
	if creatureHasAnyFlag(creature, "power", "ppower", "PPOWER") {
		limit += 3
	}
	if creatureHasAnyFlag(creature, "meditate", "pmedit", "PMEDIT") {
		limit += 3
	}
	if creatureHasAnyFlag(creature, "prayed", "pprayd", "PPRAYD") {
		limit += 3
	}
	if total > limit+3 {
		return nil
	}

	for _, key := range statKeys {
		if err := world.SetCreatureStat(creature.ID, key, creatureStat(creature, key)+1); err != nil {
			return err
		}
	}
	return nil
}

func drinkCreatureLevel(creature model.Creature) int {
	if creature.Level != 0 {
		return creature.Level
	}
	return creatureStat(creature, "level")
}

func drinkMovePlayerToSpecialRoom(ctx *Context, world DrinkWorld, player model.Player, creature model.Creature, object model.ObjectInstance) error {
	roomID, ok := drinkResolveTargetRoomID(world, object)
	if !ok {
		return nil
	}

	name := creature.DisplayName

	if err := world.MovePlayerToRoom(player.ID, roomID); err != nil {
		return err
	}

	arriveMsg := fmt.Sprintf("\n%s%s 도착하였습니다.", name, krtext.Particle(name, '1'))
	return roomBroadcast(ctx, roomID, arriveMsg)
}

func drinkResolveTargetRoomID(world DrinkWorld, object model.ObjectInstance) (model.RoomID, bool) {
	var candidates []string

	for _, key := range []string{"roomID", "roomId", "targetRoomID", "targetRoomId"} {
		if raw, ok := objectStringProperty(world, object, key); ok {
			candidates = append(candidates, raw)
		}
	}

	if raw, ok := objectStringProperty(world, object, "value"); ok {
		candidates = append(candidates, raw)
		if value, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			candidates = append(candidates, fmt.Sprintf("room:%d", value))
			candidates = append(candidates, fmt.Sprintf("room:%04d", value))
			candidates = append(candidates, fmt.Sprintf("room:%05d", value))
		}
	}

	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var roomID model.RoomID
		if strings.HasPrefix(raw, "room:") {
			roomID = model.RoomID(raw)
		} else if strings.HasPrefix(raw, "r") {
			roomID = model.RoomID("room:" + strings.TrimPrefix(raw, "r"))
		} else {
			roomID = model.RoomID("room:" + raw)
		}

		numericPart := strings.TrimPrefix(string(roomID), "room:")
		if num, err := strconv.Atoi(numericPart); err == nil {
			roomID = model.RoomID(fmt.Sprintf("room:%05d", num))
		}

		if _, ok := world.Room(roomID); ok {
			return roomID, true
		}

		if err := world.LoadRoom(roomID); err == nil {
			if _, ok := world.Room(roomID); ok {
				return roomID, true
			}
		}
	}

	return "", false
}

func drinkDestroyCarriedPotion(world DrinkWorld, creatureID model.CreatureID, object model.ObjectInstance) error {
	if object.Location.CreatureID == creatureID && object.Location.Slot != "" && object.Location.Slot != "inventory" {
		if err := world.MoveObject(object.ID, model.ObjectLocation{CreatureID: creatureID, Slot: "inventory"}); err != nil {
			return err
		}
	}
	_, err := world.DestroyCreatureInventoryObject(object.ID, creatureID)
	return err
}
