package state

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
)

func TestRealDataORENCHPrototypeRawFlagMaterializesAndSurvivesPlayerRestart(t *testing.T) {
	summary := loadRealDataSummary(t)
	protoID, proto := findRealDataORENCHPrototype(t, summary.World)
	playerID, creatureID, _ := findRealDataTargetPlayer(t, summary.World)

	runtime := NewWorld(summary.World)
	dbRoot := t.TempDir()
	runtime.SetDBRoot(dbRoot)

	runtime.rLockDomains(true, true, true, true, true, true, true)
	detected := runtime.objectHasRandomEnchantLocked(model.ObjectInstance{
		ID:          "object:real-data-orench-detection",
		PrototypeID: protoID,
		Quantity:    1,
		Location:    model.ObjectLocation{CreatureID: creatureID, Slot: "inventory"},
	})
	runtime.rUnlockDomains(true, true, true, true, true, true, true)
	if !detected {
		t.Fatalf("real prototype %s has raw ORENCH flag but state did not recognize it", protoID)
	}

	cloneID, err := runtime.CloneObjectToCreatureInventory(model.ObjectInstanceID(protoID), creatureID)
	if err != nil {
		t.Fatalf("CloneObjectToCreatureInventory(%s): %v", protoID, err)
	}
	created, ok := runtime.Object(cloneID)
	if !ok {
		t.Fatalf("created clone %s missing", cloneID)
	}
	if created.PrototypeID != protoID {
		t.Fatalf("created clone prototype = %s, want %s", created.PrototypeID, protoID)
	}
	if created.Location.CreatureID != creatureID {
		t.Fatalf("created clone location = %+v, want creature %s", created.Location, creatureID)
	}

	if err := runtime.SavePlayer(playerID); err != nil {
		t.Fatalf("SavePlayer(%s): %v", playerID, err)
	}
	restarted := NewWorld(summary.World)
	restarted.SetDBRoot(dbRoot)
	save, ok, err := LoadPlayer(dbRoot, playerID)
	if err != nil || !ok {
		t.Fatalf("LoadPlayer(%s) = ok %v err %v", playerID, ok, err)
	}
	if err := restarted.MergePlayerSaveIntoWorld(save); err != nil {
		t.Fatalf("MergePlayerSaveIntoWorld(%s): %v", playerID, err)
	}
	restored, ok := restarted.Object(cloneID)
	if !ok {
		t.Fatalf("restored clone %s missing after player save/restart", cloneID)
	}
	assertRealDataObjectEqual(t, restored, created)
	assertRealDataCreatureContainsObject(t, restarted, creatureID, cloneID)

	t.Logf("real ORENCH prototype %s name=%q path=%s tags=%v", protoID, proto.DisplayName, proto.Metadata.LegacyPath, proto.Metadata.Tags)
}

func TestRealDataDMCreateObjectFromPrototypeHonorsRawORENCHFlag(t *testing.T) {
	summary := loadRealDataSummary(t)
	protoID, proto := findRealDataORENCHPrototype(t, summary.World)
	playerID, creatureID, _ := findRealDataTargetPlayer(t, summary.World)

	runtime := NewWorld(summary.World)
	dbRoot := t.TempDir()
	runtime.SetDBRoot(dbRoot)

	instance, err := runtime.CreateObjectInstanceFromPrototype(protoID, creatureID)
	if err != nil {
		t.Fatalf("CreateObjectInstanceFromPrototype(%s): %v", protoID, err)
	}
	if instance.PrototypeID != protoID {
		t.Fatalf("created prototype = %s, want %s", instance.PrototypeID, protoID)
	}
	if instance.Location.CreatureID != creatureID {
		t.Fatalf("created location = %+v, want creature %s", instance.Location, creatureID)
	}

	runtime.rLockDomains(true, true, true, true, true, true, true)
	detected := runtime.objectHasRandomEnchantLocked(instance)
	runtime.rUnlockDomains(true, true, true, true, true, true, true)
	if !detected {
		t.Fatalf("DM-created real prototype %s lost raw ORENCH detection", protoID)
	}
	assertRealDataPlayerDirty(t, runtime, playerID)

	if err := runtime.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after DM create: %v", err)
	}
	restarted := restartRealDataWorldWithSaves(t, summary.World, dbRoot, []model.PlayerID{playerID}, nil)
	assertRealDataObjectEqualAfterRestart(t, restarted, instance.ID, instance)
	assertRealDataCreatureContainsObject(t, restarted, creatureID, instance.ID)

	t.Logf("real DM create ORENCH prototype %s name=%q path=%s tags=%v", protoID, proto.DisplayName, proto.Metadata.LegacyPath, proto.Metadata.Tags)
}

