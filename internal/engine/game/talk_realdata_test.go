package game

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/persist/legacykr"
	"muhan/internal/session"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestRealObjmonTalkFilesParseLikeLegacyTalkCrtAct(t *testing.T) {
	root := realTalkDataRoot(t)
	dir := filepath.Join(root, "objmon", "talk")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}

	counts := map[string]int{}
	examples := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Path: path, Field: "talk"}, data)
		if err != nil {
			t.Fatalf("decode %q error = %v", path, err)
		}
		displayPath := realTalkDisplayPath(entry.Name())
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		for i, line := range lines {
			wantKey, wantAction := legacyTalkCrtActForRealDataTest(line)
			if wantAction.Type == "" {
				continue
			}
			gotKey := talkFileKey(line)
			if gotKey != wantKey {
				t.Fatalf("%s:%d key = %q, want C parser key %q", displayPath, i+1, gotKey, wantKey)
			}
			gotAction := talkFileActionFromLine(line)
			if gotAction != wantAction {
				t.Fatalf("%s:%d action = %+v, want C parser action %+v from %q", displayPath, i+1, gotAction, wantAction, line)
			}
			counts[wantAction.Type]++
			if _, ok := examples[wantAction.Type]; !ok {
				examples[wantAction.Type] = displayPath + ":" + strconvLine(i+1) + " " + strings.TrimSpace(line)
			}
		}
	}

	for _, typ := range []string{"ACTION", "GIVE", "ATTACK", "CAST"} {
		if counts[typ] == 0 {
			t.Fatalf("real objmon/talk scan found no %s directive; counts=%+v examples=%+v", typ, counts, examples)
		}
	}
	t.Logf("real objmon/talk directive counts=%+v examples=%+v", counts, examples)
}

func TestRealObjmonTalkActionNamesAreExecutable(t *testing.T) {
	root := realTalkDataRoot(t)
	dir := filepath.Join(root, "objmon", "talk")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}

	seen := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Path: path, Field: "talk"}, data)
		if err != nil {
			t.Fatalf("decode %q error = %v", path, err)
		}
		displayPath := realTalkDisplayPath(entry.Name())
		lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
		for i, line := range lines {
			_, action := legacyTalkCrtActForRealDataTest(line)
			if action.Type != "ACTION" {
				continue
			}
			if _, ok := legacyTalkActionName(action.Name); !ok {
				t.Fatalf("%s:%d ACTION %q is parsed but not executable by Go talk_action", displayPath, i+1, action.Name)
			}
			if _, ok := seen[action.Name]; !ok {
				seen[action.Name] = displayPath + ":" + strconvLine(i+1)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("real objmon/talk scan found no ACTION directives")
	}
	t.Logf("real objmon/talk ACTION names executable=%+v", seen)
}

func TestRealObjmonTalkFileEntriesLoadSideEffectDirectives(t *testing.T) {
	root := realTalkDataRoot(t)
	tests := []struct {
		name             string
		level            int
		topic            string
		responseContains string
		wantAction       talkFileAction
	}{
		{name: "초향", level: 50, topic: "안녕", responseContains: "당신은 누구죠", wantAction: talkFileAction{Type: "ACTION", Name: "보아"}},
		{name: "아가씨", level: 1, topic: "사랑", responseContains: "저도 사랑해요", wantAction: talkFileAction{Type: "ACTION", Name: "뽀뽀", Target: "PLAYER"}},
		{name: "스핑크스", level: 99, topic: "인간", responseContains: "정답을 맞추다니", wantAction: talkFileAction{Type: "ATTACK"}},
		{name: "아미타불", level: 25, topic: "치료", responseContains: "어때 많이 좋아졌는가", wantAction: talkFileAction{Type: "CAST", Name: "회복"}},
		{name: "떠돌이 검객", level: 60, topic: "이놈", responseContains: "다시는 내 앞에", wantAction: talkFileAction{Type: "CAST", Name: "뇌전"}},
		{name: "불의 공주", level: 1, topic: "용사", responseContains: "이 열쇠로 감옥", wantAction: talkFileAction{Type: "GIVE", Name: "104"}},
	}

	for _, tt := range tests {
		creature := model.Creature{DisplayName: tt.name, Level: tt.level}
		entry, loaded, ok, err := loadTalkFileEntry(root, model.Room{}, creature, tt.topic)
		if err != nil {
			t.Fatalf("%s/%s loadTalkFileEntry() error = %v", tt.name, tt.topic, err)
		}
		if !loaded || !ok {
			t.Fatalf("%s/%s loaded=%t ok=%t, want true/true", tt.name, tt.topic, loaded, ok)
		}
		if !strings.Contains(entry.Response, tt.responseContains) {
			t.Fatalf("%s/%s response = %q, want contains %q", tt.name, tt.topic, entry.Response, tt.responseContains)
		}
		if entry.Action != tt.wantAction {
			t.Fatalf("%s/%s action = %+v, want %+v", tt.name, tt.topic, entry.Action, tt.wantAction)
		}
	}
}

