package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	worldload "github.com/0xc0de1ab/muhan/internal/world/load"
	"github.com/0xc0de1ab/muhan/internal/world/model"
)

var (
	ErrInvalid  = errors.New("store: invalid")
	ErrNotFound = errors.New("store: not found")
)

// Store is the persistence boundary the runtime needs after legacy bootstrap.
type Store interface {
	LoadBootstrap(context.Context) (*worldload.World, error)
	Save(context.Context, ChangeSet) error
	MovePlayer(context.Context, model.PlayerID, model.RoomID) error
	MoveCreature(context.Context, model.CreatureID, model.RoomID) error
	MoveObject(context.Context, model.ObjectInstanceID, model.ObjectLocation) error
}

// ChangeSet groups runtime entity writes so callers can publish a coherent tick.
type ChangeSet struct {
	Rooms            []model.Room
	Players          []model.Player
	Creatures        []model.Creature
	Families         []model.Family
	Banks            []model.BankAccount
	Objects          []model.ObjectInstance
	ObjectPrototypes []model.ObjectPrototype
}

type MemoryStore struct {
	mu    sync.RWMutex
	world *worldload.World
}

var _ Store = (*MemoryStore)(nil)

func NewMemoryStore(world *worldload.World) (*MemoryStore, error) {
	clone := cloneWorld(world)
	if err := validateWorld(clone); err != nil {
		return nil, err
	}
	return &MemoryStore{world: clone}, nil
}

func NewEmptyMemoryStore() *MemoryStore {
	return &MemoryStore{world: worldload.NewWorld()}
}

func (s *MemoryStore) LoadBootstrap(ctx context.Context) (*worldload.World, error) {
	if s == nil {
		return nil, invalidf("nil memory store")
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return cloneWorld(s.world), nil
}

func (s *MemoryStore) Save(ctx context.Context, changes ChangeSet) error {
	if s == nil {
		return invalidf("nil memory store")
	}
	if err := checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		return err
	}

	next := cloneWorld(s.world)
	if err := applyChangeSet(next, changes); err != nil {
		return err
	}
	s.world = next
	return nil
}

func (s *MemoryStore) MovePlayer(ctx context.Context, id model.PlayerID, roomID model.RoomID) error {
	if s == nil {
		return invalidf("nil memory store")
	}
	if err := checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		return err
	}

	next := cloneWorld(s.world)
	if err := movePlayer(next, id, roomID); err != nil {
		return err
	}
	s.world = next
	return nil
}

func (s *MemoryStore) MoveCreature(ctx context.Context, id model.CreatureID, roomID model.RoomID) error {
	if s == nil {
		return invalidf("nil memory store")
	}
	if err := checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		return err
	}

	next := cloneWorld(s.world)
	if err := moveCreature(next, id, roomID); err != nil {
		return err
	}
	s.world = next
	return nil
}

func (s *MemoryStore) MoveObject(ctx context.Context, id model.ObjectInstanceID, location model.ObjectLocation) error {
	if s == nil {
		return invalidf("nil memory store")
	}
	if err := checkContext(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		return err
	}

	next := cloneWorld(s.world)
	if err := moveObject(next, id, location); err != nil {
		return err
	}
	s.world = next
	return nil
}

func applyChangeSet(w *worldload.World, changes ChangeSet) error {
	for _, room := range changes.Rooms {
		if err := room.Validate(); err != nil {
			return invalidf("room %q: %v", room.ID, err)
		}
		w.Rooms[room.ID] = cloneRoom(room)
	}
	for _, proto := range changes.ObjectPrototypes {
		if err := proto.Validate(); err != nil {
			return invalidf("object prototype %q: %v", proto.ID, err)
		}
		w.ObjectPrototypes[proto.ID] = cloneObjectPrototype(proto)
	}
	for _, family := range changes.Families {
		if err := family.Validate(); err != nil {
			return invalidf("family %d: %v", family.ID, err)
		}
		w.Families[family.ID] = cloneFamily(family)
	}
	for _, account := range changes.Banks {
		if err := account.Validate(); err != nil {
			return invalidf("bank %q: %v", account.ID, err)
		}
		w.Banks[account.ID] = cloneBankAccount(account)
	}
	for _, player := range changes.Players {
		if err := savePlayer(w, player); err != nil {
			return err
		}
	}
	for _, creature := range changes.Creatures {
		if err := saveCreature(w, creature); err != nil {
			return err
		}
	}
	return saveObjects(w, changes.Objects)
}

