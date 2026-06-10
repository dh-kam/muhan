package state_test

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func TestSchemaV1LoadsMigrateQuietly(t *testing.T) {
	root := t.TempDir()
	writeJSONFile(t, filepath.Join(root, "player", "json", "alice.json"), state.PlayerSaveData{
		SchemaVersion: 1,
		Player:        model.Player{ID: "player:alice"},
	})
	writeJSONFile(t, filepath.Join(root, "room", "json", "floor1.objects.json"), state.RoomObjectsSave{
		SchemaVersion: 1,
		RoomID:        "room:floor1",
	})
	writeJSONFile(t, filepath.Join(root, "board", "json", "info.json"), state.BoardPostsSave{
		SchemaVersion: 1,
		BoardDir:      "info",
	})
	writeJSONFile(t, filepath.Join(root, "player", "family", "json", "family_news_7.json"), state.FamilyNewsSave{
		SchemaVersion: 1,
		FamilyID:      7,
		Content:       "notice",
	})
	writeJSONFile(t, filepath.Join(root, "player", "bank", "json", "alice.json"), model.BankSaveBundle{
		SchemaVersion: 1,
		BankAccount: model.BankAccount{
			ID:        "bank:player:alice",
			OwnerName: "alice",
		},
	})

	var logs bytes.Buffer
	oldLog := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(oldLog)

	player, ok, err := state.LoadPlayer(root, "player:alice")
	if err != nil || !ok {
		t.Fatalf("LoadPlayer v1 = ok %v err %v", ok, err)
	}
	if player.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("player schema = %d, want %d", player.SchemaVersion, state.CurrentSaveSchemaVersion)
	}
	if player.Objects == nil {
		t.Fatal("player objects slice stayed nil after v2 migration")
	}

	room, ok, err := state.LoadRoomObjects(root, "room:floor1")
	if err != nil || !ok {
		t.Fatalf("LoadRoomObjects v1 = ok %v err %v", ok, err)
	}
	if room.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("room schema = %d, want %d", room.SchemaVersion, state.CurrentSaveSchemaVersion)
	}
	if room.Objects == nil {
		t.Fatal("room objects slice stayed nil after v2 migration")
	}

	board, ok, err := state.LoadBoardPosts(root, "info")
	if err != nil || !ok {
		t.Fatalf("LoadBoardPosts v1 = ok %v err %v", ok, err)
	}
	if board.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("board schema = %d, want %d", board.SchemaVersion, state.CurrentSaveSchemaVersion)
	}
	if board.Posts == nil {
		t.Fatal("board posts slice stayed nil after v2 migration")
	}

	family, ok, err := state.LoadFamilyNews(root, 7)
	if err != nil || !ok {
		t.Fatalf("LoadFamilyNews v1 = ok %v err %v", ok, err)
	}
	if family.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("family schema = %d, want %d", family.SchemaVersion, state.CurrentSaveSchemaVersion)
	}

	world := state.NewWorld(nil)
	defer world.Close()
	world.SetDBRoot(root)
	bank, ok, err := world.LoadBank("bank:player:alice")
	if err != nil || !ok {
		t.Fatalf("LoadBank v1 = ok %v err %v", ok, err)
	}
	if bank.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("bank schema = %d, want %d", bank.SchemaVersion, state.CurrentSaveSchemaVersion)
	}
	if bank.Objects == nil {
		t.Fatal("bank objects slice stayed nil after v2 migration")
	}

	logText := logs.String()
	if strings.Contains(logText, "schema version mismatch") || strings.Contains(logText, "[PERSIST] MIGRATE") {
		t.Fatalf("v1 migration produced noisy logs: %q", logText)
	}
}

func TestSchemaFutureVersionRejected(t *testing.T) {
	root := t.TempDir()
	writeJSONFile(t, filepath.Join(root, "player", "json", "alice.json"), state.PlayerSaveData{
		SchemaVersion: state.CurrentSaveSchemaVersion + 1,
		Player:        model.Player{ID: "player:alice"},
	})

	_, ok, err := state.LoadPlayer(root, "player:alice")
	if err == nil {
		t.Fatal("LoadPlayer accepted unsupported future schema")
	}
	if ok {
		t.Fatal("LoadPlayer reported ok for unsupported future schema")
	}
	if !strings.Contains(err.Error(), "unsupported future schema version") {
		t.Fatalf("LoadPlayer future schema error = %v", err)
	}

	writeJSONFile(t, filepath.Join(root, "player", "bank", "json", "alice.json"), model.BankSaveBundle{
		SchemaVersion: state.CurrentSaveSchemaVersion + 1,
		BankAccount:   model.BankAccount{ID: "bank:player:alice", OwnerName: "alice"},
	})
	world := state.NewWorld(nil)
	defer world.Close()
	world.SetDBRoot(root)
	_, ok, err = world.LoadBank("bank:player:alice")
	if err == nil {
		t.Fatal("LoadBank accepted unsupported future schema")
	}
	if ok {
		t.Fatal("LoadBank reported ok for unsupported future schema")
	}
	if !strings.Contains(err.Error(), "unsupported future schema version") {
		t.Fatalf("LoadBank future schema error = %v", err)
	}
}

