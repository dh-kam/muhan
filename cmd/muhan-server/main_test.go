package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"muhan/internal/commandspec"
	enginecmd "muhan/internal/engine/command"
	"muhan/internal/engine/game"
	"muhan/internal/persist/cbin"
	"muhan/internal/persist/legacycrypt"
	"muhan/internal/persist/legacykr"
	"muhan/internal/session"
	worldload "muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

const (
	serverBoardNumberOff    = 0
	serverBoardUploaderOff  = 4
	serverBoardYearOff      = 20
	serverBoardMonthOff     = 24
	serverBoardDayOff       = 28
	serverBoardHourOff      = 32
	serverBoardMinuteOff    = 36
	serverBoardSecondOff    = 40
	serverBoardLineOff      = 44
	serverBoardReadCountOff = 48
	serverBoardTitleOff     = 52
)

func TestParseFlagsSourceRootOverridesRoot(t *testing.T) {
	var stderr bytes.Buffer

	cfg, err := parseFlags([]string{
		"-root", "legacy",
		"-source-root", "source",
		"-listen", ":4444",
		"-actor", "player:1",
		"-validate",
		"-migrate-sidecars",
	}, &stderr)
	if err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if cfg.root != "source" {
		t.Fatalf("root = %q, want source", cfg.root)
	}
	if cfg.listen != ":4444" {
		t.Fatalf("listen = %q, want :4444", cfg.listen)
	}
	if cfg.actor != "player:1" {
		t.Fatalf("actor = %q, want player:1", cfg.actor)
	}
	if !cfg.validate {
		t.Fatal("validate = false, want true")
	}
	if !cfg.migrate {
		t.Fatal("migrate = false, want true")
	}
}

func TestParseFlagsRejectsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer

	_, err := parseFlags([]string{"extra"}, &stderr)
	if err == nil {
		t.Fatal("parse flags succeeded, want error")
	}
	if !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %q, want unexpected arguments", err)
	}
}

func TestRestoreFamilyBankSidecarsMergesOnlyFamilyBanks(t *testing.T) {
	root := t.TempDir()
	savedLoaded := worldload.NewWorld()
	mustAddServerTestBank(t, savedLoaded, model.BankAccount{
		ID:        "bank:family:무영문_3",
		Kind:      "family",
		OwnerName: "무영문_3",
		Objects:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:family-root"}},
	})
	mustAddServerTestBank(t, savedLoaded, model.BankAccount{
		ID:        "bank:player:Alice",
		Kind:      "player",
		OwnerName: "Alice",
		Objects:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:player-root"}},
	})
	mustAddServerTestPrototype(t, savedLoaded, model.ObjectPrototype{
		ID:          "prototype:bank-root",
		DisplayName: "bank root",
	})
	mustAddServerTestObject(t, savedLoaded, model.ObjectInstance{
		ID:          "object:family-root",
		PrototypeID: "prototype:bank-root",
		Location:    model.ObjectLocation{BankID: "bank:family:무영문_3", Slot: "bank"},
	})
	mustAddServerTestObject(t, savedLoaded, model.ObjectInstance{
		ID:          "object:player-root",
		PrototypeID: "prototype:bank-root",
		Location:    model.ObjectLocation{BankID: "bank:player:Alice", Slot: "bank"},
	})
	savedWorld := state.NewWorld(savedLoaded)
	defer savedWorld.Close()
	savedWorld.SetDBRoot(root)
	if err := savedWorld.SaveBank("bank:family:무영문_3"); err != nil {
		t.Fatalf("SaveBank(family): %v", err)
	}
	if err := savedWorld.SaveBank("bank:player:Alice"); err != nil {
		t.Fatalf("SaveBank(player): %v", err)
	}

	freshLoaded := worldload.NewWorld()
	mustAddServerTestBank(t, freshLoaded, model.BankAccount{
		ID:        "bank:family:무영문_3",
		Kind:      "family",
		OwnerName: "무영문_3",
	})
	mustAddServerTestBank(t, freshLoaded, model.BankAccount{
		ID:        "bank:player:Alice",
		Kind:      "player",
		OwnerName: "Alice",
	})
	freshWorld := state.NewWorld(freshLoaded)
	defer freshWorld.Close()
	freshWorld.SetDBRoot(root)

	if got := restoreFamilyBankSidecars(root, freshWorld); got != 1 {
		t.Fatalf("restored family banks = %d, want 1", got)
	}
	familyBank, ok := freshWorld.Bank("bank:family:무영문_3")
	if !ok {
		t.Fatal("family bank missing after restore")
	}
	if !serverObjectListContains(familyBank.Objects.ObjectIDs, "object:family-root") {
		t.Fatalf("family bank objects = %+v, want saved root", familyBank.Objects.ObjectIDs)
	}
	if _, ok := freshWorld.Object("object:family-root"); !ok {
		t.Fatal("family bank object missing after restore")
	}
	if _, ok := freshWorld.Object("object:player-root"); ok {
		t.Fatal("player bank sidecar was merged by family bank restore")
	}
}

func TestCommandHandlersRegistersAvailableHandlers(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	handlers := commandHandlers(inputs)

	expectedKeys := []string{
		"look", "go", "move", "get", "drop", "quit", "inventory", "wear",
		"remove_obj", "equipment", "hold", "ready", "savegame", "set_title", "clear_title", "info_obj", "obj_compare", "health", "info", "where",
		"effect_flag_list", "cast", "study", "train", "list", "buy", "sell", "value", "repair", "drink", "zap", "use", "attack", "kick", "poison_mon", "up_dmg", "magic_stop", "steal", "search", "hide", "set", "clear", "openexit",
		"power", "accurate", "absorb", "backstab", "bash", "circle", "invincible_kick", "one_kill", "scratch", "eight", "nahan", "red_eye", "thief_stat", "poback",
		"bnahan", "tagu", "reflect", "shadow", "chang", "sasal", "rm_blind2", "choi", "turn", "teach", "prepare", "guard", "meditate", "lion_scream", "angel", "invince_train",
		"bank_inv", "bank", "deposit", "withdraw", "output_bank", "peek", "track",
		"closeexit", "unlock", "lock", "picklock", "flee", "prt_time", "haste", "pray", "return_square", "who", "whois", "pfinger", "follow", "lose",
		"group", "gtalk", "family_who", "family_talk", "family_news", "family", "boss_family", "fm_dis", "out_family", "fm_out", "family_member", "list_family",
		"family_bank_inv", "input_family_bank", "output_family_bank", "family_deposit", "family_withdraw",
		"invite", "marriage", "call_war", "give", "memo", "vote", "talk", "ignore", "send", "resend", "say", "broadsend", "broadsend2",
		"action", "emote", "yell", "help", "welcome",
		"look_board", "readscroll", "writeboard", "del_board", "postsend", "postread", "postdelete",
		"trade", "trans_exp", "m_send",
		"description", "passwd", "ply_alias", "suicide", "burn", "purchase", "selection", "forge", "newforge", "change_class",
		"buy_states", "chg_name", "pledge", "rescind", "sneak", "ply_aliases", "ply_suicide", "dm_resave",
		"dm_invis", "dm_send", "dm_purge", "dm_ac", "dm_users", "dm_echo",
		"dm_flushsave", "dm_shutdown", "dm_force", "dm_flush_crtobj", "dm_create_crt", "dm_stat",
		"dm_add_rom", "dm_set", "dm_log", "dm_spy", "dm_loadlockout", "dm_finger",
		"dm_broadecho", "dm_replace", "dm_list", "dm_info", "dm_param", "dm_silence",
		"dm_nameroom", "dm_append", "dm_prepend", "dm_cast", "dm_group", "notepad",
		"dm_delete", "dm_obj_name", "dm_crt_name", "list_act", "dm_dust", "dm_follow",
		"dm_help", "dm_attack", "list_enm", "list_charm", "dm_save_all_ply",
	}
	expectedKeys = append(expectedKeys, enginecmd.DefaultDMPlaceholderHandlerKeys...)

	for _, name := range expectedKeys {
		if handlers[name] == nil {
			t.Fatalf("handler %q is not registered", name)
		}
	}
	if len(handlers) != 203 {
		t.Fatalf("registered handlers = %d, want 203", len(handlers))
	}
}

func TestServerLoginBindsActorAfterLegacyPassword(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	login := newServerLoginManager(inputs.world)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithErrorFormatter(func(err error) string {
			if errors.Is(err, enginecmd.ErrUnknownCommand) || errors.Is(err, enginecmd.ErrUnhandledCommand) {
				return "무슨 말인지 모르겠습니다.\n"
			}
			return err.Error() + "\n"
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login", commands, "")
	login.Start("s-login")

	handleServerTestLine(t, loop, "s-login", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")

	handleServerTestLine(t, loop, "s-login", "wrong")
	assertServerCommandContains(t, commands, session.Command{}, "암호가 틀립니다", "암호를 다시 입력하세요")

	handleServerTestLine(t, loop, "s-login", "1234")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "무한에 접속했습니다.")

	handleServerTestLine(t, loop, "s-login", "봐")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "\n광장\n\n", "출발 광장이다.")
}

func TestServerLoginCreatesNewPlayerLikeLegacyCreatePly(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	inputs.world.SetDBRoot(inputs.summary.Root)
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}
	var broadcasts []string
	inputs.world.BroadcastAllFunc = func(message string) error {
		broadcasts = append(broadcasts, message)
		return nil
	}
	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 16)
	registerServerTestSession(t, loop, "s-create", commands, "")
	login.Start("s-create")

	handleServerTestLine(t, loop, "s-create", "새영웅")
	assertServerCommandContains(t, commands, session.Command{}, "새영웅으로 하시겠습니까(예/아니오)? ")
	handleServerTestLine(t, loop, "s-create", "예")
	assertServerCommandContains(t, commands, session.Command{}, "[엔터]를 누르십시요.")
	handleServerTestLine(t, loop, "s-create", "")
	assertServerCommandContains(t, commands, session.Command{}, "당신은 남자입니까, 여자입니까")
	handleServerTestLine(t, loop, "s-create", "남자")
	assertServerCommandContains(t, commands, session.Command{}, "직업을 고르세요: ")
	handleServerTestLine(t, loop, "s-create", "4")
	assertServerCommandContains(t, commands, session.Command{}, "당신은 54점으로", ": ")
	handleServerTestLine(t, loop, "s-create", "12 10 12 10 10")
	assertServerCommandContains(t, commands, session.Command{}, "당신에게 익숙한 무기를", ": ")
	handleServerTestLine(t, loop, "s-create", "2")
	assertServerCommandContains(t, commands, session.Command{}, "성향을 고르십시요")
	handleServerTestLine(t, loop, "s-create", "악함")
	assertServerCommandContains(t, commands, session.Command{}, "종족을 고르십시요: ")
	handleServerTestLine(t, loop, "s-create", "8")
	assertServerCommandContains(t, commands, session.Command{}, "새 암호를 넣으십시요")
	handleServerTestLine(t, loop, "s-create", "abc")
	assertServerCommandContains(t, commands, session.Command{},
		"무한에 접속했습니다.",
		"login news\n",
		loginNewsWaitPrompt,
	)
	handleServerTestLine(t, loop, "s-create", "")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"[환영]이라고 치시면",
		"레벨이 6이상 되지 않으면",
		legacyCreateNewFamilyBroadcast,
	)
	if len(broadcasts) != 1 || broadcasts[0] != legacyCreateNewFamilyBroadcast {
		t.Fatalf("new player broadcasts = %+v, want [%q]", broadcasts, legacyCreateNewFamilyBroadcast)
	}

	player, ok := inputs.world.Player("새영웅")
	if !ok {
		t.Fatal("created player missing from runtime world")
	}
	if player.DisplayName != "새영웅" || player.CreatureID != "creature:player:새영웅" || player.RoomID != "room:00001" {
		t.Fatalf("created player = %+v, want legacy ids and room", player)
	}
	creature, ok := inputs.world.Creature("creature:player:새영웅")
	if !ok {
		t.Fatal("created creature missing from runtime world")
	}
	wantStats := map[string]int{
		"class":             4,
		"race":              6,
		"level":             1,
		"strength":          13,
		"dexterity":         13,
		"constitution":      17,
		"intelligence":      9,
		"piety":             10,
		"hpMax":             56,
		"hpCurrent":         56,
		"mpMax":             50,
		"mpCurrent":         50,
		"nDice":             1,
		"sDice":             5,
		"pDice":             1,
		"gold":              1000,
		"PMALES":            1,
		"PCHAOS":            1,
		"PLECHO":            1,
		"PPROMP":            1,
		"PANSIC":            1,
		"PBRIGH":            1,
		"PNOEXT":            1,
		"PDSCRP":            1,
		"PNOSUM":            1,
		"proficiencyThrust": 1024,
		"proficiency/1":     1024,
	}
	for key, want := range wantStats {
		if got := creature.Stats[key]; got != want {
			t.Fatalf("created creature stat %s = %d, want %d; stats=%+v", key, got, want, creature.Stats)
		}
	}
	if got := legacyPasswordHash(inputs.world, player); !legacycrypt.IsBcryptHash(got) || !legacycrypt.VerifyBcrypt("abc", got) {
		t.Fatalf("created password hash = %q, want valid bcrypt hash for abc", got)
	}
	room, ok := inputs.world.Room("room:00001")
	if !ok {
		t.Fatal("starting room missing")
	}
	if !serverPlayerListContains(room.PlayerIDs, "새영웅") || !serverCreatureListContains(room.CreatureIDs, "creature:player:새영웅") {
		t.Fatalf("starting room occupants players=%+v creatures=%+v, want created player", room.PlayerIDs, room.CreatureIDs)
	}
	saved, ok, err := state.LoadPlayer(inputs.summary.Root, "새영웅")
	if err != nil || !ok {
		t.Fatalf("LoadPlayer(created) ok=%v err=%v", ok, err)
	}
	if saved.Player.ID != "새영웅" || saved.Creature == nil || saved.Creature.ID != "creature:player:새영웅" {
		t.Fatalf("created sidecar = %+v", saved)
	}
}

func TestServerLoginCreatePasswordUsesLegacyByteLength(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	inputs.world.SetDBRoot(inputs.summary.Root)
	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	create := serverLoginState{
		step: serverLoginCreatePassword,
		create: serverLoginCreateState{
			name:         "암호영웅",
			male:         true,
			class:        model.ClassFighter,
			strength:     12,
			dexterity:    10,
			constitution: 12,
			intelligence: 10,
			piety:        10,
			weapon:       1,
			race:         legacyCreateRaceHuman,
		},
	}

	tooLong, err := login.handleCreatePassword("s-create", create, "가나다라마바사아")
	if err != nil {
		t.Fatalf("overlong handleCreatePassword() error = %v", err)
	}
	if !strings.Contains(tooLong.Command.Write, "입력된 암호가 너무 깁니다.") {
		t.Fatalf("overlong output = %q, want too long", tooLong.Command.Write)
	}
	if _, ok := inputs.world.Player("암호영웅"); ok {
		t.Fatal("overlong create password created player")
	}

	accepted, err := login.handleCreatePassword("s-create", create, "가나다라마바사")
	if err != nil {
		t.Fatalf("accepted handleCreatePassword() error = %v", err)
	}
	if !strings.Contains(accepted.Command.Write, "무한에 접속했습니다.") {
		t.Fatalf("accepted output = %q, want login success", accepted.Command.Write)
	}
	player, ok := inputs.world.Player("암호영웅")
	if !ok {
		t.Fatal("accepted create password did not create player")
	}
	if got := legacyPasswordHash(inputs.world, player); !legacycrypt.IsBcryptHash(got) || !legacycrypt.VerifyBcrypt("가나다라마바사", got) {
		t.Fatalf("created password hash = %q, want valid bcrypt hash for 가나다라마바사", got)
	}
}

