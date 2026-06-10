package command

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"muhan/internal/commandspec"
	"muhan/internal/persist/cbin"
	"muhan/internal/persist/legacykr"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

const (
	boardTestNumberOff    = 0
	boardTestUploaderOff  = 4
	boardTestYearOff      = 20
	boardTestMonthOff     = 24
	boardTestDayOff       = 28
	boardTestHourOff      = 32
	boardTestMinuteOff    = 36
	boardTestSecondOff    = 40
	boardTestLineOff      = 44
	boardTestReadCountOff = 48
	boardTestTitleOff     = 52
)

func TestBoardLookHandlerRendersCurrentRoomBoardList(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "게시판")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDefault {
		t.Fatalf("status = %d, want default", status)
	}
	out := ctx.OutputString()
	for _, want := range []string{"번호 올린이", "2 무한", "둘째 공지", "1 운영자", "첫 공지"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "첫 본문") || strings.Contains(out, "둘째 본문") {
		t.Fatalf("list output should not include post body:\n%s", out)
	}
}

func TestBoardLookHandlerInstallsLegacyListMenu(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	status, err := dispatcher.DispatchLine(ctx, "게시판")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDoPrompt {
		t.Fatalf("status = %d, want StatusDoPrompt", status)
	}
	if pending == nil {
		t.Fatal("board list pending handler was not installed")
	}
	if got := ctx.OutputString(); !strings.Contains(got, boardListPrompt) {
		t.Fatalf("list output missing menu prompt:\n%s", got)
	}

	ctx.Output = nil
	status, err = pending(ctx, "q")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDefault {
		t.Fatalf("quit status = %d, want StatusDefault", status)
	}
	if pending != nil {
		t.Fatal("board list pending handler was not cleared")
	}
	if got := ctx.OutputString(); got != boardListQuitMessage {
		t.Fatalf("quit output = %q", got)
	}
}

func TestBoardLookHandlerBroadcastsListButNotDirectReadLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	var broadcasts []roomBroadcastRecord
	ctx := boardPendingBroadcastTestContext(&pending, &broadcasts)
	status, err := dispatcher.DispatchLine(ctx, "게시판")
	if err != nil {
		t.Fatalf("DispatchLine() list error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("list status=%d pending=%v, want menu state", status, pending != nil)
	}
	want := roomBroadcastRecord{
		RoomID:  "room:board",
		Exclude: "session:alice",
		Text:    "\nAlice가 게시판을 봅니다.",
	}
	if len(broadcasts) != 1 || broadcasts[0] != want {
		t.Fatalf("list broadcasts = %+v, want %+v", broadcasts, want)
	}

	broadcasts = nil
	ctx = contextWithRoomBroadcast("player:alice", "session:alice", &broadcasts)
	if _, err := dispatcher.DispatchLine(ctx, "1 게시판"); err != nil {
		t.Fatalf("DispatchLine() direct read error = %v", err)
	}
	if len(broadcasts) != 0 {
		t.Fatalf("direct read broadcasts = %+v, want none like C commented read_board broadcast", broadcasts)
	}
}

func TestBoardListMenuPaginatesLikeLegacy(t *testing.T) {
	root := boardTestRootWithPosts(t, 25)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	status, err := dispatcher.DispatchLine(ctx, "게시판")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("initial status=%d pending=%v, want menu state", status, pending != nil)
	}
	out := ctx.OutputString()
	if !strings.Contains(out, boardTestListRowPrefix(25)) || !strings.Contains(out, boardTestListRowPrefix(8)) || strings.Contains(out, boardTestListRowPrefix(7)) {
		t.Fatalf("first page output =\n%s", out)
	}

	ctx.Output = nil
	status, err = pending(ctx, "f")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("next-page status=%d pending=%v, want menu state", status, pending != nil)
	}
	out = ctx.OutputString()
	if !strings.Contains(out, boardTestListRowPrefix(7)) || !strings.Contains(out, boardTestListRowPrefix(1)) || strings.Contains(out, boardTestListRowPrefix(25)) {
		t.Fatalf("second page output =\n%s", out)
	}

	ctx.Output = nil
	status, err = pending(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("previous-page status=%d pending=%v, want menu state", status, pending != nil)
	}
	out = ctx.OutputString()
	if !strings.Contains(out, boardTestListRowPrefix(25)) || strings.Contains(out, boardTestListRowPrefix(7)) {
		t.Fatalf("previous page output =\n%s", out)
	}
}

