package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"muhan/internal/commandspec"
	"muhan/internal/persist/legacycrypt"
	"muhan/internal/persist/legacykr"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestPasswdHandlerChangesPasswordThroughPendingLines(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	var pending PendingLineHandler
	var savedPlayerID model.PlayerID
	var savedHash string

	ctx := accountTestContext(&pending)
	status, err := NewPasswdHandler(runtime, WithPasswordSink(PasswordSinkFunc(
		func(_ *Context, playerID model.PlayerID, hash string) error {
			savedPlayerID = playerID
			savedHash = hash
			return nil
		},
	)))(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("status/pending = %d/%v, want prompt with pending handler", status, pending != nil)
	}

	if status, err = pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("current password status/error = %d/%v", status, err)
	}
	if status, err = pending(ctx, "newpass"); err != nil || status != StatusDoPrompt {
		t.Fatalf("new password status/error = %d/%v", status, err)
	}
	if status, err = pending(ctx, "newpass"); err != nil || status != StatusDefault {
		t.Fatalf("confirm password status/error = %d/%v", status, err)
	}
	if pending != nil {
		t.Fatal("pending handler still installed after password change")
	}

	creature, _ := runtime.Creature("creature:alice")
	hash := creature.Properties[legacyPasswordHashProperty]
	if !legacycrypt.Verify("newpass", hash) {
		t.Fatalf("stored hash does not verify new password: %q", hash)
	}
	if savedPlayerID != "player:alice" || savedHash != hash {
		t.Fatalf("sink saved %q/%q, want player:alice/%q", savedPlayerID, savedHash, hash)
	}
	if !strings.Contains(ctx.OutputString(), "암호가 변경되었습니다.\n") {
		t.Fatalf("output missing success message:\n%s", ctx.OutputString())
	}
}