func TestRealDataNestedObjectCloneSurvivesPlayerAndRoomRestart(t *testing.T) {
	summary := loadRealDataSummary(t)
	sourceID, source := findRealDataNestedObject(t, summary.World)
	playerID, creatureID, roomID := findRealDataTargetPlayer(t, summary.World)

	runtime := NewWorld(summary.World)
	dbRoot := t.TempDir()
	runtime.SetDBRoot(dbRoot)

	cloneID, err := runtime.CloneObjectToCreatureInventory(sourceID, creatureID)
	if err != nil {
		t.Fatalf("CloneObjectToCreatureInventory(%s): %v", sourceID, err)
	}
	playerSnapshot := snapshotRealDataObjectTree(t, runtime, cloneID)
	if err := runtime.SavePlayer(playerID); err != nil {
		t.Fatalf("SavePlayer(%s) after nested clone: %v", playerID, err)
	}
	afterPlayerRestart := NewWorld(summary.World)
	afterPlayerRestart.SetDBRoot(dbRoot)
	playerSave, ok, err := LoadPlayer(dbRoot, playerID)
	if err != nil || !ok {
		t.Fatalf("LoadPlayer(%s) after nested clone = ok %v err %v", playerID, ok, err)
	}
	if err := afterPlayerRestart.MergePlayerSaveIntoWorld(playerSave); err != nil {
		t.Fatalf("MergePlayerSaveIntoWorld(%s) after nested clone: %v", playerID, err)
	}
	assertRealDataObjectTreeSnapshot(t, afterPlayerRestart, playerSnapshot)
	assertRealDataCreatureContainsObject(t, afterPlayerRestart, creatureID, cloneID)

	if err := runtime.MoveObject(cloneID, model.ObjectLocation{RoomID: roomID}); err != nil {
		t.Fatalf("MoveObject(%s -> %s): %v", cloneID, roomID, err)
	}
	roomSnapshot := snapshotRealDataObjectTree(t, runtime, cloneID)
	if err := runtime.SavePlayer(playerID); err != nil {
		t.Fatalf("SavePlayer(%s) after drop: %v", playerID, err)
	}
	if err := runtime.SaveRoomObjects(roomID); err != nil {
		t.Fatalf("SaveRoomObjects(%s): %v", roomID, err)
	}

	afterRoomRestart := NewWorld(summary.World)
	afterRoomRestart.SetDBRoot(dbRoot)
	playerSave, ok, err = LoadPlayer(dbRoot, playerID)
	if err != nil || !ok {
		t.Fatalf("LoadPlayer(%s) after drop = ok %v err %v", playerID, ok, err)
	}
	if err := afterRoomRestart.MergePlayerSaveIntoWorld(playerSave); err != nil {
		t.Fatalf("MergePlayerSaveIntoWorld(%s) after drop: %v", playerID, err)
	}
	roomSave, ok, err := LoadRoomObjects(dbRoot, roomID)
	if err != nil || !ok {
		t.Fatalf("LoadRoomObjects(%s) = ok %v err %v", roomID, ok, err)
	}
	if err := afterRoomRestart.MergeRoomObjectsSaveIntoWorld(roomSave); err != nil {
		t.Fatalf("MergeRoomObjectsSaveIntoWorld(%s): %v", roomID, err)
	}
	assertRealDataObjectTreeSnapshot(t, afterRoomRestart, roomSnapshot)
	assertRealDataRoomContainsObject(t, afterRoomRestart, roomID, cloneID)
	assertRealDataCreatureDoesNotContainObject(t, afterRoomRestart, creatureID, cloneID)

	t.Logf("real nested source %s prototype=%s path=%s children=%d", sourceID, source.PrototypeID, source.Metadata.LegacyPath, len(source.Contents.ObjectIDs))
}