func TestTalkHandlerExecutesRealObjmonTalkPlayerAction(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:lady",
		Kind:        model.CreatureKindNPC,
		DisplayName: "아가씨",
		Level:       1,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
	})
	world := state.NewWorld(loaded)
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 4)
	bob := make(chan session.Command, 4)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")
	charlie := make(chan session.Command, 4)
	registerTestSession(t, loop, "s3", charlie, "player:charlie")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "아가씨 사랑 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	assertCommand(t, bob, session.Command{Write: "\nAlice가 아가씨에게 \"사랑\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n아가씨가 Alice에게 \"저도 사랑해요. ^^\"라고 이야기합니다.\n"})
	assertCommand(t, bob, session.Command{Write: "아가씨가 Alice에게 뽀뽀를 합니다.\n"})
	assertCommand(t, alice, session.Command{Write: "\n아가씨가 당신에게 \"저도 사랑해요. ^^\"라고 이야기합니다.\n아가씨가 당신에게 뽀뽀를 합니다.\n"})
	assertNoCommand(t, charlie)
}

func TestTalkHandlerRoutesRealObjmonTalkCastToRuntime(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:amita",
		Kind:        model.CreatureKindNPC,
		DisplayName: "아미타불",
		Level:       25,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
	})
	world := &talkRealDataCastWorld{World: state.NewWorld(loaded)}
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 4)
	bob := make(chan session.Command, 4)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "아미타불 치료 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	if world.spell != "회복" || world.casterID != "creature:amita" || world.targetID != "creature:alice" {
		t.Fatalf("CastTalkSpell spell/caster/target = %q/%q/%q, want 회복/creature:amita/creature:alice", world.spell, world.casterID, world.targetID)
	}
	assertCommand(t, bob, session.Command{Write: "\nAlice가 아미타불에게 \"치료\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n아미타불이 Alice에게 \"어때 많이 좋아졌는가?\"라고 이야기합니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n아미타불이 Alice에게 회복 주문을 겁니다.\n"})
	assertCommand(t, alice, session.Command{Write: "\n아미타불이 당신에게 \"어때 많이 좋아졌는가?\"라고 이야기합니다.\n\n아미타불이 당신에게 회복 주문을 겁니다.\n"})
}