func TestPasswdHandlerRejectsWrongCurrentPassword(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	var pending PendingLineHandler

	ctx := accountTestContext(&pending)
	if _, err := NewPasswdHandler(runtime)(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if pending == nil {
		t.Fatal("pending handler not installed")
	}
	status, err := pending(ctx, "wrong")
	if err != nil {
		t.Fatalf("pending() error = %v", err)
	}
	if status != StatusDefault || pending != nil {
		t.Fatalf("status/pending = %d/%v, want abort", status, pending != nil)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "암호가 틀렸습니다.\n암호가 변경되지 않았습니다.\n") {
		t.Fatalf("output missing reject message:\n%s", got)
	}
}

func TestPasswdHandlerAcceptsNewPasswordWithinLegacyByteLimit(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	var pending PendingLineHandler

	ctx := accountTestContext(&pending)
	if status, err := NewPasswdHandler(runtime)(ctx, ResolvedCommand{}); err != nil || status != StatusDoPrompt {
		t.Fatalf("handler status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("current password status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "가나다라마바사"); err != nil || status != StatusDoPrompt {
		t.Fatalf("new password status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "가나다라마바사"); err != nil || status != StatusDefault {
		t.Fatalf("confirm password status/error = %d/%v", status, err)
	}
	if pending != nil {
		t.Fatal("pending handler still installed after password change")
	}
	if got := ctx.OutputString(); !strings.Contains(got, "암호가 변경되었습니다.\n") {
		t.Fatalf("output missing success message:\n%s", got)
	}
	creature, _ := runtime.Creature("creature:alice")
	if !legacycrypt.Verify("가나다라마바사", creature.Properties[legacyPasswordHashProperty]) {
		t.Fatalf("password hash does not verify accepted Korean password: %q", creature.Properties[legacyPasswordHashProperty])
	}
}

func TestPasswdHandlerRejectsNewPasswordOverLegacyByteLimit(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	var pending PendingLineHandler

	ctx := accountTestContext(&pending)
	if status, err := NewPasswdHandler(runtime)(ctx, ResolvedCommand{}); err != nil || status != StatusDoPrompt {
		t.Fatalf("handler status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("current password status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "가나다라마바사아"); err != nil || status != StatusDefault {
		t.Fatalf("new password status/error = %d/%v", status, err)
	}
	if pending != nil {
		t.Fatal("pending handler still installed after overlong password")
	}
	if got := ctx.OutputString(); !strings.Contains(got, "암호가 너무 깁니다.\n암호가 변경되지 않았습니다..\n") {
		t.Fatalf("output missing length reject message:\n%s", got)
	}
	creature, _ := runtime.Creature("creature:alice")
	if !legacycrypt.Verify("1234", creature.Properties[legacyPasswordHashProperty]) {
		t.Fatalf("password hash changed after overlong password: %q", creature.Properties[legacyPasswordHashProperty])
	}
}

func TestAliasHandlerAddsListsAndDeletesUTF8Aliases(t *testing.T) {
	store := NewMemoryAliasStore()
	dispatcher := accountAliasDispatcher(t, store)

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "단축 북 줄")
	if err != nil {
		t.Fatalf("add alias DispatchLine() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "줄임말이 설정되었습니다." {
		t.Fatalf("add status/output = %d/%q", status, ctx.OutputString())
	}

	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	if len(aliases) != 1 || aliases[0] != (PlayerAlias{Alias: "단축", Process: "북"}) {
		t.Fatalf("aliases = %+v, want 단축 -> 북", aliases)
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "줄"); err != nil {
		t.Fatalf("list alias DispatchLine() error = %v", err)
	}
	for _, want := range []string{"줄임말:\n", "[ 1]  단축", ": 북\n", "< 1 / 100 >개의 줄임말이 있습니다.\n"} {
		if !strings.Contains(ctx.OutputString(), want) {
			t.Fatalf("list output missing %q:\n%s", want, ctx.OutputString())
		}
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "단축 줄"); err != nil {
		t.Fatalf("delete alias DispatchLine() error = %v", err)
	}
	if ctx.OutputString() != "줄임말이 삭제되었습니다." {
		t.Fatalf("delete output = %q", ctx.OutputString())
	}
	aliases, _ = store.ListAliases("player:alice")
	if len(aliases) != 0 {
		t.Fatalf("aliases after delete = %+v, want empty", aliases)
	}
}

func TestAliasHandlerPreservesLegacyProcessSpacing(t *testing.T) {
	store := NewMemoryAliasStore()
	dispatcher := accountAliasDispatcher(t, store)

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "단축   북으로   가   줄")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "줄임말이 설정되었습니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}

	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	want := PlayerAlias{Alias: "단축", Process: "  북으로   가"}
	if len(aliases) != 1 || aliases[0] != want {
		t.Fatalf("aliases = %+v, want %+v", aliases, want)
	}
}

func TestAliasHandlerLowercasesASCIIAliasNameLikeLegacy(t *testing.T) {
	store := NewMemoryAliasStore()
	dispatcher := accountAliasDispatcher(t, store)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "Quick 북 줄"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}

	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	want := PlayerAlias{Alias: "quick", Process: "북"}
	if len(aliases) != 1 || aliases[0] != want {
		t.Fatalf("aliases = %+v, want %+v", aliases, want)
	}
}

func TestAliasHandlerRejectsAliasNameOverLegacyByteLimit(t *testing.T) {
	store := NewMemoryAliasStore()
	dispatcher := accountAliasDispatcher(t, store)

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, strings.Repeat("가", 16)+" 북 줄")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "줄임말의 길이가 너무 깁니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}

	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("aliases = %+v, want empty", aliases)
	}
}

func TestAliasHandlerRejectsLegacyInputOverByteLimit(t *testing.T) {
	store := NewMemoryAliasStore()
	dispatcher := accountAliasDispatcher(t, store)

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "단축 "+strings.Repeat("가", 100)+" 줄")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDefault || ctx.OutputString() != "줄임말의 전체적 길이가 너무 깁니다." {
		t.Fatalf("status/output = %d/%q", status, ctx.OutputString())
	}

	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("aliases = %+v, want empty", aliases)
	}
}