func TestRealDataMaterializedObjectsUseCreateGiveDropDirtyFlushPath(t *testing.T) {
	summary := loadRealDataSummary(t)
	orenchProtoID, orenchProto := findRealDataORENCHPrototype(t, summary.World)
	nestedSourceID, nestedSource := findRealDataNestedObject(t, summary.World)
	fromPlayerID, fromCreatureID, _, toPlayerID, toCreatureID, dropRoomID := findRealDataTargetPlayerPair(t, summary.World)

	orenchWorld := NewWorld(summary.World)
	orenchDBRoot := t.TempDir()
	orenchWorld.SetDBRoot(orenchDBRoot)

	orenchID, err := orenchWorld.CloneObjectToCreatureInventory(model.ObjectInstanceID(orenchProtoID), fromCreatureID)
	if err != nil {
		t.Fatalf("CloneObjectToCreatureInventory(%s): %v", orenchProtoID, err)
	}
	assertRealDataPlayerDirty(t, orenchWorld, fromPlayerID)
	orenchCreated, ok := orenchWorld.Object(orenchID)
	if !ok {
		t.Fatalf("created real ORENCH object %s missing", orenchID)
	}
	if err := orenchWorld.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after ORENCH create: %v", err)
	}
	afterORENCHCreate := restartRealDataWorldWithSaves(t, summary.World, orenchDBRoot, []model.PlayerID{fromPlayerID}, nil)
	assertRealDataObjectEqualAfterRestart(t, afterORENCHCreate, orenchID, orenchCreated)
	assertRealDataCreatureContainsObject(t, afterORENCHCreate, fromCreatureID, orenchID)

	if err := orenchWorld.MoveObject(orenchID, model.ObjectLocation{CreatureID: toCreatureID, Slot: "inventory"}); err != nil {
		t.Fatalf("MoveObject(%s -> %s): %v", orenchID, toCreatureID, err)
	}
	assertRealDataPlayerDirty(t, orenchWorld, fromPlayerID)
	assertRealDataPlayerDirty(t, orenchWorld, toPlayerID)
	orenchGiven, ok := orenchWorld.Object(orenchID)
	if !ok {
		t.Fatalf("given real ORENCH object %s missing", orenchID)
	}
	if err := orenchWorld.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after ORENCH give: %v", err)
	}
	afterORENCHGive := restartRealDataWorldWithSaves(t, summary.World, orenchDBRoot, []model.PlayerID{fromPlayerID, toPlayerID}, nil)
	assertRealDataObjectEqualAfterRestart(t, afterORENCHGive, orenchID, orenchGiven)
	assertRealDataCreatureDoesNotContainObject(t, afterORENCHGive, fromCreatureID, orenchID)
	assertRealDataCreatureContainsObject(t, afterORENCHGive, toCreatureID, orenchID)

	if err := orenchWorld.MoveObject(orenchID, model.ObjectLocation{RoomID: dropRoomID}); err != nil {
		t.Fatalf("MoveObject(%s -> %s): %v", orenchID, dropRoomID, err)
	}
	assertRealDataPlayerDirty(t, orenchWorld, toPlayerID)
	assertRealDataRoomObjectsDirty(t, orenchWorld, dropRoomID)
	orenchDropped, ok := orenchWorld.Object(orenchID)
	if !ok {
		t.Fatalf("dropped real ORENCH object %s missing", orenchID)
	}
	if err := orenchWorld.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after ORENCH drop: %v", err)
	}
	if err := orenchWorld.FlushDirtyRoomObjects(0); err != nil {
		t.Fatalf("FlushDirtyRoomObjects after ORENCH drop: %v", err)
	}
	afterORENCHDrop := restartRealDataWorldWithSaves(t, summary.World, orenchDBRoot, []model.PlayerID{toPlayerID}, []model.RoomID{dropRoomID})
	assertRealDataObjectEqualAfterRestart(t, afterORENCHDrop, orenchID, orenchDropped)
	assertRealDataCreatureDoesNotContainObject(t, afterORENCHDrop, toCreatureID, orenchID)
	assertRealDataRoomContainsObject(t, afterORENCHDrop, dropRoomID, orenchID)

	nestedWorld := NewWorld(summary.World)
	nestedDBRoot := t.TempDir()
	nestedWorld.SetDBRoot(nestedDBRoot)

	nestedID, err := nestedWorld.CloneObjectToCreatureInventory(nestedSourceID, fromCreatureID)
	if err != nil {
		t.Fatalf("CloneObjectToCreatureInventory(%s): %v", nestedSourceID, err)
	}
	assertRealDataPlayerDirty(t, nestedWorld, fromPlayerID)
	nestedCreated := snapshotRealDataObjectTree(t, nestedWorld, nestedID)
	if err := nestedWorld.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after nested create: %v", err)
	}
	afterNestedCreate := restartRealDataWorldWithSaves(t, summary.World, nestedDBRoot, []model.PlayerID{fromPlayerID}, nil)
	assertRealDataObjectTreeSnapshot(t, afterNestedCreate, nestedCreated)
	assertRealDataCreatureContainsObject(t, afterNestedCreate, fromCreatureID, nestedID)

	if err := nestedWorld.MoveObject(nestedID, model.ObjectLocation{CreatureID: toCreatureID, Slot: "inventory"}); err != nil {
		t.Fatalf("MoveObject(%s -> %s): %v", nestedID, toCreatureID, err)
	}
	assertRealDataPlayerDirty(t, nestedWorld, fromPlayerID)
	assertRealDataPlayerDirty(t, nestedWorld, toPlayerID)
	nestedGiven := snapshotRealDataObjectTree(t, nestedWorld, nestedID)
	if err := nestedWorld.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after nested give: %v", err)
	}
	afterNestedGive := restartRealDataWorldWithSaves(t, summary.World, nestedDBRoot, []model.PlayerID{fromPlayerID, toPlayerID}, nil)
	assertRealDataObjectTreeSnapshot(t, afterNestedGive, nestedGiven)
	assertRealDataCreatureDoesNotContainObject(t, afterNestedGive, fromCreatureID, nestedID)
	assertRealDataCreatureContainsObject(t, afterNestedGive, toCreatureID, nestedID)

	if err := nestedWorld.MoveObject(nestedID, model.ObjectLocation{RoomID: dropRoomID}); err != nil {
		t.Fatalf("MoveObject(%s -> %s): %v", nestedID, dropRoomID, err)
	}
	assertRealDataPlayerDirty(t, nestedWorld, toPlayerID)
	assertRealDataRoomObjectsDirty(t, nestedWorld, dropRoomID)
	nestedDropped := snapshotRealDataObjectTree(t, nestedWorld, nestedID)
	if err := nestedWorld.FlushDirtyPlayersAndBanks(0); err != nil {
		t.Fatalf("FlushDirtyPlayersAndBanks after nested drop: %v", err)
	}
	if err := nestedWorld.FlushDirtyRoomObjects(0); err != nil {
		t.Fatalf("FlushDirtyRoomObjects after nested drop: %v", err)
	}
	afterNestedDrop := restartRealDataWorldWithSaves(t, summary.World, nestedDBRoot, []model.PlayerID{toPlayerID}, []model.RoomID{dropRoomID})
	assertRealDataObjectTreeSnapshot(t, afterNestedDrop, nestedDropped)
	assertRealDataCreatureDoesNotContainObject(t, afterNestedDrop, toCreatureID, nestedID)
	assertRealDataRoomContainsObject(t, afterNestedDrop, dropRoomID, nestedID)

	t.Logf("real ORENCH prototype %s name=%q path=%s", orenchProtoID, orenchProto.DisplayName, orenchProto.Metadata.LegacyPath)
	t.Logf("real nested source %s prototype=%s path=%s children=%d", nestedSourceID, nestedSource.PrototypeID, nestedSource.Metadata.LegacyPath, len(nestedSource.Contents.ObjectIDs))
}