func TestBoardListMenuReadsPostThenReentersListOnNextInputLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if pending == nil {
		t.Fatal("board list pending handler was not installed")
	}

	ctx.Output = nil
	status, err := pending(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("read status=%d pending=%v, want pending list restart", status, pending != nil)
	}
	out := ctx.OutputString()
	for _, want := range []string{"번호: 1", "첫 본문입니다"} {
		if !strings.Contains(out, want) {
			t.Fatalf("read output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "번호 올린이") || strings.Contains(out, boardListPrompt) {
		t.Fatalf("read output should not immediately re-render list:\n%s", out)
	}
	if got := boardReadCount(t, root, 1); got != 8 {
		t.Fatalf("post 1 read count = %d, want 8 after menu read", got)
	}

	ctx.Output = nil
	status, err = pending(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("restart status=%d pending=%v, want menu state", status, pending != nil)
	}
	out = ctx.OutputString()
	for _, want := range []string{"번호 올린이", "2 무한", "첫 공지", boardListPrompt} {
		if !strings.Contains(out, want) {
			t.Fatalf("restart output missing %q:\n%s", want, out)
		}
	}
}

func TestBoardListMenuLongReadDoesNotContinueViewFileLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	if err := os.WriteFile(filepath.Join(root, "board", "info", "board.1"), []byte(helpLongLines(24)), 0o644); err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	ctx.Output = nil
	status, err := pending(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("long read status=%d pending=%v, want pending list restart", status, pending != nil)
	}
	out := ctx.OutputString()
	for _, want := range []string{"번호: 1", "line S\n", postReadContinuePrompt} {
		if !strings.Contains(out, want) {
			t.Fatalf("long read output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line T\n") || strings.Contains(out, "번호 올린이") {
		t.Fatalf("long read output should only include first view_file page:\n%s", out)
	}

	ctx.Output = nil
	status, err = pending(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("restart status=%d pending=%v, want menu state", status, pending != nil)
	}
	out = ctx.OutputString()
	if !strings.Contains(out, "번호 올린이") || !strings.Contains(out, boardListPrompt) || strings.Contains(out, "line T\n") {
		t.Fatalf("restart output =\n%s", out)
	}
}

func TestBoardListMenuWriteReentersListOnNextInputLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	world.SetDBRoot(root)
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if pending == nil {
		t.Fatal("board list pending handler was not installed")
	}

	ctx.Output = nil
	status, err := pending(ctx, "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil || ctx.OutputString() != "제목: " {
		t.Fatalf("write menu status=%d pending=%v output=%q", status, pending != nil, ctx.OutputString())
	}

	ctx.Output = nil
	if status, err = pending(ctx, "메뉴 글"); err != nil || status != StatusDoPrompt {
		t.Fatalf("title status=%d err=%v", status, err)
	}
	ctx.Output = nil
	if status, err = pending(ctx, "본문"); err != nil || status != StatusDoPrompt {
		t.Fatalf("body status=%d err=%v", status, err)
	}
	ctx.Output = nil
	if status, err = pending(ctx, "."); err != nil || status != StatusDoPrompt {
		t.Fatalf("finish status=%d err=%v", status, err)
	}
	if pending == nil {
		t.Fatal("board list restart handler was not restored after write")
	}
	out := ctx.OutputString()
	if got := out; got != boardWriteDoneMessage {
		t.Fatalf("finish output = %q, want register message only", got)
	}
	if err := world.FlushDirtyBoardsAndFamilyNews(0); err != nil {
		t.Fatalf("flush dirty boards = %v", err)
	}

	ctx.Output = nil
	status, err = pending(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("restart status=%d pending=%v, want menu state", status, pending != nil)
	}
	out = ctx.OutputString()
	for _, want := range []string{"4 Alice", "메뉴 글", boardListPrompt} {
		if !strings.Contains(out, want) {
			t.Fatalf("restart output missing %q:\n%s", want, out)
		}
	}
}

func TestBoardListMenuWriteBroadcastsLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	var broadcasts []roomBroadcastRecord
	ctx := boardPendingBroadcastTestContext(&pending, &broadcasts)
	if status, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil || status != StatusDoPrompt || pending == nil {
		t.Fatalf("list status=%d pending=%v err=%v", status, pending != nil, err)
	}
	broadcasts = nil
	ctx.Output = nil
	if status, err := pending(ctx, "w"); err != nil || status != StatusDoPrompt {
		t.Fatalf("menu write status=%d err=%v", status, err)
	}
	want := roomBroadcastRecord{
		RoomID:  "room:board",
		Exclude: "session:alice",
		Text:    "\nAlice가 게시판에 글을 씁니다.",
	}
	if len(broadcasts) != 1 || broadcasts[0] != want {
		t.Fatalf("write broadcasts = %+v, want %+v", broadcasts, want)
	}
	if got := ctx.OutputString(); got != "제목: " {
		t.Fatalf("write output = %q, want title prompt", got)
	}
}

