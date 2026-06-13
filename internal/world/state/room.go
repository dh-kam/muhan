package state

import (
	"fmt"
	"maps"
	"github.com/0xc0de1ab/muhan/internal/world/model"
	"slices"
	"strconv"
	"strings"
	"time"
)

// MarkRoomObjectsDirty marks a room as having its floor objects (including any
// containers dropped on the ground and their recursive contents) changed.
// Called from MoveObject when crossing room boundary, DropCreatureGoldToRoom,
// death corpse/item scatter, etc. (D phase - floor persistence)
func (w *World) MarkRoomObjectsDirty(rid model.RoomID) {
	if rid.IsZero() {
		return
	}
	w.dirtyMu.Lock()
	if w.roomObjectDirty == nil {
		w.roomObjectDirty = make(map[model.RoomID]int64)
	}
	w.roomObjectDirty[rid] = time.Now().Unix()
	w.dirtyMu.Unlock()
}

// Room returns a copy of the room with id.
func (w *World) Room(id model.RoomID) (model.Room, bool) {
	if w == nil {
		return model.Room{}, false
	}
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)

	room, ok := w.rooms[id]
	if !ok {
		return model.Room{}, false
	}
	return cloneRoom(room), true
}

// AllRoomIDs returns a slice of all room IDs in the world.
func (w *World) AllRoomIDs() []model.RoomID {
	if w == nil {
		return nil
	}
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)

	ids := make([]model.RoomID, 0, len(w.rooms))
	for id := range w.rooms {
		ids = append(ids, id)
	}
	return ids
}

// GetRoom returns a copy of the room with id.
func (w *World) GetRoom(id model.RoomID) (model.Room, bool) {
	return w.Room(id)
}