func TestServerLoginReportsWaitingPostLikeLegacy(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}
	postDir := filepath.Join(inputs.summary.Root, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		t.Fatalf("mkdir post dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(postDir, "Alice"), []byte("legacy mail\n"), 0o600); err != nil {
		t.Fatalf("write Alice post file: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-mail", commands, "")
	login.Start("s-login-mail")

	handleServerTestLine(t, loop, "s-login-mail", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-mail", "1234")
	assertServerCommandContains(t, commands, session.Command{},
		"무한에 접속했습니다.",
		"login news\n",
		loginNewsWaitPrompt,
	)
	handleServerTestLine(t, loop, "s-login-mail", "")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"*** 우체국에 편지가 와있습니다.",
	)
}

func TestServerLoginRendersNewsAndWaitsLikeLegacy(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-news", commands, "")
	login.Start("s-login-news")

	handleServerTestLine(t, loop, "s-login-news", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-news", "1234")
	assertServerCommandContains(t, commands, session.Command{},
		"무한에 접속했습니다.",
		"login news\n",
		loginNewsWaitPrompt,
	)
	handleServerTestLine(t, loop, "s-login-news", "")
	assertServerCommand(t, commands, session.Command{Prompt: "> "})

	handleServerTestLine(t, loop, "s-login-news", "봐")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "\n광장\n\n", "출발 광장이다.")
}

func TestServerLoginRendersDialinForLegacyAddressBeforeNews(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	helpDir := filepath.Join(inputs.summary.Root, "help")
	if err := os.MkdirAll(helpDir, 0o700); err != nil {
		t.Fatalf("mkdir help dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(helpDir, "dialin"), []byte("dialin notice\n"), 0o600); err != nil {
		t.Fatalf("write dialin notice: %v", err)
	}
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-dialin", commands, "")
	login.Start("s-login-dialin", legacyLoginDialinHost)

	handleServerTestLine(t, loop, "s-login-dialin", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-dialin", "1234")
	got := receiveServerCommand(t, commands)
	dialinAt := strings.Index(got.Write, "dialin notice\n")
	newsAt := strings.Index(got.Write, "login news\n")
	if got.Prompt != "" || dialinAt < 0 || newsAt < 0 || dialinAt > newsAt ||
		!strings.Contains(got.Write, loginNewsWaitPrompt) {
		t.Fatalf("dialin login command = %#v", got)
	}
}

func TestServerLoginPaginatesLongNewsLikeLegacyViewFile(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	var b strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&b, "news line %02d\n", i)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write long login news: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-long-news", commands, "")
	login.Start("s-login-long-news")

	handleServerTestLine(t, loop, "s-login-long-news", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-long-news", "1234")
	first := receiveServerCommand(t, commands)
	if first.Prompt != "" || !strings.Contains(first.Write, "news line 19\n") ||
		strings.Contains(first.Write, "news line 20\n") ||
		!strings.Contains(first.Write, enginecmd.LegacyViewFileContinuePrompt) {
		t.Fatalf("first long-news login command = %#v", first)
	}

	handleServerTestLine(t, loop, "s-login-long-news", "")
	second := receiveServerCommand(t, commands)
	if second.Prompt != "" || !strings.Contains(second.Write, "news line 20\n") ||
		!strings.Contains(second.Write, "news line 25\n") ||
		!strings.Contains(second.Write, loginNewsWaitPrompt) {
		t.Fatalf("second long-news login command = %#v", second)
	}

	handleServerTestLine(t, loop, "s-login-long-news", "")
	assertServerCommand(t, commands, session.Command{Prompt: "> "})
}

func TestServerLoginShowsFamilyNewsAndFalThenDeletesFalLikeLegacy(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}
	familyDir := filepath.Join(inputs.summary.Root, "player", "family")
	if err := os.MkdirAll(familyDir, 0o700); err != nil {
		t.Fatalf("mkdir family dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(familyDir, "family_news_7"), []byte("family login news\n"), 0o600); err != nil {
		t.Fatalf("write family login news: %v", err)
	}
	falDir := filepath.Join(inputs.summary.Root, "player", "fal")
	if err := os.MkdirAll(falDir, 0o700); err != nil {
		t.Fatalf("mkdir fal dir: %v", err)
	}
	falPath := filepath.Join(falDir, "Alice")
	if err := os.WriteFile(falPath, []byte("fal login notice\n"), 0o600); err != nil {
		t.Fatalf("write fal notice: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-family-fal", commands, "")
	login.Start("s-login-family-fal")

	handleServerTestLine(t, loop, "s-login-family-fal", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-family-fal", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "login news\n", loginNewsWaitPrompt)

	handleServerTestLine(t, loop, "s-login-family-fal", "")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"family login news\n",
		"fal login notice\n",
	)
	if _, err := os.Stat(falPath); !os.IsNotExist(err) {
		t.Fatalf("fal stat error = %v, want removed", err)
	}
}

func TestServerLoginUsesDMNewsForDMClassLikeLegacy(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.SetCreatureStat("creature:dm", "class", legacyLoginDMClass); err != nil {
		t.Fatalf("set DM class: %v", err)
	}
	if _, err := inputs.world.SetCreaturePasswordHash("creature:dm", "WOCZU5Ja1Vg"); err != nil {
		t.Fatalf("set DM password hash: %v", err)
	}
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("ordinary login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "DM_news"), []byte("dm login news\n"), 0o600); err != nil {
		t.Fatalf("write DM login news: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-dm-news", commands, "")
	login.Start("s-login-dm-news")

	handleServerTestLine(t, loop, "s-login-dm-news", "player:dm")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-dm-news", "1234")
	got := receiveServerCommand(t, commands)
	if got.Prompt != "" || !strings.Contains(got.Write, "dm login news\n") ||
		strings.Contains(got.Write, "ordinary login news\n") ||
		!strings.Contains(got.Write, loginNewsWaitPrompt) {
		t.Fatalf("DM login news command = %#v", got)
	}
}

func TestServerLoginCapsExcessGoldLikeLegacyLoadPly(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.SetCreatureStat("creature:alice", "gold", legacyLoginGoldCap+1); err != nil {
		t.Fatalf("set Alice gold: %v", err)
	}
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithUnauthenticatedLineHandler(login.HandleLine),
	)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login-gold-cap", commands, "")
	login.Start("s-login-gold-cap")

	handleServerTestLine(t, loop, "s-login-gold-cap", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-login-gold-cap", "1234")
	assertServerCommandContains(t, commands, session.Command{}, "login news\n", loginNewsWaitPrompt)
	handleServerTestLine(t, loop, "s-login-gold-cap", "")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"너무 많은 돈을 가지고 있습니다.",
		"3억만 남겨놓고",
	)

	creature, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice after login")
	}
	if got := creature.Stats["gold"]; got != legacyLoginGoldCap {
		t.Fatalf("Alice gold after login = %d, want %d", got, legacyLoginGoldCap)
	}
}

func TestServerLoginDisconnectsAfterThreeBadPasswords(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	login := newServerLoginManager(inputs.world)
	loop := game.NewLoop(serverDispatcher(inputs), game.WithUnauthenticatedLineHandler(login.HandleLine))
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-login", commands, "")
	login.Start("s-login")

	handleServerTestLine(t, loop, "s-login", "player:alice")
	_ = receiveServerCommand(t, commands)
	for i := 0; i < 2; i++ {
		handleServerTestLine(t, loop, "s-login", "wrong")
		got := receiveServerCommand(t, commands)
		if got.Close {
			t.Fatalf("attempt %d closed session early: %#v", i+1, got)
		}
	}
	handleServerTestLine(t, loop, "s-login", "wrong")
	got := receiveServerCommand(t, commands)
	if !got.Close || !strings.Contains(got.Write, "접속을 끊습니다") {
		t.Fatalf("third bad password command = %#v, want close", got)
	}
}

func TestServerLoginSitePasswordGate(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	login := newServerLoginManager(inputs.world)
	loop := game.NewLoop(serverDispatcher(inputs), game.WithUnauthenticatedLineHandler(login.HandleLine))

	blocked := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s-blocked", blocked, "")
	login.StartWithSitePassword("s-blocked", "sitepass")
	handleServerTestLine(t, loop, "s-blocked", "wrong")
	got := receiveServerCommand(t, blocked)
	if !got.Close || !strings.Contains(got.Write, "Your site is locked out.") {
		t.Fatalf("bad site password command = %#v, want lockout close", got)
	}

	allowed := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-allowed", allowed, "")
	login.StartWithSitePassword("s-allowed", "sitepass")
	handleServerTestLine(t, loop, "s-allowed", "sitepass")
	assertServerCommandContains(t, allowed, session.Command{}, "당신의 이름은 무엇입니까?")
	handleServerTestLine(t, loop, "s-allowed", "player:alice")
	assertServerCommandContains(t, allowed, session.Command{}, "암호를 넣어 주십시요: ")
}

func TestServerLoginSitePasswordPreservesDialinHost(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	helpDir := filepath.Join(inputs.summary.Root, "help")
	if err := os.MkdirAll(helpDir, 0o700); err != nil {
		t.Fatalf("mkdir help dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(helpDir, "dialin"), []byte("dialin after site password\n"), 0o600); err != nil {
		t.Fatalf("write dialin notice: %v", err)
	}
	logDir := filepath.Join(inputs.summary.Root, "log")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatalf("mkdir log dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "news"), []byte("login news\n"), 0o600); err != nil {
		t.Fatalf("write login news: %v", err)
	}

	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	loop := game.NewLoop(serverDispatcher(inputs), game.WithUnauthenticatedLineHandler(login.HandleLine))
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-site-dialin", commands, "")
	login.StartWithSitePassword("s-site-dialin", "sitepass", legacyLoginDialinHost)

	handleServerTestLine(t, loop, "s-site-dialin", "sitepass")
	assertServerCommandContains(t, commands, session.Command{}, "당신의 이름은 무엇입니까?")
	handleServerTestLine(t, loop, "s-site-dialin", "player:alice")
	assertServerCommandContains(t, commands, session.Command{}, "암호를 넣어 주십시요: ")
	handleServerTestLine(t, loop, "s-site-dialin", "1234")
	assertServerCommandContains(t, commands, session.Command{},
		"dialin after site password\n",
		"login news\n",
		loginNewsWaitPrompt,
	)
}

func TestRealRootHelpCommandDispatches(t *testing.T) {
	inputs, err := loadRuntimeInputs("../..")
	if err != nil {
		t.Fatalf("load runtime inputs: %v", err)
	}
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "인제로")

	handleServerTestLine(t, loop, "s1", "도움")
	assertServerCommandContains(t, commands, session.Command{}, "명령어",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ")
	handleServerTestLine(t, loop, "s1", ".")
	assertServerCommand(t, commands, session.Command{Write: "중단합니다.\n", Prompt: "> "})

	handleServerTestLine(t, loop, "s1", "누구 도움")
	assertServerCommandContains(t, commands, session.Command{}, "누구")
}

func TestServerDispatcherRunsCurrentRegisteredCommandSequence(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	aliceCommands := make(chan session.Command, 16)
	bobCommands := make(chan session.Command, 16)
	registerServerTestSession(t, loop, "s1", aliceCommands, "player:alice")
	registerServerTestSession(t, loop, "s2", bobCommands, "player:bob")

	handleServerTestLine(t, loop, "s1", "봐")
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "\n광장\n\n출발 광장이다.\n[ 출구 : 동 ]\nBob님이 서 있습니다.\n경비병이 서 있다.\n작은 돌이 놓여져 있습니다.\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "작은 돌 주워")
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 작은 돌을 줍습니다.\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "동 가")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"\n동쪽\n\n", "동쪽 방이다.\n", "[ 출구 : 서 ]\n")

	handleServerTestLine(t, loop, "s1", "서")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"\n광장\n\n", "출발 광장이다.\n", "[ 출구 : 동 ]\n")

	handleServerTestLine(t, loop, "s1", "소지품")
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "소지품:\n  빛나는 검, 작은 돌.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "장비")
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 걸치고 있는게 아무것도 없습니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "점수")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"Alice :", "(레벨", "[체  력]", "[도  력]", "[방어력]", "[  돈  ]")

	handleServerTestLine(t, loop, "s1", "어디")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"사용자", "장소", "Alice", "Bob", "광장", "총 2명의 사용자가 통계무한을 이용하고 있습니다.")

	handleServerTestLine(t, loop, "s1", "상태")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"========================================================================\n", "현재 Alice님의 상태\n")

	inputs.world.SetLegacyTime(23)
	handleServerTestLine(t, loop, "s1", "시간")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"현재 시간: 오후 11시.\n", "실제 시간:", "(KST).")

	handleServerTestLine(t, loop, "s1", "작은 돌 버려")
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 작은 돌을 버렸습니다.\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "누구")
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "접속자:\n - Alice\n - Bob\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "도움")
	assertServerCommandContains(t, aliceCommands, session.Command{}, "기본 도움말")

	handleServerTestLine(t, loop, "s1", "누구 도움")
	assertServerCommandContains(t, aliceCommands, session.Command{}, "누구 도움말")

	handleServerTestLine(t, loop, "s1", "환영")
	assertServerCommandContains(t, aliceCommands, session.Command{}, "환영 도움말")

	handleServerTestLine(t, loop, "s1", "안녕 말")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice가 \"안녕\"라고 말합니다."})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "예. 좋습니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "Bob 귓속말 이야기")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice가 당신에게 \"귓속말\"라고 이야기합니다."})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "Bob님에게 말을 전달하였습니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s2", "잘 들려 대답")
	assertServerCommand(t, aliceCommands, session.Command{Write: "\nBob가 당신에게 \"잘 들려\"라고 대답합니다."})
	assertServerCommand(t, bobCommands, session.Command{
		Write:  "Alice님에게 말을 전하였습니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "모두 안녕 잡담")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice> 모두 안녕"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "\nAlice> 모두 안녕",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "좋다 환호")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice님이 \"좋다\"라고 환호를 합니다."})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "\nAlice님이 \"좋다\"라고 환호를 합니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "패거리누구")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "},
		"당신은 [패거리7] 패거리에 소속되어 있습니다.", "Alice", "Bob", "총 2명의 패거리원들이 이용중입니다.")

	handleServerTestLine(t, loop, "s1", "전원 준비 패거리말")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice>>> 전원 준비"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "\nAlice>>> 전원 준비",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s2", "Alice 따라")
	assertServerCommand(t, aliceCommands, session.Command{Write: "\nBob가 이제부터 당신을 따라다닙니다."})
	assertServerCommand(t, bobCommands, session.Command{
		Write:  "당신은 이제부터 Alice님을 따라다닙니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "그룹")
	assertServerCommandContains(t, aliceCommands, session.Command{Prompt: "> "}, "그룹원:", "Alice", "(대장)", "Bob")

	handleServerTestLine(t, loop, "s2", "준비 그룹말")
	assertServerCommand(t, aliceCommands, session.Command{Write: "Bob가 그룹원들에게 \"준비\"라고 말합니다.\n"})
	assertServerCommand(t, bobCommands, session.Command{
		Write:  "Bob가 그룹원들에게 \"준비\"라고 말합니다.\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "Bob 내보내")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice가 당신이 못따라 오도록 하였습니다."})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 Bob가 당신을 못따라 오도록 하였습니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "미소")
	assertServerCommand(t, bobCommands, session.Command{Write: "Alice가 밝은 미소를 짓습니다.\n"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 밝은 미소를 짓습니다.\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "경비병 안녕")
	assertServerCommand(t, bobCommands, session.Command{Write: "Alice가 경비병에게 인사를 합니다. \"안녕하세요~\"\n"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 경비병에게 인사를 합니다. \"안녕하세요~\"\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "환하게 웃는다 표현")
	assertServerCommand(t, bobCommands, session.Command{Write: "\n:Alice가 환하게 웃는다."})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "예. 좋습니다.\n",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "도와줘 외쳐")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice이 \"도와줘 외쳐!\"라고 외칩니다."})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "예. 좋습니다.",
		Prompt: "> ",
	})

	handleServerTestLine(t, loop, "s1", "끝")
	assertServerCommand(t, aliceCommands, session.Command{
		Write: "안녕히 가세요.\n",
		Close: true,
	})

	assertNoServerCommand(t, bobCommands)

	player, ok := inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice")
	}
	if player.RoomID != "room:plaza" {
		t.Fatalf("alice room = %q, want room:plaza", player.RoomID)
	}
	creature, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice")
	}
	if creature.RoomID != "room:plaza" {
		t.Fatalf("alice creature room = %q, want room:plaza", creature.RoomID)
	}
	object, ok := inputs.world.Object("object:stone")
	if !ok {
		t.Fatal("missing object:stone")
	}
	if object.Location.RoomID != "room:plaza" || !object.Location.CreatureID.IsZero() {
		t.Fatalf("stone location = %+v, want room:plaza", object.Location)
	}
}