func TestBoardListMenuWriteHonorsNoticeOnlyBoardLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	loaded := boardTestWorld(t, true)
	room := loaded.Rooms["room:board"]
	room.Objects.ObjectIDs = append(room.Objects.ObjectIDs, "object:notice-board-marker")
	loaded.Rooms[room.ID] = room
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:notice-board-marker",
		DisplayName: "공지용",
	})
	mustAddLookObject(t, loaded, model.ObjectInstance{
		ID:          "object:notice-board-marker",
		PrototypeID: "prototype:notice-board-marker",
		Location:    model.ObjectLocation{RoomID: "room:board"},
	})
	world := state.NewWorld(loaded)
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if pending == nil {
		t.Fatal("board list pending handler was not installed")
	}

	ctx.Output = nil
	status, err := pending(ctx, "w")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("notice write status=%d pending=%v, want same menu state", status, pending != nil)
	}
	out := ctx.OutputString()
	if !strings.Contains(out, "공지용 게시판입니다.") || strings.Contains(out, boardListPrompt) || strings.Contains(out, "제목: ") {
		t.Fatalf("notice write output =\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "board", "info", "board.4")); !os.IsNotExist(err) {
		t.Fatalf("notice write created board.4: %v", err)
	}

	ctx.Output = nil
	status, err = pending(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDoPrompt || pending == nil {
		t.Fatalf("menu status=%d pending=%v, want menu state", status, pending != nil)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "번호 올린이") || !strings.Contains(got, boardListPrompt) {
		t.Fatalf("menu output =\n%s", got)
	}
}

func TestBoardLookHandlerRendersSelectedPost(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "1 게시판")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDoPrompt {
		t.Fatalf("status = %d, want StatusDoPrompt", status)
	}
	out := ctx.OutputString()
	for _, want := range []string{"번호: 1", "올린이: 운영자", "제목: 첫 공지", "첫 본문입니다", "둘째 줄"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "둘째 본문") {
		t.Fatalf("selected post output contains other post body:\n%s", out)
	}
}

func TestBoardLookHandlerPaginatesSelectedPostLikeLegacyViewFile(t *testing.T) {
	root := boardTestRoot(t)
	if err := os.WriteFile(filepath.Join(root, "board", "info", "board.1"), []byte(helpLongLines(24)), 0o644); err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	ctx.ActorID = "player:alice"
	status, err := dispatcher.DispatchLine(ctx, "1 게시판")
	if err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if status != StatusDoPrompt {
		t.Fatalf("first page status = %d, want StatusDoPrompt", status)
	}
	out := ctx.OutputString()
	for _, want := range []string{"번호: 1", "line S\n", postReadContinuePrompt} {
		if !strings.Contains(out, want) {
			t.Fatalf("first page output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line T\n") {
		t.Fatalf("first page output included continuation line:\n%s", out)
	}
	if pending == nil {
		t.Fatal("pending continuation handler was not installed")
	}

	ctx.Output = nil
	status, err = pending(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDefault {
		t.Fatalf("continuation status = %d, want StatusDefault", status)
	}
	out = ctx.OutputString()
	if !strings.Contains(out, "line T\n") || !strings.Contains(out, "line X\n") {
		t.Fatalf("continuation output missing remaining lines:\n%s", out)
	}
	if pending != nil {
		t.Fatal("pending continuation handler was not cleared")
	}
}

func TestBoardLookHandlerSupportsCommandFirstRead(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판 2"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "번호: 2") || !strings.Contains(got, "둘째 본문입니다") {
		t.Fatalf("output = %q, want second post", got)
	}
}

func TestBoardReadAliasHandlerRoutesBoardReadCommands(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판 읽어"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	for _, want := range []string{"번호 올린이", "2 무한", "둘째 공지", "1 운영자", "첫 공지"} {
		if got := ctx.OutputString(); !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판 2 읽어"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "번호: 2") || !strings.Contains(got, "둘째 본문입니다") {
		t.Fatalf("output = %q, want second post", got)
	}
}

func TestBoardLookHandlerRequiresBoardObject(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, false))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if got := ctx.OutputString(); got != boardNoBoardMessage {
		t.Fatalf("output = %q, want no board message", got)
	}
}

func TestBoardLookupUsesLegacyFindObjNameAndKeys(t *testing.T) {
	root := boardTestRoot(t)
	loaded := boardTestWorld(t, true)
	proto := loaded.ObjectPrototypes["prototype:board"]
	proto.DisplayName = "알림판"
	loaded.ObjectPrototypes[proto.ID] = proto
	world := state.NewWorld(loaded)
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if got := ctx.OutputString(); got != boardNoBoardMessage {
		t.Fatalf("metadata-only board output = %q, want no board", got)
	}

	loaded = boardTestWorld(t, true)
	proto = loaded.ObjectPrototypes["prototype:board"]
	proto.DisplayName = "알림판"
	proto.Properties["key[0]"] = "게시판"
	loaded.ObjectPrototypes[proto.ID] = proto
	world = state.NewWorld(loaded)
	defer world.Close()
	dispatcher = boardTestDispatcher(t, world, root)

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() with key error = %v", err)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "번호 올린이") {
		t.Fatalf("key-matched board output = %q, want board list", got)
	}
}

func TestBoardLookupVisibilityUsesPDINVI(t *testing.T) {
	root := boardTestRoot(t)
	loaded := boardTestWorld(t, true)
	board := loaded.Objects["object:board"]
	board.Metadata.Tags = []string{"OINVIS"}
	loaded.Objects[board.ID] = board
	world := state.NewWorld(loaded)
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if got := ctx.OutputString(); got != boardNoBoardMessage {
		t.Fatalf("invisible board output = %q, want no board", got)
	}

	loaded = boardTestWorld(t, true)
	board = loaded.Objects["object:board"]
	board.Metadata.Tags = []string{"OINVIS"}
	loaded.Objects[board.ID] = board
	creature := loaded.Creatures["creature:alice"]
	creature.Metadata.Tags = []string{"PDINVI"}
	loaded.Creatures[creature.ID] = creature
	world = state.NewWorld(loaded)
	defer world.Close()
	dispatcher = boardTestDispatcher(t, world, root)

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() with PDINVI error = %v", err)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "번호 올린이") {
		t.Fatalf("detect invisible board output = %q, want board list", got)
	}
}

