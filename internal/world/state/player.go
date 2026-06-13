package state

import (
	"fmt"
	"github.com/0xc0de1ab/muhan/internal/world/model"
	"strconv"
	"strings"
	"time"
)

// MarkPlayerDirty marks a player as needing persistence.
// This should be called at mutation time (preferred over marking inside Save*).
func (w *World) MarkPlayerDirty(pid model.PlayerID) {
	if pid.IsZero() {
		return
	}
	w.dirtyMu.Lock()
	if w.playerDirty == nil {
		w.playerDirty = make(map[model.PlayerID]int64)
	}
	w.playerDirty[pid] = time.Now().Unix()
	w.dirtyMu.Unlock()
}

// Player returns a copy of the player with id.
func (w *World) Player(id model.PlayerID) (model.Player, bool) {
	if w == nil {
		return model.Player{}, false
	}
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)

	player, ok := w.players[id]
	if !ok {
		return model.Player{}, false
	}
	return clonePlayer(player), true
}

// GetPlayer returns a copy of the player with id.
func (w *World) GetPlayer(id model.PlayerID) (model.Player, bool) {
	return w.Player(id)
}

// EnsurePlayerBankRoot creates the player's value-backed bank root when it does
// not exist, mirroring legacy load_bank() callers that materialize an empty bank.
func (w *World) EnsurePlayerBankRoot(playerID model.PlayerID) (model.BankAccount, model.ObjectInstance, error) {
	if w == nil {
		return model.BankAccount{}, model.ObjectInstance{}, fmt.Errorf("ensure player bank root: world state is nil")
	}
	if playerID.IsZero() {
		return model.BankAccount{}, model.ObjectInstance{}, fmt.Errorf("ensure player bank root: player id is required")
	}

	w.lockDomains(true, true, true, true, true, true, true)
	player, ok := w.players[playerID]
	if !ok {
		w.unlockDomains(true, true, true, true, true, true, true)
		return model.BankAccount{}, model.ObjectInstance{}, fmt.Errorf("ensure player bank root: player %q not found", playerID)
	}
	ownerName := statePlayerBankOwnerName(player, playerID)
	bankID := model.BankID("bank:player:" + ownerName)
	if account, ok := w.banks[bankID]; ok {
		for _, objectID := range account.Objects.ObjectIDs {
			if objectID.IsZero() {
				continue
			}
			if root, found := w.objects[objectID]; found {
				w.unlockDomains(true, true, true, true, true, true, true)
				return cloneBankAccount(account), cloneObject(root), nil
			}
		}
	}

	protoID := model.PrototypeID("proto:bank-root")
	if _, ok := w.prototypes[protoID]; !ok {
		w.prototypes[protoID] = model.ObjectPrototype{
			ID:          protoID,
			Kind:        model.ObjectKindContainer,
			DisplayName: "보관함",
			Properties: map[string]string{
				"kind":   string(model.ObjectKindContainer),
				"OCONTN": "1",
			},
		}
	}
	rootID := w.nextPlayerBankRootIDLocked(ownerName)
	root := model.ObjectInstance{
		ID:          rootID,
		PrototypeID: protoID,
		Location:    model.ObjectLocation{BankID: bankID, Slot: "bank"},
		Properties: map[string]string{
			"value":        "0",
			"shotsCurrent": "0",
			"shotsMax":     "200",
			"kind":         string(model.ObjectKindContainer),
			"OCONTN":       "1",
		},
	}
	account := model.BankAccount{
		ID:            bankID,
		Kind:          "player",
		OwnerName:     ownerName,
		OwnerPlayerID: playerID,
		Objects:       model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{rootID}},
	}
	w.objects[rootID] = root
	w.banks[bankID] = account
	w.unlockDomains(true, true, true, true, true, true, true)

	w.MarkBankDirty(bankID)
	return cloneBankAccount(account), cloneObject(root), nil
}

