package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"muhan/internal/commandspec"
	"muhan/internal/engine/game"
	"muhan/internal/persist/legacycrypt"
	"muhan/internal/session"
)

func TestServerLoopPasswdQueuesRuntimePasswordSave(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "암호", Number: 78, Handler: "passwd"},
	)
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "암호")
	assertServerCommandContains(t, commands, session.Command{}, "현재 암호를 입력하십시요: ")
	handleServerTestLine(t, loop, "s1", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 입력하십시요: ")
	handleServerTestLine(t, loop, "s1", "newpass")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 다시 넣으십시요: ")
	handleServerTestLine(t, loop, "s1", "newpass")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "암호가 변경되었습니다.")

	player, ok := inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice")
	}
	if !legacycrypt.Verify("newpass", legacyPasswordHash(inputs.world, player)) {
		t.Fatalf("password hash was not changed to newpass")
	}
}

func TestServerLoopPasswdAcceptsKoreanPasswordAtLegacyByteLimit(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "암호", Number: 78, Handler: "passwd"},
	)
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "암호")
	assertServerCommandContains(t, commands, session.Command{}, "현재 암호를 입력하십시요: ")
	handleServerTestLine(t, loop, "s1", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 입력하십시요: ")
	handleServerTestLine(t, loop, "s1", "가나다라마바사")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 다시 넣으십시요: ")
	handleServerTestLine(t, loop, "s1", "가나다라마바사")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "암호가 변경되었습니다.")

	player, ok := inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice")
	}
	if !legacycrypt.Verify("가나다라마바사", legacyPasswordHash(inputs.world, player)) {
		t.Fatalf("password hash was not changed to accepted Korean password")
	}
}

func TestServerLoopPasswdPendingConfirmDoesNotRunAfterDisconnect(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "암호", Number: 78, Handler: "passwd"},
	)
	player, ok := inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice")
	}
	oldHash := legacyPasswordHash(inputs.world, player)
	if !legacycrypt.Verify("1234", oldHash) {
		t.Fatalf("initial password hash does not verify 1234: %q", oldHash)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "암호")
	assertServerCommandContains(t, commands, session.Command{}, "현재 암호를 입력하십시요: ")
	handleServerTestLine(t, loop, "s1", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 입력하십시요: ")
	handleServerTestLine(t, loop, "s1", "newpass")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 다시 넣으십시요: ")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(closed) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "newpass"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	select {
	case got := <-commands:
		t.Fatalf("unexpected command after closed passwd prompt: %+v", got)
	default:
	}

	player, ok = inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice after closed passwd prompt")
	}
	gotHash := legacyPasswordHash(inputs.world, player)
	// After bcrypt migration, the hash may have been upgraded from DES to
	// bcrypt during the current-password verification step. The important
	// invariant is that the original password ("1234") still verifies and
	// the new password ("newpass") was never applied.
	if !legacycrypt.Verify("1234", gotHash) {
		t.Fatalf("original password no longer verifies after closed passwd prompt: hash %q", gotHash)
	}
	if legacycrypt.Verify("newpass", gotHash) {
		t.Fatal("closed passwd prompt changed password to newpass")
	}
}