func savePlayer(w *worldload.World, player model.Player) error {
	if err := player.Validate(); err != nil {
		return invalidf("player %q: %v", player.ID, err)
	}
	if !player.RoomID.IsZero() {
		if _, ok := w.Rooms[player.RoomID]; !ok {
			return notFoundf("player %q room %q", player.ID, player.RoomID)
		}
	}

	old, ok := w.Players[player.ID]
	if ok {
		removePlayerFromRoom(w, player.ID, old.RoomID)
	}
	w.Players[player.ID] = clonePlayer(player)
	addPlayerToRoom(w, player.ID, player.RoomID)
	return nil
}

func saveCreature(w *worldload.World, creature model.Creature) error {
	if err := creature.Validate(); err != nil {
		return invalidf("creature %q: %v", creature.ID, err)
	}
	if !creature.RoomID.IsZero() {
		if _, ok := w.Rooms[creature.RoomID]; !ok {
			return notFoundf("creature %q room %q", creature.ID, creature.RoomID)
		}
	}

	old, ok := w.Creatures[creature.ID]
	if ok {
		removeCreatureFromRoom(w, creature.ID, old.RoomID)
	}
	w.Creatures[creature.ID] = cloneCreature(creature)
	addCreatureToRoom(w, creature.ID, creature.RoomID)
	return nil
}

func saveObjects(w *worldload.World, objects []model.ObjectInstance) error {
	for _, object := range objects {
		if err := object.Validate(); err != nil {
			return invalidf("object %q: %v", object.ID, err)
		}
		if old, ok := w.Objects[object.ID]; ok {
			removeObjectFromHolder(w, object.ID, old.Location)
		}
		w.Objects[object.ID] = cloneObjectInstance(object)
	}
	for _, object := range objects {
		if err := validateObjectDestination(w, object.ID, object.Location); err != nil {
			return err
		}
		if err := addObjectToHolder(w, object.ID, object.Location); err != nil {
			return err
		}
	}
	return nil
}

func movePlayer(w *worldload.World, id model.PlayerID, roomID model.RoomID) error {
	if id.IsZero() {
		return invalidf("player id is required")
	}
	if roomID.IsZero() {
		return invalidf("target room id is required")
	}
	if _, ok := w.Rooms[roomID]; !ok {
		return notFoundf("room %q", roomID)
	}

	player, ok := w.Players[id]
	if !ok {
		return notFoundf("player %q", id)
	}

	removePlayerFromRoom(w, id, player.RoomID)
	player.RoomID = roomID
	if err := player.Validate(); err != nil {
		return invalidf("player %q: %v", player.ID, err)
	}
	w.Players[id] = player
	addPlayerToRoom(w, id, roomID)

	if player.CreatureID.IsZero() {
		return nil
	}
	if _, ok := w.Creatures[player.CreatureID]; !ok {
		return notFoundf("player %q creature %q", id, player.CreatureID)
	}
	return moveCreature(w, player.CreatureID, roomID)
}

func moveCreature(w *worldload.World, id model.CreatureID, roomID model.RoomID) error {
	if id.IsZero() {
		return invalidf("creature id is required")
	}
	if roomID.IsZero() {
		return invalidf("target room id is required")
	}
	if _, ok := w.Rooms[roomID]; !ok {
		return notFoundf("room %q", roomID)
	}

	creature, ok := w.Creatures[id]
	if !ok {
		return notFoundf("creature %q", id)
	}

	removeCreatureFromRoom(w, id, creature.RoomID)
	creature.RoomID = roomID
	if err := creature.Validate(); err != nil {
		return invalidf("creature %q: %v", creature.ID, err)
	}
	w.Creatures[id] = creature
	addCreatureToRoom(w, id, roomID)

	if creature.PlayerID.IsZero() {
		return nil
	}
	player, ok := w.Players[creature.PlayerID]
	if !ok {
		return notFoundf("creature %q player %q", id, creature.PlayerID)
	}
	removePlayerFromRoom(w, player.ID, player.RoomID)
	player.RoomID = roomID
	if err := player.Validate(); err != nil {
		return invalidf("player %q: %v", player.ID, err)
	}
	w.Players[player.ID] = player
	addPlayerToRoom(w, player.ID, roomID)
	return nil
}