func TestTalkHandlerAppliesRealObjmonKoreanCastFallback(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	aliceCreature := loaded.Creatures["creature:alice"]
	aliceCreature.Stats["hpCurrent"] = 20
	aliceCreature.Stats["hpMax"] = 30
	loaded.Creatures[aliceCreature.ID] = aliceCreature
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:amita",
		Kind:        model.CreatureKindNPC,
		DisplayName: "아미타불",
		Level:       25,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
		Stats:       map[string]int{"mpCurrent": 10, "mpMax": 10},
	})
	world := state.NewWorld(loaded)
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 4)
	bob := make(chan session.Command, 4)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "아미타불 치료 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	updatedAlice, ok := world.Creature("creature:alice")
	if !ok {
		t.Fatal("Creature(alice) missing")
	}
	if got, want := updatedAlice.Stats["hpCurrent"], 30; got != want {
		t.Fatalf("alice hpCurrent = %d, want %d", got, want)
	}
	updatedAmita, ok := world.Creature("creature:amita")
	if !ok {
		t.Fatal("Creature(amita) missing")
	}
	if got, want := updatedAmita.Stats["mpCurrent"], 5; got != want {
		t.Fatalf("amita mpCurrent = %d, want %d", got, want)
	}
	assertCommand(t, bob, session.Command{Write: "\nAlice가 아미타불에게 \"치료\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n아미타불이 Alice에게 \"어때 많이 좋아졌는가?\"라고 이야기합니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n아미타불이 Alice에게 회복 주문을 겁니다.\n"})
	assertCommand(t, alice, session.Command{Write: "\n아미타불이 당신에게 \"어때 많이 좋아졌는가?\"라고 이야기합니다.\n\n아미타불이 당신에게 회복 주문을 겁니다.\n"})
}

func TestTalkHandlerAppliesRealObjmonOffensiveCastFallback(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	aliceCreature := loaded.Creatures["creature:alice"]
	aliceCreature.Stats["hpCurrent"] = 200
	aliceCreature.Stats["hpMax"] = 200
	loaded.Creatures[aliceCreature.ID] = aliceCreature
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:wanderer",
		Kind:        model.CreatureKindNPC,
		DisplayName: "떠돌이 검객",
		Level:       60,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
		Stats:       map[string]int{"mpCurrent": 15, "mpMax": 15},
	})
	world := state.NewWorld(loaded)
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 6)
	bob := make(chan session.Command, 6)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")

	entry, loadedFile, ok, err := loadTalkFileEntry(root, model.Room{}, model.Creature{DisplayName: "떠돌이 검객", Level: 60}, "이놈")
	if err != nil {
		t.Fatalf("loadTalkFileEntry() error = %v", err)
	}
	if !loadedFile || !ok || entry.Action != (talkFileAction{Type: "CAST", Name: "뇌전"}) {
		t.Fatalf("real 떠돌이 검객/이놈 talk entry loaded=%t ok=%t action=%+v", loadedFile, ok, entry.Action)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "떠돌이 이놈 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	assertCommand(t, bob, session.Command{Write: "\nAlice가 떠돌이 검객에게 \"이놈\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n떠돌이 검객이 Alice에게 \"" + entry.Response + "\"라고 이야기합니다.\n"})
	bobCast := <-bob
	if !strings.Contains(bobCast.Write, "떠돌이 검객이 Alice에게 뇌전 주문을 외웠습니다.") ||
		!strings.Contains(bobCast.Write, "만큼의 피해를 입힙니다.") {
		t.Fatalf("bob cast output = %q, want real offensive talk cast room damage", bobCast.Write)
	}
	aliceCast := <-alice
	if !strings.Contains(aliceCast.Write, "떠돌이 검객이 당신에게 \""+entry.Response+"\"라고 이야기합니다.") ||
		!strings.Contains(aliceCast.Write, "떠돌이 검객이 당신에게 뇌전 주문을 외웠습니다.") ||
		!strings.Contains(aliceCast.Write, "만큼의 상처를 입혔습니다.") {
		t.Fatalf("alice cast output = %q, want real offensive talk cast target damage", aliceCast.Write)
	}

	updatedAlice, ok := world.Creature("creature:alice")
	if !ok {
		t.Fatal("Creature(alice) missing")
	}
	applied := 200 - updatedAlice.Stats["hpCurrent"]
	if applied < 21 || applied > 30 {
		t.Fatalf("real talk offensive damage = %d, want C SLGHTN 3d4+18 range [21,30]", applied)
	}
	assertTalkCreatureStat(t, world, "creature:wanderer", "mpCurrent", 0)
	wandererEnemies, err := world.CreatureEnemies("creature:wanderer")
	if err != nil {
		t.Fatalf("CreatureEnemies(wanderer) error = %v", err)
	}
	if !talkStringListContains(wandererEnemies, "Alice") {
		t.Fatalf("wanderer enemies = %+v, want Alice", wandererEnemies)
	}
	aliceEnemies, err := world.CreatureEnemies("creature:alice")
	if err != nil {
		t.Fatalf("CreatureEnemies(alice) error = %v", err)
	}
	if !talkStringListContains(aliceEnemies, "떠돌이 검객") {
		t.Fatalf("alice enemies = %+v, want 떠돌이 검객", aliceEnemies)
	}
}