func TestBoardLookHandlerRejectsOutOfRangeAndDeletedPost(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	for _, line := range []string{"99 게시판"} {
		t.Run(line, func(t *testing.T) {
			ctx := &Context{ActorID: "player:alice"}
			if _, err := dispatcher.DispatchLine(ctx, line); err != nil {
				t.Fatalf("DispatchLine() error = %v", err)
			}
			if got := ctx.OutputString(); got != boardOutOfRangeMessage {
				t.Fatalf("output = %q, want out-of-range message", got)
			}
		})
	}

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "3 게시판"); err != nil {
		t.Fatalf("DispatchLine() deleted error = %v", err)
	}
	if got := ctx.OutputString(); got != boardDeletedPostMessage {
		t.Fatalf("deleted output = %q, want legacy deleted message", got)
	}
}

func TestBoardLookHandlerIncrementsReadCountLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "1 게시판"); err != nil {
		t.Fatalf("DispatchLine() error = %v", err)
	}
	if got := boardReadCount(t, root, 1); got != 8 {
		t.Fatalf("post 1 read count = %d, want 8 after non-author read", got)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "\n번호: 1 올린이: 운영자 제목: 첫 공지\n") || !strings.Contains(got, "읽은횟수: 7") {
		t.Fatalf("read output = %q, want pre-increment count", got)
	}

	if _, err := appendBoardPost(root, "info", "Alice", "자기 글", []string{"본문"}, time.Now()); err != nil {
		t.Fatalf("appendBoardPost() error = %v", err)
	}
	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "4 게시판"); err != nil {
		t.Fatalf("DispatchLine() own post error = %v", err)
	}
	if got := boardReadCount(t, root, 4); got != 0 {
		t.Fatalf("own post read count = %d, want unchanged 0", got)
	}
}

func TestBoardLookHandlerDMCanListAndReadDeletedPostsLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	loaded := boardTestWorld(t, true)
	creature := loaded.Creatures["creature:alice"]
	creature.Stats = map[string]int{"class": model.ClassDM}
	loaded.Creatures[creature.ID] = creature
	world := state.NewWorld(loaded)
	defer world.Close()
	world.SetDBRoot(root)
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() list error = %v", err)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "3 무한") || !strings.Contains(got, "삭제 글") {
		t.Fatalf("DM list output missing deleted post:\n%s", got)
	}

	ctx = &Context{ActorID: "player:alice"}
	status, err := dispatcher.DispatchLine(ctx, "3 게시판")
	if err != nil {
		t.Fatalf("DispatchLine() deleted read error = %v", err)
	}
	if status != StatusDoPrompt {
		t.Fatalf("deleted read status = %d, want StatusDoPrompt", status)
	}
	out := ctx.OutputString()
	if !strings.Contains(out, "번호: 3") || !strings.Contains(out, "삭제 본문입니다") || !strings.Contains(out, "읽은횟수: -1") {
		t.Fatalf("DM deleted read output = %q", out)
	}
	if got := boardReadCount(t, root, 3); got != -2 {
		t.Fatalf("deleted post read count = %d, want -2 after DM non-author read", got)
	}
}

func TestBoardDeleteRequiresLegacyBoardSpecial(t *testing.T) {
	root := boardTestRoot(t)
	loaded := boardTestWorld(t, true)
	proto := loaded.ObjectPrototypes["prototype:board"]
	delete(proto.Properties, "special")
	loaded.ObjectPrototypes[proto.ID] = proto
	creature := loaded.Creatures["creature:alice"]
	creature.Stats = map[string]int{"class": model.ClassDM}
	loaded.Creatures[creature.ID] = creature
	world := state.NewWorld(loaded)
	defer world.Close()
	world.SetDBRoot(root)
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판"); err != nil {
		t.Fatalf("DispatchLine() list error = %v", err)
	}
	if got := ctx.OutputString(); !strings.Contains(got, "번호 올린이") {
		t.Fatalf("type-only board should still list like C read/write paths, output = %q", got)
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판 1 글삭제"); err != nil {
		t.Fatalf("DispatchLine() delete error = %v", err)
	}
	if got := ctx.OutputString(); got != boardNoBoardMessage {
		t.Fatalf("delete output = %q, want no board when SP_BOARD is absent", got)
	}
	if got := boardReadCount(t, root, 1); got != 7 {
		t.Fatalf("post 1 read count = %d, want unchanged 7", got)
	}
}

func TestBoardDeleteRejectsUnauthorizedPlayerLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	world.SetDBRoot(root)
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판 2 글삭제"); err != nil {
		t.Fatalf("DispatchLine() delete error = %v", err)
	}
	if got := ctx.OutputString(); got != boardDeleteDenyMessage {
		t.Fatalf("delete output = %q, want denied message", got)
	}
	if got := boardReadCount(t, root, 2); got != 8 {
		t.Fatalf("post 2 read count = %d, want unchanged 8", got)
	}
}