func TestFileAliasStoreReadsLegacyAndWritesCanonicalUTF8(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	root := t.TempDir()
	store := NewFileAliasStore(root, runtime)
	path := filepath.Join(root, "player", "alias", "인제로")

	legacyText := "단축\n북으로 가\n~!\n무림 지존\n~!\n"
	legacyRaw, err := legacykr.EncodeEUCKR(legacyText)
	if err != nil {
		t.Fatalf("EncodeEUCKR() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, legacyRaw, 0o660); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() legacy error = %v", err)
	}
	wantLegacy := []PlayerAlias{{Alias: "단축", Process: "북으로 가"}}
	if len(got) != 1 || got[0] != wantLegacy[0] {
		t.Fatalf("legacy aliases = %+v, want %+v", got, wantLegacy)
	}

	aliases := []PlayerAlias{{Alias: "남", Process: "남쪽으로 가"}}
	if err := store.SaveAliases("player:alice", aliases); err != nil {
		t.Fatalf("SaveAliases() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !utf8.Valid(raw) {
		t.Fatalf("alias file is not UTF-8 after write: % X", raw)
	}
	if gotText := string(raw); gotText != "남\n남쪽으로 가\n~!\n무림 지존\n~!\n" {
		t.Fatalf("alias file text = %q", gotText)
	}

	got, err = store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	if len(got) != 1 || got[0] != aliases[0] {
		t.Fatalf("aliases = %+v, want %+v", got, aliases)
	}
}

func TestFileAliasStoreDeleteAliasesRemovesLegacyAliasFile(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	root := t.TempDir()
	store := NewFileAliasStore(root, runtime)

	if err := store.SaveAliases("player:alice", []PlayerAlias{{Alias: "단축", Process: "북"}}); err != nil {
		t.Fatalf("SaveAliases() error = %v", err)
	}
	path := filepath.Join(root, "player", "alias", "인제로")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("alias file before delete stat error = %v", err)
	}
	if err := store.DeleteAliases("player:alice"); err != nil {
		t.Fatalf("DeleteAliases() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("alias file still exists after DeleteAliases(), stat error = %v", err)
	}
}

func TestSuicideHandlerCallsSinkAfterPasswordAndExactConfirm(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	var pending PendingLineHandler
	var called model.PlayerID

	ctx := accountTestContext(&pending)
	status, err := NewPlySuicideHandler(runtime, WithSuicideSink(SuicideSinkFunc(
		func(_ *Context, playerID model.PlayerID) error {
			called = playerID
			return nil
		},
	)))(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("status/pending = %d/%v, want prompt with pending handler", status, pending != nil)
	}

	if status, err = pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("password status/error = %d/%v", status, err)
	}
	if status, err = pending(ctx, "찐짜로"); err != nil || status != StatusDisconnect {
		t.Fatalf("confirm status/error = %d/%v", status, err)
	}
	if pending != nil {
		t.Fatal("pending handler still installed after suicide confirm")
	}
	if called != "player:alice" {
		t.Fatalf("sink called with %q, want player:alice", called)
	}
}