func loadRealDataSummary(t *testing.T) worldload.Summary {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "objmon")); err != nil {
		t.Skipf("legacy objmon data not available under %s: %v", root, err)
	}
	summary, err := worldload.LoadRoot(root)
	if err != nil {
		t.Fatalf("LoadRoot(%s): %v", root, err)
	}
	if len(summary.Errors) != 0 {
		t.Fatalf("LoadRoot(%s) returned %d errors", root, len(summary.Errors))
	}
	return summary
}

func findRealDataORENCHPrototype(t *testing.T, loaded *worldload.World) (model.PrototypeID, model.ObjectPrototype) {
	t.Helper()

	ids := make([]model.PrototypeID, 0, len(loaded.ObjectPrototypes))
	for id := range loaded.ObjectPrototypes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		proto := loaded.ObjectPrototypes[id]
		if proto.Metadata.LegacyKind == "objectPrototype" &&
			strings.HasPrefix(proto.Metadata.LegacyPath, "objmon/") &&
			metadataHasLegacyObjectFlag(proto.Metadata, legacyObjectRandomEnchantmentFlagBit) {
			return id, proto
		}
	}
	t.Fatalf("no real objmon object prototype with raw ORENCH flag bit %d found", legacyObjectRandomEnchantmentFlagBit)
	return "", model.ObjectPrototype{}
}