func TestBoardDeleteUsesLegacyParsedSlots(t *testing.T) {
	root := boardTestRoot(t)
	loaded := boardTestWorld(t, true)
	creature := loaded.Creatures["creature:alice"]
	creature.Stats = map[string]int{"class": model.ClassDM}
	loaded.Creatures[creature.ID] = creature
	world := state.NewWorld(loaded)
	defer world.Close()
	world.SetDBRoot(root)
	dispatcher := boardTestDispatcher(t, world, root)

	ctx := &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "3 게시판 글삭제"); err != nil {
		t.Fatalf("DispatchLine() delete error = %v", err)
	}
	if got := ctx.OutputString(); got != boardDeleteMarkMessage {
		t.Fatalf("delete output = %q, want marked message", got)
	}
	if got := boardReadCount(t, root, 1); got != -7 {
		t.Fatalf("post 1 read count = %d, want -7 from legacy val[1]", got)
	}
	if got := boardReadCount(t, root, 3); got != -1 {
		t.Fatalf("post 3 read count = %d, want unchanged -1", got)
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "3 게시판 글삭제"); err != nil {
		t.Fatalf("DispatchLine() restore error = %v", err)
	}
	if got := ctx.OutputString(); got != boardDeleteUndoMessage {
		t.Fatalf("restore output = %q, want restored message", got)
	}
	if got := boardReadCount(t, root, 1); got != 7 {
		t.Fatalf("post 1 read count = %d, want restored 7", got)
	}
	if err := world.FlushDirtyBoardsAndFamilyNews(0); err != nil {
		t.Fatalf("flush dirty boards = %v", err)
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판x 1 글삭제"); err != nil {
		t.Fatalf("DispatchLine() invalid order error = %v", err)
	}
	if got := ctx.OutputString(); got != boardDeleteUsageMessage {
		t.Fatalf("invalid order output = %q, want usage", got)
	}

	ctx = &Context{ActorID: "player:alice"}
	if _, err := dispatcher.DispatchLine(ctx, "게시판 99 글삭제"); err != nil {
		t.Fatalf("DispatchLine() out-of-range delete error = %v", err)
	}
	if got := ctx.OutputString(); got != boardOutOfRangeMessage {
		t.Fatalf("out-of-range delete output = %q, want legacy message", got)
	}
}

func TestBoardWriteHandlerAppendsPostAndSnapshotsSidecar(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	world.SetDBRoot(root)

	var pending PendingLineHandler
	var broadcasts []roomBroadcastRecord
	ctx := boardPendingBroadcastTestContext(&pending, &broadcasts)
	now := func() time.Time {
		return time.Date(2026, 5, 31, 8, 40, 0, 0, time.FixedZone("KST", 9*60*60))
	}
	handler := newBoardWriteHandler(world, root, now)

	status, err := handler(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("write start error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil || ctx.OutputString() != "제목: " {
		t.Fatalf("write start status=%d pending=%v output=%q", status, pending != nil, ctx.OutputString())
	}
	wantBroadcast := roomBroadcastRecord{
		RoomID:  "room:board",
		Exclude: "session:alice",
		Text:    "\nAlice가 게시판에 글을 씁니다.",
	}
	if len(broadcasts) != 1 || broadcasts[0] != wantBroadcast {
		t.Fatalf("write broadcasts = %+v, want %+v", broadcasts, wantBroadcast)
	}

	ctx.Output = nil
	if status, err = pending(ctx, "새 공지"); err != nil || status != StatusDoPrompt {
		t.Fatalf("title status=%d err=%v", status, err)
	}
	if pending == nil {
		t.Fatal("body pending handler was not installed")
	}
	if got := ctx.OutputString(); !strings.Contains(got, "입력하십시요.") || strings.Contains(got, "입력하십시오.") {
		t.Fatalf("body prompt = %q, want legacy spelling", got)
	}
	for _, line := range []string{"첫 줄", "둘째 줄"} {
		ctx.Output = nil
		if status, err = pending(ctx, line); err != nil || status != StatusDoPrompt {
			t.Fatalf("body line %q status=%d err=%v", line, status, err)
		}
	}

	ctx.Output = nil
	if status, err = pending(ctx, "."); err != nil || status != StatusDefault {
		t.Fatalf("finish status=%d err=%v", status, err)
	}
	if pending != nil {
		t.Fatal("pending handler was not cleared after final dot")
	}
	if got := ctx.OutputString(); got != boardWriteDoneMessage {
		t.Fatalf("finish output = %q", got)
	}

	body, err := os.ReadFile(filepath.Join(root, "board", "info", "board.4"))
	if err != nil {
		t.Fatalf("new body read error = %v", err)
	}
	bodyText, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Field: "board body"}, body)
	if err != nil {
		t.Fatalf("new body decode error = %v", err)
	}
	if got := bodyText; got != "첫 줄\n둘째 줄\n" {
		t.Fatalf("new body = %q", got)
	}

	board, err := loadBoard(root, "info")
	if err != nil {
		t.Fatalf("load board after write = %v", err)
	}
	var found bool
	for _, post := range board.Posts {
		if post.Number == 4 {
			found = true
			if post.Title != "새 공지" || post.Uploader != "Alice" || post.Body != "첫 줄\n둘째 줄\n" || post.LineCount != 3 {
				t.Fatalf("post 4 = %+v", post)
			}
		}
	}
	if !found {
		t.Fatalf("post 4 not found in board: %+v", board.Posts)
	}

	if err := world.FlushDirtyBoardsAndFamilyNews(0); err != nil {
		t.Fatalf("flush dirty boards = %v", err)
	}
	save, ok, err := state.LoadBoardPosts(root, "info")
	if err != nil || !ok {
		t.Fatalf("load board sidecar ok=%v err=%v", ok, err)
	}
	found = false
	for _, post := range save.Posts {
		if post.Number == 4 {
			found = true
			if post.Title != "새 공지" || post.BodyPreview != "첫 줄\n둘째 줄" {
				t.Fatalf("sidecar post 4 = %+v", post)
			}
		}
	}
	if !found {
		t.Fatalf("post 4 not found in sidecar: %+v", save.Posts)
	}
}