func TestSuicideHandlerCleansAliasesOnlyAfterExactConfirm(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	store := NewMemoryAliasStore()
	if err := store.SaveAliases("player:alice", []PlayerAlias{{Alias: "단축", Process: "북"}}); err != nil {
		t.Fatalf("SaveAliases() error = %v", err)
	}

	var pending PendingLineHandler
	var sinkCalls int
	ctx := accountTestContext(&pending)
	handler := NewPlySuicideHandler(runtime,
		WithSuicideSink(SuicideSinkFunc(func(_ *Context, playerID model.PlayerID) error {
			sinkCalls++
			if playerID != "player:alice" {
				t.Fatalf("sink playerID = %q, want player:alice", playerID)
			}
			return nil
		})),
		WithSuicideAliasStore(store),
	)

	if _, err := handler(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status, err := pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("password status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "뻥으로"); err != nil || status != StatusDefault {
		t.Fatalf("cancel status/error = %d/%v", status, err)
	}
	if pending != nil {
		t.Fatal("pending handler still installed after cancel")
	}
	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	if sinkCalls != 0 || len(aliases) != 1 {
		t.Fatalf("cancel sinkCalls/aliases = %d/%+v, want 0/one alias", sinkCalls, aliases)
	}

	ctx = accountTestContext(&pending)
	if _, err := handler(ctx, ResolvedCommand{}); err != nil {
		t.Fatalf("handler second run error = %v", err)
	}
	if status, err := pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("password second status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "찐짜로"); err != nil || status != StatusDisconnect {
		t.Fatalf("confirm status/error = %d/%v", status, err)
	}
	aliases, err = store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() after confirm error = %v", err)
	}
	if sinkCalls != 1 || len(aliases) != 0 {
		t.Fatalf("confirm sinkCalls/aliases = %d/%+v, want 1/empty", sinkCalls, aliases)
	}
}

func TestSuicideHandlerWrongPasswordDoesNotCallHooks(t *testing.T) {
	runtime := accountRuntime(t, 6, "WOCZU5Ja1Vg")
	store := NewMemoryAliasStore()
	if err := store.SaveAliases("player:alice", []PlayerAlias{{Alias: "단축", Process: "북"}}); err != nil {
		t.Fatalf("SaveAliases() error = %v", err)
	}

	var pending PendingLineHandler
	var sinkCalls int
	ctx := accountTestContext(&pending)
	status, err := NewPlySuicideHandler(runtime,
		WithSuicideSink(SuicideSinkFunc(func(_ *Context, _ model.PlayerID) error {
			sinkCalls++
			return nil
		})),
		WithSuicideAliasStore(store),
	)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("status/pending = %d/%v, want prompt", status, pending != nil)
	}
	if status, err = pending(ctx, "wrong"); err != nil || status != StatusDefault {
		t.Fatalf("wrong password status/error = %d/%v", status, err)
	}
	aliases, err := store.ListAliases("player:alice")
	if err != nil {
		t.Fatalf("ListAliases() error = %v", err)
	}
	if pending != nil || sinkCalls != 0 || len(aliases) != 1 {
		t.Fatalf("pending/sinkCalls/aliases = %v/%d/%+v, want nil/0/one alias", pending != nil, sinkCalls, aliases)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "암호가 틀립니다.\n삭제되지 않았습니다.") {
		t.Fatalf("wrong password output = %q", got)
	}
}