func TestMalformedSidecarsRejected(t *testing.T) {
	root := t.TempDir()
	files := []string{
		filepath.Join(root, "player", "json", "alice.json"),
		filepath.Join(root, "player", "bank", "json", "alice.json"),
		filepath.Join(root, "room", "json", "floor1.objects.json"),
		filepath.Join(root, "board", "json", "info.json"),
		filepath.Join(root, "player", "family", "json", "family_news_7.json"),
	}
	for _, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(`{"schemaVersion":`), 0o600); err != nil {
			t.Fatalf("write malformed %s: %v", path, err)
		}
	}

	if _, ok, err := state.LoadPlayer(root, "player:alice"); err == nil || ok || !strings.Contains(err.Error(), "parse saved player") {
		t.Fatalf("LoadPlayer malformed = ok %v err %v, want parse error", ok, err)
	}

	world := state.NewWorld(nil)
	defer world.Close()
	world.SetDBRoot(root)
	if _, ok, err := world.LoadBank("bank:player:alice"); err == nil || ok || !strings.Contains(err.Error(), "parse bank JSON") {
		t.Fatalf("LoadBank malformed = ok %v err %v, want parse error", ok, err)
	}

	if _, ok, err := state.LoadRoomObjects(root, "room:floor1"); err == nil || ok || !strings.Contains(err.Error(), "parse saved room objects") {
		t.Fatalf("LoadRoomObjects malformed = ok %v err %v, want parse error", ok, err)
	}
	if _, ok, err := state.LoadBoardPosts(root, "info"); err == nil || ok || !strings.Contains(err.Error(), "parse board posts sidecar") {
		t.Fatalf("LoadBoardPosts malformed = ok %v err %v, want parse error", ok, err)
	}
	if _, ok, err := state.LoadFamilyNews(root, 7); err == nil || ok || !strings.Contains(err.Error(), "parse family news sidecar") {
		t.Fatalf("LoadFamilyNews malformed = ok %v err %v, want parse error", ok, err)
	}
}

func TestSchemaMigrateSidecarsRewritesV1AndReportsFuture(t *testing.T) {
	root := t.TempDir()
	playerPath := filepath.Join(root, "player", "json", "alice.json")
	roomPath := filepath.Join(root, "room", "json", "floor1.objects.json")
	boardPath := filepath.Join(root, "board", "json", "info.json")
	familyNewsPath := filepath.Join(root, "player", "family", "json", "family_news_7.json")
	futureBoardPath := filepath.Join(root, "board", "json", "future.json")

	writeJSONFile(t, playerPath, state.PlayerSaveData{
		SchemaVersion: 1,
		Player:        model.Player{ID: "player:alice"},
	})
	writeJSONFile(t, roomPath, state.RoomObjectsSave{
		SchemaVersion: 1,
		RoomID:        "room:floor1",
	})
	writeJSONFile(t, boardPath, state.BoardPostsSave{
		SchemaVersion: 1,
		BoardDir:      "info",
	})
	writeJSONFile(t, familyNewsPath, state.FamilyNewsSave{
		SchemaVersion: 1,
		FamilyID:      7,
		Content:       "notice",
	})
	writeJSONFile(t, futureBoardPath, state.BoardPostsSave{
		SchemaVersion: state.CurrentSaveSchemaVersion + 1,
		BoardDir:      "future",
	})

	report, err := state.MigrateSidecars(root)
	if err != nil {
		t.Fatalf("MigrateSidecars returned top-level error: %v", err)
	}
	if report.Migrated != 4 {
		t.Fatalf("migrated = %d, want 4; report = %+v", report.Migrated, report)
	}
	wantByType := map[string]int{
		"player":     1,
		"room":       1,
		"board":      2,
		"familynews": 1,
	}
	for typ, want := range wantByType {
		if got := report.ByType[typ]; got != want {
			t.Fatalf("byType[%q] = %d, want %d; report = %+v", typ, got, want, report)
		}
	}
	if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "unsupported future schema version") {
		t.Fatalf("errors = %#v, want unsupported future schema", report.Errors)
	}
	if !strings.Contains(report.Errors[0], futureBoardPath) {
		t.Fatalf("future schema error = %q, want source path %q", report.Errors[0], futureBoardPath)
	}

	var player state.PlayerSaveData
	readJSONFile(t, playerPath, &player)
	if player.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("rewritten player schema = %d, want %d", player.SchemaVersion, state.CurrentSaveSchemaVersion)
	}

	var room state.RoomObjectsSave
	readJSONFile(t, roomPath, &room)
	if room.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("rewritten room schema = %d, want %d", room.SchemaVersion, state.CurrentSaveSchemaVersion)
	}

	var board state.BoardPostsSave
	readJSONFile(t, boardPath, &board)
	if board.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("rewritten board schema = %d, want %d", board.SchemaVersion, state.CurrentSaveSchemaVersion)
	}

	var familyNews state.FamilyNewsSave
	readJSONFile(t, familyNewsPath, &familyNews)
	if familyNews.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("rewritten family news schema = %d, want %d", familyNews.SchemaVersion, state.CurrentSaveSchemaVersion)
	}

	var futureBoard state.BoardPostsSave
	readJSONFile(t, futureBoardPath, &futureBoard)
	if futureBoard.SchemaVersion != state.CurrentSaveSchemaVersion+1 {
		t.Fatalf("future board schema = %d, want unchanged %d", futureBoard.SchemaVersion, state.CurrentSaveSchemaVersion+1)
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}