func TestBoardWriteHandlerAcceptsWhitespaceOnlyTitleLikeLegacy(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	world.SetDBRoot(root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	handler := newBoardWriteHandler(world, root, func() time.Time {
		return time.Date(2026, 6, 7, 11, 12, 13, 0, time.FixedZone("KST", 9*60*60))
	})

	if status, err := handler(ctx, ResolvedCommand{}); err != nil || status != StatusDoPrompt {
		t.Fatalf("write start status=%d err=%v", status, err)
	}
	ctx.Output = nil
	if status, err := pending(ctx, "   "); err != nil || status != StatusDoPrompt {
		t.Fatalf("whitespace title status=%d err=%v output=%q", status, err, ctx.OutputString())
	}
	if pending == nil {
		t.Fatal("body pending handler was not installed for whitespace title")
	}
	if got := ctx.OutputString(); strings.Contains(got, "게시물 작성을 취소합니다.") || !strings.Contains(got, "  1: ") {
		t.Fatalf("whitespace title output = %q, want body prompt", got)
	}

	ctx.Output = nil
	if status, err := pending(ctx, "본문"); err != nil || status != StatusDoPrompt {
		t.Fatalf("body status=%d err=%v", status, err)
	}
	ctx.Output = nil
	if status, err := pending(ctx, "."); err != nil || status != StatusDefault {
		t.Fatalf("finish status=%d err=%v", status, err)
	}

	index, err := os.ReadFile(filepath.Join(root, "board", "info", "board_index"))
	if err != nil {
		t.Fatalf("board_index read error = %v", err)
	}
	last := index[3*cbin.BoardIndexSize:]
	rawTitle := last[boardTestTitleOff : boardTestTitleOff+40]
	if string(rawTitle[:3]) != "   " {
		t.Fatalf("raw title prefix = %q, want three spaces", string(rawTitle[:3]))
	}
	for i, b := range rawTitle[3:] {
		if b != 0 {
			t.Fatalf("raw title byte %d = %#x, want NUL padding", i+3, b)
		}
	}
	if err := world.FlushDirtyBoardsAndFamilyNews(0); err != nil {
		t.Fatalf("flush dirty boards = %v", err)
	}
}

func TestAppendBoardPostWritesLegacyRawBytesLikeC(t *testing.T) {
	root := boardTestRoot(t)

	title := strings.Repeat("a", 39) + "가나다"
	number, err := appendBoardPost(root, "info", "Alice", title, []string{"본문", "둘째 줄"}, time.Date(2026, 6, 7, 1, 2, 3, 0, time.FixedZone("KST", 9*60*60)))
	if err != nil {
		t.Fatalf("appendBoardPost() error = %v", err)
	}
	if number != 4 {
		t.Fatalf("number = %d, want 4", number)
	}

	body, err := os.ReadFile(filepath.Join(root, "board", "info", "board.4"))
	if err != nil {
		t.Fatalf("new body read error = %v", err)
	}
	wantBody, err := legacykr.EncodeEUCKR("본문\n둘째 줄\n")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(wantBody) {
		t.Fatalf("body raw bytes = % X, want % X", body, wantBody)
	}

	index, err := os.ReadFile(filepath.Join(root, "board", "info", "board_index"))
	if err != nil {
		t.Fatalf("board_index read error = %v", err)
	}
	if len(index) != 4*cbin.BoardIndexSize {
		t.Fatalf("board_index size = %d, want %d", len(index), 4*cbin.BoardIndexSize)
	}
	last := index[3*cbin.BoardIndexSize:]
	if got := int(int32(binary.LittleEndian.Uint32(last[boardTestLineOff:]))); got != 3 {
		t.Fatalf("line count = %d, want 3 like legacy board_pos", got)
	}
	wantTitle := append([]byte(strings.Repeat("a", 39)), 0xB0)
	if got := last[boardTestTitleOff : boardTestTitleOff+40]; string(got) != string(wantTitle) {
		t.Fatalf("title raw bytes = % X, want % X", got, wantTitle)
	}
}

func TestBoardWriteHandlerDoesNotPersistBeforeFinalDot(t *testing.T) {
	root := boardTestRoot(t)
	world := state.NewWorld(boardTestWorld(t, true))
	defer world.Close()
	world.SetDBRoot(root)

	var pending PendingLineHandler
	ctx := boardPendingTestContext(&pending)
	handler := newBoardWriteHandler(world, root, func() time.Time {
		return time.Date(2026, 6, 6, 10, 20, 0, 0, time.FixedZone("KST", 9*60*60))
	})

	status, err := handler(ctx, ResolvedCommand{})
	if err != nil {
		t.Fatalf("write start error = %v", err)
	}
	if status != StatusDoPrompt || pending == nil || ctx.OutputString() != "제목: " {
		t.Fatalf("write start status=%d pending=%v output=%q", status, pending != nil, ctx.OutputString())
	}

	ctx.Output = nil
	if status, err = pending(ctx, "닫히는 글"); err != nil || status != StatusDoPrompt {
		t.Fatalf("title status=%d err=%v", status, err)
	}
	if pending == nil {
		t.Fatal("body pending handler was not installed")
	}
	ctx.Output = nil
	if status, err = pending(ctx, "아직 저장되면 안 됩니다"); err != nil || status != StatusDoPrompt {
		t.Fatalf("body status=%d err=%v", status, err)
	}
	if pending == nil {
		t.Fatal("body pending handler was cleared before final dot")
	}

	if _, err := os.Stat(filepath.Join(root, "board", "info", "board.4")); !os.IsNotExist(err) {
		t.Fatalf("board.4 stat error = %v, want no file before final dot", err)
	}
	board, err := loadBoard(root, "info")
	if err != nil {
		t.Fatalf("load board after partial write = %v", err)
	}
	for _, post := range board.Posts {
		if post.Number == 4 {
			t.Fatalf("partial write created index post 4: %+v", post)
		}
	}
	if _, ok, err := state.LoadBoardPosts(root, "info"); err != nil || ok {
		t.Fatalf("partial write sidecar ok=%v err=%v, want absent nil", ok, err)
	}
}

func TestLegacyBoardPathRejectsTraversalAndUnicodeSlash(t *testing.T) {
	root := t.TempDir()
	tests := []string{
		"../escape",
		"..\\escape",
		"/tmp/escape",
		"sub/escape",
		"..\u2044escape",
		"..\u2215escape",
		"..\uff0fescape",
		"..\uff3cescape",
	}
	for _, dir := range tests {
		t.Run(dir, func(t *testing.T) {
			if _, err := legacyBoardPath(root, dir); err == nil {
				t.Fatalf("legacyBoardPath(%q) error = nil, want unsafe rejection", dir)
			}
			if _, err := loadBoard(root, dir); err == nil {
				t.Fatalf("loadBoard(%q) error = nil, want unsafe rejection", dir)
			}
			if _, err := appendBoardPost(root, dir, "Alice", "제목", []string{"본문"}, time.Now()); err == nil {
				t.Fatalf("appendBoardPost(%q) error = nil, want unsafe rejection", dir)
			}
			if _, err := toggleBoardPostDeleted(root, dir, 1, "Alice", legacyBoardDMClass); err == nil {
				t.Fatalf("toggleBoardPostDeleted(%q) error = nil, want unsafe rejection", dir)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "escape", "board.1")); !os.IsNotExist(err) {
		t.Fatalf("unsafe board write touched escape path: %v", err)
	}
}

func boardTestDispatcher(t *testing.T, world *state.World, root string) Dispatcher {
	t.Helper()
	return Dispatcher{
		Registry: mustRegistry(t, []commandspec.CommandSpec{
			{Name: "게시판", Number: 94, Handler: "look_board"},
			{Name: "읽어", Number: 40, Handler: "readscroll"},
			{Name: "글삭제", Number: 93, Handler: "del_board"},
		}),
		Handlers: map[string]Handler{
			"look_board": NewBoardLookHandler(world, root),
			"readscroll": NewReadScrollHandler(world, root, nil),
			"del_board":  NewBoardDeleteHandler(world, root),
		},
	}
}

func boardTestWorld(t *testing.T, withBoard bool) *worldload.World {
	t.Helper()

	loaded := worldload.NewWorld()
	room := model.Room{
		ID:          "room:board",
		DisplayName: "Board Room",
	}
	if withBoard {
		room.Objects = model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:board"}}
	}
	mustAddLookRoom(t, loaded, room)
	mustAddLookPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:board",
	})
	mustAddLookCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		RoomID:      "room:board",
	})
	mustAddLookPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:board",
		DisplayName: "게시판",
		Properties:  map[string]string{"type": "100", "special": "4"},
	})
	if withBoard {
		mustAddLookObject(t, loaded, model.ObjectInstance{
			ID:          "object:board",
			PrototypeID: "prototype:board",
			Location:    model.ObjectLocation{RoomID: "room:board"},
		})
	}
	return loaded
}