func TestTalkHandlerExecutesRealObjmonTalkGivePrototype(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:fire-princess",
		Kind:        model.CreatureKindNPC,
		DisplayName: "불의 공주",
		Level:       1,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
	})
	protoID := legacyTalkGivePrototypeID(104)
	proto := realTalkObjectPrototype(t, root, protoID)
	if err := loaded.AddObjectPrototype(proto); err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld(loaded)
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 6)
	bob := make(chan session.Command, 6)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")

	entry, loadedFile, ok, err := loadTalkFileEntry(root, model.Room{}, model.Creature{DisplayName: "불의 공주", Level: 1}, "용사")
	if err != nil {
		t.Fatalf("loadTalkFileEntry() error = %v", err)
	}
	if !loadedFile || !ok || entry.Action != (talkFileAction{Type: "GIVE", Name: "104"}) {
		t.Fatalf("real 불의 공주/용사 talk entry loaded=%t ok=%t action=%+v", loadedFile, ok, entry.Action)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "불의 용사 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	objectID := assertTalkCreatureHasPrototype(t, world, "creature:alice", protoID, 1)
	object, ok := world.Object(objectID)
	if !ok {
		t.Fatalf("gift object %s missing", objectID)
	}
	objectName := giveObjectDisplayName(world, object)
	if objectName == "" {
		t.Fatalf("gift object %s display name is empty; real prototype=%+v", objectID, proto)
	}
	assertCommand(t, bob, session.Command{Write: "\nAlice가 불의 공주에게 \"용사\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n불의 공주가 Alice에게 \"" + entry.Response + "\"라고 이야기합니다.\n"})
	assertCommand(t, bob, session.Command{Write: renderGiveObjectRoom("불의 공주", "Alice", objectName)})
	assertCommand(t, alice, session.Command{Write: "\n불의 공주가 당신에게 \"" + entry.Response + "\"라고 이야기합니다.\n" + renderGiveObjectTarget("불의 공주", objectName)})
	t.Logf("real talk GIVE 불의 공주-1 topic=용사 prototype=%s object=%q", protoID, objectName)
}

func TestTalkHandlerRollsBackRealObjmonTalkGivePrototypeWhenFull(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:fire-princess",
		Kind:        model.CreatureKindNPC,
		DisplayName: "불의 공주",
		Level:       1,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
	})
	protoID := legacyTalkGivePrototypeID(104)
	if err := loaded.AddObjectPrototype(realTalkObjectPrototype(t, root, protoID)); err != nil {
		t.Fatal(err)
	}
	if err := loaded.AddObjectPrototype(model.ObjectPrototype{
		ID:          "prototype:talk-realdata-full-filler",
		DisplayName: "소지품",
	}); err != nil {
		t.Fatal(err)
	}
	aliceCreature := loaded.Creatures["creature:alice"]
	for i := 0; i <= giveInventoryLimit; i++ {
		objectID := model.ObjectInstanceID("object:talk-realdata-full-filler-" + strconv.Itoa(i))
		aliceCreature.Inventory.ObjectIDs = append(aliceCreature.Inventory.ObjectIDs, objectID)
		if err := loaded.AddObjectInstance(model.ObjectInstance{
			ID:          objectID,
			PrototypeID: "prototype:talk-realdata-full-filler",
			Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	loaded.Creatures[aliceCreature.ID] = aliceCreature

	world := state.NewWorld(loaded)
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 6)
	bob := make(chan session.Command, 6)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")

	entry, _, ok, err := loadTalkFileEntry(root, model.Room{}, model.Creature{DisplayName: "불의 공주", Level: 1}, "용사")
	if err != nil {
		t.Fatalf("loadTalkFileEntry() error = %v", err)
	}
	if !ok || entry.Action != (talkFileAction{Type: "GIVE", Name: "104"}) {
		t.Fatalf("real 불의 공주/용사 talk action = %+v, ok=%t", entry.Action, ok)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "불의 용사 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	assertCommand(t, bob, session.Command{Write: "\nAlice가 불의 공주에게 \"용사\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n불의 공주가 Alice에게 \"" + entry.Response + "\"라고 이야기합니다.\n"})
	assertNoCommand(t, bob)
	assertCommand(t, alice, session.Command{Write: "\n불의 공주가 당신에게 \"" + entry.Response + "\"라고 이야기합니다.\n" + talkGiveInventoryFullMessage()})
	assertTalkCreatureHasPrototype(t, world, "creature:alice", protoID, 0)
}