func moveObject(w *worldload.World, id model.ObjectInstanceID, location model.ObjectLocation) error {
	if id.IsZero() {
		return invalidf("object id is required")
	}
	if err := location.Validate(); err != nil {
		return invalidf("object %q location: %v", id, err)
	}

	object, ok := w.Objects[id]
	if !ok {
		return notFoundf("object %q", id)
	}
	if err := validateObjectDestination(w, id, location); err != nil {
		return err
	}

	removeObjectFromHolder(w, id, object.Location)
	object.Location = location
	if err := object.Validate(); err != nil {
		return invalidf("object %q: %v", object.ID, err)
	}
	w.Objects[id] = object
	return addObjectToHolder(w, id, location)
}

func validateObjectDestination(w *worldload.World, id model.ObjectInstanceID, location model.ObjectLocation) error {
	if err := location.Validate(); err != nil {
		return invalidf("object %q location: %v", id, err)
	}
	switch {
	case !location.RoomID.IsZero():
		if _, ok := w.Rooms[location.RoomID]; !ok {
			return notFoundf("object %q room %q", id, location.RoomID)
		}
	case !location.CreatureID.IsZero():
		if _, ok := w.Creatures[location.CreatureID]; !ok {
			return notFoundf("object %q creature %q", id, location.CreatureID)
		}
	case !location.BankID.IsZero():
		if _, ok := w.Banks[location.BankID]; !ok {
			return notFoundf("object %q bank %q", id, location.BankID)
		}
	case !location.ContainerID.IsZero():
		if _, ok := w.Objects[location.ContainerID]; !ok {
			return notFoundf("object %q container %q", id, location.ContainerID)
		}
		if location.ContainerID == id {
			return invalidf("object %q cannot contain itself", id)
		}
		if containsAncestor(w, location.ContainerID, id) {
			return invalidf("object %q cannot move into descendant %q", id, location.ContainerID)
		}
	}
	return nil
}

func containsAncestor(w *worldload.World, start, want model.ObjectInstanceID) bool {
	seen := map[model.ObjectInstanceID]struct{}{}
	for id := start; !id.IsZero(); {
		if id == want {
			return true
		}
		if _, ok := seen[id]; ok {
			return false
		}
		seen[id] = struct{}{}
		object, ok := w.Objects[id]
		if !ok {
			return false
		}
		id = object.Location.ContainerID
	}
	return false
}

func addObjectToHolder(w *worldload.World, id model.ObjectInstanceID, location model.ObjectLocation) error {
	switch {
	case !location.RoomID.IsZero():
		room, ok := w.Rooms[location.RoomID]
		if !ok {
			return notFoundf("room %q", location.RoomID)
		}
		room.Objects.ObjectIDs = appendObjectID(room.Objects.ObjectIDs, id)
		w.Rooms[location.RoomID] = room
	case !location.CreatureID.IsZero():
		creature, ok := w.Creatures[location.CreatureID]
		if !ok {
			return notFoundf("creature %q", location.CreatureID)
		}
		creature.Inventory.ObjectIDs = appendObjectID(creature.Inventory.ObjectIDs, id)
		if location.Slot != "" && location.Slot != "inventory" {
			if creature.Equipment == nil {
				creature.Equipment = map[string]model.ObjectInstanceID{}
			}
			creature.Equipment[location.Slot] = id
		}
		w.Creatures[location.CreatureID] = creature
	case !location.BankID.IsZero():
		account, ok := w.Banks[location.BankID]
		if !ok {
			return notFoundf("bank %q", location.BankID)
		}
		account.Objects.ObjectIDs = appendObjectID(account.Objects.ObjectIDs, id)
		w.Banks[location.BankID] = account
	case !location.ContainerID.IsZero():
		container, ok := w.Objects[location.ContainerID]
		if !ok {
			return notFoundf("container %q", location.ContainerID)
		}
		container.Contents.ObjectIDs = appendObjectID(container.Contents.ObjectIDs, id)
		w.Objects[location.ContainerID] = container
	}
	return nil
}