func statePlayerBankOwnerName(player model.Player, fallback model.PlayerID) string {
	for _, candidate := range []string{
		string(player.ID),
		strings.TrimSpace(player.DisplayName),
		strings.TrimSpace(player.AccountName),
		strings.TrimSpace(player.Metadata.LegacyID),
		string(fallback),
	} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return string(fallback)
}

func (w *World) nextPlayerBankRootIDLocked(ownerName string) model.ObjectInstanceID {
	base := model.ObjectInstanceID("object:bank-root:" + ownerName)
	if _, exists := w.objects[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		candidate := model.ObjectInstanceID(fmt.Sprintf("%s:%d", base, i))
		if _, exists := w.objects[candidate]; !exists {
			return candidate
		}
	}
}

// MovePlayer moves playerID through the named exit from the player's current room.
func (w *World) MovePlayer(playerID model.PlayerID, exitName string) error {
	if w == nil {
		return fmt.Errorf("move player %q: world state is nil", playerID)
	}
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	player, ok := w.players[playerID]
	if !ok {
		return fmt.Errorf("move player %q: player not found", playerID)
	}
	if player.RoomID.IsZero() {
		return fmt.Errorf("move player %q: player has no room", playerID)
	}

	fromRoom, ok := w.rooms[player.RoomID]
	if !ok {
		return fmt.Errorf("move player %q: current room %q not found", playerID, player.RoomID)
	}

	exit, ok := findExit(fromRoom, exitName)
	if !ok {
		return fmt.Errorf("move player %q: exit %q not found in room %q", playerID, exitName, fromRoom.ID)
	}
	if flag, blocked := blockedMoveExitFlag(exit); blocked {
		return fmt.Errorf("move player %q: exit %q blocked by flag %q", playerID, exitName, flag)
	}
	toRoom, ok := w.rooms[exit.ToRoomID]
	if !ok {
		return fmt.Errorf("move player %q: target room %q not found", playerID, exit.ToRoomID)
	}

	var creature model.Creature
	hasCreature := false
	if !player.CreatureID.IsZero() {
		creature, ok = w.creatures[player.CreatureID]
		if !ok {
			return fmt.Errorf("move player %q: linked creature %q not found", playerID, player.CreatureID)
		}
		if !creature.PlayerID.IsZero() && creature.PlayerID != player.ID {
			return fmt.Errorf("move player %q: linked creature %q belongs to player %q", playerID, creature.ID, creature.PlayerID)
		}
		hasCreature = true
	}

	if err := w.validateMovePlayerRestrictions(player, exitName, exit, toRoom, creature, hasCreature); err != nil {
		return err
	}

	player.RoomID = exit.ToRoomID
	w.players[player.ID] = player

	if hasCreature {
		creature.RoomID = exit.ToRoomID
		w.creatures[creature.ID] = creature
	}

	// Remove player/creature from old room (C-8)
	fromRoom.PlayerIDs = removeID(fromRoom.PlayerIDs, player.ID)
	if hasCreature {
		fromRoom.CreatureIDs = removeID(fromRoom.CreatureIDs, creature.ID)
	}
	w.rooms[fromRoom.ID] = fromRoom

	toRoom = w.rooms[exit.ToRoomID]
	toRoom.PlayerIDs = w.insertPlayerIDLegacySortedLocked(toRoom.PlayerIDs, player.ID)
	if hasCreature {
		toRoom.CreatureIDs = w.insertCreatureIDLegacySortedLocked(toRoom.CreatureIDs, creature.ID)
	}
	w.rooms[toRoom.ID] = toRoom
	nowUnix := time.Now().Unix()
	w.refreshRoomPermanentSpawnsLocked(toRoom.ID, nowUnix)
	w.checkRoomExitsLocked(toRoom.ID, nowUnix)

	return nil
}