func TestTalkHandlerExecutesRealObjmonTalkAttackPrimer(t *testing.T) {
	root := realTalkDataRoot(t)
	loaded := socialWorld(t)
	mustAddLoopCreature(t, loaded, model.Creature{
		ID:          "creature:sphinx",
		Kind:        model.CreatureKindNPC,
		DisplayName: "스핑크스",
		Level:       99,
		RoomID:      "room:one",
		Metadata:    model.Metadata{Tags: []string{"talks"}},
	})
	world := state.NewWorld(loaded)
	if err := world.SetCreatureCooldown("creature:sphinx", "attack", 1000, 60); err != nil {
		t.Fatalf("SetCreatureCooldown() error = %v", err)
	}
	loop := NewLoop(enginecmd.Dispatcher{
		Registry: socialRegistry(t),
		Handlers: map[string]enginecmd.Handler{
			"talk": NewTalkHandlerWithRoot(world, root),
		},
	})
	alice := make(chan session.Command, 6)
	bob := make(chan session.Command, 6)
	registerTestSession(t, loop, "s1", alice, "player:alice")
	registerTestSession(t, loop, "s2", bob, "player:bob")

	entry, loadedFile, ok, err := loadTalkFileEntry(root, model.Room{}, model.Creature{DisplayName: "스핑크스", Level: 99}, "인간")
	if err != nil {
		t.Fatalf("loadTalkFileEntry() error = %v", err)
	}
	if !loadedFile || !ok || entry.Action != (talkFileAction{Type: "ATTACK"}) {
		t.Fatalf("real 스핑크스/인간 talk entry loaded=%t ok=%t action=%+v", loadedFile, ok, entry.Action)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "스핑크스 인간 대화"}); err != nil {
		t.Fatalf("HandleEvent() error = %v", err)
	}

	assertCommand(t, bob, session.Command{Write: "\nAlice가 스핑크스에게 \"인간\"에 관해 물어봅니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n스핑크스가 Alice에게 \"" + entry.Response + "\"라고 이야기합니다.\n"})
	assertCommand(t, bob, session.Command{Write: "\n스핑크스가 Alice를 공격합니다.\n"})
	assertCommand(t, alice, session.Command{Write: "\n스핑크스가 당신에게 \"" + entry.Response + "\"라고 이야기합니다.\n\n스핑크스가 당신을 공격합니다.\n"})

	enemies, err := world.CreatureEnemies("creature:sphinx")
	if err != nil {
		t.Fatalf("CreatureEnemies() error = %v", err)
	}
	if !talkStringListContains(enemies, "Alice") {
		t.Fatalf("sphinx enemies = %+v, want Alice after real talk ATTACK", enemies)
	}
	sphinx, ok := world.Creature("creature:sphinx")
	if !ok {
		t.Fatal("Creature(sphinx) missing")
	}
	if !talkMetadataHasTag(sphinx.Metadata, "was_attacked") {
		t.Fatalf("sphinx tags = %+v, want was_attacked combat primer", sphinx.Metadata.Tags)
	}
	if remaining, usable, err := world.UseCreatureCooldown("creature:sphinx", "attack", 1000, 2); err != nil {
		t.Fatalf("UseCreatureCooldown() error = %v", err)
	} else if !usable {
		t.Fatalf("attack cooldown still active for %d seconds; want expired for automatic combat", remaining)
	}
}