func TestServerLoopMemoCommandWritesLegacyTargetFile(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "bob 확인 메모")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "메모를 남겼습니다.")

	data, err := os.ReadFile(filepath.Join(inputs.summary.Root, "player", "fal", "Bob"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, " 에 [Alice] 님이 남기신 메모 : \n>>>>> 확인\n") {
		t.Fatalf("memo file = %q, want Alice legacy memo entry", text)
	}
	if _, err := os.Stat(filepath.Join(inputs.summary.Root, "player", "fal", "bob")); !os.IsNotExist(err) {
		t.Fatalf("lowercase memo file stat error = %v, want not exist", err)
	}
}

func TestServerLoopAliasNameUsesLegacyByteLimit(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", strings.Repeat("가", 16)+" 북 줄")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "줄임말의 길이가 너무 깁니다.")

	if _, err := os.Stat(filepath.Join(inputs.summary.Root, "player", "alias", "Alice")); !os.IsNotExist(err) {
		t.Fatalf("alias file stat error = %v, want not exist after rejected alias", err)
	}
}

func TestServerLoopInfoCommandUsesPendingSecondPage(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "정보")
	assertServerCommandContains(t, commands, session.Command{},
		"\n[이  름] Alice        [배우자]",
		"## 무기사용능력 ##",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ")

	handleServerTestLine(t, loop, "s1", "")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"## 주 술  계 열 ##",
		"주문: ",
		"당신의 현주문: ",
		"당신은 현재 달성한 임무가 없습니다.")

	handleServerTestLine(t, loop, "s1", "점수")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"Alice :", "[체  력]", "[  돈  ]")

	handleServerTestLine(t, loop, "s1", "정보")
	assertServerCommandContains(t, commands, session.Command{},
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ")

	handleServerTestLine(t, loop, "s1", ".")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"중단되었습니다.\n")

	handleServerTestLine(t, loop, "s1", "점수")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"Alice :", "[체  력]", "[  돈  ]")
	assertNoServerCommand(t, commands)
}

func TestForceWorldWrapperDispatchesToTargetSession(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	var loop *game.Loop
	inputs.getLoop = func() *game.Loop {
		return loop
	}
	loop = newServerTestLoop(inputs)
	aliceCommands := make(chan session.Command, 4)
	bobCommands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", aliceCommands, "player:alice")
	registerServerTestSession(t, loop, "s2", bobCommands, "player:bob")

	force := &forceWorldWrapper{World: inputs.world, getLoop: inputs.getLoop}
	if err := force.ForcePlayerCommand("player:bob", "봐"); err != nil {
		t.Fatalf("ForcePlayerCommand() error = %v", err)
	}
	assertServerCommandContains(t, bobCommands, session.Command{Prompt: "> "}, "\n광장\n\n", "출발 광장이다.")
	assertNoServerCommand(t, aliceCommands)
}

func TestForceWorldWrapperRejectsPendingTargetLikeLegacy(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	var loop *game.Loop
	inputs.getLoop = func() *game.Loop {
		return loop
	}
	loop = newServerTestLoop(inputs)
	bobCommands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s2", bobCommands, "player:bob")

	handleServerTestLine(t, loop, "s2", "정보")
	assertServerCommandContains(t, bobCommands, session.Command{},
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ")

	force := &forceWorldWrapper{World: inputs.world, getLoop: inputs.getLoop}
	if force.CanForcePlayerCommand("player:bob") {
		t.Fatal("CanForcePlayerCommand(player:bob) = true, want false while pending")
	}
	err := force.ForcePlayerCommand("player:bob", "봐")
	if err == nil || !strings.Contains(err.Error(), "command input unavailable") {
		t.Fatalf("ForcePlayerCommand() error = %v, want command input unavailable", err)
	}
	assertNoServerCommand(t, bobCommands)
}

func TestServerLoopGiveCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	aliceCommands := make(chan session.Command, 4)
	bobCommands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", aliceCommands, "player:alice")
	registerServerTestSession(t, loop, "s2", bobCommands, "player:bob")

	handleServerTestLine(t, loop, "s1", "빛나는 검 Bob 줘")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice가 당신에게 빛나는 검을 줍니다.\n"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 Bob에게 빛나는 검을 줍니다.\n",
		Prompt: "> ",
	})
	assertServerObjectLocation(t, inputs.world, "object:sword", model.ObjectLocation{CreatureID: "creature:bob", Slot: "inventory"})
	assertServerCreatureInventory(t, inputs.world, "creature:alice", "object:sword", false)
	assertServerCreatureInventory(t, inputs.world, "creature:bob", "object:sword", true)

	handleServerTestLine(t, loop, "s1", "1,000냥 Bob 줘")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice가 당신에게 1냥을 주었습니다.\n"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "당신은 Bob에게 1냥을 주었습니다.\n",
		Prompt: "> ",
	})
	assertServerCreatureGold(t, inputs.world, "creature:alice", 99999)
	assertServerCreatureGold(t, inputs.world, "creature:bob", 1)
	assertNoServerCommand(t, aliceCommands)
	assertNoServerCommand(t, bobCommands)
}

func TestServerLoopTalkCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	aliceCommands := make(chan session.Command, 4)
	bobCommands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", aliceCommands, "player:alice")
	registerServerTestSession(t, loop, "s2", bobCommands, "player:bob")

	handleServerTestLine(t, loop, "s1", "경비병 대화")
	assertServerCommand(t, bobCommands, session.Command{Write: "\nAlice가 경비병과 이야기를 합니다.\n"})
	assertServerCommand(t, bobCommands, session.Command{Write: "\n경비병이 Alice에게 \"광장에서는 천천히 움직이십시오.\"라고 이야기합니다.\n"})
	assertServerCommand(t, aliceCommands, session.Command{
		Write:  "\n경비병이 당신에게 \"광장에서는 천천히 움직이십시오.\"라고 이야기합니다.\n",
		Prompt: "> ",
	})
	assertNoServerCommand(t, aliceCommands)
	assertNoServerCommand(t, bobCommands)
}

func TestServerLoopBoardListAndReadCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:mallory")

	handleServerTestLine(t, loop, "s1", "게시판")
	assertServerCommandContains(t, commands, session.Command{},
		"번호 올린이", "2 무한", "둘째 공지", "1 운영자", "첫 공지", "번호, 앞페이지")
	handleServerTestLine(t, loop, "s1", "1")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{}, "번호: 1", "첫 본문입니다")
	if strings.Contains(got.Write, "번호 올린이") {
		t.Fatalf("menu read immediately re-rendered list:\n%s", got.Write)
	}
	handleServerTestLine(t, loop, "s1", "")
	assertServerCommandContains(t, commands, session.Command{},
		"번호 올린이", "2 무한", "둘째 공지", "1 운영자", "첫 공지", "번호, 앞페이지")
	handleServerTestLine(t, loop, "s1", "q")
	assertServerCommand(t, commands, session.Command{Write: "게시물을 그만 봅니다.", Prompt: "> "})

	handleServerTestLine(t, loop, "s1", "게시판 읽어")
	assertServerCommandContains(t, commands, session.Command{},
		"번호 올린이", "2 무한", "둘째 공지", "1 운영자", "첫 공지", "번호, 앞페이지")
	handleServerTestLine(t, loop, "s1", "q")
	assertServerCommand(t, commands, session.Command{Write: "게시물을 그만 봅니다.", Prompt: "> "})

	handleServerTestLine(t, loop, "s1", "1 게시판")
	assertServerCommandContains(t, commands, session.Command{},
		"번호: 1", "올린이: 운영자", "제목: 첫 공지", "첫 본문입니다")

	handleServerTestLine(t, loop, "s1", "게시판 2 읽어")
	assertServerCommandContains(t, commands, session.Command{},
		"번호: 2", "둘째 본문입니다")

	handleServerTestLine(t, loop, "s1", "게시판 2")
	assertServerCommandContains(t, commands, session.Command{},
		"번호: 2", "둘째 본문입니다")
}

func TestServerLoopBoardReadDisconnectStopsPendingPager(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	bodyPath := filepath.Join(inputs.summary.Root, "board", "info", "board.1")
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(bodyPath, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:mallory")

	handleServerTestLine(t, loop, "s1", "1 게시판")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{},
		"번호: 1",
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)
	if strings.Contains(got.Write, "line T\n") {
		t.Fatalf("first board page included continuation line:\n%s", got.Write)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(board read continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(bodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("board read changed body after closed-session continue:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopBoardWriteAndDeleteCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	inputs.world.SetDBRoot(inputs.summary.Root)
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:mallory")

	handleServerTestLine(t, loop, "s1", "써")
	assertServerCommand(t, commands, session.Command{Write: "제목: "})

	handleServerTestLine(t, loop, "s1", "새 공지")
	assertServerCommandContains(t, commands, session.Command{},
		"게시물을 작성합니다.", "  1: ")

	handleServerTestLine(t, loop, "s1", "첫 줄")
	assertServerCommand(t, commands, session.Command{Write: "  2: "})

	handleServerTestLine(t, loop, "s1", "둘째 줄")
	assertServerCommand(t, commands, session.Command{Write: "  3: "})

	handleServerTestLine(t, loop, "s1", ".")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "게시물이 등록되었습니다.")

	handleServerTestLine(t, loop, "s1", "게시판")
	assertServerCommandContains(t, commands, session.Command{}, "3 Mallory", "새 공지", "번호, 앞페이지")
	handleServerTestLine(t, loop, "s1", "q")
	assertServerCommand(t, commands, session.Command{Write: "게시물을 그만 봅니다.", Prompt: "> "})

	handleServerTestLine(t, loop, "s1", "3 게시판")
	assertServerCommandContains(t, commands, session.Command{},
		"번호: 3", "올린이: Mallory", "제목: 새 공지", "첫 줄", "둘째 줄")

	handleServerTestLine(t, loop, "s1", "게시판 3 글삭제")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "게시물이 삭제되었습니다.")

	handleServerTestLine(t, loop, "s1", "게시판")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{}, "번호 올린이", "둘째 공지", "번호, 앞페이지")
	if strings.Contains(got.Write, "새 공지") {
		t.Fatalf("deleted post still appears in list:\n%s", got.Write)
	}
	handleServerTestLine(t, loop, "s1", "q")
	assertServerCommand(t, commands, session.Command{Write: "게시물을 그만 봅니다.", Prompt: "> "})

	handleServerTestLine(t, loop, "s1", "3 게시판")
	assertServerCommand(t, commands, session.Command{Write: "삭제된 게시물입니다. [엔터]를 눌러주세요. ", Prompt: "> "})
}

func TestServerLoopBoardWriteDisconnectBeforeFinalDotDoesNotPersist(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	root := inputs.summary.Root
	boardDir := filepath.Join(root, "board", "info")
	indexPath := filepath.Join(boardDir, "board_index")
	bodyPath := filepath.Join(boardDir, "board.3")

	beforeIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat board index before write: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:mallory")

	handleServerTestLine(t, loop, "s1", "써")
	assertServerCommand(t, commands, session.Command{Write: "제목: "})
	handleServerTestLine(t, loop, "s1", "닫히는 글")
	assertServerCommandContains(t, commands, session.Command{}, "게시물을 작성합니다.", "  1: ")
	handleServerTestLine(t, loop, "s1", "아직 저장되면 안 됩니다")
	assertServerCommand(t, commands, session.Command{Write: "  2: "})

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "."})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(final dot after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	if _, err := os.Stat(bodyPath); !os.IsNotExist(err) {
		t.Fatalf("partial board body stat = %v, want no board.3 after disconnect", err)
	}
	afterIndex, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat board index after disconnect: %v", err)
	}
	if afterIndex.Size() != beforeIndex.Size() {
		t.Fatalf("board index size = %d, want unchanged %d", afterIndex.Size(), beforeIndex.Size())
	}
	if _, ok, err := state.LoadBoardPosts(root, "info"); err != nil || ok {
		t.Fatalf("partial board sidecar ok=%v err=%v, want absent nil", ok, err)
	}
}