func TestServerLoopSuicideFinalizesAndCleansFiles(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "자살", Number: 76, Handler: "suicide"},
	)
	if err := inputs.world.SetCreatureStat("creature:alice", "level", 6); err != nil {
		t.Fatalf("raise alice level: %v", err)
	}
	root := inputs.summary.Root
	aliasPath := filepath.Join(inputs.summary.Root, "player", "alias", "Alice")
	legacyPlayerPath := filepath.Join(root, "player", "temp", "Alice")
	playerJSONPath := filepath.Join(root, "player", "json", "Alice.json")
	playerIDJSONPath := filepath.Join(root, "player", "json", "alice.json")
	bankPath := filepath.Join(root, "player", "bank", "Alice")
	bankJSONPath := filepath.Join(root, "player", "bank", "json", "Alice.json")
	familyMemberPath := filepath.Join(root, "player", "family", "family_member_7")
	writeServerSuicideFixtureFile(t, aliasPath, "단축\n북\n~!\n~!\n")
	writeServerSuicideFixtureFile(t, legacyPlayerPath, "legacy-player")
	writeServerSuicideFixtureFile(t, playerJSONPath, "{}")
	writeServerSuicideFixtureFile(t, playerIDJSONPath, "{}")
	writeServerSuicideFixtureFile(t, bankPath, "legacy-bank")
	writeServerSuicideFixtureFile(t, bankJSONPath, "{}")
	writeServerSuicideFixtureFile(t, familyMemberPath, "8 Alice\n4 Bob\n0 패거리7\n")

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "자살")
	assertServerCommandContains(t, commands, session.Command{},
		"당신에 관한 데이터를 완전히 삭제합니다.",
		"당신의 현재 암호를 넣어주십시요",
	)
	handleServerTestLine(t, loop, "s1", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "찐짜로? (찐짜로/뻥으로)")
	handleServerTestLine(t, loop, "s1", "찐짜로")
	assertServerCommandContains(t, commands, session.Command{}, "### Alice님이 자살신청을 하였습니다.")
	assertServerCommand(t, commands, session.Command{Close: true})

	for _, path := range []string{aliasPath, legacyPlayerPath, playerJSONPath, playerIDJSONPath, bankPath, bankJSONPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s stat after suicide = %v, want not exist", path, err)
		}
	}
	if _, ok := inputs.world.Player("player:alice"); ok {
		t.Fatal("player still exists after finalized suicide")
	}
	if _, ok := inputs.world.Creature("creature:alice"); ok {
		t.Fatal("creature still exists after finalized suicide")
	}
	familyRaw, err := os.ReadFile(familyMemberPath)
	if err != nil {
		t.Fatalf("read family member file: %v", err)
	}
	if got := string(familyRaw); got != "4 Bob\n0 패거리7\n" {
		t.Fatalf("family member file = %q, want Alice removed", got)
	}
}

func TestServerLoopLowLevelQuitCleansFilesAndRuntimePlayerLikeLegacy(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.SetCreatureStat("creature:alice", "class", 4); err != nil {
		t.Fatalf("set alice class: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "level", 5); err != nil {
		t.Fatalf("set alice level: %v", err)
	}
	player, ok := inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice")
	}
	room, ok := inputs.world.Room(player.RoomID)
	if !ok {
		t.Fatalf("missing alice room %q", player.RoomID)
	}
	if !serverPlayerListContains(room.PlayerIDs, player.ID) || !serverCreatureListContains(room.CreatureIDs, player.CreatureID) {
		t.Fatalf("alice is not indexed in room before quit: %+v", room)
	}

	root := inputs.summary.Root
	aliasPath := filepath.Join(root, "player", "alias", "Alice")
	legacyPlayerPath := filepath.Join(root, "player", "temp", "Alice")
	playerJSONPath := filepath.Join(root, "player", "json", "Alice.json")
	playerIDJSONPath := filepath.Join(root, "player", "json", "alice.json")
	bankPath := filepath.Join(root, "player", "bank", "Alice")
	bankJSONPath := filepath.Join(root, "player", "bank", "json", "Alice.json")
	familyMemberPath := filepath.Join(root, "player", "family", "family_member_7")
	writeServerSuicideFixtureFile(t, aliasPath, "단축\n북\n~!\n~!\n")
	writeServerSuicideFixtureFile(t, legacyPlayerPath, "legacy-player")
	writeServerSuicideFixtureFile(t, playerJSONPath, "{}")
	writeServerSuicideFixtureFile(t, playerIDJSONPath, "{}")
	writeServerSuicideFixtureFile(t, bankPath, "legacy-bank")
	writeServerSuicideFixtureFile(t, bankJSONPath, "{}")
	writeServerSuicideFixtureFile(t, familyMemberPath, "8 Alice\n4 Bob\n0 패거리7\n")

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "끝")
	assertServerCommand(t, commands, session.Command{
		Write: "안녕히 가세요.\n",
		Close: true,
	})

	for _, path := range []string{aliasPath, legacyPlayerPath, playerJSONPath, playerIDJSONPath, bankPath, bankJSONPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s stat after low-level quit = %v, want not exist", path, err)
		}
	}
	if _, ok := inputs.world.Player(player.ID); ok {
		t.Fatal("player still exists after low-level quit")
	}
	if _, ok := inputs.world.Creature(player.CreatureID); ok {
		t.Fatal("creature still exists after low-level quit")
	}
	room, ok = inputs.world.Room(player.RoomID)
	if !ok {
		t.Fatalf("missing alice room %q after quit", player.RoomID)
	}
	if serverPlayerListContains(room.PlayerIDs, player.ID) {
		t.Fatalf("room player ids = %v, want alice removed", room.PlayerIDs)
	}
	if serverCreatureListContains(room.CreatureIDs, player.CreatureID) {
		t.Fatalf("room creature ids = %v, want alice removed", room.CreatureIDs)
	}
	familyRaw, err := os.ReadFile(familyMemberPath)
	if err != nil {
		t.Fatalf("read family member file: %v", err)
	}
	if got := string(familyRaw); got != "8 Alice\n4 Bob\n0 패거리7\n" {
		t.Fatalf("family member file = %q, want low-level quit to preserve membership", got)
	}
}