type talkRealDataCastWorld struct {
	*state.World
	spell    string
	casterID model.CreatureID
	targetID model.CreatureID
}

func (w *talkRealDataCastWorld) CastTalkSpell(caster model.Creature, target model.Creature, player model.Player, spell string) (bool, error) {
	w.spell = spell
	w.casterID = caster.ID
	w.targetID = target.ID
	return true, nil
}

func realTalkDataRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if pathExists(filepath.Join(dir, "objmon", "talk")) && pathExists(filepath.Join(dir, "src", "files3.c")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("legacy objmon/talk data root not found")
		}
		dir = parent
	}
}

func realTalkObjectPrototype(t *testing.T, root string, protoID model.PrototypeID) model.ObjectPrototype {
	t.Helper()
	summary, err := worldload.LoadRoot(root)
	if err != nil {
		t.Fatalf("LoadRoot(%s): %v", root, err)
	}
	if len(summary.Errors) != 0 {
		t.Fatalf("LoadRoot(%s) returned %d errors", root, len(summary.Errors))
	}
	proto, ok := summary.World.ObjectPrototypes[protoID]
	if !ok {
		t.Fatalf("real object prototype %s not found", protoID)
	}
	if strings.TrimSpace(proto.DisplayName) == "" {
		t.Fatalf("real object prototype %s has empty display name", protoID)
	}
	return proto
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func realTalkDisplayPath(name string) string {
	if utf8.ValidString(name) {
		return filepath.Join("objmon", "talk", name)
	}
	decoded, err := legacykr.DecodeEUCKR([]byte(name))
	if err != nil {
		return filepath.Join("objmon", "talk", name)
	}
	return filepath.Join("objmon", "talk", decoded)
}

func legacyTalkCrtActForRealDataTest(line string) (string, talkFileAction) {
	words := legacyTalkCWordsForRealDataTest(line, 4)
	if len(words) == 0 {
		return "", talkFileAction{}
	}
	key := words[0]
	if len(words) < 2 {
		return key, talkFileAction{}
	}
	switch words[1] {
	case "ATTACK":
		return key, talkFileAction{Type: "ATTACK"}
	case "ACTION":
		if len(words) < 3 {
			return key, talkFileAction{}
		}
		action := talkFileAction{Type: "ACTION", Name: words[2]}
		if len(words) > 3 {
			action.Target = words[3]
		}
		return key, action
	case "CAST":
		if len(words) < 3 {
			return key, talkFileAction{}
		}
		action := talkFileAction{Type: "CAST", Name: words[2]}
		if len(words) > 3 {
			action.Target = words[3]
		}
		return key, action
	case "GIVE":
		if len(words) < 3 {
			return key, talkFileAction{}
		}
		return key, talkFileAction{Type: "GIVE", Name: words[2]}
	default:
		return key, talkFileAction{}
	}
}

func legacyTalkCWordsForRealDataTest(line string, limit int) []string {
	words := make([]string, 0, limit)
	for pos := 0; pos < len(line) && len(words) < limit; {
		for pos < len(line) && legacyTalkCIsSpaceForRealDataTest(line[pos]) {
			pos++
		}
		if pos >= len(line) {
			break
		}
		start := pos
		for pos < len(line) {
			r, size := utf8.DecodeRuneInString(line[pos:])
			if size <= 0 || !legacyTalkCIsWordRuneForRealDataTest(r) {
				break
			}
			pos += size
		}
		if pos == start {
			_, size := utf8.DecodeRuneInString(line[pos:])
			if size <= 0 {
				break
			}
			pos += size
			continue
		}
		words = append(words, line[start:pos])
	}
	return words
}

func legacyTalkCIsSpaceForRealDataTest(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func legacyTalkCIsWordRuneForRealDataTest(r rune) bool {
	if r > 127 {
		return true
	}
	return r == '-' ||
		(r >= '0' && r <= '9') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z')
}

func strconvLine(n int) string {
	return strconv.Itoa(n)
}