func findRealDataNestedObject(t *testing.T, loaded *worldload.World) (model.ObjectInstanceID, model.ObjectInstance) {
	t.Helper()

	ids := make([]model.ObjectInstanceID, 0, len(loaded.Objects))
	for id := range loaded.Objects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var fallbackID model.ObjectInstanceID
	var fallback model.ObjectInstance
	for _, id := range ids {
		object := loaded.Objects[id]
		if len(object.Contents.ObjectIDs) == 0 || !realDataObjectTreeComplete(loaded.Objects, id, map[model.ObjectInstanceID]struct{}{}) {
			continue
		}
		size := realDataObjectTreeSize(loaded.Objects, id, map[model.ObjectInstanceID]struct{}{})
		if size > 1 && size <= 16 {
			return id, object
		}
		if fallbackID.IsZero() {
			fallbackID, fallback = id, object
		}
	}
	if !fallbackID.IsZero() {
		return fallbackID, fallback
	}
	t.Fatal("no complete real nested object tree found")
	return "", model.ObjectInstance{}
}

func findRealDataTargetPlayer(t *testing.T, loaded *worldload.World) (model.PlayerID, model.CreatureID, model.RoomID) {
	t.Helper()

	ids := make([]model.PlayerID, 0, len(loaded.Players))
	for id := range loaded.Players {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		if strings.Contains(string(id), "/") {
			continue
		}
		player := loaded.Players[id]
		if player.CreatureID.IsZero() {
			continue
		}
		creature, ok := loaded.Creatures[player.CreatureID]
		if !ok || creature.PlayerID != id {
			continue
		}
		roomID := creature.RoomID
		if roomID.IsZero() {
			roomID = player.RoomID
		}
		if roomID.IsZero() {
			continue
		}
		if _, ok := loaded.Rooms[roomID]; !ok {
			continue
		}
		return id, player.CreatureID, roomID
	}
	t.Fatal("no real target player with creature and room found")
	return "", "", ""
}