// MovePlayerToRoom moves playerID directly to roomID, preserving the linked
// creature and room occupant indexes. It is used by command effects such as
// legacy recall/teleport that do not traverse an exit.
func (w *World) MovePlayerToRoom(playerID model.PlayerID, roomID model.RoomID) error {
	if w == nil {
		return fmt.Errorf("move player %q to room %q: world state is nil", playerID, roomID)
	}
	if playerID.IsZero() {
		return fmt.Errorf("move player to room %q: player id is required", roomID)
	}
	if roomID.IsZero() {
		return fmt.Errorf("move player %q to room: room id is required", playerID)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	player, ok := w.players[playerID]
	if !ok {
		return fmt.Errorf("move player %q to room %q: player not found", playerID, roomID)
	}
	toRoom, ok := w.rooms[roomID]
	if !ok {
		return fmt.Errorf("move player %q to room %q: target room not found", playerID, roomID)
	}

	var creature model.Creature
	hasCreature := false
	if !player.CreatureID.IsZero() {
		creature, ok = w.creatures[player.CreatureID]
		if !ok {
			return fmt.Errorf("move player %q to room %q: linked creature %q not found", playerID, roomID, player.CreatureID)
		}
		if !creature.PlayerID.IsZero() && creature.PlayerID != player.ID {
			return fmt.Errorf("move player %q to room %q: linked creature %q belongs to player %q", playerID, roomID, creature.ID, creature.PlayerID)
		}
		hasCreature = true
	}

	player.RoomID = roomID
	w.players[player.ID] = player
	if hasCreature {
		creature.RoomID = roomID
		w.creatures[creature.ID] = creature
	}

	for currentRoomID, room := range w.rooms {
		room.PlayerIDs = removeID(room.PlayerIDs, player.ID)
		if hasCreature {
			room.CreatureIDs = removeID(room.CreatureIDs, creature.ID)
		}
		w.rooms[currentRoomID] = room
	}

	toRoom = w.rooms[toRoom.ID]
	toRoom.PlayerIDs = w.insertPlayerIDLegacySortedLocked(toRoom.PlayerIDs, player.ID)
	if hasCreature {
		toRoom.CreatureIDs = w.insertCreatureIDLegacySortedLocked(toRoom.CreatureIDs, creature.ID)
	}
	w.rooms[toRoom.ID] = toRoom
	nowUnix := time.Now().Unix()
	w.refreshRoomPermanentSpawnsLocked(toRoom.ID, nowUnix)
	w.checkRoomExitsLocked(toRoom.ID, nowUnix)
	return nil
}

// SetCreatureClass updates the legacy class stat and recomputes combat stats
// that C recalculated after class-sensitive mutations.
func (w *World) SetCreatureClass(creatureID model.CreatureID, class int) (model.Creature, error) {
	if w == nil {
		return model.Creature{}, fmt.Errorf("set creature %q class: world state is nil", creatureID)
	}
	if creatureID.IsZero() {
		return model.Creature{}, fmt.Errorf("set creature class: creature id is required")
	}
	if class < 0 {
		return model.Creature{}, fmt.Errorf("set creature %q class: class must be non-negative", creatureID)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	creature, ok := w.creatures[creatureID]
	if !ok {
		return model.Creature{}, fmt.Errorf("set creature %q class: creature not found", creatureID)
	}
	if creature.Stats == nil {
		creature.Stats = map[string]int{}
	}
	creature.Stats["class"] = class
	w.recalculateCreatureCombatStatsLocked(&creature)
	w.creatures[creatureID] = creature
	if !creature.PlayerID.IsZero() {
		w.MarkPlayerDirty(creature.PlayerID)
	}
	return cloneCreature(creature), nil
}

// PreparePlayerSuicide marks a player as confirmed for deletion without
// removing files or runtime records. The destructive part can be implemented
// behind a command sink after bank/alias/family cleanup is wired.
func (w *World) PreparePlayerSuicide(playerID model.PlayerID, requestedAt int64) (model.Player, model.Creature, error) {
	if w == nil {
		return model.Player{}, model.Creature{}, fmt.Errorf("prepare player %q suicide: world state is nil", playerID)
	}
	if playerID.IsZero() {
		return model.Player{}, model.Creature{}, fmt.Errorf("prepare player suicide: player id is required")
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	player, ok := w.players[playerID]
	if !ok {
		return model.Player{}, model.Creature{}, fmt.Errorf("prepare player %q suicide: player not found", playerID)
	}
	if player.CreatureID.IsZero() {
		return model.Player{}, model.Creature{}, fmt.Errorf("prepare player %q suicide: creature id is required", playerID)
	}
	creature, ok := w.creatures[player.CreatureID]
	if !ok {
		return model.Player{}, model.Creature{}, fmt.Errorf("prepare player %q suicide: creature %q not found", playerID, player.CreatureID)
	}

	player.Metadata.Tags = addMetadataTags(player.Metadata.Tags, []string{stateSuicidePendingTag})
	creature.Metadata.Tags = addMetadataTags(creature.Metadata.Tags, []string{stateSuicidePendingTag})
	if creature.Properties == nil {
		creature.Properties = map[string]string{}
	}
	creature.Properties[stateSuicideRequestedAtProperty] = strconv.FormatInt(requestedAt, 10)

	w.players[playerID] = player
	w.creatures[creature.ID] = creature
	w.MarkPlayerDirty(playerID)
	return clonePlayer(player), cloneCreature(creature), nil
}

// UpdateFamilyMemberAfterClassChange mirrors C edit_member(name, class,
// daily[DL_EXPND].max, 3): update an existing family_member_N row's class
// after a successful class change. Missing family/member rows are safe no-ops
// because the class change itself has already succeeded in the caller.
func (w *World) UpdateFamilyMemberAfterClassChange(name string, class int, dailyExpndMax int) error {
	if w == nil {
		return fmt.Errorf("update family member after class change: world is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" || dailyExpndMax <= 0 {
		return nil
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	familyID, family, ok := w.familyByLegacyNumberLocked(dailyExpndMax)
	if !ok {
		return nil
	}
	for i := range family.Members {
		if strings.TrimSpace(family.Members[i].DisplayName) != name {
			continue
		}
		family.Members[i].Class = class
		if family.Members[i].Metadata.RawFields != nil {
			family.Members[i].Metadata.RawFields["line"] = []byte(fmt.Sprintf("%d %s", class, family.Members[i].DisplayName))
		}
		w.families[familyID] = family
		return nil
	}
	return nil
}

// UpdatePlayer updates a player in the world state.
func (w *World) UpdatePlayer(player model.Player) error {
	if w == nil {
		return fmt.Errorf("update player: world is nil")
	}
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.players[player.ID] = player
	return nil
}

// CreatePlayerCharacter inserts a newly created player and linked creature,
// then attaches both to their starting room in one runtime-state mutation.
func (w *World) CreatePlayerCharacter(player model.Player, creature model.Creature) error {
	if w == nil {
		return fmt.Errorf("create player character: world is nil")
	}
	if player.ID.IsZero() {
		return fmt.Errorf("create player character: player id is required")
	}
	if creature.ID.IsZero() {
		return fmt.Errorf("create player character %q: creature id is required", player.ID)
	}
	if player.CreatureID.IsZero() {
		player.CreatureID = creature.ID
	}
	if creature.PlayerID.IsZero() {
		creature.PlayerID = player.ID
	}
	if player.CreatureID != creature.ID {
		return fmt.Errorf("create player character %q: player creature %q does not match %q", player.ID, player.CreatureID, creature.ID)
	}
	if creature.PlayerID != player.ID {
		return fmt.Errorf("create player character %q: creature owner %q does not match", player.ID, creature.PlayerID)
	}
	if creature.Kind == "" {
		creature.Kind = model.CreatureKindPlayer
	}
	if creature.Kind != model.CreatureKindPlayer {
		return fmt.Errorf("create player character %q: creature kind must be player", player.ID)
	}
	if player.RoomID.IsZero() {
		player.RoomID = creature.RoomID
	}
	if creature.RoomID.IsZero() {
		creature.RoomID = player.RoomID
	}
	if player.RoomID.IsZero() || creature.RoomID.IsZero() || player.RoomID != creature.RoomID {
		return fmt.Errorf("create player character %q: player and creature must share a starting room", player.ID)
	}
	if err := player.Validate(); err != nil {
		return fmt.Errorf("create player character %q: %w", player.ID, err)
	}
	if err := creature.Validate(); err != nil {
		return fmt.Errorf("create player character %q: %w", player.ID, err)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	if _, exists := w.players[player.ID]; exists {
		return fmt.Errorf("create player character %q: player already exists", player.ID)
	}
	if _, exists := w.creatures[creature.ID]; exists {
		return fmt.Errorf("create player character %q: creature %q already exists", player.ID, creature.ID)
	}
	room, ok := w.rooms[player.RoomID]
	if !ok {
		return fmt.Errorf("create player character %q: starting room %q not found", player.ID, player.RoomID)
	}

	player = clonePlayer(player)
	creature = cloneCreature(creature)
	if creature.Stats == nil {
		creature.Stats = map[string]int{}
	}
	w.recalculateCreatureCombatStatsLocked(&creature)

	w.players[player.ID] = player
	w.creatures[creature.ID] = creature
	room.PlayerIDs = w.insertPlayerIDLegacySortedLocked(room.PlayerIDs, player.ID)
	room.CreatureIDs = w.insertCreatureIDLegacySortedLocked(room.CreatureIDs, creature.ID)
	w.rooms[room.ID] = room

	w.MarkPlayerDirty(player.ID)
	return nil
}

func creatureStateClass(creature model.Creature) int {
	return creatureStateInt(creature, "class")
}

// UpdatePlayerTags adds and removes player metadata tags under the world lock.
func (w *World) UpdatePlayerTags(playerID model.PlayerID, add []string, remove []string) (model.Player, error) {
	if w == nil {
		return model.Player{}, fmt.Errorf("update player %q tags: world state is nil", playerID)
	}
	if playerID.IsZero() {
		return model.Player{}, fmt.Errorf("update player tags: player id is required")
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	player, ok := w.players[playerID]
	if !ok {
		return model.Player{}, fmt.Errorf("update player %q tags: player not found", playerID)
	}
	player.Metadata.Tags = addMetadataTags(removeMetadataTags(player.Metadata.Tags, remove), add)
	w.players[playerID] = player
	return clonePlayer(player), nil
}

func (w *World) creatureIsPlayerRewardRecipientLocked(creature model.Creature) bool {
	if creature.PlayerID.IsZero() {
		return false
	}
	player, ok := w.players[creature.PlayerID]
	if !ok || player.CreatureID != creature.ID {
		return false
	}
	return creature.Kind == model.CreatureKindPlayer || !creature.PlayerID.IsZero()
}

func (w *World) roomVisiblePlayerCountLocked(room model.Room) int {
	count := 0
	seen := map[model.PlayerID]struct{}{}
	for _, playerID := range room.PlayerIDs {
		if playerID.IsZero() {
			continue
		}
		seen[playerID] = struct{}{}
		player, ok := w.players[playerID]
		if !ok {
			count++
			continue
		}
		if w.playerHasDMInvisibleFlagLocked(player) {
			continue
		}
		count++
	}
	for _, creatureID := range room.CreatureIDs {
		creature, ok := w.creatures[creatureID]
		if !ok || creature.PlayerID.IsZero() {
			continue
		}
		if _, ok := seen[creature.PlayerID]; ok {
			continue
		}
		if w.creatureHasDMInvisibleFlagLocked(creature) {
			continue
		}
		count++
	}
	return count
}

func (w *World) playerHasDMInvisibleFlagLocked(player model.Player) bool {
	if hasAnyNormalizedFlag(player.Metadata.Tags, "PDMINV", "dmInvisible") {
		return true
	}
	if player.CreatureID.IsZero() {
		return false
	}
	creature, ok := w.creatures[player.CreatureID]
	return ok && w.creatureHasDMInvisibleFlagLocked(creature)
}

func (w *World) sortPlayerIDsLegacyLocked(ids []model.PlayerID) []model.PlayerID {
	var out []model.PlayerID
	for _, id := range ids {
		out = w.insertPlayerIDLegacySortedLocked(out, id)
	}
	return out
}

func (w *World) insertPlayerIDLegacySortedLocked(ids []model.PlayerID, playerID model.PlayerID) []model.PlayerID {
	for _, existing := range ids {
		if existing == playerID {
			return ids
		}
	}
	out := make([]model.PlayerID, 0, len(ids)+1)
	inserted := false
	for _, existing := range ids {
		if !inserted && w.playerIDLegacyLessLocked(playerID, existing) {
			out = append(out, playerID)
			inserted = true
		}
		out = append(out, existing)
	}
	if !inserted {
		out = append(out, playerID)
	}
	return out
}

func (w *World) playerIDLegacyLessLocked(leftID, rightID model.PlayerID) bool {
	return strings.Compare(w.playerLegacySortNameLocked(leftID), w.playerLegacySortNameLocked(rightID)) < 0
}

func (w *World) playerLegacySortNameLocked(playerID model.PlayerID) string {
	player, ok := w.players[playerID]
	if !ok {
		return string(playerID)
	}
	if !player.CreatureID.IsZero() {
		if creature, ok := w.creatures[player.CreatureID]; ok {
			if name := creatureLegacySortName(creature); name != "" {
				return name
			}
		}
	}
	if name := strings.TrimSpace(player.DisplayName); name != "" {
		return name
	}
	return string(player.ID)
}

func filterRoomPlayerIDs(roomID model.RoomID, ids []model.PlayerID, players map[model.PlayerID]model.Player) []model.PlayerID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]model.PlayerID, 0, len(ids))
	seen := make(map[model.PlayerID]struct{}, len(ids))
	for _, id := range ids {
		if id.IsZero() {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		player, ok := players[id]
		if ok && player.RoomID != roomID {
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

func (w *World) validateMovePlayerRestrictions(
	player model.Player,
	exitName string,
	exit model.Exit,
	toRoom model.Room,
	creature model.Creature,
	hasCreature bool,
) error {
	level := movePlayerLevel(creature, hasCreature)
	if minLevel, ok := roomMinLevel(toRoom); ok && level < minLevel {
		return fmt.Errorf(
			"move player %q: target room %q minLevel restriction: player level %d below %d",
			player.ID,
			toRoom.ID,
			level,
			minLevel,
		)
	}
	if maxLevel, ok := roomMaxLevel(toRoom); ok && level > maxLevel {
		return fmt.Errorf(
			"move player %q: target room %q maxLevel restriction: player level %d above %d",
			player.ID,
			toRoom.ID,
			level,
			maxLevel,
		)
	}
	playerCount := w.roomVisiblePlayerCountLocked(toRoom)
	if limit, name := roomPlayerLimit(toRoom); limit > 0 && playerCount >= limit {
		return fmt.Errorf(
			"move player %q: target room %q %s restriction: player count %d at limit %d",
			player.ID,
			toRoom.ID,
			name,
			playerCount,
			limit,
		)
	}
	if err := w.validateMovePlayerFamilyRestrictions(player, toRoom, creature, hasCreature); err != nil {
		return err
	}
	if exitHasAnyFlag(exit, "naked", "xnaked") && hasCreature {
		if weight := w.creatureCarriedWeightLocked(creature); weight != 0 {
			return fmt.Errorf(
				"move player %q: exit %q naked restriction: linked creature %q carried weight %d",
				player.ID,
				exitName,
				creature.ID,
				weight,
			)
		}
	}
	return nil
}

func (w *World) validateMovePlayerFamilyRestrictions(
	player model.Player,
	room model.Room,
	creature model.Creature,
	hasCreature bool,
) error {
	if roomHasAnyFlag(room, "family") && !movePlayerCreatureHasFamilyFlag(creature, hasCreature) {
		return fmt.Errorf(
			"move player %q: target room %q family restriction: linked creature missing family flag",
			player.ID,
			room.ID,
		)
	}

	playerClass := movePlayerClass(creature, hasCreature)
	roomSpecial, hasRoomSpecial := roomSpecialValue(room)
	if roomHasAnyFlag(room, "onlyFamily", "familyOnly", "ronfml") && playerClass < movePlayerDMClass {
		if !hasRoomSpecial || !movePlayerCreatureHasIntValue(creature, hasCreature, roomSpecial,
			"familyID",
			"dailyExpndMax",
			"legacyDailyExpndMax",
		) {
			return fmt.Errorf(
				"move player %q: target room %q onlyFamily restriction: linked creature family does not match room special",
				player.ID,
				room.ID,
			)
		}
	}
	if roomHasAnyFlag(room, "onlyMarried", "marriedOnly", "ronmar") && playerClass < movePlayerDMClass {
		matchesMarriage := hasRoomSpecial && movePlayerCreatureHasIntValue(creature, hasCreature, roomSpecial,
			"marriageID",
			"dailyMarriageMax",
			"legacyDailyMarriageMax",
		)
		if !matchesMarriage && (!hasRoomSpecial || !w.hasMarriageInviteLocked(player, model.SpecialID(roomSpecial))) {
			return fmt.Errorf(
				"move player %q: target room %q onlyMarried restriction: linked creature marriage does not match room special",
				player.ID,
				room.ID,
			)
		}
	}
	return nil
}

func movePlayerInviteNameMatches(player model.Player, invitedName string) bool {
	invitedName = strings.TrimSpace(invitedName)
	if invitedName == "" {
		return false
	}
	return invitedName == strings.TrimSpace(player.DisplayName) ||
		invitedName == strings.TrimSpace(string(player.ID))
}

func movePlayerLevel(creature model.Creature, hasCreature bool) int {
	if !hasCreature {
		return 0
	}
	if creature.Stats != nil {
		if level, ok := creature.Stats["level"]; ok {
			return level
		}
	}
	if creature.Level != 0 {
		return creature.Level
	}
	if level, ok := stateCreatureIntValue(creature, "level"); ok {
		return level
	}
	return 0
}

func movePlayerClass(creature model.Creature, hasCreature bool) int {
	if !hasCreature {
		return 0
	}
	return creatureStateClass(creature)
}

func movePlayerCreatureHasFamilyFlag(creature model.Creature, hasCreature bool) bool {
	if !hasCreature {
		return false
	}
	targets := normalizedFlagSet("familyFlag", "PFAMIL")
	for key, value := range creature.Stats {
		if _, ok := targets[normalizeFlagName(key)]; ok && value != 0 {
			return true
		}
	}
	for key, value := range creature.Properties {
		if _, ok := targets[normalizeFlagName(key)]; !ok {
			continue
		}
		if propertyFlagEnabled(value) {
			return true
		}
		if parsed, parsedOK := parseMoveIntValue(value); parsedOK && parsed != 0 {
			return true
		}
	}
	return false
}

func movePlayerCreatureHasIntValue(creature model.Creature, hasCreature bool, want int, names ...string) bool {
	if !hasCreature {
		return false
	}
	targets := normalizedFlagSet(names...)
	for key, value := range creature.Stats {
		if _, ok := targets[normalizeFlagName(key)]; ok && value == want {
			return true
		}
	}
	for key, value := range creature.Properties {
		if _, ok := targets[normalizeFlagName(key)]; !ok {
			continue
		}
		parsed, ok := parseMoveIntValue(value)
		if ok && parsed == want {
			return true
		}
	}
	return false
}

func roomPlayerLimit(room model.Room) (int, string) {
	limit := 0
	name := ""
	for _, option := range []struct {
		name  string
		limit int
	}{
		{name: "onePlayer", limit: 1},
		{name: "twoPlayers", limit: 2},
		{name: "threePlayers", limit: 3},
	} {
		if roomHasAnyFlag(room, option.name) && (limit == 0 || option.limit < limit) {
			limit = option.limit
			name = option.name
		}
	}
	return limit, name
}

func clonePlayer(player model.Player) model.Player {
	player.Metadata = cloneMetadata(player.Metadata)
	return player
}