func TestServerLoopNotepadDisconnectStopsPendingAppender(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	root := inputs.summary.Root
	padPath := filepath.Join(root, "post", "DM_pad")

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-dm", commands, "player:dm")

	handleServerTestLine(t, loop, "s-dm", "*notepad a")
	assertServerCommand(t, commands, session.Command{Write: "DM notepad:\n->"})
	handleServerTestLine(t, loop, "s-dm", "line1")
	assertServerCommand(t, commands, session.Command{Write: "->"})

	before, err := os.ReadFile(padPath)
	if err != nil {
		t.Fatalf("read DM_pad after first line: %v", err)
	}
	if got, want := string(before), "            === DM Notepad ===\n\nline1\n"; got != want {
		t.Fatalf("DM_pad after first line = %q, want %q", got, want)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dm", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dm", Kind: session.EventLine, Line: "line2"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(line after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(padPath)
	if err != nil {
		t.Fatalf("read DM_pad after disconnect: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("DM_pad changed after closed-session input:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopNotepadReadDisconnectStopsPendingPager(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	root := inputs.summary.Root
	postDir := filepath.Join(root, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	padPath := filepath.Join(postDir, "DM_pad")
	if err := os.WriteFile(padPath, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(padPath)
	if err != nil {
		t.Fatal(err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-dm", commands, "player:dm")

	handleServerTestLine(t, loop, "s-dm", "*notepad")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{},
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)
	if strings.Contains(got.Write, "line T\n") {
		t.Fatalf("first notepad page included continuation line:\n%s", got.Write)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dm", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dm", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(notepad read continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(padPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("notepad read changed DM_pad after closed-session continue:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopFamilyNewsDisconnectStopsPendingAppender(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	inputs.world.SetDBRoot(inputs.summary.Root)
	newsPath := filepath.Join(inputs.summary.Root, "player", "family", "family_news_7")

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "패거리공지 a")
	assertServerCommand(t, commands, session.Command{Write: "패거리 공지:\n->"})
	handleServerTestLine(t, loop, "s1", "첫 공지")
	assertServerCommand(t, commands, session.Command{Write: "->"})

	before, err := os.ReadFile(newsPath)
	if err != nil {
		t.Fatalf("read family news after first line: %v", err)
	}
	want := "                      === 패거리 공지 ===\n\n첫 공지\n"
	if string(before) != want {
		t.Fatalf("family news after first line = %q, want %q", string(before), want)
	}
	beforeSidecar, ok, err := state.LoadFamilyNews(inputs.summary.Root, 7)
	if err != nil || !ok {
		t.Fatalf("LoadFamilyNews after first line ok=%v err=%v", ok, err)
	}
	if beforeSidecar.Content != want {
		t.Fatalf("family news sidecar after first line = %q, want %q", beforeSidecar.Content, want)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "둘째 공지"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(line after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(newsPath)
	if err != nil {
		t.Fatalf("read family news after disconnect: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("family news changed after closed-session input:\nbefore=%q\nafter=%q", string(before), string(after))
	}
	afterSidecar, ok, err := state.LoadFamilyNews(inputs.summary.Root, 7)
	if err != nil || !ok {
		t.Fatalf("LoadFamilyNews after disconnect ok=%v err=%v", ok, err)
	}
	if afterSidecar.Content != beforeSidecar.Content {
		t.Fatalf("family news sidecar changed after closed-session input:\nbefore=%q\nafter=%q", beforeSidecar.Content, afterSidecar.Content)
	}
}

func TestServerLoopFamilyNewsReadDisconnectStopsPendingPager(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	newsDir := filepath.Join(inputs.summary.Root, "player", "family")
	if err := os.MkdirAll(newsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	newsPath := filepath.Join(newsDir, "family_news_7")
	if err := os.WriteFile(newsPath, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(newsPath)
	if err != nil {
		t.Fatal(err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "패거리공지")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{},
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)
	if strings.Contains(got.Write, "line T\n") {
		t.Fatalf("first family news page included continuation line:\n%s", got.Write)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(family news read continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(newsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("family news read changed file after closed-session continue:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopFamilyJoinDisconnectBeforeConfirmDoesNotMutate(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "패거리가입", Number: 148, Handler: "family"},
	)
	if err := inputs.world.UpdateFamily(model.Family{
		ID:          7,
		Slot:        7,
		DisplayName: "무영문",
		BossName:    "Alice",
		JoinSubsidy: 5,
		Members: []model.FamilyMember{
			{DisplayName: "Alice", Class: 10},
		},
	}); err != nil {
		t.Fatalf("UpdateFamily() error = %v", err)
	}
	if _, err := inputs.world.UpdateCreatureFamilyState("creature:alice", 7, true, false, true); err != nil {
		t.Fatalf("UpdateCreatureFamilyState(alice boss) error = %v", err)
	}
	if _, err := inputs.world.UpdateCreatureFamilyState("creature:dave", 0, false, false, false); err != nil {
		t.Fatalf("UpdateCreatureFamilyState(dave clear) error = %v", err)
	}

	loop := newServerTestLoop(inputs)
	aliceCommands := make(chan session.Command, 4)
	daveCommands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-alice", aliceCommands, "player:alice")
	registerServerTestSession(t, loop, "s-dave", daveCommands, "player:dave")

	handleServerTestLine(t, loop, "s-dave", "패거리가입 무영문")
	assertServerCommandContains(t, daveCommands, session.Command{},
		"무영문에 가입을 하시겠습니까? (예/아니오) ",
	)
	assertNoServerCommand(t, aliceCommands)

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dave", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dave", Kind: session.EventLine, Line: "예"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(family join confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, daveCommands)
	assertNoServerCommand(t, aliceCommands)

	dave, ok := inputs.world.Creature("creature:dave")
	if !ok {
		t.Fatal("missing dave creature after disconnect")
	}
	if dave.Stats["familyFlag"] != 0 || dave.Stats["PFAMIL"] != 0 ||
		dave.Stats["familyID"] != 0 || dave.Stats["dailyExpndMax"] != 0 ||
		dave.Stats["PRDFML"] != 0 || dave.Stats["PFMBOS"] != 0 {
		t.Fatalf("dave family stats changed after closed-session join confirm: %+v", dave.Stats)
	}
	if serverStringListContains(dave.Metadata.Tags, "PRDFML") || serverStringListContains(dave.Metadata.Tags, "PFAMIL") {
		t.Fatalf("dave tags = %+v, want no family tags after closed-session join confirm", dave.Metadata.Tags)
	}
}

func TestServerLoopFamilyLeaveDisconnectBeforeConfirmDoesNotMutate(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "패거리탈퇴", Number: 148, Handler: "out_family"},
	)
	if err := inputs.world.UpdateFamily(model.Family{
		ID:          7,
		Slot:        7,
		DisplayName: "무영문",
		BossName:    "Alice",
		JoinSubsidy: 5,
		Members: []model.FamilyMember{
			{DisplayName: "Alice", Class: 10},
			{DisplayName: "Dave", Class: 8},
		},
	}); err != nil {
		t.Fatalf("UpdateFamily() error = %v", err)
	}
	if _, err := inputs.world.UpdateCreatureFamilyState("creature:dave", 7, true, false, false); err != nil {
		t.Fatalf("UpdateCreatureFamilyState(dave) error = %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:dave", "gold", 120000); err != nil {
		t.Fatalf("SetCreatureStat(dave gold) error = %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-dave", commands, "player:dave")

	handleServerTestLine(t, loop, "s-dave", "패거리탈퇴")
	assertServerCommandContains(t, commands, session.Command{},
		"당신은 지금 현재의 패거리를 탈퇴하실 생각입니까? (예/아니오) ",
	)

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dave", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dave", Kind: session.EventLine, Line: "예"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(family leave confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	dave, ok := inputs.world.Creature("creature:dave")
	if !ok {
		t.Fatal("missing dave creature after disconnect")
	}
	if dave.Stats["gold"] != 120000 {
		t.Fatalf("dave gold = %d, want unchanged 120000", dave.Stats["gold"])
	}
	if dave.Stats["familyFlag"] != 1 || dave.Stats["PFAMIL"] != 1 ||
		dave.Stats["familyID"] != 7 || dave.Stats["dailyExpndMax"] != 7 ||
		dave.Stats["PRDFML"] != 0 || dave.Stats["PFMBOS"] != 0 {
		t.Fatalf("dave family stats changed after closed-session confirm: %+v", dave.Stats)
	}
	if !serverStringListContains(dave.Metadata.Tags, "PFAMIL") {
		t.Fatalf("dave tags = %+v, want PFAMIL after closed-session confirm", dave.Metadata.Tags)
	}
	family, ok := inputs.world.Family(7)
	if !ok {
		t.Fatal("missing family 7 after disconnect")
	}
	foundDave := false
	for _, member := range family.Members {
		if member.DisplayName == "Dave" {
			foundDave = true
			break
		}
	}
	if !foundDave {
		t.Fatalf("family members = %+v, want Dave retained after closed-session confirm", family.Members)
	}
}

func TestServerLoopVoteDisconnectBeforeChoiceDoesNotWriteVote(t *testing.T) {
	inputs, votePath := serverVoteTestRuntimeInputs(t)

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "투표")
	assertServerCommandContains(t, commands, session.Command{}, "\n갑\n당신의 선택은? : ")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "A"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(vote choice after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	if _, err := os.Stat(votePath); !os.IsNotExist(err) {
		t.Fatalf("vote file stat after closed-session choice = %v, want not exist", err)
	}
}

func TestServerLoopVoteDisconnectAfterChangeConfirmationDoesNotWriteReplacement(t *testing.T) {
	inputs, votePath := serverVoteTestRuntimeInputs(t)
	if err := os.MkdirAll(filepath.Dir(votePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(votePath, []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "투표")
	assertServerCommandContains(t, commands, session.Command{},
		"당신은 이미 투표를 했습니다.",
		"당신의 선택을 바꾸시겠습니까? (y/n): ",
	)
	handleServerTestLine(t, loop, "s1", "y")
	assertServerCommandContains(t, commands, session.Command{}, "\n갑\n당신의 선택은? : ")
	if _, err := os.Stat(votePath); !os.IsNotExist(err) {
		t.Fatalf("vote file stat after C-style change confirmation = %v, want deleted", err)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "A"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(replacement vote choice after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	if _, err := os.Stat(votePath); !os.IsNotExist(err) {
		t.Fatalf("vote file stat after closed-session replacement choice = %v, want not exist", err)
	}
}

func TestServerLoopInvinceTrainDisconnectBeforeConfirmDoesNotMutate(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "무적수련", Number: 156, Handler: "invince_train"},
	)
	for _, flag := range []int{4, 6, 7} {
		if err := inputs.world.UpdateRoomFlag("room:combat", flag, true); err != nil {
			t.Fatalf("set training room flag %d: %v", flag, err)
		}
	}
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:combat"); err != nil {
		t.Fatalf("move alice to training room: %v", err)
	}
	const legacyClassInvincibleForServerTest = 9
	if err := inputs.world.SetCreatureStat("creature:alice", "class", legacyClassInvincibleForServerTest); err != nil {
		t.Fatalf("set alice class: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "experience", 1000000); err != nil {
		t.Fatalf("set alice experience: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "pDice", 0); err != nil {
		t.Fatalf("set alice pDice: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "무적수련")
	assertServerCommandContains(t, commands, session.Command{},
		"무적수련을 하려면 경험치 100만이 필요합니다.",
		"무적수련을 하시겠습니까?(예/아니오): ",
	)

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(closed) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "예"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	creature, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice after closed invince_train prompt")
	}
	if got := creature.Stats["experience"]; got != 1000000 {
		t.Fatalf("experience after closed invince_train prompt = %d, want unchanged 1000000", got)
	}
	if got := creature.Stats["class"]; got != legacyClassInvincibleForServerTest {
		t.Fatalf("class after closed invince_train prompt = %d, want unchanged %d", got, legacyClassInvincibleForServerTest)
	}
	if got := creature.Stats["pDice"]; got != 0 {
		t.Fatalf("pDice after closed invince_train prompt = %d, want unchanged 0", got)
	}
	if serverStringListContains(creature.Metadata.Tags, "SFIGHTER") {
		t.Fatalf("creature tags after closed invince_train prompt = %+v, want no SFIGHTER", creature.Metadata.Tags)
	}
	player, ok := inputs.world.Player("player:alice")
	if !ok {
		t.Fatal("missing player:alice after closed invince_train prompt")
	}
	if serverStringListContains(player.Metadata.Tags, "SFIGHTER") {
		t.Fatalf("player tags after closed invince_train prompt = %+v, want no SFIGHTER", player.Metadata.Tags)
	}
}

func TestServerLoopChangeClassDisconnectBeforeConfirmDoesNotMutate(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "직업전환", Number: 86, Handler: "change_class"},
	)
	for _, flag := range []int{4, 5} {
		if err := inputs.world.UpdateRoomFlag("room:combat", flag, true); err != nil {
			t.Fatalf("set class-change room flag %d: %v", flag, err)
		}
	}
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:combat"); err != nil {
		t.Fatalf("move alice to class-change room: %v", err)
	}
	const legacyClassFighterForServerTest = 4
	if err := inputs.world.SetCreatureStat("creature:alice", "class", legacyClassFighterForServerTest); err != nil {
		t.Fatalf("set alice class: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "experience", 150000); err != nil {
		t.Fatalf("set alice experience: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "직업전환")
	assertServerCommandContains(t, commands, session.Command{},
		"직업전환을 하려면 경험치 10만이 필요합니다.",
		"정말로 직업전환을 하시겠습니까?(예/아니오): ",
	)

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(closed) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "예"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	creature, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice after closed change_class prompt")
	}
	if got := creature.Stats["class"]; got != legacyClassFighterForServerTest {
		t.Fatalf("class after closed change_class prompt = %d, want unchanged %d", got, legacyClassFighterForServerTest)
	}
	if got := creature.Stats["experience"]; got != 150000 {
		t.Fatalf("experience after closed change_class prompt = %d, want unchanged 150000", got)
	}
}

func TestServerLoopForgeDisconnectBeforeConfirmDoesNotMutate(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "제련", Number: 85, Handler: "forge"},
	)
	if err := inputs.world.UpdateRoomFlag("room:combat", 36, true); err != nil {
		t.Fatalf("set forge room flag: %v", err)
	}
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:combat"); err != nil {
		t.Fatalf("move alice to forge room: %v", err)
	}
	const legacyClassFighterForForgeServerTest = 4
	if err := inputs.world.SetCreatureStat("creature:alice", "class", legacyClassFighterForForgeServerTest); err != nil {
		t.Fatalf("set alice class: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "gold", 100000); err != nil {
		t.Fatalf("set alice gold: %v", err)
	}
	before, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice before forge")
	}
	beforeInventory := append([]model.ObjectInstanceID(nil), before.Inventory.ObjectIDs...)

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "제련")
	assertServerCommandContains(t, commands, session.Command{}, "어떤 종류의 무기를 원하십니까?")
	handleServerTestLine(t, loop, "s1", "1")
	assertServerCommandContains(t, commands, session.Command{}, "타격치에 영향을 주는 재료")
	handleServerTestLine(t, loop, "s1", "1")
	assertServerCommandContains(t, commands, session.Command{}, "담금질")
	handleServerTestLine(t, loop, "s1", "1")
	assertServerCommandContains(t, commands, session.Command{}, "무기의 이름")
	handleServerTestLine(t, loop, "s1", "검a")
	assertServerCommandContains(t, commands, session.Command{}, "모든것에 만족하십니까?")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(closed) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "예"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice after closed forge prompt")
	}
	if got := after.Stats["gold"]; got != 100000 {
		t.Fatalf("gold after closed forge prompt = %d, want unchanged 100000", got)
	}
	if len(after.Inventory.ObjectIDs) != len(beforeInventory) {
		t.Fatalf("inventory after closed forge prompt = %+v, want unchanged %+v", after.Inventory.ObjectIDs, beforeInventory)
	}
	for i, objectID := range beforeInventory {
		if after.Inventory.ObjectIDs[i] != objectID {
			t.Fatalf("inventory after closed forge prompt = %+v, want unchanged %+v", after.Inventory.ObjectIDs, beforeInventory)
		}
	}
}

func TestServerLoopNewForgeDisconnectBeforeConfirmDoesNotMutate(t *testing.T) {
	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "무기만들기", Number: 85, Handler: "newforge"},
	)
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:00611"); err != nil {
		t.Fatalf("move alice to newforge room: %v", err)
	}
	const legacyClassFighterForNewForgeServerTest = 4
	if err := inputs.world.SetCreatureStat("creature:alice", "class", legacyClassFighterForNewForgeServerTest); err != nil {
		t.Fatalf("set alice class: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "gold", 3000000); err != nil {
		t.Fatalf("set alice gold: %v", err)
	}
	before, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice before newforge")
	}
	beforeInventory := append([]model.ObjectInstanceID(nil), before.Inventory.ObjectIDs...)

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "무기만들기")
	assertServerCommandContains(t, commands, session.Command{}, "어떤 종류의 무기를 원하십니까?")
	handleServerTestLine(t, loop, "s1", "5")
	assertServerCommandContains(t, commands, session.Command{}, "에메랄드 100만냥")
	handleServerTestLine(t, loop, "s1", "1")
	assertServerCommandContains(t, commands, session.Command{}, "900번 5백만냥")
	handleServerTestLine(t, loop, "s1", "1")
	assertServerCommandContains(t, commands, session.Command{}, "무기의 이름")
	handleServerTestLine(t, loop, "s1", "궁a")
	assertServerCommandContains(t, commands, session.Command{}, "모든것에 만족하십니까?")

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(closed) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: "예"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(confirm after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, ok := inputs.world.Creature("creature:alice")
	if !ok {
		t.Fatal("missing creature:alice after closed newforge prompt")
	}
	if got := after.Stats["gold"]; got != 3000000 {
		t.Fatalf("gold after closed newforge prompt = %d, want unchanged 3000000", got)
	}
	if len(after.Inventory.ObjectIDs) != len(beforeInventory) {
		t.Fatalf("inventory after closed newforge prompt = %+v, want unchanged %+v", after.Inventory.ObjectIDs, beforeInventory)
	}
	for i, objectID := range beforeInventory {
		if after.Inventory.ObjectIDs[i] != objectID {
			t.Fatalf("inventory after closed newforge prompt = %+v, want unchanged %+v", after.Inventory.ObjectIDs, beforeInventory)
		}
	}
}

func TestServerLoopPostCommands(t *testing.T) {
	root := t.TempDir()
	loaded := worldload.NewWorld()
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:post",
		DisplayName: "우체국",
		Metadata:    model.Metadata{Tags: []string{"postOffice"}},
	})
	for _, name := range []string{"Alice", "Bob"} {
		playerID := model.PlayerID(name)
		creatureID := model.CreatureID("creature:" + name)
		mustAddServerTestPlayer(t, loaded, model.Player{
			ID:          playerID,
			DisplayName: name,
			CreatureID:  creatureID,
			RoomID:      "room:post",
		})
		mustAddServerTestCreature(t, loaded, model.Creature{
			ID:          creatureID,
			Kind:        model.CreatureKindPlayer,
			DisplayName: name,
			PlayerID:    playerID,
			RoomID:      "room:post",
		})
	}
	registry, err := commandspec.NewRegistry([]commandspec.CommandSpec{
		{Name: "편지보내기", Number: 53, Handler: "postsend"},
		{Name: "편지받기", Number: 54, Handler: "postread"},
		{Name: "편지삭제", Number: 55, Handler: "postdelete"},
		{Name: "보관물", Number: 63, Handler: "bank_inv"},
		{Name: "잔액", Number: 63, Handler: "bank"},
		{Name: "입금", Number: 63, Handler: "deposit"},
		{Name: "출금", Number: 63, Handler: "withdraw"},
		{Name: "받아", Number: 63, Handler: "output_bank"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtimeInputs{
		summary:              worldload.Summary{Root: root, World: loaded},
		registryCommandCount: len(registry.Commands()),
		registry:             registry,
		world:                state.New(loaded),
	}
	loop := newServerTestLoop(inputs)
	alice := make(chan session.Command, 8)
	bob := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "alice", alice, "Alice")
	registerServerTestSession(t, loop, "bob", bob, "Bob")

	handleServerTestLine(t, loop, "alice", "Bob 편지보내기")
	assertServerCommandContains(t, alice, session.Command{}, "편지 내용을 입력하십시요.", "-: ")
	handleServerTestLine(t, loop, "alice", "안녕하세요")
	assertServerCommand(t, alice, session.Command{Write: ": "})
	handleServerTestLine(t, loop, "alice", ".")
	assertServerCommand(t, alice, session.Command{Write: "편지를 보냈습니다.\n", Prompt: "> "})

	handleServerTestLine(t, loop, "bob", "편지받기")
	assertServerCommandContains(t, bob, session.Command{Prompt: "> "}, "Alice (", ")님에게서의 편지:", "안녕하세요")

	handleServerTestLine(t, loop, "bob", "편지삭제")
	assertServerCommand(t, bob, session.Command{Write: "편지가 삭제되었습니다.\n", Prompt: "> "})
	if _, err := os.Stat(filepath.Join(root, "post", "Bob")); !os.IsNotExist(err) {
		t.Fatalf("Bob post stat error = %v, want not exist", err)
	}
	assertNoServerCommand(t, alice)
	assertNoServerCommand(t, bob)
}

func TestServerLoopPostsendDisconnectStopsPendingAppender(t *testing.T) {
	root := t.TempDir()
	loaded := worldload.NewWorld()
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:post",
		DisplayName: "우체국",
		Metadata:    model.Metadata{Tags: []string{"postOffice"}},
	})
	for _, name := range []string{"Alice", "Bob"} {
		playerID := model.PlayerID(name)
		creatureID := model.CreatureID("creature:" + name)
		mustAddServerTestPlayer(t, loaded, model.Player{
			ID:          playerID,
			DisplayName: name,
			CreatureID:  creatureID,
			RoomID:      "room:post",
		})
		mustAddServerTestCreature(t, loaded, model.Creature{
			ID:          creatureID,
			Kind:        model.CreatureKindPlayer,
			DisplayName: name,
			PlayerID:    playerID,
			RoomID:      "room:post",
		})
	}
	registry, err := commandspec.NewRegistry([]commandspec.CommandSpec{
		{Name: "편지보내기", Number: 53, Handler: "postsend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtimeInputs{
		summary:              worldload.Summary{Root: root, World: loaded},
		registryCommandCount: len(registry.Commands()),
		registry:             registry,
		world:                state.New(loaded),
	}
	loop := newServerTestLoop(inputs)
	alice := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "alice", alice, "Alice")

	handleServerTestLine(t, loop, "alice", "Bob 편지보내기")
	assertServerCommandContains(t, alice, session.Command{}, "편지 내용을 입력하십시요.", "-: ")
	handleServerTestLine(t, loop, "alice", "첫 줄")
	assertServerCommand(t, alice, session.Command{Write: ": "})

	postPath := filepath.Join(root, "post", "Bob")
	before, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("read Bob post after first line: %v", err)
	}
	for _, want := range []string{"\n---\nAlice (", ")님에게서의 편지:\n\n", "첫 줄\n"} {
		if !strings.Contains(string(before), want) {
			t.Fatalf("post after first line missing %q:\n%s", want, string(before))
		}
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "alice", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "alice", Kind: session.EventLine, Line: "둘째 줄"})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(line after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, alice)

	after, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("read Bob post after disconnect: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("post changed after closed-session input:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopPostReadDisconnectStopsPendingPager(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	root := inputs.summary.Root
	if err := inputs.world.UpdateRoomFlag("room:combat", 11, true); err != nil {
		t.Fatalf("set post office room flag: %v", err)
	}
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:combat"); err != nil {
		t.Fatalf("move alice to post office: %v", err)
	}

	postDir := filepath.Join(root, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	postPath := filepath.Join(postDir, "Alice")
	if err := os.WriteFile(postPath, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatal(err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "편지받기")
	assertServerCommandContains(t, commands, session.Command{},
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(postread continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("postread changed mail after closed-session continue:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopDMLogDisconnectStopsPendingPager(t *testing.T) {
	logRoot := t.TempDir()
	t.Chdir(logRoot)
	if err := os.MkdirAll("log", 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	logPath := filepath.Join("log", "log")
	if err := os.WriteFile(logPath, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "*log", Number: 121, Handler: "dm_log"},
	)
	if err := inputs.world.SetCreatureStat("creature:dm", "class", 13); err != nil {
		t.Fatalf("set DM class: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s-dm", commands, "player:dm")

	handleServerTestLine(t, loop, "s-dm", "*log")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{},
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)
	if strings.Contains(got.Write, "line T\n") {
		t.Fatalf("first DM log page included continuation line:\n%s", got.Write)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dm", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s-dm", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(dm_log continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)

	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("dm_log changed log file after closed-session continue:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}

func TestServerLoopHelpDisconnectStopsPendingPager(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(inputs.summary.Root, "help", "helpfile"), []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "도움")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{},
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)
	if strings.Contains(got.Write, "line T\n") {
		t.Fatalf("first help page included continuation line:\n%s", got.Write)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(help continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)
}

func TestServerLoopReadScrollDisconnectStopsSpecialMapPager(t *testing.T) {
	root := t.TempDir()
	objmonDir := filepath.Join(root, "objmon")
	if err := os.MkdirAll(objmonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for i := 1; i <= 24; i++ {
		builder.WriteString("line ")
		builder.WriteByte(byte('A' - 1 + i))
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(objmonDir, "고대_지도"), []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded := worldload.NewWorld()
	mustAddServerTestRoom(t, loaded, model.Room{ID: "room:library", DisplayName: "서재"})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:library",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		RoomID:      "room:library",
		Inventory:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:map"}},
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:map",
		DisplayName: "고대 지도",
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:map",
		PrototypeID: "prototype:map",
		Location:    model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"},
		Properties:  map[string]string{"special": "SP_MAPSC"},
	})
	registry, err := commandspec.NewRegistry([]commandspec.CommandSpec{
		{Name: "읽어", Number: 40, Handler: "readscroll"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtimeInputs{
		summary:              worldload.Summary{Root: root, World: loaded},
		registryCommandCount: len(registry.Commands()),
		registry:             registry,
		world:                state.New(loaded),
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "고대 읽어")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{},
		"line S\n",
		"[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ",
	)
	if strings.Contains(got.Write, "line T\n") {
		t.Fatalf("first special-map page included second-page line:\n%s", got.Write)
	}

	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventClosed}); err != nil {
		t.Fatalf("HandleEvent(close) error = %v", err)
	}
	err = loop.HandleEvent(context.Background(), session.Event{SessionID: "s1", Kind: session.EventLine, Line: ""})
	if !errors.Is(err, game.ErrSessionNotFound) {
		t.Fatalf("HandleEvent(readscroll continue after close) error = %v, want ErrSessionNotFound", err)
	}
	assertNoServerCommand(t, commands)
	assertServerObjectLocation(t, inputs.world, "object:map", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"})
}

func TestServerLoopContainerGetDropCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MoveObject("object:bag", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}); err != nil {
		t.Fatalf("move fixture bag into inventory: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "가방 보석 꺼내")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "가방", "보석")
	assertServerObjectLocation(t, inputs.world, "object:gem", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"})
	assertServerObjectContents(t, inputs.world, "object:bag", "object:gem", false)
	assertServerCreatureInventory(t, inputs.world, "creature:alice", "object:gem", true)

	handleServerTestLine(t, loop, "s1", "보석 가방 넣어")
	got = receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "가방", "보석")
	assertServerObjectLocation(t, inputs.world, "object:gem", model.ObjectLocation{ContainerID: "object:bag"})
	assertServerObjectContents(t, inputs.world, "object:bag", "object:gem", true)
	assertServerCreatureInventory(t, inputs.world, "creature:alice", "object:gem", false)
	assertServerObjectLocation(t, inputs.world, "object:bag", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"})
	assertNoServerCommand(t, commands)
}

func TestServerLoopEquipmentCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "빛나는 검 무장")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"당신은 빛나는 검으로 전투태세를 취합니다.")
	assertServerObjectLocation(t, inputs.world, "object:sword", model.ObjectLocation{CreatureID: "creature:alice", Slot: "wield"})

	handleServerTestLine(t, loop, "s1", "장비")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"  <<<  착용 장비  >>>  \n", "[ 무기 ]  빛나는 검\n")

	handleServerTestLine(t, loop, "s1", "빛나는 검 벗어")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"당신은 빛나는 검을 벗었습니다.")
	assertServerObjectLocation(t, inputs.world, "object:sword", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"})
	assertNoServerCommand(t, commands)
}

func TestServerLoopAppraiseAndCompareCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "빛나는 검 감정")
	appraiseOut := (<-commands).Write
	for _, want := range []string{"이름: 빛나는 검\n", "종류: 검 무기.\n", "타격치: 6면4굴림 더하기 1"} {
		if !strings.Contains(appraiseOut, want) {
			t.Fatalf("appraise output missing %q:\n%s", want, appraiseOut)
		}
	}

	handleServerTestLine(t, loop, "s1", "빛나는 검 비교")
	assertServerCommand(t, commands, session.Command{Write: "빛나는 검은 66 레벨부터 무장할 수 있습니다.", Prompt: "> "})
	assertNoServerCommand(t, commands)
}

func TestServerLoopShopListCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:01071"); err != nil {
		t.Fatalf("move alice to shop: %v", err)
	}
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "품목")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"상품들:", "목검", "가격: 50000", "기념패", "가격: 10000")
	assertNoServerCommand(t, commands)
}

func TestServerLoopShopBuyCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:01071"); err != nil {
		t.Fatalf("move alice to shop: %v", err)
	}
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "기념패 사")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"당신은 기념패를 샀습니다")

	handleServerTestLine(t, loop, "s1", "소지품")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"소지품:", "빛나는 검", "기념패")

	handleServerTestLine(t, loop, "s1", "정보")
	assertServerCommandContains(t, commands, session.Command{},
		"[  돈  ]", "90000", "[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ")
	assertNoServerCommand(t, commands)
}

func TestServerLoopShopValueCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:pawn"); err != nil {
		t.Fatalf("move alice to pawn shop: %v", err)
	}
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "빛나는 검 가치")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"상점주인이", "빛나는 검이라면", "25000냥")
	assertNoServerCommand(t, commands)
}

func TestServerLoopShopSellCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:pawn"); err != nil {
		t.Fatalf("move alice to pawn shop: %v", err)
	}
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "빛나는 검 팔아")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"제가 빛나는 검을 사죠.", "전당포주인이 당신에게 25000냥을 줍니다.")

	handleServerTestLine(t, loop, "s1", "소지품")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"소지품:", "없음")

	handleServerTestLine(t, loop, "s1", "정보")
	assertServerCommandContains(t, commands, session.Command{},
		"[  돈  ]", "125000", "[엔터]를 누르세요. 그만보시려면 [.]을 치세요: ")
	assertServerCreatureInventory(t, inputs.world, "creature:alice", "object:sword", false)
	if _, ok := inputs.world.Object("object:sword"); ok {
		t.Fatal("sold object still exists")
	}
	assertNoServerCommand(t, commands)
}

func TestServerLoopAttackMonsterCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:kate")

	handleServerTestLine(t, loop, "s1", "생쥐 때려")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "},
		"당신은 생쥐에게 3만큼의 피해를 주었습니다.", "생쥐가 쓰러졌습니다.")
	if _, ok := inputs.world.Creature("creature:mouse"); ok {
		t.Fatal("dead creature:mouse still exists")
	}
	room, _ := inputs.world.Room("room:combat")
	if serverCreatureListContains(room.CreatureIDs, "creature:mouse") {
		t.Fatalf("combat creatures = %+v, want dead mouse removed", room.CreatureIDs)
	}
	assertNoServerCommand(t, commands)
}

func TestServerLoopExitControlCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 8)
	registerServerTestSession(t, loop, "s1", commands, "player:dave")

	handleServerTestLine(t, loop, "s1", "동")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "문이 닫혀 있습니다.")
	assertServerActorRoom(t, inputs.world, "player:dave", "creature:dave", "room:blocked-exits")

	handleServerTestLine(t, loop, "s1", "동 열어")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "당신은 동쪽 출구를 열었습니다.")
	openExit := mustServerExit(t, inputs.world, "room:blocked-exits", "동")
	if serverExitHasFlag(openExit, "closed") {
		t.Fatalf("동 flags = %+v, want closed removed", openExit.Flags)
	}

	handleServerTestLine(t, loop, "s1", "동")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "\n동쪽\n\n")
	assertServerActorRoom(t, inputs.world, "player:dave", "creature:dave", "room:east")

	if err := inputs.world.MovePlayerToRoom("player:dave", "room:blocked-exits"); err != nil {
		t.Fatalf("move dave back: %v", err)
	}
	handleServerTestLine(t, loop, "s1", "동 닫아")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "당신은 동쪽 출구를 닫습니다.")
	closedExit := mustServerExit(t, inputs.world, "room:blocked-exits", "동")
	if !serverExitHasFlag(closedExit, "closed") {
		t.Fatalf("동 flags = %+v, want closed restored", closedExit.Flags)
	}

	handleServerTestLine(t, loop, "s1", "서 열쇠 풀어")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "딸깍")
	unlockedExit := mustServerExit(t, inputs.world, "room:blocked-exits", "서")
	if serverExitHasFlag(unlockedExit, "locked") || !serverExitHasFlag(unlockedExit, "closed") {
		t.Fatalf("서 flags = %+v, want unlocked but still closed", unlockedExit.Flags)
	}
	key, _ := inputs.world.Object("object:dave-key")
	if got := key.Properties["shotsCurrent"]; got != "1" {
		t.Fatalf("key shotsCurrent = %q, want 1", got)
	}

	handleServerTestLine(t, loop, "s1", "서 열쇠 잠궈")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "## 찰칵 ##")
	lockedExit := mustServerExit(t, inputs.world, "room:blocked-exits", "서")
	if !serverExitHasFlag(lockedExit, "locked") {
		t.Fatalf("서 flags = %+v, want locked", lockedExit.Flags)
	}
	key, _ = inputs.world.Object("object:dave-key")
	if got := key.Properties["shotsCurrent"]; got != "1" {
		t.Fatalf("key shotsCurrent = %q, want unchanged 1 after lock", got)
	}

	handleServerTestLine(t, loop, "s1", "서 따")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "당신은 문을 따는데 성공했습니다.")
	pickedExit := mustServerExit(t, inputs.world, "room:blocked-exits", "서")
	if serverExitHasFlag(pickedExit, "locked") || !serverExitHasFlag(pickedExit, "closed") {
		t.Fatalf("서 flags = %+v, want picked but still closed", pickedExit.Flags)
	}
	assertNoServerCommand(t, commands)
}

func TestServerLoopReturnSquareCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "귀환")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "귀환!")
	assertServerActorRoom(t, inputs.world, "player:alice", "creature:alice", "room:00001")
	assertNoServerCommand(t, commands)
}