func removeObjectFromHolder(w *worldload.World, id model.ObjectInstanceID, location model.ObjectLocation) {
	switch {
	case !location.RoomID.IsZero():
		room, ok := w.Rooms[location.RoomID]
		if !ok {
			return
		}
		room.Objects.ObjectIDs = removeObjectID(room.Objects.ObjectIDs, id)
		w.Rooms[location.RoomID] = room
	case !location.CreatureID.IsZero():
		creature, ok := w.Creatures[location.CreatureID]
		if !ok {
			return
		}
		creature.Inventory.ObjectIDs = removeObjectID(creature.Inventory.ObjectIDs, id)
		for slot, objectID := range creature.Equipment {
			if objectID == id {
				delete(creature.Equipment, slot)
			}
		}
		w.Creatures[location.CreatureID] = creature
	case !location.BankID.IsZero():
		account, ok := w.Banks[location.BankID]
		if !ok {
			return
		}
		account.Objects.ObjectIDs = removeObjectID(account.Objects.ObjectIDs, id)
		w.Banks[location.BankID] = account
	case !location.ContainerID.IsZero():
		container, ok := w.Objects[location.ContainerID]
		if !ok {
			return
		}
		container.Contents.ObjectIDs = removeObjectID(container.Contents.ObjectIDs, id)
		w.Objects[location.ContainerID] = container
	}
}

func addPlayerToRoom(w *worldload.World, id model.PlayerID, roomID model.RoomID) {
	if roomID.IsZero() {
		return
	}
	room, ok := w.Rooms[roomID]
	if !ok {
		return
	}
	room.PlayerIDs = appendPlayerID(room.PlayerIDs, id)
	w.Rooms[roomID] = room
}

func removePlayerFromRoom(w *worldload.World, id model.PlayerID, roomID model.RoomID) {
	if roomID.IsZero() {
		return
	}
	room, ok := w.Rooms[roomID]
	if !ok {
		return
	}
	room.PlayerIDs = removePlayerID(room.PlayerIDs, id)
	w.Rooms[roomID] = room
}

func addCreatureToRoom(w *worldload.World, id model.CreatureID, roomID model.RoomID) {
	if roomID.IsZero() {
		return
	}
	room, ok := w.Rooms[roomID]
	if !ok {
		return
	}
	room.CreatureIDs = appendCreatureID(room.CreatureIDs, id)
	w.Rooms[roomID] = room
}

func removeCreatureFromRoom(w *worldload.World, id model.CreatureID, roomID model.RoomID) {
	if roomID.IsZero() {
		return
	}
	room, ok := w.Rooms[roomID]
	if !ok {
		return
	}
	room.CreatureIDs = removeCreatureID(room.CreatureIDs, id)
	w.Rooms[roomID] = room
}