func findRealDataTargetPlayerPair(t *testing.T, loaded *worldload.World) (model.PlayerID, model.CreatureID, model.RoomID, model.PlayerID, model.CreatureID, model.RoomID) {
	t.Helper()

	ids := make([]model.PlayerID, 0, len(loaded.Players))
	for id := range loaded.Players {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	type candidate struct {
		playerID   model.PlayerID
		creatureID model.CreatureID
		roomID     model.RoomID
	}
	candidates := make([]candidate, 0, 2)
	for _, id := range ids {
		if strings.Contains(string(id), "/") {
			continue
		}
		player := loaded.Players[id]
		if player.CreatureID.IsZero() {
			continue
		}
		creature, ok := loaded.Creatures[player.CreatureID]
		if !ok || creature.PlayerID != id {
			continue
		}
		roomID := creature.RoomID
		if roomID.IsZero() {
			roomID = player.RoomID
		}
		if roomID.IsZero() {
			continue
		}
		if _, ok := loaded.Rooms[roomID]; !ok {
			continue
		}
		candidates = append(candidates, candidate{playerID: id, creatureID: player.CreatureID, roomID: roomID})
		if len(candidates) == 2 {
			return candidates[0].playerID, candidates[0].creatureID, candidates[0].roomID,
				candidates[1].playerID, candidates[1].creatureID, candidates[1].roomID
		}
	}
	t.Fatal("need two real target players with creature and room")
	return "", "", "", "", "", ""
}

func realDataObjectTreeComplete(objects map[model.ObjectInstanceID]model.ObjectInstance, id model.ObjectInstanceID, seen map[model.ObjectInstanceID]struct{}) bool {
	if _, ok := seen[id]; ok {
		return false
	}
	object, ok := objects[id]
	if !ok {
		return false
	}
	seen[id] = struct{}{}
	for _, childID := range object.Contents.ObjectIDs {
		if !realDataObjectTreeComplete(objects, childID, seen) {
			return false
		}
	}
	delete(seen, id)
	return true
}

func realDataObjectTreeSize(objects map[model.ObjectInstanceID]model.ObjectInstance, id model.ObjectInstanceID, seen map[model.ObjectInstanceID]struct{}) int {
	if _, ok := seen[id]; ok {
		return 0
	}
	object, ok := objects[id]
	if !ok {
		return 0
	}
	seen[id] = struct{}{}
	size := 1
	for _, childID := range object.Contents.ObjectIDs {
		size += realDataObjectTreeSize(objects, childID, seen)
	}
	return size
}

func snapshotRealDataObjectTree(t *testing.T, runtime *World, rootID model.ObjectInstanceID) map[model.ObjectInstanceID]model.ObjectInstance {
	t.Helper()

	snapshot := map[model.ObjectInstanceID]model.ObjectInstance{}
	var visit func(model.ObjectInstanceID)
	visit = func(id model.ObjectInstanceID) {
		if _, seen := snapshot[id]; seen {
			t.Fatalf("object tree cycle or duplicate at %s", id)
		}
		object, ok := runtime.Object(id)
		if !ok {
			t.Fatalf("object %s missing while snapshotting tree rooted at %s", id, rootID)
		}
		snapshot[id] = object
		for _, childID := range object.Contents.ObjectIDs {
			visit(childID)
		}
	}
	visit(rootID)
	if len(snapshot) < 2 {
		t.Fatalf("object tree rooted at %s has %d object(s), want nested tree", rootID, len(snapshot))
	}
	return snapshot
}

func assertRealDataObjectTreeSnapshot(t *testing.T, runtime *World, want map[model.ObjectInstanceID]model.ObjectInstance) {
	t.Helper()

	ids := make([]model.ObjectInstanceID, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		got, ok := runtime.Object(id)
		if !ok {
			t.Fatalf("object %s missing after restart", id)
		}
		assertRealDataObjectEqual(t, got, want[id])
	}
}

func assertRealDataObjectEqualAfterRestart(t *testing.T, runtime *World, objectID model.ObjectInstanceID, want model.ObjectInstance) {
	t.Helper()

	got, ok := runtime.Object(objectID)
	if !ok {
		t.Fatalf("object %s missing after restart", objectID)
	}
	assertRealDataObjectEqual(t, got, want)
}

func assertRealDataObjectEqual(t *testing.T, got, want model.ObjectInstance) {
	t.Helper()

	if got.ID != want.ID ||
		got.PrototypeID != want.PrototypeID ||
		got.DisplayNameOverride != want.DisplayNameOverride ||
		got.Quantity != want.Quantity ||
		got.Location != want.Location ||
		!slices.Equal(got.Contents.ObjectIDs, want.Contents.ObjectIDs) ||
		!reflect.DeepEqual(got.Properties, want.Properties) ||
		!slices.Equal(got.Metadata.Tags, want.Metadata.Tags) ||
		!reflect.DeepEqual(got.Metadata.RawFields, want.Metadata.RawFields) {
		t.Fatalf("object mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func restartRealDataWorldWithSaves(t *testing.T, loaded *worldload.World, dbRoot string, playerIDs []model.PlayerID, roomIDs []model.RoomID) *World {
	t.Helper()

	runtime := NewWorld(loaded)
	runtime.SetDBRoot(dbRoot)
	for _, playerID := range playerIDs {
		save, ok, err := LoadPlayer(dbRoot, playerID)
		if err != nil || !ok {
			t.Fatalf("LoadPlayer(%s) = ok %v err %v", playerID, ok, err)
		}
		if err := runtime.MergePlayerSaveIntoWorld(save); err != nil {
			t.Fatalf("MergePlayerSaveIntoWorld(%s): %v", playerID, err)
		}
	}
	for _, roomID := range roomIDs {
		save, ok, err := LoadRoomObjects(dbRoot, roomID)
		if err != nil || !ok {
			t.Fatalf("LoadRoomObjects(%s) = ok %v err %v", roomID, ok, err)
		}
		if err := runtime.MergeRoomObjectsSaveIntoWorld(save); err != nil {
			t.Fatalf("MergeRoomObjectsSaveIntoWorld(%s): %v", roomID, err)
		}
	}
	return runtime
}

func assertRealDataPlayerDirty(t *testing.T, runtime *World, playerID model.PlayerID) {
	t.Helper()

	runtime.dirtyMu.Lock()
	_, ok := runtime.playerDirty[playerID]
	runtime.dirtyMu.Unlock()
	if !ok {
		t.Fatalf("player %s was not marked dirty", playerID)
	}
}

func assertRealDataRoomObjectsDirty(t *testing.T, runtime *World, roomID model.RoomID) {
	t.Helper()

	runtime.dirtyMu.Lock()
	_, ok := runtime.roomObjectDirty[roomID]
	runtime.dirtyMu.Unlock()
	if !ok {
		t.Fatalf("room %s objects were not marked dirty", roomID)
	}
}

func assertRealDataCreatureContainsObject(t *testing.T, runtime *World, creatureID model.CreatureID, objectID model.ObjectInstanceID) {
	t.Helper()

	creature, ok := runtime.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %s", creatureID)
	}
	if !slices.Contains(creature.Inventory.ObjectIDs, objectID) {
		t.Fatalf("creature %s inventory = %+v, want %s", creatureID, creature.Inventory.ObjectIDs, objectID)
	}
}

func assertRealDataCreatureDoesNotContainObject(t *testing.T, runtime *World, creatureID model.CreatureID, objectID model.ObjectInstanceID) {
	t.Helper()

	creature, ok := runtime.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %s", creatureID)
	}
	if slices.Contains(creature.Inventory.ObjectIDs, objectID) {
		t.Fatalf("creature %s inventory = %+v, did not want %s", creatureID, creature.Inventory.ObjectIDs, objectID)
	}
}

func assertRealDataRoomContainsObject(t *testing.T, runtime *World, roomID model.RoomID, objectID model.ObjectInstanceID) {
	t.Helper()

	room, ok := runtime.Room(roomID)
	if !ok {
		t.Fatalf("missing room %s", roomID)
	}
	if !slices.Contains(room.Objects.ObjectIDs, objectID) {
		t.Fatalf("room %s objects = %+v, want %s", roomID, room.Objects.ObjectIDs, objectID)
	}
}