func TestServerLoopMovesThroughRuntimeExitNameCommand(t *testing.T) {
	loaded := worldload.NewWorld()
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:plaza",
		DisplayName:      "광장",
		ShortDescription: "출발 광장이다.",
		Exits: []model.Exit{{
			Name:     "백화점",
			ToRoomID: "room:shop",
		}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:shop",
		DisplayName:      "백화점 로비",
		ShortDescription: "백화점의 로비이다.",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:plaza",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		RoomID:      "room:plaza",
	})
	registry, err := commandspec.NewRegistry([]commandspec.CommandSpec{
		{Name: "가", Number: 30, Handler: "go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry, err = registryWithRoomExitCommands(registry, loaded)
	if err != nil {
		t.Fatal(err)
	}
	inputs := runtimeInputs{
		summary:              worldload.Summary{Root: t.TempDir(), World: loaded},
		registryCommandCount: len(registry.Commands()),
		registry:             registry,
		world:                state.New(loaded),
	}
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "백화점")
	assertServerCommandContains(t, commands, session.Command{Prompt: "> "}, "\n백화점 로비\n\n", "백화점의 로비이다.")
	assertServerActorRoom(t, inputs.world, "player:alice", "creature:alice", "room:shop")
	assertNoServerCommand(t, commands)
}

func TestServerLoopTargetLookObjectCommands(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MoveObject("object:bag", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}); err != nil {
		t.Fatalf("move fixture bag into inventory: %v", err)
	}
	if err := inputs.world.MoveObject("object:gem", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}); err != nil {
		t.Fatalf("move fixture gem into inventory: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	t.Run("bag", func(t *testing.T) {
		handleServerTestLine(t, loop, "s1", "가방 봐")
		got := receiveServerCommand(t, commands)
		assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "가방", "낡았지만 튼튼한 가방이다.")
	})

	t.Run("gem", func(t *testing.T) {
		handleServerTestLine(t, loop, "s1", "보석 봐")
		got := receiveServerCommand(t, commands)
		assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "보석", "은은하게 빛나는 보석이다.")
	})

	assertNoServerCommand(t, commands)
}

func TestServerLoopTargetLookContainerContents(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.MoveObject("object:bag", model.ObjectLocation{CreatureID: "creature:alice", Slot: "inventory"}); err != nil {
		t.Fatalf("move fixture bag into inventory: %v", err)
	}

	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "가방 봐")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "가방", "낡았지만 튼튼한 가방이다.", "내용물: 보석")
	assertNoServerCommand(t, commands)
}

func TestServerLoopTargetLookCreatureCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "경비병 봐")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "경비병", "경비병이 서 있다.")
	assertNoServerCommand(t, commands)
}

func TestServerLoopTargetLookExitCommand(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "동 봐")
	got := receiveServerCommand(t, commands)
	assertServerActorRoom(t, inputs.world, "player:alice", "creature:alice", "room:plaza")

	if got == (session.Command{Write: "그런 건 보이지 않습니다.\n", Prompt: "> "}) {
		t.Fatalf("exit target look returned unsupported output")
	}

	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "},
		"\n동쪽\n\n", "동쪽 방이다.", "[ 출구 : 서 ]\n", "가방이 놓여져 있습니다.\n")
	if strings.Contains(got.Write, "출발 광장이다.") {
		t.Fatalf("exit target look rendered Alice's current room:\n%s", got.Write)
	}
	assertNoServerCommand(t, commands)
}

func TestServerLoopTargetLookAliases(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "작은 조사")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "작은 돌이다.")

	handleServerTestLine(t, loop, "s1", "동 보다")
	got = receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, "\n동쪽\n\n", "동쪽 방이다.")
	assertServerActorRoom(t, inputs.world, "player:alice", "creature:alice", "room:plaza")
	assertNoServerCommand(t, commands)
}

func TestServerLoopMoveMissingDestinationExitDoesNotMoveActor(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s3", commands, "player:carol")

	handleServerTestLine(t, loop, "s3", "허공")
	assertServerCommand(t, commands, session.Command{
		Write:  "그쪽으로 지도가 없습니다. 신에게 연락해 주세요.\n",
		Prompt: "> ",
	})
	assertServerActorRoom(t, inputs.world, "player:carol", "creature:carol", "room:broken-exit")

	handleServerTestLine(t, loop, "s3", "허공 가")

	assertServerCommand(t, commands, session.Command{
		Write:  "그 방향의 지도가 없습니다.\n",
		Prompt: "> ",
	})
	assertServerActorRoom(t, inputs.world, "player:carol", "creature:carol", "room:broken-exit")
	assertNoServerCommand(t, commands)
}

func TestServerLoopMoveBlockedExitFlagsDoNotMoveActor(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s4", commands, "player:dave")

	handleServerTestLine(t, loop, "s4", "동")
	assertServerCommand(t, commands, session.Command{
		Write:  "문이 닫혀 있습니다.\n",
		Prompt: "> ",
	})
	assertServerActorRoom(t, inputs.world, "player:dave", "creature:dave", "room:blocked-exits")

	handleServerTestLine(t, loop, "s4", "서 가")
	assertServerCommand(t, commands, session.Command{
		Write:  "그 출구는 잠겨 있습니다.\n",
		Prompt: "> ",
	})
	assertServerActorRoom(t, inputs.world, "player:dave", "creature:dave", "room:blocked-exits")
	assertNoServerCommand(t, commands)
}

func TestServerLoopRoomLookHidesNonVisibleExitFlags(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s5", commands, "player:eve")

	handleServerTestLine(t, loop, "s5", "봐")

	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "},
		"\n관문\n\n", "[ 출구 : 닫힌, 혼인 ]\n")
	for _, hidden := range []string{"비밀", "숨김", "은신"} {
		if strings.Contains(got.Write, hidden) {
			t.Fatalf("room look includes hidden exit %q:\n%s", hidden, got.Write)
		}
	}
	assertServerActorRoom(t, inputs.world, "player:eve", "creature:eve", "room:flagged-exits")
	assertNoServerCommand(t, commands)
}

func TestServerLoopTargetLookExitFlagsAndDestinationRestrictions(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	loop := newServerTestLoop(inputs)
	commands := make(chan session.Command, 4)
	registerServerTestSession(t, loop, "s5", commands, "player:eve")

	handleServerTestLine(t, loop, "s5", "닫힌 봐")
	assertServerCommand(t, commands, session.Command{
		Write:  "그 출구는 닫혀 있습니다.",
		Prompt: "> ",
	})
	assertServerActorRoom(t, inputs.world, "player:eve", "creature:eve", "room:flagged-exits")

	handleServerTestLine(t, loop, "s5", "비밀 봐")
	got := receiveServerCommand(t, commands)
	assertServerCommandValueContains(t, got, session.Command{Prompt: "> "},
		"\n비밀방\n\n", "[ 출구 : 없음 ]\n")
	assertServerActorRoom(t, inputs.world, "player:eve", "creature:eve", "room:flagged-exits")

	handleServerTestLine(t, loop, "s5", "혼인 봐")
	assertServerCommand(t, commands, session.Command{
		Write:  "그 방은 볼 수가 없습니다.",
		Prompt: "> ",
	})
	assertServerActorRoom(t, inputs.world, "player:eve", "creature:eve", "room:flagged-exits")
	assertNoServerCommand(t, commands)
}

func TestServerLoopMoveVisibilityExitFlags(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		want     string
		wantRoom model.RoomID
		contains []string
	}{
		{
			name:     "direct noSee",
			line:     "숨김",
			want:     "길이 막혀 있습니다.\n",
			wantRoom: "room:flagged-exits",
		},
		{
			name:     "go invisible",
			line:     "은신 가",
			want:     "그런 출구는 없습니다.\n",
			wantRoom: "room:flagged-exits",
		},
		{
			name:     "direct invisible",
			line:     "은신",
			wantRoom: "room:east",
			contains: []string{"\n동쪽\n\n", "동쪽 방이다.", "[ 출구 : 서 ]\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
			loop := newServerTestLoop(inputs)
			commands := make(chan session.Command, 2)
			registerServerTestSession(t, loop, "s5", commands, "player:eve")

			handleServerTestLine(t, loop, "s5", tt.line)

			got := receiveServerCommand(t, commands)
			if tt.want != "" {
				if got != (session.Command{Write: tt.want, Prompt: "> "}) {
					t.Fatalf("command = %#v, want output %q with prompt", got, tt.want)
				}
			} else {
				assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, tt.contains...)
			}
			assertServerActorRoom(t, inputs.world, "player:eve", "creature:eve", tt.wantRoom)
			assertNoServerCommand(t, commands)
		})
	}
}

func TestServerLoopMoveNakedExitInventoryRestrictions(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		playerID   model.PlayerID
		creatureID model.CreatureID
		line       string
		want       string
		wantRoom   model.RoomID
		contains   []string
	}{
		{
			name:       "direct inventory blocks",
			actor:      "player:frank",
			playerID:   "player:frank",
			creatureID: "creature:frank",
			line:       "탈의",
			want:       "뭘 가지고는 들어갈수 없습니다.\n",
			wantRoom:   "room:naked-exit",
		},
		{
			name:       "go inventory blocks",
			actor:      "player:frank",
			playerID:   "player:frank",
			creatureID: "creature:frank",
			line:       "탈의 가",
			want:       "뭘 가지고는 들어갈 수 없습니다.\n",
			wantRoom:   "room:naked-exit",
		},
		{
			name:       "direct empty inventory moves",
			actor:      "player:grace",
			playerID:   "player:grace",
			creatureID: "creature:grace",
			line:       "탈의",
			wantRoom:   "room:east",
			contains:   []string{"\n동쪽\n\n", "동쪽 방이다.", "[ 출구 : 서 ]\n"},
		},
		{
			name:       "go empty inventory moves",
			actor:      "player:grace",
			playerID:   "player:grace",
			creatureID: "creature:grace",
			line:       "탈의 가",
			wantRoom:   "room:east",
			contains:   []string{"\n동쪽\n\n", "동쪽 방이다.", "[ 출구 : 서 ]\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
			loop := newServerTestLoop(inputs)
			commands := make(chan session.Command, 2)
			registerServerTestSession(t, loop, "s6", commands, tt.actor)

			handleServerTestLine(t, loop, "s6", tt.line)

			got := receiveServerCommand(t, commands)
			if tt.want != "" {
				if got != (session.Command{Write: tt.want, Prompt: "> "}) {
					t.Fatalf("command = %#v, want output %q with prompt", got, tt.want)
				}
			} else {
				assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, tt.contains...)
			}
			assertServerActorRoom(t, inputs.world, tt.playerID, tt.creatureID, tt.wantRoom)
			assertNoServerCommand(t, commands)
		})
	}
}

func TestServerLoopMoveFamilyDestinationRestrictions(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		want     string
		wantRoom model.RoomID
		contains []string
	}{
		{
			name:     "direct family blocks",
			line:     "문파",
			want:     "그쪽으로 갈 수 없습니다.\n",
			wantRoom: "room:destination-restrictions",
		},
		{
			name:     "go onlyFamily blocks",
			line:     "가족 가",
			want:     "그 방향으로 갈 수 없습니다.\n",
			wantRoom: "room:destination-restrictions",
		},
		{
			name:     "direct onlyMarried blocks",
			line:     "혼인전용",
			want:     "그쪽으로 갈 수 없습니다.\n",
			wantRoom: "room:destination-restrictions",
		},
		{
			name:     "direct unrestricted moves",
			line:     "일반",
			wantRoom: "room:east",
			contains: []string{"\n동쪽\n\n", "동쪽 방이다.", "[ 출구 : 서 ]\n"},
		},
		{
			name:     "go unrestricted moves",
			line:     "일반 가",
			wantRoom: "room:east",
			contains: []string{"\n동쪽\n\n", "동쪽 방이다.", "[ 출구 : 서 ]\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
			loop := newServerTestLoop(inputs)
			commands := make(chan session.Command, 2)
			registerServerTestSession(t, loop, "s7", commands, "player:heidi")

			handleServerTestLine(t, loop, "s7", tt.line)

			got := receiveServerCommand(t, commands)
			if tt.want != "" {
				if got != (session.Command{Write: tt.want, Prompt: "> "}) {
					t.Fatalf("command = %#v, want output %q with prompt", got, tt.want)
				}
			} else {
				assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, tt.contains...)
			}
			assertServerActorRoom(t, inputs.world, "player:heidi", "creature:heidi", tt.wantRoom)
			assertNoServerCommand(t, commands)
		})
	}
}

func TestServerLoopMoveOnlyMarriedInviteAllowsMarriageMismatch(t *testing.T) {
	tests := []struct {
		name       string
		actor      string
		playerID   model.PlayerID
		creatureID model.CreatureID
		invited    bool
		want       string
		wantRoom   model.RoomID
		contains   []string
	}{
		{
			name:       "invited mismatch moves",
			actor:      "player:ivan",
			playerID:   "player:ivan",
			creatureID: "creature:ivan",
			invited:    true,
			wantRoom:   "room:married-only",
			contains:   []string{"\n혼인 전용방\n\n", "[ 출구 : 없음 ]\n"},
		},
		{
			name:       "not invited mismatch blocks",
			actor:      "player:judy",
			playerID:   "player:judy",
			creatureID: "creature:judy",
			want:       "그쪽으로 갈 수 없습니다.\n",
			wantRoom:   "room:destination-restrictions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
			loop := newServerTestLoop(inputs)
			commands := make(chan session.Command, 2)
			registerServerTestSession(t, loop, "s8", commands, tt.actor)

			handleServerTestLine(t, loop, "s8", "혼인전용")

			got := receiveServerCommand(t, commands)
			if tt.want != "" {
				if got != (session.Command{Write: tt.want, Prompt: "> "}) {
					t.Fatalf("command = %#v, want output %q with prompt", got, tt.want)
				}
			} else {
				assertServerCommandValueContains(t, got, session.Command{Prompt: "> "}, tt.contains...)
			}
			assertServerActorRoom(t, inputs.world, tt.playerID, tt.creatureID, tt.wantRoom)
			assertNoServerCommand(t, commands)
		})
	}
}

func TestServerLoopFormatsUnknownCommand(t *testing.T) {
	loop := newServerTestLoop(serverTestRuntimeInputs(t))
	commands := make(chan session.Command, 2)
	registerServerTestSession(t, loop, "s1", commands, "player:alice")

	handleServerTestLine(t, loop, "s1", "춤춰")

	assertServerCommand(t, commands, session.Command{
		Write:  "무슨 말인지 모르겠습니다.\n",
		Prompt: "> ",
	})
	assertNoServerCommand(t, commands)
}

func TestRunDryRunLoadsInputsAndDoesNotListen(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"-root", repoRoot(t), "-dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"listen: :4000",
		"registry: ",
		"runtime world: initialized",
		"mode: dry-run",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "listening:") {
		t.Fatalf("dry-run listened:\n%s", out)
	}
}

func TestMigrateSidecarsForStartupRewritesOldSchema(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "player", "json", "alice.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"player":{"id":"player:alice"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if err := migrateSidecarsForStartup(root, &stdout); err != nil {
		t.Fatalf("migrateSidecarsForStartup() error = %v; stdout:\n%s", err, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"sidecar migration: scanned=1 migrated=1 errors=0",
		"sidecar migration player: 1",
		"sidecar migrated: player ",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	save, ok, err := state.LoadPlayer(root, "player:alice")
	if err != nil || !ok {
		t.Fatalf("LoadPlayer() ok=%v err=%v", ok, err)
	}
	if save.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", save.SchemaVersion, state.CurrentSaveSchemaVersion)
	}
}