func appendObjectID(ids []model.ObjectInstanceID, id model.ObjectInstanceID) []model.ObjectInstanceID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeObjectID(ids []model.ObjectInstanceID, id model.ObjectInstanceID) []model.ObjectInstanceID {
	filtered := make([]model.ObjectInstanceID, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

func appendPlayerID(ids []model.PlayerID, id model.PlayerID) []model.PlayerID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removePlayerID(ids []model.PlayerID, id model.PlayerID) []model.PlayerID {
	filtered := make([]model.PlayerID, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

func appendCreatureID(ids []model.CreatureID, id model.CreatureID) []model.CreatureID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeCreatureID(ids []model.CreatureID, id model.CreatureID) []model.CreatureID {
	filtered := make([]model.CreatureID, 0, len(ids))
	for _, existing := range ids {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	return filtered
}

func validateWorld(w *worldload.World) error {
	for id, room := range w.Rooms {
		if err := room.Validate(); err != nil {
			return invalidf("room %q: %v", id, err)
		}
	}
	for id, player := range w.Players {
		if err := player.Validate(); err != nil {
			return invalidf("player %q: %v", id, err)
		}
	}
	for id, creature := range w.Creatures {
		if err := creature.Validate(); err != nil {
			return invalidf("creature %q: %v", id, err)
		}
	}
	for id, family := range w.Families {
		if err := family.Validate(); err != nil {
			return invalidf("family %d: %v", id, err)
		}
	}
	for id, account := range w.Banks {
		if err := account.Validate(); err != nil {
			return invalidf("bank %q: %v", id, err)
		}
	}
	for id, object := range w.Objects {
		if err := object.Validate(); err != nil {
			return invalidf("object %q: %v", id, err)
		}
	}
	for id, proto := range w.ObjectPrototypes {
		if err := proto.Validate(); err != nil {
			return invalidf("object prototype %q: %v", id, err)
		}
	}
	return nil
}

func cloneWorld(in *worldload.World) *worldload.World {
	out := worldload.NewWorld()
	if in == nil {
		return out
	}

	for id, room := range in.Rooms {
		out.Rooms[id] = cloneRoom(room)
	}
	for id, player := range in.Players {
		out.Players[id] = clonePlayer(player)
	}
	for id, creature := range in.Creatures {
		out.Creatures[id] = cloneCreature(creature)
	}
	for id, family := range in.Families {
		out.Families[id] = cloneFamily(family)
	}
	for id, account := range in.Banks {
		out.Banks[id] = cloneBankAccount(account)
	}
	for id, object := range in.Objects {
		out.Objects[id] = cloneObjectInstance(object)
	}
	for id, proto := range in.ObjectPrototypes {
		out.ObjectPrototypes[id] = cloneObjectPrototype(proto)
	}
	out.MarriageInvites = cloneMarriageInvites(in.MarriageInvites)
	return out
}

func cloneRoom(room model.Room) model.Room {
	room.Exits = cloneExits(room.Exits)
	room.CreatureIDs = append([]model.CreatureID(nil), room.CreatureIDs...)
	room.PlayerIDs = append([]model.PlayerID(nil), room.PlayerIDs...)
	room.Objects = cloneObjectRefList(room.Objects)
	room.Properties = cloneStringMap(room.Properties)
	room.Metadata = cloneMetadata(room.Metadata)
	return room
}

func cloneExits(exits []model.Exit) []model.Exit {
	if exits == nil {
		return nil
	}
	cloned := make([]model.Exit, len(exits))
	for i, exit := range exits {
		exit.Flags = append([]string(nil), exit.Flags...)
		exit.Metadata = cloneMetadata(exit.Metadata)
		cloned[i] = exit
	}
	return cloned
}

func clonePlayer(player model.Player) model.Player {
	player.Metadata = cloneMetadata(player.Metadata)
	return player
}

func cloneCreature(creature model.Creature) model.Creature {
	creature.Inventory = cloneObjectRefList(creature.Inventory)
	creature.Equipment = cloneEquipment(creature.Equipment)
	creature.Stats = cloneIntMap(creature.Stats)
	creature.Properties = cloneStringMap(creature.Properties)
	creature.Metadata = cloneMetadata(creature.Metadata)
	return creature
}

func cloneFamily(family model.Family) model.Family {
	family.Members = append([]model.FamilyMember(nil), family.Members...)
	for i := range family.Members {
		family.Members[i].Metadata = cloneMetadata(family.Members[i].Metadata)
	}
	family.Metadata = cloneMetadata(family.Metadata)
	return family
}

func cloneBankAccount(account model.BankAccount) model.BankAccount {
	account.Objects = cloneObjectRefList(account.Objects)
	account.Metadata = cloneMetadata(account.Metadata)
	return account
}

func cloneObjectPrototype(proto model.ObjectPrototype) model.ObjectPrototype {
	proto.Keywords = append([]string(nil), proto.Keywords...)
	proto.Properties = cloneStringMap(proto.Properties)
	proto.Metadata = cloneMetadata(proto.Metadata)
	return proto
}

func cloneObjectInstance(object model.ObjectInstance) model.ObjectInstance {
	object.Contents = cloneObjectRefList(object.Contents)
	object.Properties = cloneStringMap(object.Properties)
	object.Metadata = cloneMetadata(object.Metadata)
	return object
}

func cloneObjectRefList(refs model.ObjectRefList) model.ObjectRefList {
	refs.ObjectIDs = append([]model.ObjectInstanceID(nil), refs.ObjectIDs...)
	return refs
}

func cloneMetadata(metadata model.Metadata) model.Metadata {
	metadata.RawFields = cloneRawFields(metadata.RawFields)
	metadata.Tags = append([]string(nil), metadata.Tags...)
	metadata.Notes = append([]string(nil), metadata.Notes...)
	if metadata.PrototypeResolution != nil {
		resolution := *metadata.PrototypeResolution
		resolution.Candidates = append([]model.PrototypeResolutionCandidate(nil), resolution.Candidates...)
		metadata.PrototypeResolution = &resolution
	}
	return metadata
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneEquipment(in map[string]model.ObjectInstanceID) map[string]model.ObjectInstanceID {
	if in == nil {
		return nil
	}
	out := make(map[string]model.ObjectInstanceID, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRawFields(in map[string][]byte) map[string][]byte {
	if in == nil {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return invalidf("nil context")
	}
	return ctx.Err()
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func notFoundf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, args...))
}