func boardPendingTestContext(pending *PendingLineHandler) *Context {
	return &Context{
		ActorID: "player:alice",
		Values: map[string]any{
			ContextPendingLineKey: func(handler PendingLineHandler) {
				*pending = handler
			},
		},
	}
}

func boardPendingBroadcastTestContext(pending *PendingLineHandler, records *[]roomBroadcastRecord) *Context {
	return &Context{
		ActorID:   "player:alice",
		SessionID: "session:alice",
		Values: map[string]any{
			ContextPendingLineKey: func(handler PendingLineHandler) {
				*pending = handler
			},
			ContextRoomBroadcastKey: RoomBroadcastFunc(func(roomID model.RoomID, excludeSessionID string, text string) error {
				*records = append(*records, roomBroadcastRecord{
					RoomID:  string(roomID),
					Exclude: excludeSessionID,
					Text:    text,
				})
				return nil
			}),
		},
	}
}

func boardTestRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	boardDir := filepath.Join(root, "board", "info")
	if err := os.MkdirAll(boardDir, 0o700); err != nil {
		t.Fatal(err)
	}

	index := append([]byte{}, boardTestIndexRecord(t, 1, "운영자", "첫 공지", 126, 5, 20, 1, 2, 3, 7)...)
	index = append(index, boardTestIndexRecord(t, 2, "무한", "둘째 공지", 126, 5, 21, 2, 3, 4, 8)...)
	index = append(index, boardTestIndexRecord(t, 3, "무한", "삭제 글", 126, 5, 22, 3, 4, 5, -1)...)
	if err := os.WriteFile(filepath.Join(boardDir, "board_index"), index, 0o600); err != nil {
		t.Fatal(err)
	}
	boardWriteEncoded(t, filepath.Join(boardDir, "board.1"), "첫 본문입니다\n둘째 줄")
	boardWriteEncoded(t, filepath.Join(boardDir, "board.2"), "둘째 본문입니다")
	boardWriteEncoded(t, filepath.Join(boardDir, "board.3"), "삭제 본문입니다")
	return root
}