func TestMigrateSidecarsForStartupReportsBoardAndFamilyNews(t *testing.T) {
	root := t.TempDir()
	boardPath := filepath.Join(root, "board", "json", "info.json")
	familyNewsPath := filepath.Join(root, "player", "family", "json", "family_news_7.json")
	for path, data := range map[string]string{
		boardPath:      `{"schemaVersion":1,"boardDir":"info"}`,
		familyNewsPath: `{"schemaVersion":1,"familyId":7,"content":"notice"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	var stdout bytes.Buffer
	if err := migrateSidecarsForStartup(root, &stdout); err != nil {
		t.Fatalf("migrateSidecarsForStartup() error = %v; stdout:\n%s", err, stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"sidecar migration: scanned=2 migrated=2 errors=0",
		"sidecar migration board: 1",
		"sidecar migration familynews: 1",
		"sidecar migrated: board " + boardPath + " v1 -> v" + strconv.Itoa(state.CurrentSaveSchemaVersion),
		"sidecar migrated: familynews " + familyNewsPath + " v1 -> v" + strconv.Itoa(state.CurrentSaveSchemaVersion),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}

	board, ok, err := state.LoadBoardPosts(root, "info")
	if err != nil || !ok {
		t.Fatalf("LoadBoardPosts() ok=%v err=%v", ok, err)
	}
	if board.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("board schemaVersion = %d, want %d", board.SchemaVersion, state.CurrentSaveSchemaVersion)
	}

	familyNews, ok, err := state.LoadFamilyNews(root, 7)
	if err != nil || !ok {
		t.Fatalf("LoadFamilyNews() ok=%v err=%v", ok, err)
	}
	if familyNews.SchemaVersion != state.CurrentSaveSchemaVersion {
		t.Fatalf("family news schemaVersion = %d, want %d", familyNews.SchemaVersion, state.CurrentSaveSchemaVersion)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func serverVoteTestRuntimeInputs(t *testing.T) (runtimeInputs, string) {
	t.Helper()

	inputs := withServerTestCommands(t, serverTestRuntimeInputs(t),
		commandspec.CommandSpec{Name: "투표", Number: 79, Handler: "vote"},
	)
	postDir := filepath.Join(inputs.summary.Root, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(postDir, "ISSUE"), []byte("1 문주\n갑\n을\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := inputs.world.UpdateRoomFlag("room:combat", 35, true); err != nil {
		t.Fatalf("set election room flag: %v", err)
	}
	if err := inputs.world.MovePlayerToRoom("player:alice", "room:combat"); err != nil {
		t.Fatalf("move alice to election room: %v", err)
	}
	if err := inputs.world.SetCreatureStat("creature:alice", "age", 21); err != nil {
		t.Fatalf("set alice age: %v", err)
	}

	return inputs, filepath.Join(inputs.summary.Root, "player", "vote", "Alice_v")
}

func newServerTestLoop(inputs runtimeInputs) *game.Loop {
	return game.NewLoop(serverDispatcher(inputs),
		game.WithPrompt(func(session.ID, *enginecmd.Context, enginecmd.Status) string {
			return "> "
		}),
		game.WithPromptPolicy(func(status enginecmd.Status, err error) bool {
			return status != enginecmd.StatusDisconnect && status != enginecmd.StatusDoPrompt
		}),
		game.WithErrorFormatter(func(err error) string {
			if errors.Is(err, enginecmd.ErrUnknownCommand) || errors.Is(err, enginecmd.ErrUnhandledCommand) {
				return "무슨 말인지 모르겠습니다.\n"
			}
			return err.Error() + "\n"
		}),
		game.WithCommandContextValues(map[string]any{enginecmd.ContextShopSellBonusKey: false}),
	)
}

func serverTestRuntimeInputs(t *testing.T) runtimeInputs {
	t.Helper()

	registry, err := commandspec.NewRegistry([]commandspec.CommandSpec{
		{Name: "봐", Number: 2, Handler: "look"},
		{Name: "보다", Number: 2, Handler: "look"},
		{Name: "보아", Number: 100, Handler: "action"},
		{Name: "조사", Number: 2, Handler: "look"},
		{Name: "미소", Number: 100, Handler: "action"},
		{Name: "안녕", Number: 100, Handler: "action"},
		{Name: "표현", Number: 25, Handler: "emote"},
		{Name: "외쳐", Number: 29, Handler: "yell"},
		{Name: "이야기", Number: 17, Handler: "send"},
		{Name: "얘기", Number: 17, Handler: "send"},
		{Name: "대답", Number: 154, Handler: "resend"},
		{Name: "잡담", Number: 59, Handler: "broadsend"},
		{Name: "환호", Number: 70, Handler: "broadsend2"},
		{Name: "따라", Number: 18, Handler: "follow"},
		{Name: "내보내", Number: 19, Handler: "lose"},
		{Name: "그룹", Number: 20, Handler: "group"},
		{Name: "무리", Number: 20, Handler: "group"},
		{Name: "그룹말", Number: 57, Handler: "gtalk"},
		{Name: "무리말", Number: 57, Handler: "gtalk"},
		{Name: "=", Number: 57, Handler: "gtalk"},
		{Name: "패거리누구", Number: 148, Handler: "family_who"},
		{Name: "패거리말", Number: 148, Handler: "family_talk"},
		{Name: "]", Number: 148, Handler: "family_talk"},
		{Name: "패거리공지", Number: 148, Handler: "family_news"},
		{Name: "전쟁선포", Number: 72, Handler: "call_war"},
		{Name: "줘", Number: 47, Handler: "give"},
		{Name: "메모", Number: 151, Handler: "memo"},
		{Name: "대화", Number: 56, Handler: "talk"},
		{Name: "가", Number: 30, Handler: "go"},
		{Name: "동", Number: 1, Handler: "move"},
		{Name: "서", Number: 1, Handler: "move"},
		{Name: "허공", Number: 1, Handler: "move"},
		{Name: "숨김", Number: 1, Handler: "move"},
		{Name: "은신", Number: 1, Handler: "move"},
		{Name: "탈의", Number: 1, Handler: "move"},
		{Name: "문파", Number: 1, Handler: "move"},
		{Name: "가족", Number: 1, Handler: "move"},
		{Name: "혼인전용", Number: 1, Handler: "move"},
		{Name: "일반", Number: 1, Handler: "move"},
		{Name: "주워", Number: 5, Handler: "get"},
		{Name: "꺼내", Number: 5, Handler: "get"},
		{Name: "버려", Number: 7, Handler: "drop"},
		{Name: "넣어", Number: 7, Handler: "drop"},
		{Name: "끝", Number: 3, Handler: "quit"},
		{Name: "소지품", Number: 40, Handler: "inventory"},
		{Name: "입어", Number: 9, Handler: "wear"},
		{Name: "벗어", Number: 10, Handler: "remove_obj"},
		{Name: "장비", Number: 11, Handler: "equipment"},
		{Name: "쥐어", Number: 12, Handler: "hold"},
		{Name: "잡아", Number: 12, Handler: "hold"},
		{Name: "무장", Number: 13, Handler: "ready"},
		{Name: "감정", Number: 96, Handler: "info_obj"},
		{Name: "비교", Number: 96, Handler: "obj_compare"},
		{Name: "점수", Number: 15, Handler: "health"},
		{Name: "건강", Number: 15, Handler: "health"},
		{Name: "정보", Number: 16, Handler: "info"},
		{Name: "어디", Number: 8, Handler: "where"},
		{Name: "상태", Number: 172, Handler: "effect_flag_list"},
		{Name: "품목", Number: 41, Handler: "list"},
		{Name: "사", Number: 42, Handler: "buy"},
		{Name: "팔아", Number: 43, Handler: "sell"},
		{Name: "가치", Number: 44, Handler: "value"},
		{Name: "가격", Number: 44, Handler: "value"},
		{Name: "마셔", Number: 58, Handler: "drink"},
		{Name: "먹어", Number: 58, Handler: "drink"},
		{Name: "사용", Number: 67, Handler: "use"},
		{Name: "엿봐", Number: 22, Handler: "peek"},
		{Name: "때려", Number: 23, Handler: "attack"},
		{Name: "공격", Number: 23, Handler: "attack"},
		{Name: "검색", Number: 24, Handler: "search"},
		{Name: "찾아", Number: 24, Handler: "search"},
		{Name: "숨겨", Number: 26, Handler: "hide"},
		{Name: "숨어", Number: 26, Handler: "hide"},
		{Name: "설정", Number: 27, Handler: "set"},
		{Name: "해제", Number: 28, Handler: "clear"},
		{Name: "열어", Number: 31, Handler: "openexit"},
		{Name: "닫아", Number: 32, Handler: "closeexit"},
		{Name: "풀어", Number: 33, Handler: "unlock"},
		{Name: "잠궈", Number: 34, Handler: "lock"},
		{Name: "따", Number: 35, Handler: "picklock"},
		{Name: "훔쳐", Number: 36, Handler: "steal"},
		{Name: "도망", Number: 37, Handler: "flee"},
		{Name: "도", Number: 37, Handler: "flee"},
		{Name: "시간", Number: 49, Handler: "prt_time"},
		{Name: "귀환", Number: 81, Handler: "return_square"},
		{Name: "누구", Number: 8, Handler: "who"},
		{Name: "사용자검색", Number: 69, Handler: "whois"},
		{Name: "사용자정보", Number: 80, Handler: "pfinger"},
		{Name: "줄임말", Number: 82, Handler: "ply_aliases"},
		{Name: "줄", Number: 82, Handler: "ply_aliases"},
		{Name: "말", Number: 4, Handler: "say"},
		{Name: "도움말", Number: 14, Handler: "help"},
		{Name: "환영", Number: 61, Handler: "welcome"},
		{Name: "써", Number: 92, Handler: "writeboard"},
		{Name: "글삭제", Number: 93, Handler: "del_board"},
		{Name: "게시판", Number: 94, Handler: "look_board"},
		{Name: "읽어", Number: 40, Handler: "readscroll"},
		{Name: "편지보내기", Number: 53, Handler: "postsend"},
		{Name: "편지받기", Number: 54, Handler: "postread"},
		{Name: "편지삭제", Number: 55, Handler: "postdelete"},
		{Name: "*notepad", Number: 136, Handler: "notepad"},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded := worldload.NewWorld()
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:00001",
		DisplayName: "생명의 나무",
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:plaza",
		DisplayName:      "광장",
		ShortDescription: "출발 광장이다.",
		Exits: []model.Exit{{
			Name:     "동",
			ToRoomID: "room:east",
		}},
		Objects: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:stone"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:east",
		DisplayName:      "동쪽",
		ShortDescription: "동쪽 방이다.",
		Exits: []model.Exit{{
			Name:     "서",
			ToRoomID: "room:plaza",
		}},
		Objects: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:bag"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:broken-exit",
		DisplayName:      "끊어진 길",
		ShortDescription: "목적지 지도가 빠진 출구가 있는 방이다.",
		Exits: []model.Exit{{
			Name:     "허공",
			ToRoomID: "room:missing",
		}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:blocked-exits",
		DisplayName:      "막힌 길",
		ShortDescription: "닫히거나 잠긴 출구가 있는 방이다.",
		Exits: []model.Exit{
			{Name: "동", ToRoomID: "room:east", Flags: []string{"closed", "closable"}},
			{Name: "서", ToRoomID: "room:east", Flags: []string{"locked", "closed", "lockable", "key:7"}},
		},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:flagged-exits",
		DisplayName:      "관문",
		ShortDescription: "여러 갈래의 출구가 있다.",
		Exits: []model.Exit{
			{Name: "비밀", ToRoomID: "room:secret", Flags: []string{"secret"}},
			{Name: "숨김", ToRoomID: "room:east", Flags: []string{"noSee"}},
			{Name: "은신", ToRoomID: "room:east", Flags: []string{"invisible"}},
			{Name: "닫힌", ToRoomID: "room:east", Flags: []string{"closed"}},
			{Name: "혼인", ToRoomID: "room:married-only"},
		},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:secret",
		DisplayName: "비밀방",
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:married-only",
		DisplayName: "혼인 전용방",
		Properties:  map[string]string{"special": "84"},
		Metadata:    model.Metadata{Tags: []string{"onlyMarried"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:naked-exit",
		DisplayName:      "탈의 관문",
		ShortDescription: "소지품을 내려놓아야 지나갈 수 있는 방이다.",
		Exits: []model.Exit{{
			Name:     "탈의",
			ToRoomID: "room:east",
			Flags:    []string{"naked"},
		}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:destination-restrictions",
		DisplayName:      "제한 관문",
		ShortDescription: "목적지 제한을 확인하는 방이다.",
		Exits: []model.Exit{
			{Name: "문파", ToRoomID: "room:family-destination"},
			{Name: "가족", ToRoomID: "room:only-family-destination"},
			{Name: "혼인전용", ToRoomID: "room:married-only"},
			{Name: "일반", ToRoomID: "room:east"},
		},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:family-destination",
		DisplayName: "문파 전용방",
		Metadata:    model.Metadata{Tags: []string{"family"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:only-family-destination",
		DisplayName: "가족 전용방",
		Metadata:    model.Metadata{Tags: []string{"onlyFamily"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:01071",
		DisplayName: "기념품점",
		Metadata:    model.Metadata{Tags: []string{"shoppe"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:01072",
		DisplayName: "기념품점 창고",
		Objects:     model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:shop-sword", "object:shop-plaque"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:pawn",
		DisplayName: "전당포",
		Metadata:    model.Metadata{Tags: []string{"pawnShop"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:          "room:00611",
		DisplayName: "특별 대장간",
		Metadata:    model.Metadata{Tags: []string{"forge"}},
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:combat",
		DisplayName:      "수련장",
		ShortDescription: "기본 전투를 확인하는 방이다.",
	})
	mustAddServerTestRoom(t, loaded, model.Room{
		ID:               "room:board-test",
		DisplayName:      "게시판 방",
		ShortDescription: "게시판 테스트 방이다.",
		Objects:          model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:board"}},
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:alice",
		DisplayName: "Alice",
		CreatureID:  "creature:alice",
		RoomID:      "room:plaza",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:bob",
		DisplayName: "Bob",
		CreatureID:  "creature:bob",
		RoomID:      "room:plaza",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:offline",
		DisplayName: "Offline",
		RoomID:      "room:plaza",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:carol",
		DisplayName: "Carol",
		CreatureID:  "creature:carol",
		RoomID:      "room:broken-exit",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:dave",
		DisplayName: "Dave",
		CreatureID:  "creature:dave",
		RoomID:      "room:blocked-exits",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:eve",
		DisplayName: "Eve",
		CreatureID:  "creature:eve",
		RoomID:      "room:flagged-exits",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:frank",
		DisplayName: "Frank",
		CreatureID:  "creature:frank",
		RoomID:      "room:naked-exit",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:grace",
		DisplayName: "Grace",
		CreatureID:  "creature:grace",
		RoomID:      "room:naked-exit",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:heidi",
		DisplayName: "Heidi",
		CreatureID:  "creature:heidi",
		RoomID:      "room:destination-restrictions",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:ivan",
		DisplayName: "Ivan",
		CreatureID:  "creature:ivan",
		RoomID:      "room:destination-restrictions",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:judy",
		DisplayName: "Judy",
		CreatureID:  "creature:judy",
		RoomID:      "room:destination-restrictions",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:kate",
		DisplayName: "Kate",
		CreatureID:  "creature:kate",
		RoomID:      "room:combat",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:mallory",
		DisplayName: "Mallory",
		CreatureID:  "creature:mallory",
		RoomID:      "room:board-test",
	})
	mustAddServerTestPlayer(t, loaded, model.Player{
		ID:          "player:dm",
		DisplayName: "DM",
		CreatureID:  "creature:dm",
		RoomID:      "room:plaza",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:alice",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Alice",
		PlayerID:    "player:alice",
		RoomID:      "room:plaza",
		Properties:  map[string]string{"legacyPasswordHash": "WOCZU5Ja1Vg"},
		Stats:       map[string]int{"gold": 100000, "familyFlag": 1, "familyID": 7, "class": 8, "level": 20, "hpCurrent": 100, "hpMax": 100},
		Inventory: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{
			"object:sword",
		}},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:bob",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Bob",
		PlayerID:    "player:bob",
		RoomID:      "room:plaza",
		Stats:       map[string]int{"familyFlag": 1, "familyID": 7},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:guard",
		Kind:        model.CreatureKindNPC,
		DisplayName: "경비병",
		Description: "경비병이 서 있다.",
		RoomID:      "room:plaza",
		Properties:  map[string]string{"legacyTalk": "광장에서는 천천히 움직이십시오."},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:carol",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Carol",
		PlayerID:    "player:carol",
		RoomID:      "room:broken-exit",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:dave",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Dave",
		PlayerID:    "player:dave",
		RoomID:      "room:blocked-exits",
		Stats:       map[string]int{"class": 8, "level": 40},
		Inventory:   model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:dave-key"}},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:eve",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Eve",
		PlayerID:    "player:eve",
		RoomID:      "room:flagged-exits",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:frank",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Frank",
		PlayerID:    "player:frank",
		RoomID:      "room:naked-exit",
		Inventory: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{
			"object:cloak",
		}},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:grace",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Grace",
		PlayerID:    "player:grace",
		RoomID:      "room:naked-exit",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:heidi",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Heidi",
		PlayerID:    "player:heidi",
		RoomID:      "room:destination-restrictions",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:ivan",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Ivan",
		PlayerID:    "player:ivan",
		RoomID:      "room:destination-restrictions",
		Stats:       map[string]int{"marriageID": 83},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:judy",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Judy",
		PlayerID:    "player:judy",
		RoomID:      "room:destination-restrictions",
		Stats:       map[string]int{"marriageID": 83},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:kate",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Kate",
		PlayerID:    "player:kate",
		RoomID:      "room:combat",
		Stats:       map[string]int{"class": 4, "hpCurrent": 20, "hpMax": 20, "pDice": 4},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:mouse",
		Kind:        model.CreatureKindMonster,
		DisplayName: "생쥐",
		Description: "생쥐가 잡히지 않으려고 이리저리 도망다니고 있습니다.",
		RoomID:      "room:combat",
		Stats:       map[string]int{"hpCurrent": 3, "hpMax": 3},
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:mallory",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "Mallory",
		PlayerID:    "player:mallory",
		RoomID:      "room:board-test",
	})
	mustAddServerTestCreature(t, loaded, model.Creature{
		ID:          "creature:dm",
		Kind:        model.CreatureKindPlayer,
		DisplayName: "DM",
		PlayerID:    "player:dm",
		RoomID:      "room:plaza",
		Stats:       map[string]int{"class": 13},
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:sword",
		Kind:        model.ObjectKindWeapon,
		DisplayName: "목검",
		Properties:  map[string]string{"type": "1", "wearFlag": "20", "value": "50000", "sDice": "6", "nDice": "4", "pDice": "1"},
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:stone",
		DisplayName: "돌",
		Description: "작은 돌이다.",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:bag",
		Kind:        model.ObjectKindContainer,
		DisplayName: "가방",
		Description: "낡았지만 튼튼한 가방이다.",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:gem",
		DisplayName: "보석",
		Description: "은은하게 빛나는 보석이다.",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:cloak",
		DisplayName: "망토",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:key",
		Kind:        model.ObjectKindKey,
		DisplayName: "열쇠",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:plaque",
		DisplayName: "기념패",
		Properties:  map[string]string{"value": "10000"},
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "prototype:board",
		DisplayName: "게시판",
		Description: "게시판입니다.",
		Properties:  map[string]string{"type": "100", "special": "4"},
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "object:o09:0",
		Kind:        model.ObjectKindWeapon,
		DisplayName: "제련 도",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "object:o09:1",
		Kind:        model.ObjectKindWeapon,
		DisplayName: "제련 검",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "object:o09:2",
		Kind:        model.ObjectKindWeapon,
		DisplayName: "제련 봉",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "object:o09:3",
		Kind:        model.ObjectKindWeapon,
		DisplayName: "제련 창",
	})
	mustAddServerTestPrototype(t, loaded, model.ObjectPrototype{
		ID:          "object:o09:4",
		Kind:        model.ObjectKindWeapon,
		DisplayName: "제련 궁",
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:                  "object:sword",
		PrototypeID:         "prototype:sword",
		DisplayNameOverride: "빛나는 검",
		Quantity:            1,
		Location: model.ObjectLocation{
			CreatureID: "creature:alice",
			Slot:       "inventory",
		},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:                  "object:stone",
		PrototypeID:         "prototype:stone",
		DisplayNameOverride: "작은 돌",
		Quantity:            1,
		Location: model.ObjectLocation{
			RoomID: "room:plaza",
		},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:bag",
		PrototypeID: "prototype:bag",
		Quantity:    1,
		Location: model.ObjectLocation{
			RoomID: "room:east",
		},
		Contents: model.ObjectRefList{ObjectIDs: []model.ObjectInstanceID{"object:gem"}},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:gem",
		PrototypeID: "prototype:gem",
		Quantity:    1,
		Location: model.ObjectLocation{
			ContainerID: "object:bag",
		},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:cloak",
		PrototypeID: "prototype:cloak",
		Quantity:    1,
		Location: model.ObjectLocation{
			CreatureID: "creature:frank",
			Slot:       "inventory",
		},
		Properties: map[string]string{"weight": "1"},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:dave-key",
		PrototypeID: "prototype:key",
		Quantity:    1,
		Location: model.ObjectLocation{
			CreatureID: "creature:dave",
			Slot:       "inventory",
		},
		Properties: map[string]string{"nDice": "7", "shotsCurrent": "2", "useOutput": "딸깍"},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:shop-sword",
		PrototypeID: "prototype:sword",
		Quantity:    1,
		Location: model.ObjectLocation{
			RoomID: "room:01072",
		},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:shop-plaque",
		PrototypeID: "prototype:plaque",
		Quantity:    1,
		Location: model.ObjectLocation{
			RoomID: "room:01072",
		},
	})
	mustAddServerTestObject(t, loaded, model.ObjectInstance{
		ID:          "object:board",
		PrototypeID: "prototype:board",
		Quantity:    1,
		Location: model.ObjectLocation{
			RoomID: "room:board-test",
		},
	})
	loaded.MarriageInvites[84] = []string{"Ivan"}
	root := writeServerTestHelpRoot(t)

	return runtimeInputs{
		summary:  worldload.Summary{Root: root},
		registry: registry,
		world:    state.NewWorld(loaded),
	}
}

func writeServerTestHelpRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	helpDir := filepath.Join(root, "help")
	if err := os.MkdirAll(helpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"helpfile": "기본 도움말\n",
		"help.8":   "누구 도움말\n",
		"welcome":  "환영 도움말\n",
	} {
		if err := os.WriteFile(filepath.Join(helpDir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeServerTestBoardRoot(t, root)
	return root
}

func writeServerTestBoardRoot(t *testing.T, root string) {
	t.Helper()
	boardDir := filepath.Join(root, "board", "info")
	if err := os.MkdirAll(boardDir, 0o755); err != nil {
		t.Fatal(err)
	}

	index := append([]byte{}, serverBoardIndexRecord(t, 1, "운영자", "첫 공지", 126, 5, 20, 1, 2, 3, 7)...)
	index = append(index, serverBoardIndexRecord(t, 2, "무한", "둘째 공지", 126, 5, 21, 2, 3, 4, 8)...)
	if err := os.WriteFile(filepath.Join(boardDir, "board_index"), index, 0o644); err != nil {
		t.Fatal(err)
	}
	serverBoardWriteEncoded(t, filepath.Join(boardDir, "board.1"), "첫 본문입니다\n둘째 줄")
	serverBoardWriteEncoded(t, filepath.Join(boardDir, "board.2"), "둘째 본문입니다")
}

func serverBoardIndexRecord(t *testing.T, number int, uploader, title string, year, month, day, hour, minute, second, readCount int) []byte {
	t.Helper()
	data := make([]byte, cbin.BoardIndexSize)
	binary.LittleEndian.PutUint32(data[serverBoardNumberOff:], uint32(int32(number)))
	serverBoardCopyEncoded(t, data[serverBoardUploaderOff:serverBoardUploaderOff+16], uploader)
	binary.LittleEndian.PutUint32(data[serverBoardYearOff:], uint32(int32(year)))
	binary.LittleEndian.PutUint32(data[serverBoardMonthOff:], uint32(int32(month)))
	binary.LittleEndian.PutUint32(data[serverBoardDayOff:], uint32(int32(day)))
	binary.LittleEndian.PutUint32(data[serverBoardHourOff:], uint32(int32(hour)))
	binary.LittleEndian.PutUint32(data[serverBoardMinuteOff:], uint32(int32(minute)))
	binary.LittleEndian.PutUint32(data[serverBoardSecondOff:], uint32(int32(second)))
	binary.LittleEndian.PutUint32(data[serverBoardLineOff:], 2)
	binary.LittleEndian.PutUint32(data[serverBoardReadCountOff:], uint32(int32(readCount)))
	serverBoardCopyEncoded(t, data[serverBoardTitleOff:serverBoardTitleOff+40], title)
	return data
}

func serverBoardWriteEncoded(t *testing.T, path string, text string) {
	t.Helper()
	encoded, err := legacykr.EncodeEUCKR(text)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func serverBoardCopyEncoded(t *testing.T, dst []byte, text string) {
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

func registerServerTestSession(t *testing.T, loop *game.Loop, id session.ID, commands chan<- session.Command, actorID string) {
	t.Helper()
	if err := loop.RegisterSession(id, commands, actorID); err != nil {
		t.Fatal(err)
	}
}

func handleServerTestLine(t *testing.T, loop *game.Loop, id session.ID, line string) {
	t.Helper()
	if err := loop.HandleEvent(context.Background(), session.Event{SessionID: id, Kind: session.EventLine, Line: line}); err != nil {
		t.Fatalf("HandleEvent(%q) error = %v", line, err)
	}
}

func receiveServerCommand(t *testing.T, commands <-chan session.Command) session.Command {
	t.Helper()
	select {
	case got := <-commands:
		return got
	default:
		t.Fatal("no command received")
		return session.Command{}
	}
}

func assertServerCommand(t *testing.T, commands <-chan session.Command, want session.Command) {
	t.Helper()
	select {
	case got := <-commands:
		if got != want {
			t.Fatalf("command = %#v, want %#v", got, want)
		}
	default:
		t.Fatalf("no command received, want %#v", want)
	}
}

func assertServerCommandValueContains(t *testing.T, got session.Command, want session.Command, substrings ...string) {
	t.Helper()
	if got.Prompt != want.Prompt || got.Close != want.Close || got.SetCallback != want.SetCallback {
		t.Fatalf("command metadata = %#v, want %#v", got, want)
	}
	for _, substring := range substrings {
		if !strings.Contains(got.Write, substring) {
			t.Fatalf("command output missing %q:\n%s", substring, got.Write)
		}
	}
}

func assertServerCommandContains(t *testing.T, commands <-chan session.Command, want session.Command, substrings ...string) {
	t.Helper()
	select {
	case got := <-commands:
		if got.Prompt != want.Prompt || got.Close != want.Close || got.SetCallback != want.SetCallback {
			t.Fatalf("command metadata = %#v, want %#v", got, want)
		}
		for _, substring := range substrings {
			if !strings.Contains(got.Write, substring) {
				t.Fatalf("command output missing %q:\n%s", substring, got.Write)
			}
		}
	default:
		t.Fatalf("no command received, want output containing %v", substrings)
	}
}

func assertServerObjectLocation(t *testing.T, world *state.World, objectID model.ObjectInstanceID, want model.ObjectLocation) {
	t.Helper()
	object, ok := world.Object(objectID)
	if !ok {
		t.Fatalf("missing object %q", objectID)
	}
	if object.Location != want {
		t.Fatalf("%s location = %+v, want %+v", objectID, object.Location, want)
	}
}

func assertServerObjectContents(t *testing.T, world *state.World, containerID, childID model.ObjectInstanceID, want bool) {
	t.Helper()
	container, ok := world.Object(containerID)
	if !ok {
		t.Fatalf("missing container %q", containerID)
	}
	if got := serverObjectListContains(container.Contents.ObjectIDs, childID); got != want {
		t.Fatalf("%s contents contain %s = %v, want %v; contents = %+v",
			containerID, childID, got, want, container.Contents.ObjectIDs)
	}
}

func assertServerCreatureInventory(t *testing.T, world *state.World, creatureID model.CreatureID, objectID model.ObjectInstanceID, want bool) {
	t.Helper()
	creature, ok := world.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %q", creatureID)
	}
	if got := serverObjectListContains(creature.Inventory.ObjectIDs, objectID); got != want {
		t.Fatalf("%s inventory contains %s = %v, want %v; inventory = %+v",
			creatureID, objectID, got, want, creature.Inventory.ObjectIDs)
	}
}

func assertServerCreatureGold(t *testing.T, world *state.World, creatureID model.CreatureID, want int) {
	t.Helper()
	creature, ok := world.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %q", creatureID)
	}
	if got := creature.Stats["gold"]; got != want {
		t.Fatalf("%s gold = %d, want %d", creatureID, got, want)
	}
}

func assertServerActorRoom(t *testing.T, world *state.World, playerID model.PlayerID, creatureID model.CreatureID, want model.RoomID) {
	t.Helper()
	player, ok := world.Player(playerID)
	if !ok {
		t.Fatalf("missing player %q", playerID)
	}
	if player.RoomID != want {
		t.Fatalf("%s room = %q, want %q", playerID, player.RoomID, want)
	}
	creature, ok := world.Creature(creatureID)
	if !ok {
		t.Fatalf("missing creature %q", creatureID)
	}
	if creature.RoomID != want {
		t.Fatalf("%s room = %q, want %q", creatureID, creature.RoomID, want)
	}
}

func serverObjectListContains(ids []model.ObjectInstanceID, id model.ObjectInstanceID) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

func serverPlayerListContains(ids []model.PlayerID, id model.PlayerID) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

func serverCreatureListContains(ids []model.CreatureID, id model.CreatureID) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}

func mustServerExit(t *testing.T, world *state.World, roomID model.RoomID, exitName string) model.Exit {
	t.Helper()
	room, ok := world.Room(roomID)
	if !ok {
		t.Fatalf("missing room %q", roomID)
	}
	for _, exit := range room.Exits {
		if exit.Name == exitName {
			return exit
		}
	}
	t.Fatalf("missing exit %q in room %q", exitName, roomID)
	return model.Exit{}
}

func serverExitHasFlag(exit model.Exit, flag string) bool {
	for _, existing := range exit.Flags {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(flag)) {
			return true
		}
	}
	return false
}

func assertNoServerCommand(t *testing.T, commands <-chan session.Command) {
	t.Helper()
	select {
	case got := <-commands:
		t.Fatalf("unexpected command: %#v", got)
	default:
	}
}

func mustAddServerTestRoom(t *testing.T, world *worldload.World, room model.Room) {
	t.Helper()
	if err := world.AddRoom(room); err != nil {
		t.Fatal(err)
	}
}

func mustAddServerTestPlayer(t *testing.T, world *worldload.World, player model.Player) {
	t.Helper()
	if err := world.AddPlayer(player); err != nil {
		t.Fatal(err)
	}
}

func mustAddServerTestCreature(t *testing.T, world *worldload.World, creature model.Creature) {
	t.Helper()
	if err := world.AddCreature(creature); err != nil {
		t.Fatal(err)
	}
}

func mustAddServerTestBank(t *testing.T, world *worldload.World, bank model.BankAccount) {
	t.Helper()
	if err := world.AddBank(bank); err != nil {
		t.Fatal(err)
	}
}

func mustAddServerTestPrototype(t *testing.T, world *worldload.World, proto model.ObjectPrototype) {
	t.Helper()
	if err := world.AddObjectPrototype(proto); err != nil {
		t.Fatal(err)
	}
}

func mustAddServerTestObject(t *testing.T, world *worldload.World, object model.ObjectInstance) {
	t.Helper()
	if err := world.AddObjectInstance(object); err != nil {
		t.Fatal(err)
	}
}