func TestSuicideHandlerRejectsLowLevelPlayer(t *testing.T) {
	runtime := accountRuntime(t, 5, "WOCZU5Ja1Vg")
	var pending PendingLineHandler

	ctx := accountTestContext(&pending)
	status, err := NewPlySuicideHandler(runtime)(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if status != StatusDefault || pending != nil || ctx.OutputString() != "레벨 5이하는 자살 할 수 없습니다.\n" {
		t.Fatalf("status/pending/output = %d/%v/%q", status, pending != nil, ctx.OutputString())
	}
}

func TestPasswdHandlerQueuesRuntimeSaveWhenNoPasswordSink(t *testing.T) {
	world := newAccountHookWorld(6, "WOCZU5Ja1Vg")
	var pending PendingLineHandler

	ctx := accountTestContext(&pending)
	if status, err := NewPasswdHandler(world)(ctx, ResolvedCommand{}); err != nil || status != StatusDoPrompt {
		t.Fatalf("handler status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "1234"); err != nil || status != StatusDoPrompt {
		t.Fatalf("current status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "newpass"); err != nil || status != StatusDoPrompt {
		t.Fatalf("new password status/error = %d/%v", status, err)
	}
	if status, err := pending(ctx, "newpass"); err != nil || status != StatusDefault {
		t.Fatalf("confirm status/error = %d/%v", status, err)
	}
	if !legacycrypt.Verify("newpass", world.creature.Properties[legacyPasswordHashProperty]) {
		t.Fatalf("password hash was not updated: %q", world.creature.Properties[legacyPasswordHashProperty])
	}
	if len(world.marked) == 0 || world.marked[0] != "player:alice" ||
		len(world.queued) == 0 || world.queued[0] != "player:alice" {
		t.Fatalf("marked/queued = %+v/%+v, want player:alice at least once", world.marked, world.queued)
	}
}

func accountAliasDispatcher(t *testing.T, store AliasStore) Dispatcher {
	t.Helper()
	registry := mustRegistry(t, []commandspec.CommandSpec{
		{Name: "줄", Number: 82, Handler: "ply_aliases"},
	})
	return Dispatcher{
		Registry: registry,
		Handlers: map[string]Handler{
			"ply_aliases": NewPlyAliasesHandler(store),
		},
	}
}

func accountRuntime(t *testing.T, level int, passwordHash string) *state.World {
	t.Helper()
	loaded := worldload.NewWorld()
	if err := loaded.AddRoom(model.Room{ID: "room:plaza", DisplayName: "광장"}); err != nil {
		t.Fatal(err)
	}
	if err := loaded.AddPlayer(model.Player{
		ID:          "player:alice",
		DisplayName: "인제로",
		AccountName: "injeiro",
		CreatureID:  "creature:alice",
		RoomID:      "room:plaza",
	}); err != nil {
		t.Fatal(err)
	}
	if err := loaded.AddCreature(model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "인제로",
		PlayerID:    "player:alice",
		RoomID:      "room:plaza",
		Level:       level,
		Properties: map[string]string{
			legacyPasswordHashProperty: passwordHash,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return state.NewWorld(loaded)
}

func accountTestContext(pending *PendingLineHandler) *Context {
	return &Context{
		ActorID: "player:alice",
		Values: map[string]any{
			ContextPendingLineKey: func(handler PendingLineHandler) {
				*pending = handler
			},
		},
	}
}

type accountHookWorld struct {
	player   model.Player
	creature model.Creature
	marked   []model.PlayerID
	queued   []model.PlayerID
}

func newAccountHookWorld(level int, passwordHash string) *accountHookWorld {
	return &accountHookWorld{
		player: model.Player{
			ID:          "player:alice",
			DisplayName: "인제로",
			AccountName: "injeiro",
			CreatureID:  "creature:alice",
			RoomID:      "room:plaza",
		},
		creature: model.Creature{
			ID:          "creature:alice",
			Kind:        model.CreatureKindPlayer,
			DisplayName: "인제로",
			PlayerID:    "player:alice",
			RoomID:      "room:plaza",
			Level:       level,
			Properties: map[string]string{
				legacyPasswordHashProperty: passwordHash,
			},
		},
	}
}

func (w *accountHookWorld) Player(playerID model.PlayerID) (model.Player, bool) {
	return w.player, playerID == w.player.ID
}

func (w *accountHookWorld) Creature(creatureID model.CreatureID) (model.Creature, bool) {
	return w.creature, creatureID == w.creature.ID
}

func (w *accountHookWorld) SetCreatureProperty(creatureID model.CreatureID, key string, value string) (model.Creature, error) {
	if creatureID != w.creature.ID {
		return model.Creature{}, ErrAccountCreatureNotFound
	}
	if w.creature.Properties == nil {
		w.creature.Properties = map[string]string{}
	}
	if value == "" {
		delete(w.creature.Properties, key)
	} else {
		w.creature.Properties[key] = value
	}
	return w.creature, nil
}

func (w *accountHookWorld) MarkPlayerDirty(playerID model.PlayerID) {
	w.marked = append(w.marked, playerID)
}

func (w *accountHookWorld) QueueSave(playerID model.PlayerID, _ model.BankID) {
	w.queued = append(w.queued, playerID)
}