func boardTestRootWithPosts(t *testing.T, count int) string {
	t.Helper()
	root := t.TempDir()
	boardDir := filepath.Join(root, "board", "info")
	if err := os.MkdirAll(boardDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var index []byte
	for i := 1; i <= count; i++ {
		index = append(index, boardTestIndexRecord(t, i, "운영자", fmt.Sprintf("공지 %02d", i), 126, 5, 1+i%28, 1, 2, 3, 0)...)
		boardWriteEncoded(t, filepath.Join(boardDir, fmt.Sprintf("board.%d", i)), fmt.Sprintf("본문 %02d", i))
	}
	if err := os.WriteFile(filepath.Join(boardDir, "board_index"), index, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func boardTestListRowPrefix(number int) string {
	return fmt.Sprintf("%4d 운영자", number)
}

func boardTestIndexRecord(t *testing.T, number int, uploader, title string, year, month, day, hour, minute, second, readCount int) []byte {
	t.Helper()
	data := make([]byte, cbin.BoardIndexSize)
	binary.LittleEndian.PutUint32(data[boardTestNumberOff:], uint32(int32(number)))
	boardCopyEncoded(t, data[boardTestUploaderOff:boardTestUploaderOff+16], uploader)
	binary.LittleEndian.PutUint32(data[boardTestYearOff:], uint32(int32(year)))
	binary.LittleEndian.PutUint32(data[boardTestMonthOff:], uint32(int32(month)))
	binary.LittleEndian.PutUint32(data[boardTestDayOff:], uint32(int32(day)))
	binary.LittleEndian.PutUint32(data[boardTestHourOff:], uint32(int32(hour)))
	binary.LittleEndian.PutUint32(data[boardTestMinuteOff:], uint32(int32(minute)))
	binary.LittleEndian.PutUint32(data[boardTestSecondOff:], uint32(int32(second)))
	binary.LittleEndian.PutUint32(data[boardTestLineOff:], 2)
	binary.LittleEndian.PutUint32(data[boardTestReadCountOff:], uint32(int32(readCount)))
	boardCopyEncoded(t, data[boardTestTitleOff:boardTestTitleOff+40], title)
	return data
}

func boardReadCount(t *testing.T, root string, number int) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "board", "info", "board_index"))
	if err != nil {
		t.Fatal(err)
	}
	offset := (number - 1) * cbin.BoardIndexSize
	if offset < 0 || offset+boardTestReadCountOff+4 > len(data) {
		t.Fatalf("post %d offset %d out of board_index size %d", number, offset, len(data))
	}
	return int(int32(binary.LittleEndian.Uint32(data[offset+boardTestReadCountOff:])))
}

func boardWriteEncoded(t *testing.T, path string, text string) {
	t.Helper()
	encoded, err := legacykr.EncodeEUCKR(text)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func boardCopyEncoded(t *testing.T, dst []byte, text string) {
	t.Helper()
	encoded, err := legacykr.EncodeEUCKR(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > len(dst) {
		t.Fatalf("encoded text %q is %d bytes, max %d", text, len(encoded), len(dst))
	}
	copy(dst, encoded)
}