// RemoveMarriageInvite removes name from a runtime marriage/special room invite list.
// It returns false when the trimmed name is not present.
func (w *World) RemoveMarriageInvite(specialID model.SpecialID, name string) (bool, error) {
	if w == nil {
		return false, fmt.Errorf("remove marriage invite %q: world state is nil", specialID)
	}
	if specialID.IsZero() {
		return false, fmt.Errorf("remove marriage invite: special id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("remove marriage invite %q: name is required", specialID)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	invites := w.marriageInvites[specialID]
	match := -1
	for i, existing := range invites {
		if strings.TrimSpace(existing) == name {
			match = i
		}
	}
	if match < 0 {
		return false, nil
	}
	invites = append(invites[:match], invites[match+1:]...)
	if len(invites) == 0 {
		delete(w.marriageInvites, specialID)
	} else {
		w.marriageInvites[specialID] = invites
	}
	return true, nil
}

func (w *World) refreshRoomPermanentSpawnsLocked(roomID model.RoomID, nowUnix int64) {
	room, ok := w.rooms[roomID]
	if !ok {
		return
	}
	for _, group := range roomPermanentDueGroups(roomPermanentSlots(room, "perm_mon", "permMon", "permanentCreature"), nowUnix) {
		w.spawnRoomPermanentCreatureGroupLocked(roomID, group.misc, group.count)
	}
	room = w.rooms[roomID]
	for _, group := range roomPermanentDueGroups(roomPermanentSlots(room, "perm_obj", "permObj", "permanentObject"), nowUnix) {
		w.spawnRoomPermanentObjectGroupLocked(roomID, group.misc, group.count)
	}
}

func roomPermanentSlots(room model.Room, prefixes ...string) []roomPermanentSlot {
	if len(room.Properties) == 0 {
		return nil
	}
	slots := make([]roomPermanentSlot, 0, 10)
	for i := 0; i < 10; i++ {
		for _, prefix := range prefixes {
			misc, ok := roomPermanentSlotInt(room.Properties, prefix, i, "misc")
			if !ok || misc == 0 {
				continue
			}
			ltime, _ := roomPermanentSlotInt(room.Properties, prefix, i, "ltime")
			interval, _ := roomPermanentSlotInt(room.Properties, prefix, i, "interval")
			slots = append(slots, roomPermanentSlot{
				index:    i,
				misc:     int(misc),
				ltime:    ltime,
				interval: interval,
			})
			break
		}
	}
	return slots
}

func roomPermanentDueGroups(slots []roomPermanentSlot, nowUnix int64) []roomPermanentGroup {
	if len(slots) == 0 {
		return nil
	}
	checked := make([]bool, len(slots))
	groups := make([]roomPermanentGroup, 0, len(slots))
	for i, slot := range slots {
		if checked[i] || slot.misc == 0 || slot.ltime+slot.interval > nowUnix {
			continue
		}
		count := 1
		for j := i + 1; j < len(slots); j++ {
			other := slots[j]
			if checked[j] || other.misc != slot.misc || other.ltime+other.interval >= nowUnix {
				continue
			}
			count++
			checked[j] = true
		}
		groups = append(groups, roomPermanentGroup{misc: slot.misc, count: count})
	}
	return groups
}

func roomPermanentSlotInt(properties map[string]string, prefix string, index int, field string) (int64, bool) {
	for _, key := range []string{
		fmt.Sprintf("%s.%d.%s", prefix, index, field),
		fmt.Sprintf("%s[%d].%s", prefix, index, field),
		fmt.Sprintf("%s.%02d.%s", prefix, index, field),
		fmt.Sprintf("%s[%02d].%s", prefix, index, field),
	} {
		if value, ok := parseStateInt(properties[key]); ok {
			return int64(value), true
		}
	}
	return 0, false
}

func (w *World) spawnRoomPermanentCreatureGroupLocked(roomID model.RoomID, misc int, wanted int) {
	if misc <= 0 || wanted <= 0 {
		return
	}
	protoID := model.CreatureID(fmt.Sprintf("creature:m%02d:%d", misc/100, misc%100))
	proto, ok := w.creatures[protoID]
	if !ok {
		return
	}
	name := creatureLegacySortName(proto)
	if name == "" {
		name = string(proto.ID)
	}
	current := 0
	if room, ok := w.rooms[roomID]; ok {
		for _, creatureID := range room.CreatureIDs {
			creature, ok := w.creatures[creatureID]
			if !ok || !creatureHasAnyFlag(creature, "MPERMT", "permanent") {
				continue
			}
			if existingName := creatureLegacySortName(creature); existingName == name || (existingName == "" && string(creature.ID) == name) {
				current++
			}
		}
	}
	for i := 0; i < wanted-current; i++ {
		_, _ = w.spawnCreatureLocked(protoID, roomID, true, []string{"MPERMT", "permanent"})
	}
}

func (w *World) spawnRoomPermanentObjectGroupLocked(roomID model.RoomID, misc int, wanted int) {
	if misc <= 0 || wanted <= 0 {
		return
	}
	protoID := legacyCarryObjectPrototypeID(misc)
	if _, ok := w.prototypes[protoID]; !ok {
		return
	}
	name := w.objectPrototypeLegacySortNameLocked(protoID)
	current := 0
	if room, ok := w.rooms[roomID]; ok {
		for _, objectID := range room.Objects.ObjectIDs {
			object, ok := w.objects[objectID]
			if !ok || !w.objectHasAnyLegacyFlagLocked(object, "OPERMT", "roomPermanent", "permanent") {
				continue
			}
			if w.objectLegacySortNameFromObjectLocked(object) == name {
				current++
			}
		}
	}
	for i := 0; i < wanted-current; i++ {
		objectID, err := w.createObjectFromPrototypeLocked(protoID, model.ObjectLocation{RoomID: roomID})
		if err != nil {
			continue
		}
		object := w.objects[objectID]
		object.Metadata.Tags = addMetadataTags(object.Metadata.Tags, []string{"OPERMT", "roomPermanent", "permanent"})
		w.objects[objectID] = object
		w.MarkRoomObjectsDirty(roomID)
	}
}

// MoveCreatureToRoom moves a creature directly to roomID, updating the room occupant lists.
func (w *World) MoveCreatureToRoom(creatureID model.CreatureID, roomID model.RoomID) error {
	if w == nil {
		return fmt.Errorf("move creature %q to room %q: world state is nil", creatureID, roomID)
	}
	if creatureID.IsZero() {
		return fmt.Errorf("move creature to room %q: creature id is required", roomID)
	}
	if roomID.IsZero() {
		return fmt.Errorf("move creature %q to room: room id is required", creatureID)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	creature, ok := w.creatures[creatureID]
	if !ok {
		return fmt.Errorf("move creature %q to room %q: creature not found", creatureID, roomID)
	}
	toRoom, ok := w.rooms[roomID]
	if !ok {
		return fmt.Errorf("move creature %q to room %q: target room not found", creatureID, roomID)
	}

	if !creature.PlayerID.IsZero() {
		player, ok := w.players[creature.PlayerID]
		if ok {
			player.RoomID = roomID
			w.players[player.ID] = player
		}
	}

	creature.RoomID = roomID
	w.creatures[creature.ID] = creature

	for currentRoomID, room := range w.rooms {
		if !creature.PlayerID.IsZero() {
			room.PlayerIDs = removeID(room.PlayerIDs, creature.PlayerID)
		}
		room.CreatureIDs = removeID(room.CreatureIDs, creature.ID)
		w.rooms[currentRoomID] = room
	}

	toRoom = w.rooms[toRoom.ID]
	if !creature.PlayerID.IsZero() {
		toRoom.PlayerIDs = w.insertPlayerIDLegacySortedLocked(toRoom.PlayerIDs, creature.PlayerID)
	}
	toRoom.CreatureIDs = w.insertCreatureIDLegacySortedLocked(toRoom.CreatureIDs, creature.ID)
	w.rooms[toRoom.ID] = toRoom
	return nil
}

// MoveObjectToCreatureInventory moves objectID into creatureID's inventory.
func (w *World) MoveObjectToCreatureInventory(objectID model.ObjectInstanceID, creatureID model.CreatureID) error {
	return w.MoveObject(objectID, model.ObjectLocation{CreatureID: creatureID, Slot: "inventory"})
}

// MoveObjectToRoom moves objectID into roomID.
func (w *World) MoveObjectToRoom(objectID model.ObjectInstanceID, roomID model.RoomID) error {
	return w.MoveObject(objectID, model.ObjectLocation{RoomID: roomID})
}

// MoveObject moves objectID to location and keeps old and new holder ref lists in sync.
func (w *World) MoveObject(objectID model.ObjectInstanceID, location model.ObjectLocation) error {
	if w == nil {
		return fmt.Errorf("move object %q: world state is nil", objectID)
	}
	if objectID.IsZero() {
		return fmt.Errorf("move object: object id is required")
	}
	if err := location.Validate(); err != nil {
		return fmt.Errorf("move object %q: location: %w", objectID, err)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	object, ok := w.objects[objectID]
	if !ok {
		return fmt.Errorf("move object %q: object not found", objectID)
	}
	if err := w.validateObjectDestinationLocked(objectID, location); err != nil {
		return err
	}

	nextObject := object
	nextObject.Location = location
	if err := nextObject.Validate(); err != nil {
		return fmt.Errorf("move object %q: %w", objectID, err)
	}

	w.removeObjectFromHolderLocked(objectID, object.Location)
	w.objects[objectID] = nextObject
	w.addObjectToHolderLocked(objectID, location)

	if !object.Location.CreatureID.IsZero() {
		if c, ok := w.creatures[object.Location.CreatureID]; ok && !c.PlayerID.IsZero() {
			w.MarkPlayerDirty(c.PlayerID)
		}
	}
	if !location.CreatureID.IsZero() {
		if c, ok := w.creatures[location.CreatureID]; ok && !c.PlayerID.IsZero() {
			w.MarkPlayerDirty(c.PlayerID)
		}
	}
	if !object.Location.BankID.IsZero() {
		w.MarkBankDirty(object.Location.BankID)
	}
	if !location.BankID.IsZero() {
		w.MarkBankDirty(location.BankID)
	}

	if !object.Location.RoomID.IsZero() {
		w.MarkRoomObjectsDirty(object.Location.RoomID)
	}
	if !location.RoomID.IsZero() {
		w.MarkRoomObjectsDirty(location.RoomID)
	}

	return nil
}

// DropCreatureGoldToRoom debits creature gold and creates a money object in a
// room under one lock. ok is false when the creature has insufficient gold.
func (w *World) DropCreatureGoldToRoom(creatureID model.CreatureID, roomID model.RoomID, amount int) (objectID model.ObjectInstanceID, remainingGold int, ok bool, err error) {
	if w == nil {
		return "", 0, false, fmt.Errorf("drop gold from %q to room %q: world state is nil", creatureID, roomID)
	}
	if creatureID.IsZero() {
		return "", 0, false, fmt.Errorf("drop gold to room %q: creature id is required", roomID)
	}
	if roomID.IsZero() {
		return "", 0, false, fmt.Errorf("drop gold from %q: room id is required", creatureID)
	}
	if amount < 1 {
		return "", 0, false, fmt.Errorf("drop gold from %q to room %q: amount must be positive", creatureID, roomID)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	creature, okCreature := w.creatures[creatureID]
	if !okCreature {
		return "", 0, false, fmt.Errorf("drop gold from %q: creature not found", creatureID)
	}
	if _, okRoom := w.rooms[roomID]; !okRoom {
		return "", 0, false, fmt.Errorf("drop gold to room %q: room not found", roomID)
	}
	gold := creature.Stats["gold"]
	if gold < amount {
		return "", gold, false, nil
	}

	if creature.Stats == nil {
		creature.Stats = map[string]int{}
	}
	remainingGold = gold - amount
	creature.Stats["gold"] = remainingGold
	w.creatures[creatureID] = creature

	if !creature.PlayerID.IsZero() {
		w.MarkPlayerDirty(creature.PlayerID)
	}

	objectID = w.nextObjectCloneIDLocked("object:money")
	moneyPrototypeID := model.PrototypeID("prototype:money")
	if _, ok := w.prototypes[moneyPrototypeID]; !ok {
		w.prototypes[moneyPrototypeID] = model.ObjectPrototype{
			ID:          moneyPrototypeID,
			Kind:        model.ObjectKindMoney,
			DisplayName: "돈",
		}
	}
	object := model.ObjectInstance{
		ID:                  objectID,
		PrototypeID:         moneyPrototypeID,
		DisplayNameOverride: fmt.Sprintf("%d냥", amount),
		Location:            model.ObjectLocation{RoomID: roomID},
		Properties: map[string]string{
			"kind":  string(model.ObjectKindMoney),
			"type":  "10",
			"value": strconv.Itoa(amount),
		},
	}
	if err := object.Validate(); err != nil {
		return "", 0, false, fmt.Errorf("drop gold object %q: %w", objectID, err)
	}
	w.objects[objectID] = object
	w.addObjectToHolderLocked(objectID, object.Location)

	w.MarkRoomObjectsDirty(roomID)

	return objectID, remainingGold, true, nil
}

func removeCreatureFamilyStateProperties(creature *model.Creature) {
	if len(creature.Properties) == 0 {
		return
	}
	targets := normalizedFlagSet(creatureFamilyStatePropertyNames()...)
	for key := range creature.Properties {
		if _, ok := targets[normalizeFlagName(key)]; ok {
			delete(creature.Properties, key)
		}
	}
	if len(creature.Properties) == 0 {
		creature.Properties = nil
	}
}

func removeCreatureMarriageStateProperties(creature *model.Creature) {
	if len(creature.Properties) == 0 {
		return
	}
	targets := normalizedFlagSet(creatureMarriageStatePropertyNames()...)
	for key := range creature.Properties {
		if _, ok := targets[normalizeFlagName(key)]; ok {
			delete(creature.Properties, key)
		}
	}
	if len(creature.Properties) == 0 {
		creature.Properties = nil
	}
}

// SetExitFlag enables or disables a room exit flag under the world lock.
func (w *World) SetExitFlag(roomID model.RoomID, exitName string, flag string, enabled bool) (model.Exit, error) {
	if w == nil {
		return model.Exit{}, fmt.Errorf("set exit %q flag %q: world state is nil", exitName, flag)
	}
	if roomID.IsZero() {
		return model.Exit{}, fmt.Errorf("set exit %q flag %q: room id is required", exitName, flag)
	}
	exitName = strings.TrimSpace(exitName)
	if exitName == "" {
		return model.Exit{}, fmt.Errorf("set exit flag %q: exit name is required", flag)
	}
	flag = strings.TrimSpace(flag)
	if flag == "" {
		return model.Exit{}, fmt.Errorf("set exit %q flag: flag is required", exitName)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	room, ok := w.rooms[roomID]
	if !ok {
		return model.Exit{}, fmt.Errorf("set exit %q flag %q: room %q not found", exitName, flag, roomID)
	}
	for i := range room.Exits {
		if room.Exits[i].Name != exitName {
			continue
		}
		exit := room.Exits[i]
		exit.Flags = setExitFlag(exit.Flags, flag, enabled)
		room.Exits[i] = exit
		w.rooms[roomID] = room
		return cloneExit(exit), nil
	}
	return model.Exit{}, fmt.Errorf("set exit %q flag %q: exit not found in room %q", exitName, flag, roomID)
}

// TouchExitTimer updates an exit's legacy ltime.ltime value.
func (w *World) TouchExitTimer(roomID model.RoomID, exitName string, nowUnix int64) (model.Exit, error) {
	if w == nil {
		return model.Exit{}, fmt.Errorf("touch exit %q timer: world state is nil", exitName)
	}
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	room, exitIndex, err := w.findExitForUpdateLocked(roomID, exitName, "touch")
	if err != nil {
		return model.Exit{}, err
	}
	exit := setExitTimerLTime(room.Exits[exitIndex], nowUnix)
	room.Exits[exitIndex] = exit
	w.rooms[roomID] = room
	return cloneExit(exit), nil
}

// CheckRoomExits applies the legacy room.c check_exits() timed relock/reclose pass.
func (w *World) CheckRoomExits(roomID model.RoomID, nowUnix int64) error {
	if w == nil {
		return fmt.Errorf("check exits in room %q: world state is nil", roomID)
	}
	if roomID.IsZero() {
		return fmt.Errorf("check exits: room id is required")
	}
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	if _, ok := w.rooms[roomID]; !ok {
		return fmt.Errorf("check exits in room %q: room not found", roomID)
	}
	w.checkRoomExitsLocked(roomID, nowUnix)
	return nil
}

// UnlockExitWithKey unlocks a locked exit and consumes one key charge.
func (w *World) UnlockExitWithKey(roomID model.RoomID, exitName string, keyID model.ObjectInstanceID) (model.Exit, model.ObjectInstance, error) {
	if w == nil {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("unlock exit %q: world state is nil", exitName)
	}
	if keyID.IsZero() {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("unlock exit %q: key id is required", exitName)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	room, exitIndex, err := w.findExitForUpdateLocked(roomID, exitName, "unlock")
	if err != nil {
		return model.Exit{}, model.ObjectInstance{}, err
	}
	exit := room.Exits[exitIndex]
	if !exitHasAnyFlag(exit, "locked", "xlockd", "xlocked") {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("unlock exit %q: exit is not locked", exitName)
	}

	key, err := w.validateExitKeyLocked(keyID, exit)
	if err != nil {
		return model.Exit{}, model.ObjectInstance{}, err
	}
	charges, _ := w.objectIntPropertyLocked(key, "shotsCurrent")
	if charges < 1 {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("unlock exit %q: key %q is broken", exitName, keyID)
	}

	exit.Flags = setExitFlag(exit.Flags, "locked", false)
	exit = setExitTimerLTime(exit, time.Now().Unix())
	room.Exits[exitIndex] = exit
	w.rooms[roomID] = room

	key.Properties = maps.Clone(key.Properties)
	if key.Properties == nil {
		key.Properties = map[string]string{}
	}
	key.Properties["shotsCurrent"] = strconv.Itoa(charges - 1)
	w.objects[key.ID] = key

	return cloneExit(exit), cloneObject(key), nil
}

// LockExitWithKey locks a closed, lockable exit with a matching key.
func (w *World) LockExitWithKey(roomID model.RoomID, exitName string, keyID model.ObjectInstanceID) (model.Exit, model.ObjectInstance, error) {
	if w == nil {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("lock exit %q: world state is nil", exitName)
	}
	if keyID.IsZero() {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("lock exit %q: key id is required", exitName)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	room, exitIndex, err := w.findExitForUpdateLocked(roomID, exitName, "lock")
	if err != nil {
		return model.Exit{}, model.ObjectInstance{}, err
	}
	exit := room.Exits[exitIndex]
	if exitHasAnyFlag(exit, "locked", "xlockd", "xlocked") {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("lock exit %q: exit is already locked", exitName)
	}
	if !exitHasAnyFlag(exit, "lockable", "xlocks") {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("lock exit %q: exit is not lockable", exitName)
	}
	if !exitHasAnyFlag(exit, "closed", "xclosd", "xclosed") {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("lock exit %q: exit must be closed", exitName)
	}

	key, err := w.validateExitKeyLocked(keyID, exit)
	if err != nil {
		return model.Exit{}, model.ObjectInstance{}, err
	}
	charges, _ := w.objectIntPropertyLocked(key, "shotsCurrent")
	if charges < 1 {
		return model.Exit{}, model.ObjectInstance{}, fmt.Errorf("lock exit %q: key %q is broken", exitName, keyID)
	}

	exit.Flags = setExitFlag(exit.Flags, "locked", true)
	room.Exits[exitIndex] = exit
	w.rooms[roomID] = room
	return cloneExit(exit), cloneObject(key), nil
}

func (w *World) findExitForUpdateLocked(roomID model.RoomID, exitName string, action string) (model.Room, int, error) {
	if roomID.IsZero() {
		return model.Room{}, -1, fmt.Errorf("%s exit %q: room id is required", action, exitName)
	}
	exitName = strings.TrimSpace(exitName)
	if exitName == "" {
		return model.Room{}, -1, fmt.Errorf("%s exit: exit name is required", action)
	}
	room, ok := w.rooms[roomID]
	if !ok {
		return model.Room{}, -1, fmt.Errorf("%s exit %q: room %q not found", action, exitName, roomID)
	}
	for i, exit := range room.Exits {
		if exit.Name == exitName {
			return room, i, nil
		}
	}
	return model.Room{}, -1, fmt.Errorf("%s exit %q: exit not found in room %q", action, exitName, roomID)
}

func (w *World) validateExitKeyLocked(keyID model.ObjectInstanceID, exit model.Exit) (model.ObjectInstance, error) {
	key, ok := w.objects[keyID]
	if !ok {
		return model.ObjectInstance{}, fmt.Errorf("exit key %q: object not found", keyID)
	}
	if !w.objectKindIsLocked(key, model.ObjectKindKey) {
		return model.ObjectInstance{}, fmt.Errorf("exit key %q: object is not a key", keyID)
	}
	keyNumber, ok := exitKeyNumber(exit)
	if !ok {
		return model.ObjectInstance{}, fmt.Errorf("exit %q: key number not found", exit.Name)
	}
	objectKeyNumber, ok := w.objectIntPropertyLocked(key, "nDice")
	if !ok || objectKeyNumber != keyNumber {
		return model.ObjectInstance{}, fmt.Errorf("exit key %q: key does not match", keyID)
	}
	return key, nil
}

func exitKeyNumber(exit model.Exit) (int, bool) {
	for _, flag := range exit.Flags {
		flag = strings.TrimSpace(flag)
		if flag == "" {
			continue
		}
		key, raw, ok := strings.Cut(flag, ":")
		if !ok || normalizeFlagName(key) != "key" {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && value > 0 {
			return value, true
		}
	}
	if raw := exit.Metadata.RawFields["key"]; len(raw) > 0 && raw[0] > 0 {
		return int(raw[0]), true
	}
	return 0, false
}

func (w *World) checkRoomExitsLocked(roomID model.RoomID, nowUnix int64) {
	room, ok := w.rooms[roomID]
	if !ok {
		return
	}
	changed := false
	for i := range room.Exits {
		exit := room.Exits[i]
		if !exitTimerExpired(exit, nowUnix) {
			continue
		}
		switch {
		case exitHasAnyFlag(exit, "lockable", "xlocks"):
			exit.Flags = setExitFlag(exit.Flags, "locked", true)
			exit.Flags = setExitFlag(exit.Flags, "closed", true)
		case exitHasAnyFlag(exit, "closable", "xcloss"):
			exit.Flags = setExitFlag(exit.Flags, "closed", true)
		default:
			continue
		}
		room.Exits[i] = exit
		changed = true
	}
	if changed {
		w.rooms[roomID] = room
	}
}

func exitTimerExpired(exit model.Exit, nowUnix int64) bool {
	interval, ltime, ok := exitTimerValues(exit)
	if !ok {
		return false
	}
	return ltime+interval < nowUnix
}

func exitTimerValues(exit model.Exit) (interval int64, ltime int64, ok bool) {
	raw := exit.Metadata.RawFields
	if len(raw) == 0 {
		return 0, 0, false
	}
	interval, hasInterval := rawFieldInt64(raw[exitLTimeIntervalRawField], 4)
	ltime, hasLTime := rawFieldInt64(raw[exitLTimeLTimeRawField], 4)
	if !hasInterval {
		interval, hasInterval = rawFieldInt64(raw["interval"], 4)
	}
	if !hasLTime {
		ltime, hasLTime = rawFieldInt64(raw["lasttime"], 4)
	}
	return interval, ltime, hasInterval || hasLTime
}

func setExitTimerLTime(exit model.Exit, nowUnix int64) model.Exit {
	if exit.Metadata.RawFields == nil {
		exit.Metadata.RawFields = map[string][]byte{}
	} else {
		exit.Metadata.RawFields = cloneRawFields(exit.Metadata.RawFields)
	}
	exit.Metadata.RawFields[exitLTimeLTimeRawField] = int32RawField(nowUnix)
	return exit
}

func setExitFlag(flags []string, flag string, enabled bool) []string {
	normalized := normalizeFlagName(flag)
	if normalized == "" {
		return slices.Clone(flags)
	}
	out := make([]string, 0, len(flags)+1)
	found := false
	for _, existing := range flags {
		if exitFlagSameKind(existing, normalized) {
			found = true
			continue
		}
		out = append(out, existing)
	}
	if enabled {
		if normalized == "closed" || normalized == "xclosd" || normalized == "xclosed" {
			out = append(out, "closed")
		} else if normalized == "locked" || normalized == "xlockd" || normalized == "xlocked" {
			out = append(out, "locked")
		} else if !found {
			out = append(out, flag)
		} else {
			out = append(out, flag)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func removeMetadataTags(tags []string, remove []string) []string {
	if len(tags) == 0 || len(remove) == 0 {
		return slices.Clone(tags)
	}
	targets := normalizedFlagSet(remove...)
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if _, ok := targets[normalizeFlagName(tag)]; ok {
			continue
		}
		out = append(out, tag)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func exitFlagSameKind(flag string, normalizedTarget string) bool {
	normalized := normalizeFlagName(flag)
	for _, target := range ExpandFlagNames(normalizedTarget) {
		if normalized == target {
			return true
		}
	}
	return false
}

func (w *World) reconcileRoomOccupants() {
	for id, room := range w.rooms {
		room.PlayerIDs = w.sortPlayerIDsLegacyLocked(filterRoomPlayerIDs(id, room.PlayerIDs, w.players))
		room.CreatureIDs = w.sortCreatureIDsLegacyLocked(filterRoomCreatureIDs(id, room.CreatureIDs, w.creatures))
		w.rooms[id] = room
	}

	playerIDs := make([]model.PlayerID, 0, len(w.players))
	for id := range w.players {
		playerIDs = append(playerIDs, id)
	}
	slices.Sort(playerIDs)
	for _, id := range playerIDs {
		player := w.players[id]
		if player.RoomID.IsZero() {
			continue
		}
		room, ok := w.rooms[player.RoomID]
		if !ok {
			continue
		}
		room.PlayerIDs = w.insertPlayerIDLegacySortedLocked(room.PlayerIDs, player.ID)
		w.rooms[player.RoomID] = room
	}

	creatureIDs := make([]model.CreatureID, 0, len(w.creatures))
	for id := range w.creatures {
		creatureIDs = append(creatureIDs, id)
	}
	slices.Sort(creatureIDs)
	for _, id := range creatureIDs {
		creature := w.creatures[id]
		if creature.RoomID.IsZero() {
			continue
		}
		room, ok := w.rooms[creature.RoomID]
		if !ok {
			continue
		}
		room.CreatureIDs = w.insertCreatureIDLegacySortedLocked(room.CreatureIDs, creature.ID)
		w.rooms[creature.RoomID] = room
	}
}

func (w *World) removeObjectFromHolderLocked(objectID model.ObjectInstanceID, location model.ObjectLocation) {
	switch {
	case !location.RoomID.IsZero():
		room, ok := w.rooms[location.RoomID]
		if !ok {
			return
		}
		room.Objects.ObjectIDs = removeID(room.Objects.ObjectIDs, objectID)
		w.rooms[location.RoomID] = room
	case !location.CreatureID.IsZero():
		creature, ok := w.creatures[location.CreatureID]
		if !ok {
			return
		}
		creature.Inventory.ObjectIDs = removeID(creature.Inventory.ObjectIDs, objectID)
		for slot, equippedObjectID := range creature.Equipment {
			if equippedObjectID == objectID {
				delete(creature.Equipment, slot)
			}
		}
		w.creatures[location.CreatureID] = creature
	case !location.BankID.IsZero():
		account, ok := w.banks[location.BankID]
		if !ok {
			return
		}
		account.Objects.ObjectIDs = removeID(account.Objects.ObjectIDs, objectID)
		w.banks[location.BankID] = account
	case !location.ContainerID.IsZero():
		container, ok := w.objects[location.ContainerID]
		if !ok {
			return
		}
		container.Contents.ObjectIDs = removeID(container.Contents.ObjectIDs, objectID)
		w.objects[location.ContainerID] = container
	}
}

func filterRoomCreatureIDs(roomID model.RoomID, ids []model.CreatureID, creatures map[model.CreatureID]model.Creature) []model.CreatureID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]model.CreatureID, 0, len(ids))
	seen := make(map[model.CreatureID]struct{}, len(ids))
	for _, id := range ids {
		if id.IsZero() {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		creature, ok := creatures[id]
		if ok && creature.RoomID != roomID {
			continue
		}
		out = append(out, id)
		seen[id] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func findExit(room model.Room, name string) (model.Exit, bool) {
	for _, exit := range room.Exits {
		if exit.Name == name {
			return exit, true
		}
	}
	return model.Exit{}, false
}

func blockedMoveExitFlag(exit model.Exit) (string, bool) {
	blocked := map[string]struct{}{
		"closed":  {},
		"xclosd":  {},
		"xclosed": {},
		"locked":  {},
		"xlockd":  {},
		"xlocked": {},
		"nosee":   {},
		"xnosee":  {},
	}
	for _, flag := range exit.Flags {
		normalized := normalizeFlagName(flag)
		if _, ok := blocked[normalized]; ok {
			return flag, true
		}
	}
	return "", false
}

func roomSpecialValue(room model.Room) (int, bool) {
	if len(room.Properties) == 0 {
		return 0, false
	}
	if raw, ok := room.Properties["special"]; ok {
		return parseMoveIntValue(raw)
	}
	for key, raw := range room.Properties {
		if normalizeFlagName(key) == "special" {
			return parseMoveIntValue(raw)
		}
	}
	return 0, false
}

func parseMoveIntValue(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func roomMinLevel(room model.Room) (int, bool) {
	levels := roomLevelLimits(room, "minLevel")
	if len(levels) == 0 {
		return 0, false
	}
	minLevel := levels[0]
	for _, level := range levels[1:] {
		if level > minLevel {
			minLevel = level
		}
	}
	return minLevel, true
}

func roomMaxLevel(room model.Room) (int, bool) {
	levels := roomLevelLimits(room, "maxLevel")
	if len(levels) == 0 {
		return 0, false
	}
	maxLevel := levels[0]
	for _, level := range levels[1:] {
		if level < maxLevel {
			maxLevel = level
		}
	}
	return maxLevel, true
}

func roomLevelLimits(room model.Room, name string) []int {
	var levels []int
	target := normalizeFlagName(name)

	for key, value := range room.Properties {
		if normalizeFlagName(key) == target {
			if level, ok := parseMoveLevelValue(value); ok {
				levels = append(levels, level)
			}
		}
		levels = appendNamedMoveLevelLimits(levels, value, target)
	}

	for _, tag := range room.Metadata.Tags {
		levels = appendNamedMoveLevelLimits(levels, tag, target)
	}

	return levels
}

func appendNamedMoveLevelLimits(levels []int, value string, target string) []int {
	if level, ok := parseNamedMoveLevelLimit(value, target); ok {
		levels = append(levels, level)
	}
	for _, token := range moveRestrictionTokens(value) {
		if level, ok := parseNamedMoveLevelLimit(token, target); ok {
			levels = append(levels, level)
		}
	}
	return levels
}

func parseNamedMoveLevelLimit(value string, target string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	for _, sep := range []string{":", "="} {
		key, rawLevel, ok := strings.Cut(value, sep)
		if ok && normalizeFlagName(key) == target {
			return parseMoveLevelValue(rawLevel)
		}
	}

	normalized := normalizeFlagName(value)
	if !strings.HasPrefix(normalized, target) || len(normalized) == len(target) {
		return 0, false
	}
	return parseMoveLevelValue(normalized[len(target):])
}

func parseMoveLevelValue(value string) (int, bool) {
	level, ok := parseStateInt(value)
	if !ok || level < 0 {
		return 0, false
	}
	return level, true
}

func moveRestrictionTokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' '
	})
}

func roomHasAnyFlag(room model.Room, names ...string) bool {
	if hasAnyNormalizedFlag(room.Metadata.Tags, names...) {
		return true
	}
	if len(room.Properties) == 0 {
		return false
	}

	targets := normalizedFlagSet(names...)
	for key, value := range room.Properties {
		normalizedKey := normalizeFlagName(key)
		if _, ok := targets[normalizedKey]; ok && propertyFlagEnabled(value) {
			return true
		}
		for _, token := range moveRestrictionTokens(value) {
			if _, ok := targets[normalizeFlagName(token)]; ok {
				return true
			}
		}
	}
	return false
}

func exitHasAnyFlag(exit model.Exit, names ...string) bool {
	return hasAnyNormalizedFlag(exit.Flags, names...)
}

func parseMoveObjectWeight(value string) (int, bool) {
	return parseStateInt(value)
}

func removeID[T comparable](ids []T, id T) []T {
	kept := make([]T, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			kept = append(kept, existing)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func cloneRoom(room model.Room) model.Room {
	room.Exits = slices.Clone(room.Exits)
	for i := range room.Exits {
		room.Exits[i] = cloneExit(room.Exits[i])
	}
	room.CreatureIDs = slices.Clone(room.CreatureIDs)
	room.PlayerIDs = slices.Clone(room.PlayerIDs)
	room.Objects = cloneObjectRefList(room.Objects)
	room.Properties = maps.Clone(room.Properties)
	room.Metadata = cloneMetadata(room.Metadata)
	return room
}

func cloneExit(exit model.Exit) model.Exit {
	exit.Flags = slices.Clone(exit.Flags)
	exit.Metadata = cloneMetadata(exit.Metadata)
	return exit
}

// PurgeRoom deletes all monsters (non-players) and floor objects from the room.
func (w *World) PurgeRoom(roomID model.RoomID) error {
	if w == nil {
		return fmt.Errorf("purge room %q: world state is nil", roomID)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	room, ok := w.rooms[roomID]
	if !ok {
		return fmt.Errorf("purge room %q: room not found", roomID)
	}

	// 1. Purge creatures (excluding players)
	var remainingCreatures []model.CreatureID
	for _, cID := range room.CreatureIDs {
		crt, exists := w.creatures[cID]
		if !exists {
			continue
		}
		if crt.Kind == model.CreatureKindPlayer || !crt.PlayerID.IsZero() {
			remainingCreatures = append(remainingCreatures, cID)
			continue
		}
		delete(w.creatures, cID)
		delete(w.monsterDamage, cID)
	}
	room.CreatureIDs = remainingCreatures

	// 2. Purge floor objects. The C path clears the room object list before
	// deciding whether to free each object, so OTEMPP objects do not remain
	// visible in the room after purge.
	var objIDs []model.ObjectInstanceID
	objIDs = append(objIDs, room.Objects.ObjectIDs...)

	seen := make(map[model.ObjectInstanceID]struct{})
	for _, oID := range objIDs {
		w.deleteObjectTreeLocked(oID, seen)
	}

	w.MarkRoomObjectsDirty(roomID)

	updatedRoom := w.rooms[roomID]
	updatedRoom.CreatureIDs = room.CreatureIDs
	w.rooms[roomID] = updatedRoom

	return nil
}