func TestServerLoopSuicidePendingConfirmDoesNotRunAfterDisconnect(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "자살", Number: 76, Handler: "suicide"},
	)
	if err := inputs.world.SetCreatureStat("creature:alice", "level", 6); err != nil {
		t.Fatalf("raise alice level: %v", err)
	}
	root := inputs.summary.Root
	aliasPath := filepath.Join(root, "player", "alias", "Alice")
	legacyPlayerPath := filepath.Join(root, "player", "temp", "Alice")
	playerJSONPath := filepath.Join(root, "player", "json", "Alice.json")
	bankPath := filepath.Join(root, "player", "bank", "Alice")
	writeServerSuicideFixtureFile(t, aliasPath, "단축\n북\n~!\n~!\n")
	writeServerSuicideFixtureFile(t, legacyPlayerPath, "legacy-player")
	writeServerSuicideFixtureFile(t, playerJSONPath, "{}")
	writeServerSuicideFixtureFile(t, bankPath, "legacy-bank")

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "자살")
	assertServerCommandContains(t, commands, session.Command{},
		"당신에 관한 데이터를 완전히 삭제합니다.",
		"당신의 현재 암호를 넣어주십시요",
	)
	handleServerTestLine(t, loop, "s1", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "찐짜로? (찐짜로/뻥으로)")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(closed) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "찐짜로"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	select {
	case got := <-commands:
		t.Fatalf("unexpected command after closed suicide prompt: %+v", got)
	default:
	}

	for _, path := range []string{aliasPath, legacyPlayerPath, playerJSONPath, bankPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s stat after closed suicide prompt = %v, want file preserved", path, err)
		}
	}
	if _, ok := inputs.world.Player("player:alice"); !ok {
		t.Fatal("player was deleted after closed suicide prompt")
	}
	if _, ok := inputs.world.Creature("creature:alice"); !ok {
		t.Fatal("creature was deleted after closed suicide prompt")
	}
}

func writeServerSuicideFixtureFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o660); err != nil {
		t.Fatal(err)
	}
}

func withServerTestCommands(t *testing.T, inputs runtimeInputs, specs ...commandspec.CommandSpec) runtimeInputs {
	t.Helper()
	commands := append(inputs.registry.Commands(), specs...)
	registry, err := commandspec.NewRegistry(commands)
	if err != nil {
		t.Fatal(err)
	}
	inputs.registry = registry
	inputs.registryCommandCount = len(commands)
	return inputs
}

func serverStringListContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
