package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"net/http"

	"golang.org/x/net/websocket"

	"muhan/internal/commandspec"
	enginecmd "muhan/internal/engine/command"
	"muhan/internal/engine/command/table"
	"muhan/internal/engine/game"
	"muhan/internal/krtext"
	"muhan/internal/persist/legacycrypt"
	"muhan/internal/persist/legacykr"
	"muhan/internal/session"
	"muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

const defaultListenAddr = ":4000"
const loginNamePrompt = "\n당신의 이름은 무엇입니까? "
const loginNewsWaitPrompt = "\n[엔터]를 누르십시요."
const legacyCreateNewFamilyBroadcast = "\n### 새로운 무한 가족입니다. 많이 지켜봐 주세요."
const legacyLoginDMClass = 13
const legacyLoginDialinHost = "128.200.142.2"
const legacyLoginGoldCap = 300000000
const legacyLoginGoldCapMessage = "\n\n너무 많은 돈을 가지고 있습니다.\n신이 자기보다 더 많은 돈을 가지고 있다고 하여, \n가지고 있는 돈중에 3억만 남겨놓고, 나머지 부분을\n신이 그냥 가져갑니다. (신 : 재수~~~~ )\n\n"

type config struct {
	root     string
	listen   string
	wsListen string
	actor    string
	ansi     bool
	validate bool
	dryRun   bool
	migrate  bool
}

type runtimeInputs struct {
	summary              load.Summary
	registryCommandCount int
	registry             commandspec.Registry
	world                *state.World
	getLoop              func() *game.Loop
}

type validationError struct {
	errors int
}

func (e validationError) Error() string {
	return fmt.Sprintf("world validation reported %d errors", e.errors)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "muhan-server: %v\n", err)
		return 2
	}

	if err := runServer(cfg, stdout); err != nil {
		fmt.Fprintf(stderr, "muhan-server: %v\n", err)
		var validationErr validationError
		if errors.As(err, &validationErr) {
			return 1
		}
		return 2
	}
	return 0
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("muhan-server", flag.ContinueOnError)
	fs.SetOutput(stderr)

	root := fs.String("root", ".", "legacy Muhan source/data root")
	sourceRoot := fs.String("source-root", "", "legacy Muhan source/data root (overrides -root)")
	listen := fs.String("listen", defaultListenAddr, "TCP listen address")
	wsListen := fs.String("ws-listen", "127.0.0.1:4041", "WebSocket listen address")
	actor := fs.String("actor", "", "temporary actor player ID for accepted sessions until login is ported")
	ansi := fs.Bool("ansi", true, "emit ANSI color sequences for clients")
	validate := fs.Bool("validate", false, "load and validate runtime inputs, then exit without listening")
	dryRun := fs.Bool("dry-run", false, "load runtime inputs, then exit without listening")
	migrate := fs.Bool("migrate-sidecars", false, "rewrite supported old JSON sidecar schemas before startup")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *sourceRoot != "" {
		*root = *sourceRoot
	}
	return config{
		root:     *root,
		listen:   *listen,
		wsListen: *wsListen,
		actor:    *actor,
		ansi:     *ansi,
		validate: *validate,
		dryRun:   *dryRun,
		migrate:  *migrate,
	}, nil
}

func runServer(cfg config, stdout io.Writer) error {
	if cfg.migrate {
		if err := migrateSidecarsForStartup(cfg.root, stdout); err != nil {
			return err
		}
	}

	inputs, err := loadRuntimeInputs(cfg.root)
	if err != nil {
		return err
	}

	writeSummary(stdout, cfg, inputs)
	if inputs.summary.Counts.Errors > 0 {
		return validationError{errors: inputs.summary.Counts.Errors}
	}

	if cfg.validate || cfg.dryRun {
		if cfg.validate {
			fmt.Fprintln(stdout, "mode: validate")
		} else {
			fmt.Fprintln(stdout, "mode: dry-run")
		}
		return nil
	}

	// B: Ensure we flush on any exit path after this point (reliable single path)
	defer func() {
		if inputs.world != nil {
			if err := inputs.world.FlushActivePlayersAndBanks(); err != nil {
				log.Printf("[PERSIST] ERROR defer shutdown FlushActivePlayersAndBanks: %v", err)
			} else {
				log.Printf("[PERSIST] INFO defer shutdown full flush complete (players+banks+rooms)")
			}
		}
	}()
	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", cfg.listen, err)
	}
	defer listener.Close()

	fmt.Fprintf(stdout, "listening: %s\n", listener.Addr())
	if cfg.wsListen != "" {
		tcpPort := listener.Addr().(*net.TCPAddr).Port
		tcpAddr := fmt.Sprintf("127.0.0.1:%d", tcpPort)
		startWebSocketProxy(cfg.wsListen, tcpAddr, stdout)
	}
	if cfg.actor != "" {
		fmt.Fprintf(stdout, "temporary actor binding: %s\n", cfg.actor)
	} else {
		fmt.Fprintln(stdout, "login: enabled")
	}
	return serve(context.Background(), listener, inputs, cfg.actor, cfg.ansi, stdout)
}

func migrateSidecarsForStartup(root string, stdout io.Writer) error {
	report, err := state.MigrateSidecars(root)
	if err != nil {
		return fmt.Errorf("migrate sidecars: %w", err)
	}
	fmt.Fprintf(stdout, "sidecar migration: scanned=%d migrated=%d errors=%d\n", report.TotalScanned, report.Migrated, len(report.Errors))
	if len(report.ByType) > 0 {
		keys := make([]string, 0, len(report.ByType))
		for key := range report.ByType {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(stdout, "sidecar migration %s: %d\n", key, report.ByType[key])
		}
	}
	for _, detail := range report.Details {
		fmt.Fprintf(stdout, "sidecar migrated: %s %s v%d -> v%d\n", detail.Type, detail.Path, detail.FromVer, detail.ToVer)
	}
	for _, msg := range report.Errors {
		fmt.Fprintf(stdout, "sidecar migration error: %s\n", msg)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("migrate sidecars: %d file(s) failed", len(report.Errors))
	}
	return nil
}

func loadRuntimeInputs(root string) (runtimeInputs, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return runtimeInputs{}, fmt.Errorf("resolve root: %w", err)
	}

	registry, _, err := table.LoadLegacyRegistry(absRoot)
	if err != nil {
		return runtimeInputs{}, fmt.Errorf("load legacy registry: %w", err)
	}

	summary, err := load.LoadRoot(absRoot)
	if err != nil {
		return runtimeInputs{}, fmt.Errorf("load world: %w", err)
	}
	registry, err = registryWithRoomExitCommands(registry, summary.World)
	if err != nil {
		return runtimeInputs{}, fmt.Errorf("build runtime registry: %w", err)
	}

	world := state.New(summary.World)
	world.SetDBRoot(absRoot)
	if err := world.LoadLockouts(); err != nil {
		log.Printf("[SECURITY] WARN load lockouts failed: %v", err)
	}

	// B: Minimal runtime persistence restore (player + bank JSON sidecars)
	// Basic restart resilience for player progress and bank contents.
	if absRoot != "" {
		restoredPlayers := 0
		restoredBanks := 0
		for pid := range summary.World.Players {
			if saved, ok, err := state.LoadPlayer(absRoot, pid); err == nil && ok {
				if err := world.MergePlayerSaveIntoWorld(saved); err == nil {
					restoredPlayers++
				} else {
					log.Printf("[PERSIST] WARN MergePlayerSave %s failed: %v", pid, err)
				}
			} else if err != nil {
				log.Printf("[PERSIST] WARN LoadPlayer %s failed: %v", pid, err)
			}
			bankID := model.BankID("bank:player:" + string(pid))
			if bundle, ok, err := world.LoadBank(bankID); err == nil && ok {
				if err := world.MergeBankSave(bundle); err == nil {
					restoredBanks++
				} else {
					log.Printf("[PERSIST] WARN MergeBankSave %s failed: %v", bankID, err)
				}
			} else if err != nil && !os.IsNotExist(err) {
				log.Printf("[PERSIST] WARN LoadBank %s failed: %v", bankID, err)
			}
		}
		restoredBanks += restoreFamilyBankSidecars(absRoot, world)
		if restoredPlayers > 0 || restoredBanks > 0 {
			log.Printf("[PERSIST] INFO startup restore: %d players + %d banks from JSON sidecars", restoredPlayers, restoredBanks)
		} else {
			log.Printf("[PERSIST] INFO startup: no prior player/bank JSON sidecars found (fresh or legacy DB)")
		}

		// D: Restore room floor objects (dropped items, corpses, ground money etc.)
		restoredRooms := 0
		for rid := range summary.World.Rooms {
			if saved, ok, err := state.LoadRoomObjects(absRoot, rid); err == nil && ok {
				if err := world.MergeRoomObjectsSaveIntoWorld(saved); err == nil {
					restoredRooms++
				} else {
					log.Printf("[PERSIST] WARN MergeRoomObjects %s failed: %v", rid, err)
				}
			} else if err != nil && !os.IsNotExist(err) {
				log.Printf("[PERSIST] WARN LoadRoomObjects %s failed: %v", rid, err)
			}
		}
		if restoredRooms > 0 {
			log.Printf("[PERSIST] INFO startup restore: %d rooms with floor objects from sidecars", restoredRooms)
		}

		// C: Restore board posts + family news runtime sidecars (Package C complete)
		restoredBoards := 0
		knownBoardDirs := []string{"info", "notice", "user", "family", "family1", "family2", "family3", "family4"}
		for _, bdir := range knownBoardDirs {
			if saved, ok, err := state.LoadBoardPosts(absRoot, bdir); err == nil && ok {
				if err := world.MergeBoardPostsSaveIntoWorld(saved); err == nil {
					restoredBoards++
				} else {
					log.Printf("[PERSIST] WARN MergeBoardPosts %s failed: %v", bdir, err)
				}
			} else if err != nil && !os.IsNotExist(err) {
				log.Printf("[PERSIST] WARN LoadBoardPosts %s failed: %v", bdir, err)
			}
		}
		restoredFamNews := 0
		for fid := 1; fid <= 16; fid++ {
			if saved, ok, err := state.LoadFamilyNews(absRoot, fid); err == nil && ok {
				if err := world.MergeFamilyNewsSaveIntoWorld(saved); err == nil {
					restoredFamNews++
				} else {
					log.Printf("[PERSIST] WARN MergeFamilyNews %d failed: %v", fid, err)
				}
			}
		}
		if restoredBoards > 0 || restoredFamNews > 0 {
			log.Printf("[PERSIST] INFO startup restore C: %d boards + %d family news sidecars", restoredBoards, restoredFamNews)
		}
	}

	// F: world is now surfaced via WithCommandContextValues (see serve() setup) for savegame + direct Save* from handlers.

	return runtimeInputs{
		summary:              summary,
		registryCommandCount: len(registry.Commands()),
		registry:             registry,
		world:                world,
	}, nil
}

func restoreFamilyBankSidecars(absRoot string, world *state.World) int {
	if absRoot == "" || world == nil {
		return 0
	}
	entries, err := os.ReadDir(filepath.Join(absRoot, "player", "bank", "json"))
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[PERSIST] WARN scan family bank sidecars failed: %v", err)
		}
		return 0
	}

	restored := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".json")
		if stem == "" {
			continue
		}
		bankID := model.BankID("bank:family:" + stem)
		bundle, ok, err := world.LoadBank(bankID)
		if err != nil {
			log.Printf("[PERSIST] WARN LoadFamilyBank %s failed: %v", bankID, err)
			continue
		}
		if !ok || !strings.HasPrefix(string(bundle.BankAccount.ID), "bank:family:") {
			continue
		}
		if err := world.MergeBankSave(bundle); err != nil {
			log.Printf("[PERSIST] WARN MergeFamilyBank %s failed: %v", bundle.BankAccount.ID, err)
			continue
		}
		restored++
	}
	return restored
}

func registryWithRoomExitCommands(registry commandspec.Registry, world *load.World) (commandspec.Registry, error) {
	specs := registry.Commands()
	existing := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name != "" {
			existing[name] = struct{}{}
		}
	}

	exitNames := make(map[string]struct{})
	if world != nil {
		for _, room := range world.Rooms {
			for _, exit := range room.Exits {
				name := strings.TrimSpace(exit.Name)
				if name == "" {
					continue
				}
				if _, ok := existing[name]; ok {
					continue
				}
				exitNames[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(exitNames))
	for name := range exitNames {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		specs = append(specs, commandspec.CommandSpec{Name: name, Number: 1, Handler: "move"})
	}
	return commandspec.NewRegistry(specs)
}

func serve(ctx context.Context, listener net.Listener, inputs runtimeInputs, actorID string, ansi bool, stdout io.Writer) error {
	events := make(chan session.Event, 64)
	login := newServerLoginManager(inputs.world, inputs.summary.Root)
	options := []game.Option{
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
		game.WithRoomBroadcastWorld(inputs.world),
		game.WithWorld(inputs.world),
	}
	// F: Always surface world (and optional ANSI) into command ctx values.
	// This activates the savegame handler's direct SavePlayer/SaveBank path
	// (ctx.Values["game.world"]) which was previously dead/unwired despite B/C code.
	// No new logic; enables existing explicit "저장" persistence + other ctx consumers.
	ctxVals := map[string]any{
		"game.world": inputs.world,
	}
	if ansi {
		ctxVals[enginecmd.ContextANSIKey] = true
		ctxVals[enginecmd.ContextANSIBrightKey] = true
	}
	options = append(options, game.WithCommandContextValues(ctxVals))

	if actorID == "" {
		options = append(options, game.WithUnauthenticatedLineHandler(login.HandleLine))
	}
	var loop *game.Loop
	inputs.getLoop = func() *game.Loop {
		return loop
	}
	inputs.world.BroadcastAllFunc = func(message string) error {
		activeLoop := inputs.getLoop()
		if activeLoop == nil {
			return nil
		}
		return activeLoop.Broadcast(ctx, session.Command{Write: message})
	}
	loop = game.NewLoop(serverDispatcher(inputs), options...)

	// F: Context values (incl. world) injected via options; savegame etc use ctx.Values for direct access.

	loopErr := make(chan error, 1)
	go func() {
		loopErr <- loop.Run(ctx, events)
	}()

	// Package B: Proper OS signal handling (SIGINT, SIGTERM, SIGHUP) for graceful shutdown.
	// On signal: [PERSIST] log, full FlushActivePlayersAndBanks (players + banks + room floor objects),
	// cleanly unblock accept to stop serve loop, exit gracefully (defers also flush).
	// Integrates with DM *shutdown timer (which sets shutdownLTime; its final path also uses flush).
	// C ref (src/io.c): sock_init ignores SIGTERM/SIGPIPE, only SIGCHLD; sock_loop is while(1) with no OS signal shutdown;
	// shutdown only via dm_shutdown setting timer -> update_shutdown does resave/save then kill -9/exit. No signal.Notify equiv.
	// muhandown.c is just the "down for maintenance" banner server on :4000.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		sig := <-sigCh
		log.Printf("[PERSIST] INFO received OS signal %v: initiating graceful full flush + shutdown", sig)
		if inputs.world != nil {
			if err := inputs.world.FlushActivePlayersAndBanks(); err != nil {
				log.Printf("[PERSIST] ERROR signal shutdown FlushActivePlayersAndBanks: %v", err)
			} else {
				log.Printf("[PERSIST] INFO signal shutdown full flush complete (players+banks+rooms)")
			}
		}
		// Clean stop: close listener -> Accept errs -> serve returns -> defers run -> clean exit
		_ = listener.Close()
	}()

	var nextID uint64
	for {
		select {
		case err := <-loopErr:
			// B: Flush persistence on loop exit (crash or shutdown) - single reliable path
			if inputs.world != nil {
				if ferr := inputs.world.FlushActivePlayersAndBanks(); ferr != nil {
					log.Printf("[PERSIST] ERROR loopErr shutdown FlushActivePlayersAndBanks: %v", ferr)
				} else {
					log.Printf("[PERSIST] INFO loopErr shutdown full flush complete (players+banks+rooms)")
				}
			}
			return err
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		lockoutMode, sitePassword := state.LockoutAllow, ""
		if inputs.world != nil {
			lockoutMode, sitePassword = inputs.world.CheckLockout(serverRemoteHost(conn.RemoteAddr()))
		}
		if lockoutMode == state.LockoutDeny {
			_, _ = io.WriteString(conn, "\r\nYour site is locked out.\r\n")
			_ = conn.Close()
			fmt.Fprintf(stdout, "rejected: lockout %s\n", serverRemoteHost(conn.RemoteAddr()))
			continue
		}

		id := session.ID(fmt.Sprintf("s%d", atomic.AddUint64(&nextID, 1)))
		commands := make(chan session.Command, 16)
		if err := loop.RegisterSession(id, commands, actorID); err != nil {
			_ = conn.Close()
			return err
		}

		s, err := session.New(id, conn, events, commands)
		if err != nil {
			loop.UnregisterSession(id)
			_ = conn.Close()
			return err
		}

		fmt.Fprintf(stdout, "accepted: %s %s\n", id, conn.RemoteAddr())
		if actorID == "" {
			if lockoutMode == state.LockoutPassword {
				login.StartWithSitePassword(id, sitePassword, serverRemoteHost(conn.RemoteAddr()))
				commands <- session.Command{Write: "\nA password is required to play from that site.\nPlease enter site password: "}
			} else {
				login.Start(id, serverRemoteHost(conn.RemoteAddr()))
				commands <- session.Command{Write: loginNamePrompt}
			}
		} else {
			commands <- session.Command{Write: "무한에 접속했습니다.\n", Prompt: "> "}
		}
		go func() {
			if err := s.Run(ctx); err != nil {
				fmt.Fprintf(stdout, "session %s ended: %v\n", id, err)
			}
		}()
	}
}

type serverLoginStep int

const (
	serverLoginSitePassword serverLoginStep = iota
	serverLoginName
	serverLoginPassword
	serverLoginCreateConfirm
	serverLoginCreateEnter
	serverLoginCreateGender
	serverLoginCreateClass
	serverLoginCreateStats
	serverLoginCreateWeapon
	serverLoginCreateAlignment
	serverLoginCreateRace
	serverLoginCreatePassword
)

type serverLoginState struct {
	step         serverLoginStep
	playerID     model.PlayerID
	failures     int
	sitePassword string
	remoteHost   string
	create       serverLoginCreateState
}

type serverLoginCreateState struct {
	name         string
	male         bool
	class        int
	strength     int
	dexterity    int
	constitution int
	intelligence int
	piety        int
	weapon       int
	chaos        bool
	race         int
}

type serverLoginManager struct {
	mu       sync.Mutex
	world    *state.World
	root     string
	sessions map[session.ID]serverLoginState
}

func newServerLoginManager(world *state.World, root ...string) *serverLoginManager {
	dbRoot := ""
	if len(root) > 0 {
		dbRoot = root[0]
	}
	return &serverLoginManager{
		world:    world,
		root:     dbRoot,
		sessions: map[session.ID]serverLoginState{},
	}
}

func (m *serverLoginManager) Start(id session.ID, remoteHost ...string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = serverLoginState{step: serverLoginName, remoteHost: firstLoginRemoteHost(remoteHost)}
}

func (m *serverLoginManager) StartWithSitePassword(id session.ID, password string, remoteHost ...string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = serverLoginState{step: serverLoginSitePassword, sitePassword: password, remoteHost: firstLoginRemoteHost(remoteHost)}
}

func firstLoginRemoteHost(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func (m *serverLoginManager) HandleLine(ctx context.Context, id session.ID, line string) (game.UnauthenticatedLineResult, error) {
	if m == nil {
		return game.UnauthenticatedLineResult{}, errors.New("login manager is nil")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	login := m.sessions[id]
	switch login.step {
	case serverLoginSitePassword:
		return m.handleSitePassword(id, login, line)
	case serverLoginPassword:
		return m.handlePassword(id, login, line)
	case serverLoginCreateConfirm:
		return m.handleCreateConfirm(id, login, line)
	case serverLoginCreateEnter:
		return m.handleCreateEnter(id, login, line)
	case serverLoginCreateGender:
		return m.handleCreateGender(id, login, line)
	case serverLoginCreateClass:
		return m.handleCreateClass(id, login, line)
	case serverLoginCreateStats:
		return m.handleCreateStats(id, login, line)
	case serverLoginCreateWeapon:
		return m.handleCreateWeapon(id, login, line)
	case serverLoginCreateAlignment:
		return m.handleCreateAlignment(id, login, line)
	case serverLoginCreateRace:
		return m.handleCreateRace(id, login, line)
	case serverLoginCreatePassword:
		return m.handleCreatePassword(id, login, line)
	default:
		return m.handleName(id, login, line)
	}
}

func (m *serverLoginManager) handleSitePassword(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	if line != login.sitePassword {
		delete(m.sessions, id)
		return game.UnauthenticatedLineResult{
			Command: session.Command{Write: "\r\nYour site is locked out.\r\n", Close: true},
		}, nil
	}
	m.sessions[id] = serverLoginState{step: serverLoginName, remoteHost: login.remoteHost}
	return loginCommand(loginNamePrompt), nil
}

func (m *serverLoginManager) handleName(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	name := strings.TrimSpace(line)
	if name == "" {
		return loginCommand("이름은 한 자 이상이어야 합니다.\n" + loginNamePrompt), nil
	}

	player, ok := m.world.Player(model.PlayerID(name))
	if !ok {
		if msg := legacyCreateNameRejection(name); msg != "" {
			return loginCommand(msg + loginNamePrompt), nil
		}
		login.create = serverLoginCreateState{name: name}
		login.step = serverLoginCreateConfirm
		m.sessions[id] = login
		return loginCommand(fmt.Sprintf("\n%s%s 하시겠습니까(예/아니오)? ", name, krtext.Particle(name, '4'))), nil
	}
	if legacyPasswordHash(m.world, player) == "" {
		delete(m.sessions, id)
		return loginCommand("저장된 암호 정보를 찾을 수 없습니다.\n" + loginNamePrompt), nil
	}

	m.sessions[id] = serverLoginState{
		step:       serverLoginPassword,
		playerID:   player.ID,
		remoteHost: login.remoteHost,
	}
	return loginCommand("암호를 넣어 주십시요: "), nil
}

func (m *serverLoginManager) handlePassword(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	player, ok := m.world.Player(login.playerID)
	if !ok {
		delete(m.sessions, id)
		return loginCommand("플레이어 정보를 다시 찾을 수 없습니다.\n" + loginNamePrompt), nil
	}
	if legacycrypt.Verify(line, legacyPasswordHash(m.world, player)) {
		delete(m.sessions, id)
		return m.loginSuccessResult(player, login.remoteHost)
	}

	login.failures++
	if strings.TrimSpace(line) == "" || login.failures >= 3 {
		delete(m.sessions, id)
		return game.UnauthenticatedLineResult{
			Command: session.Command{Write: "\n암호가 틀립니다. 접속을 끊습니다.\n\n", Close: true},
		}, nil
	}
	m.sessions[id] = login
	return loginCommand("\n암호가 틀립니다. 다시 입력하십시요.\n암호를 다시 입력하세요: "), nil
}

func (m *serverLoginManager) handleCreateConfirm(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	if !legacyYes(line) {
		login.step = serverLoginName
		login.create = serverLoginCreateState{}
		m.sessions[id] = login
		return loginCommand("당신의 이름은 무엇입니까? "), nil
	}
	login.step = serverLoginCreateEnter
	m.sessions[id] = login
	return loginCommand("\n[엔터]를 누르십시요."), nil
}

func (m *serverLoginManager) handleCreateEnter(id session.ID, login serverLoginState, _ string) (game.UnauthenticatedLineResult, error) {
	login.step = serverLoginCreateGender
	m.sessions[id] = login
	return loginCommand("\n\n당신은 남자입니까, 여자입니까(남자/여자)? "), nil
}

func (m *serverLoginManager) handleCreateGender(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "남"):
		login.create.male = true
	case strings.HasPrefix(line, "여"):
		login.create.male = false
	default:
		m.sessions[id] = login
		return loginCommand("입력이 잘못되었습니다.\n\n당신은 남자입니까, 여자입니까(남자/여자)? "), nil
	}
	login.step = serverLoginCreateClass
	m.sessions[id] = login
	return loginCommand("\n다음과 같은 직업이 있습니다.\n" +
		"1.자  객  2.권법가  3.불제자  4.검  사\n" +
		"5.도술사  6.무  사  7.포  졸  8.도  둑\n" +
		"직업을 고르세요: "), nil
}

func (m *serverLoginManager) handleCreateClass(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	class, ok := legacyCreateClassChoice(line)
	if !ok {
		m.sessions[id] = login
		return loginCommand("직업을 고르세요: "), nil
	}
	login.create.class = class
	login.step = serverLoginCreateStats
	m.sessions[id] = login
	return loginCommand("\n당신은 54점으로 다음 5가지 능력치를 구성할수 있습니다.\n" +
		"5이상 18이하의 수치로 ## ## ## ## ##의 형식으로 5가지 능력치를 적어주십시요.\n" +
		"능력: 힘 민첩 맷집 지식 신앙심\n예: 12 10 12 10 10\n\n" +
		": "), nil
}

func (m *serverLoginManager) handleCreateStats(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	values, err := legacyCreateStatChoices(line)
	if err != nil {
		m.sessions[id] = login
		return loginCommand(err.Error() + "\n: "), nil
	}
	login.create.strength = values[0]
	login.create.dexterity = values[1]
	login.create.constitution = values[2]
	login.create.intelligence = values[3]
	login.create.piety = values[4]
	login.step = serverLoginCreateWeapon
	m.sessions[id] = login
	return loginCommand("\n당신에게 익숙한 무기를 고르십시요.\n" +
		"1.도   2.검   3.봉   4.창   5.궁.\n" +
		": "), nil
}

func (m *serverLoginManager) handleCreateWeapon(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	weapon, ok := legacyCreateDigitChoice(line, 1, 5)
	if !ok {
		m.sessions[id] = login
		return loginCommand("다시 고르세요.\n: "), nil
	}
	login.create.weapon = weapon - 1
	login.step = serverLoginCreateAlignment
	m.sessions[id] = login
	return loginCommand("\n선한 구성원은 다른사람을 공격하지 못하고 공격 받을수도 없으며" +
		"\n그 구성원에게서 물건을 훔칠수도 없습니다." +
		"\n그러나 악한 구성원은 공격할수도 있고 물건을 훔칠수도 있으며" +
		"\n다른 악한 구성원들에게 공격을 받을 수도 있습니다.\n" +
		"\n성향을 고르십시요(선함/악함): "), nil
}

func (m *serverLoginManager) handleCreateAlignment(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "악"):
		login.create.chaos = true
	case strings.HasPrefix(line, "선"):
		login.create.chaos = false
	default:
		m.sessions[id] = login
		return loginCommand("성향을 고르십시요(선함/악함): "), nil
	}
	login.step = serverLoginCreateRace
	m.sessions[id] = login
	return loginCommand("\n다음과 같은 종족들이 있습니다." +
		"\n1.난장이족  2.용 신 족  3.땅귀신족 4.요 괴 족" +
		"\n5.거 인 족  6.토 신 족  7.인 간 족 8.도깨비족" +
		"\n종족을 고르십시요: "), nil
}

func (m *serverLoginManager) handleCreateRace(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	race, ok := legacyCreateRaceChoice(line)
	if !ok {
		m.sessions[id] = login
		return loginCommand("\n종족을 고르십시요: "), nil
	}
	login.create.race = race
	legacyApplyCreateRaceBonus(&login.create)
	login.step = serverLoginCreatePassword
	m.sessions[id] = login
	return loginCommand("\n새 암호를 넣으십시요(3자이상 14자이하): "), nil
}

func (m *serverLoginManager) handleCreatePassword(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	passwordLen := legacyCreatePasswordByteLen(line)
	if passwordLen > 14 {
		m.sessions[id] = login
		return loginCommand("입력된 암호가 너무 깁니다.\n암호를 다시 넣으십시요(3자이상 14자이하): "), nil
	}
	if passwordLen < 3 {
		m.sessions[id] = login
		return loginCommand("입력된 암호가 너무 짧습니다.\n암호를 다시 넣으십시요(3자이상 14자이하): "), nil
	}
	hash, err := legacycrypt.Hash(line)
	if err != nil {
		return game.UnauthenticatedLineResult{}, err
	}

	player, creature, err := legacyCreatedPlayerCharacter(login.create, hash)
	if err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	if err := m.world.CreatePlayerCharacter(player, creature); err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	if err := m.world.SavePlayer(player.ID); err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	_ = m.world.BroadcastAll(legacyCreateNewFamilyBroadcast)

	delete(m.sessions, id)
	return m.loginSuccessResultWithExtra(player, login.remoteHost,
		"[환영]이라고 치시면 초보자 분들에게 도움이 되는 많은 정보를 얻을수 있습니다.\n"+
			"레벨이 6이상 되지 않으면 아이디가 삭제됩니다.\n"+
			legacyCreateNewFamilyBroadcast)
}

func loginCommand(write string) game.UnauthenticatedLineResult {
	return game.UnauthenticatedLineResult{Command: session.Command{Write: write}}
}

func legacyCreatePasswordByteLen(password string) int {
	encoded, err := legacykr.EncodeEUCKR(password)
	if err != nil {
		return len([]byte(password))
	}
	return len(encoded)
}

func (m *serverLoginManager) loginSuccessResult(player model.Player, remoteHost string) (game.UnauthenticatedLineResult, error) {
	return m.loginSuccessResultWithExtra(player, remoteHost, "")
}

func (m *serverLoginManager) loginSuccessResultWithExtra(player model.Player, remoteHost string, extra string) (game.UnauthenticatedLineResult, error) {
	steps := m.loginPostLoadSteps(player, remoteHost)
	if extra != "" {
		steps = append(steps, serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, content: extra})
	}
	sequence := &serverLoginPostLoadSequence{steps: steps}
	var pending enginecmd.PendingLineHandler
	ctx := &enginecmd.Context{
		ActorID: string(player.ID),
		Values: map[string]any{
			enginecmd.ContextPendingLineKey: func(handler enginecmd.PendingLineHandler) {
				pending = handler
			},
		},
	}
	ctx.WriteString("\n무한에 접속했습니다.\n")
	status, err := sequence.render(ctx)
	if err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	result := game.UnauthenticatedLineResult{
		ActorID: string(player.ID),
		Command: session.Command{Write: ctx.OutputString()},
	}
	if status == enginecmd.StatusDoPrompt && pending != nil {
		result.Pending = pending
		return result, nil
	}
	result.Command.Prompt = "> "
	return result, nil
}

func (m *serverLoginManager) loginPostLoadSteps(player model.Player, remoteHost string) []serverLoginPostLoadStep {
	root := strings.TrimSpace(m.root)

	creature, hasCreature := model.Creature{}, false
	if m.world != nil && !player.CreatureID.IsZero() {
		creature, hasCreature = m.world.Creature(player.CreatureID)
	}

	steps := []serverLoginPostLoadStep{}
	if root != "" {
		newsName := "news"
		if hasCreature {
			if class, ok := serverCreatureInt(creature, "class"); ok && class == legacyLoginDMClass {
				newsName = "DM_news"
			}
		}
		if strings.TrimSpace(remoteHost) == legacyLoginDialinHost {
			steps = append(steps, m.loginViewFileStep(filepath.Join(root, "help", "dialin"), true, nil))
		}
		steps = append(steps,
			m.loginViewFileStep(filepath.Join(root, "log", newsName), true, nil),
			serverLoginPostLoadStep{kind: serverLoginPostLoadStepWait, prompt: loginNewsWaitPrompt},
		)

		if hasCreature && serverCreatureFlag(creature, "familyFlag", "PFAMIL") {
			if familyID, ok := serverCreatureInt(creature, "familyID", "dailyExpndMax", "legacyDailyExpndMax"); ok && familyID > 0 {
				path := filepath.Join(root, "player", "family", fmt.Sprintf("family_news_%d", familyID))
				if loginRegularFileExists(path) {
					steps = append(steps, m.loginViewFileStep(path, true, nil))
				}
			}
		}

		if name := loginPlayerFileName(player); name != "" {
			path := filepath.Join(root, "player", "fal", name)
			if loginRegularFileExists(path) {
				steps = append(steps, m.loginViewFileStep(path, true, func() error {
					return os.Remove(path)
				}))
			}
		}
		if notice := m.loginPostNotice(player); notice != "" {
			steps = append(steps, serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, content: notice})
		}
	}
	if hasCreature {
		if step, ok := m.loginGoldCapStep(creature); ok {
			steps = append(steps, step)
		}
	}
	return steps
}

func (m *serverLoginManager) loginPostNotice(player model.Player) string {
	if m == nil || strings.TrimSpace(m.root) == "" {
		return ""
	}
	name := loginPlayerFileName(player)
	if name == "" {
		return ""
	}
	postRoot := filepath.Clean(filepath.Join(m.root, "post"))
	path := filepath.Clean(filepath.Join(postRoot, name))
	rel, err := filepath.Rel(postRoot, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return "\n*** 우체국에 편지가 와있습니다.\n"
}

func (m *serverLoginManager) loginViewFileStep(path string, reportMissing bool, onDone func() error) serverLoginPostLoadStep {
	text, ok := loginReadLegacyText(path)
	if !ok {
		if reportMissing {
			return serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, content: "화일을 읽을 수 없습니다.\n", onDone: onDone}
		}
		return serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, onDone: onDone}
	}
	return serverLoginPostLoadStep{kind: serverLoginPostLoadStepFile, content: text, onDone: onDone}
}

func (m *serverLoginManager) loginGoldCapStep(creature model.Creature) (serverLoginPostLoadStep, bool) {
	if m == nil || m.world == nil || creature.ID.IsZero() {
		return serverLoginPostLoadStep{}, false
	}
	gold, ok := serverCreatureInt(creature, "gold")
	if !ok || gold <= legacyLoginGoldCap {
		return serverLoginPostLoadStep{}, false
	}
	return serverLoginPostLoadStep{
		kind:    serverLoginPostLoadStepMessage,
		content: legacyLoginGoldCapMessage,
		onDone: func() error {
			return m.world.SetCreatureStat(creature.ID, "gold", legacyLoginGoldCap)
		},
	}, true
}

type serverLoginPostLoadStepKind int

const (
	serverLoginPostLoadStepMessage serverLoginPostLoadStepKind = iota
	serverLoginPostLoadStepFile
	serverLoginPostLoadStepWait
)

type serverLoginPostLoadStep struct {
	kind    serverLoginPostLoadStepKind
	content string
	prompt  string
	onDone  func() error
}

type serverLoginPostLoadSequence struct {
	steps []serverLoginPostLoadStep
	index int
	next  int
}

func (s *serverLoginPostLoadSequence) handleLine(ctx *enginecmd.Context, line string) (enginecmd.Status, error) {
	if s == nil || s.index >= len(s.steps) {
		enginecmd.ClearPendingLineHandler(ctx)
		return enginecmd.StatusDefault, nil
	}
	step := s.steps[s.index]
	if step.kind == serverLoginPostLoadStepWait {
		s.index++
		s.next = 0
		return s.render(ctx)
	}
	if step.kind == serverLoginPostLoadStepFile && strings.HasPrefix(line, ".") {
		ctx.WriteString("중단합니다.\n")
		if step.onDone != nil {
			if err := step.onDone(); err != nil {
				return enginecmd.StatusDefault, err
			}
		}
		s.index++
		s.next = 0
		return s.render(ctx)
	}
	return s.render(ctx)
}

func (s *serverLoginPostLoadSequence) render(ctx *enginecmd.Context) (enginecmd.Status, error) {
	if s == nil {
		return enginecmd.StatusDefault, nil
	}
	for s.index < len(s.steps) {
		step := s.steps[s.index]
		switch step.kind {
		case serverLoginPostLoadStepMessage:
			ctx.WriteString(step.content)
			if step.onDone != nil {
				if err := step.onDone(); err != nil {
					return enginecmd.StatusDefault, err
				}
			}
			s.index++
			s.next = 0
		case serverLoginPostLoadStepFile:
			if s.next >= len(step.content) {
				if step.onDone != nil {
					if err := step.onDone(); err != nil {
						return enginecmd.StatusDefault, err
					}
				}
				s.index++
				s.next = 0
				continue
			}
			page, next := enginecmd.LegacyViewFilePage(step.content, s.next)
			s.next = next
			ctx.WriteString(page)
			if s.next < len(step.content) {
				ctx.WriteString(enginecmd.LegacyViewFileContinuePrompt)
				if !enginecmd.SetPendingLineHandler(ctx, s.handleLine) {
					return enginecmd.StatusDefault, errors.New("로그인 파일 읽기 상태를 시작할 수 없습니다")
				}
				return enginecmd.StatusDoPrompt, nil
			}
			if step.onDone != nil {
				if err := step.onDone(); err != nil {
					return enginecmd.StatusDefault, err
				}
			}
			s.index++
			s.next = 0
		case serverLoginPostLoadStepWait:
			ctx.WriteString(step.prompt)
			if !enginecmd.SetPendingLineHandler(ctx, s.handleLine) {
				return enginecmd.StatusDefault, errors.New("로그인 확인 상태를 시작할 수 없습니다")
			}
			return enginecmd.StatusDoPrompt, nil
		default:
			s.index++
			s.next = 0
		}
	}
	enginecmd.ClearPendingLineHandler(ctx)
	return enginecmd.StatusDefault, nil
}

func loginReadLegacyText(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Path: path, Field: "login"}, data)
	if err != nil {
		return "", false
	}
	return text, true
}

func loginRegularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func loginPlayerFileName(player model.Player) string {
	name := strings.TrimSpace(player.DisplayName)
	if name == "" {
		name = strings.TrimSpace(string(player.ID))
	}
	if !loginSafePostFileName(name) {
		return ""
	}
	return name
}

func loginSafePostFileName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	if name == "." || name == ".." || filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

type legacyCreateClassStats struct {
	hpStart int
	mpStart int
	hp      int
	mp      int
	nDice   int
	sDice   int
	pDice   int
}

const (
	legacyCreateClassAssassin  = 1
	legacyCreateClassBarbarian = 2
	legacyCreateClassCleric    = 3
	legacyCreateClassFighter   = 4
	legacyCreateClassMage      = 5
	legacyCreateClassPaladin   = 6
	legacyCreateClassRanger    = 7
	legacyCreateClassThief     = 8

	legacyCreateRaceDwarf     = 1
	legacyCreateRaceElf       = 2
	legacyCreateRaceHalfElf   = 3
	legacyCreateRaceHobbit    = 4
	legacyCreateRaceHuman     = 5
	legacyCreateRaceOrc       = 6
	legacyCreateRaceHalfGiant = 7
	legacyCreateRaceGnome     = 8
)

var legacyCreateClassStatTable = map[int]legacyCreateClassStats{
	legacyCreateClassAssassin:  {hpStart: 55, mpStart: 40, hp: 5, mp: 2, nDice: 1, sDice: 6, pDice: 0},
	legacyCreateClassBarbarian: {hpStart: 57, mpStart: 40, hp: 7, mp: 1, nDice: 2, sDice: 3, pDice: 1},
	legacyCreateClassCleric:    {hpStart: 54, mpStart: 50, hp: 4, mp: 3, nDice: 1, sDice: 4, pDice: 0},
	legacyCreateClassFighter:   {hpStart: 56, mpStart: 50, hp: 6, mp: 1, nDice: 1, sDice: 5, pDice: 0},
	legacyCreateClassMage:      {hpStart: 54, mpStart: 50, hp: 4, mp: 3, nDice: 1, sDice: 3, pDice: 0},
	legacyCreateClassPaladin:   {hpStart: 55, mpStart: 50, hp: 5, mp: 2, nDice: 1, sDice: 4, pDice: 0},
	legacyCreateClassRanger:    {hpStart: 56, mpStart: 40, hp: 6, mp: 2, nDice: 2, sDice: 2, pDice: 0},
	legacyCreateClassThief:     {hpStart: 55, mpStart: 50, hp: 5, mp: 2, nDice: 2, sDice: 2, pDice: 1},
}

var legacyCreateProficiencyKeys = []string{
	"proficiencySharp",
	"proficiencyThrust",
	"proficiencyBlunt",
	"proficiencyPole",
	"proficiencyMissile",
}

func legacyCreateNameRejection(name string) string {
	if name == "" {
		return "이름은 한 자 이상이어야 합니다.\n"
	}
	runes := []rune(name)
	if krtext.IsAllHangulSyllables(name) {
		if len(runes) > krtext.LegacyNameMaxSyllables {
			return "이름이 너무 깁니다.\n\n"
		}
		return ""
	}
	if len(name) > 12 {
		return "이름이 너무 깁니다.\n\n"
	}
	return "이름은 한글로 적으셔야 합니다.\n\n"
}

func legacyYes(line string) bool {
	line = strings.TrimSpace(line)
	return line == "예" || strings.HasPrefix(line, "y") || strings.HasPrefix(line, "Y")
}

func legacyCreateClassChoice(line string) (int, bool) {
	value, ok := legacyCreateDigitChoice(line, 1, 8)
	if !ok {
		return 0, false
	}
	switch value {
	case 1:
		return legacyCreateClassAssassin, true
	case 2:
		return legacyCreateClassBarbarian, true
	case 3:
		return legacyCreateClassCleric, true
	case 4:
		return legacyCreateClassFighter, true
	case 5:
		return legacyCreateClassMage, true
	case 6:
		return legacyCreateClassPaladin, true
	case 7:
		return legacyCreateClassRanger, true
	case 8:
		return legacyCreateClassThief, true
	default:
		return 0, false
	}
}

func legacyCreateRaceChoice(line string) (int, bool) {
	value, ok := legacyCreateDigitChoice(line, 1, 8)
	if !ok {
		return 0, false
	}
	switch value {
	case 1:
		return legacyCreateRaceDwarf, true
	case 2:
		return legacyCreateRaceElf, true
	case 3:
		return legacyCreateRaceGnome, true
	case 4:
		return legacyCreateRaceHalfElf, true
	case 5:
		return legacyCreateRaceHalfGiant, true
	case 6:
		return legacyCreateRaceHobbit, true
	case 7:
		return legacyCreateRaceHuman, true
	case 8:
		return legacyCreateRaceOrc, true
	default:
		return 0, false
	}
}

func legacyCreateDigitChoice(line string, min int, max int) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	r := rune(line[0])
	if r < '0' || r > '9' {
		return 0, false
	}
	value := int(r - '0')
	return value, value >= min && value <= max
}

func legacyCreateStatChoices(line string) ([5]int, error) {
	var values [5]int
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return values, errors.New("5가지 능력치 모두를 위의 형식대로 적어 주십시요.")
	}
	sum := 0
	for i := 0; i < 5; i++ {
		value, err := strconv.Atoi(fields[i])
		if err != nil {
			value = 0
		}
		if value < 5 || value > 18 {
			return values, errors.New("각 능력치는 5이상 18이하로 설정해야 합니다.")
		}
		values[i] = value
		sum += value
	}
	if sum > 54 {
		return values, errors.New("각 능력치의 합이 54점을 초과할수 없습니다.")
	}
	return values, nil
}

func legacyApplyCreateRaceBonus(create *serverLoginCreateState) {
	if create == nil {
		return
	}
	switch create.race {
	case legacyCreateRaceDwarf:
		create.strength += 5
		create.piety--
		create.dexterity += 2
	case legacyCreateRaceElf:
		create.intelligence += 2
		create.constitution--
		create.strength--
	case legacyCreateRaceGnome:
		create.piety += 5
		create.strength--
	case legacyCreateRaceHalfElf:
		create.intelligence += 5
		create.constitution--
	case legacyCreateRaceHobbit:
		create.dexterity += 5
		create.strength--
		create.piety -= 2
	case legacyCreateRaceHuman:
		create.constitution--
		create.strength -= 3
	case legacyCreateRaceOrc:
		create.strength++
		create.constitution += 5
		create.dexterity += 3
		create.intelligence--
	case legacyCreateRaceHalfGiant:
		create.strength += 5
		create.intelligence -= 3
		create.piety--
	}
}

func legacyCreatedPlayerCharacter(create serverLoginCreateState, passwordHash string) (model.Player, model.Creature, error) {
	name := strings.TrimSpace(create.name)
	if name == "" {
		return model.Player{}, model.Creature{}, errors.New("create player: name is required")
	}
	stats, ok := legacyCreateClassStatTable[create.class]
	if !ok {
		return model.Player{}, model.Creature{}, fmt.Errorf("create player %q: invalid class %d", name, create.class)
	}
	if create.race == 0 {
		return model.Player{}, model.Creature{}, fmt.Errorf("create player %q: race is required", name)
	}
	if create.weapon < 0 || create.weapon >= len(legacyCreateProficiencyKeys) {
		return model.Player{}, model.Creature{}, fmt.Errorf("create player %q: weapon proficiency is required", name)
	}

	playerID := model.PlayerID(name)
	creatureID := model.CreatureID("creature:player:" + name)
	roomID := model.RoomID("room:00001")
	pDice := stats.pDice
	if pDice < 1 {
		pDice = 1
	}

	creatureStats := map[string]int{
		"class":         create.class,
		"race":          create.race,
		"level":         1,
		"strength":      create.strength,
		"dexterity":     create.dexterity,
		"constitution":  create.constitution,
		"intelligence":  create.intelligence,
		"piety":         create.piety,
		"hpMax":         stats.hpStart,
		"hpCurrent":     stats.hpStart,
		"mpMax":         stats.mpStart,
		"mpCurrent":     stats.mpStart,
		"nDice":         stats.nDice,
		"sDice":         stats.sDice,
		"pDice":         pDice,
		"gold":          1000,
		"PLECHO":        1,
		"PPROMP":        1,
		"PANSIC":        1,
		"PBRIGH":        1,
		"PNOEXT":        1,
		"PDSCRP":        1,
		"PNOSUM":        1,
		"experience":    0,
		"alignment":     0,
		"inventoryGold": 0,
	}
	if create.male {
		creatureStats["PMALES"] = 1
	}
	if create.chaos {
		creatureStats["PCHAOS"] = 1
	}
	creatureStats[legacyCreateProficiencyKeys[create.weapon]] = 1024
	creatureStats[fmt.Sprintf("proficiency/%d", create.weapon)] = 1024

	player := model.Player{
		ID:          playerID,
		DisplayName: name,
		CreatureID:  creatureID,
		RoomID:      roomID,
	}
	creature := model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: name,
		Level:       1,
		RoomID:      roomID,
		PlayerID:    playerID,
		Stats:       creatureStats,
		Properties:  map[string]string{"legacyPasswordHash": passwordHash},
		Metadata:    model.Metadata{Tags: []string{"PLECHO", "PPROMP", "PANSIC", "PBRIGH", "PNOEXT", "PDSCRP", "PNOSUM"}},
	}
	if create.male {
		creature.Metadata.Tags = append(creature.Metadata.Tags, "PMALES")
	}
	if create.chaos {
		creature.Metadata.Tags = append(creature.Metadata.Tags, "PCHAOS")
	}
	return player, creature, nil
}

func serverRemoteHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	raw := addr.String()
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return raw
	}
	return host
}

func legacyPasswordHash(world *state.World, player model.Player) string {
	if world == nil || player.CreatureID.IsZero() {
		return ""
	}
	creature, ok := world.Creature(player.CreatureID)
	if !ok {
		return ""
	}
	if hash := strings.TrimRight(strings.TrimSpace(creature.Properties["legacyPasswordHash"]), "\x00"); hash != "" {
		return hash
	}
	if raw := creature.Metadata.RawFields["creature.password"]; len(raw) != 0 {
		return strings.TrimRight(strings.TrimSpace(string(raw)), "\x00")
	}
	return ""
}

type serverPasswordWorld struct {
	*state.World
}

func (w serverPasswordWorld) SetCreatureProperty(creatureID model.CreatureID, key string, value string) (model.Creature, error) {
	if w.World == nil {
		return model.Creature{}, errors.New("password world is nil")
	}
	if key == "legacyPasswordHash" {
		return w.World.SetCreaturePasswordHash(creatureID, value)
	}
	return w.World.SetCreatureProperty(creatureID, key, value)
}

type serverPasswordSink struct {
	world *state.World
}

func (s serverPasswordSink) SavePassword(_ *enginecmd.Context, playerID model.PlayerID, _ string) error {
	if s.world == nil {
		return errors.New("password sink world is nil")
	}
	if _, ok := s.world.Player(playerID); !ok {
		return fmt.Errorf("save password: player %q not found", playerID)
	}
	s.world.QueueSave(playerID, "")
	return nil
}

type serverSuicideSink struct {
	world      *state.World
	root       string
	aliasStore interface {
		DeleteAliases(model.PlayerID) error
	}
	now  func() time.Time
	logf func(string, ...any)
}

type serverLowLevelQuitSink struct {
	world      *state.World
	root       string
	aliasStore interface {
		DeleteAliases(model.PlayerID) error
	}
}

func (s serverLowLevelQuitSink) CleanupLowLevelQuit(_ *enginecmd.Context, playerID model.PlayerID) error {
	if s.world == nil {
		return errors.New("low-level quit sink world is nil")
	}
	player, ok := s.world.Player(playerID)
	if !ok {
		return fmt.Errorf("low-level quit: player %q not found", playerID)
	}
	cleanup := serverSuicideSink{
		world: s.world,
		root:  s.root,
	}
	if s.aliasStore != nil {
		if err := s.aliasStore.DeleteAliases(playerID); err != nil {
			return err
		}
	}
	if err := cleanup.removePlayerFiles(playerID, player); err != nil {
		return err
	}
	if err := cleanup.removeBankFiles(playerID, player); err != nil {
		return err
	}
	return s.world.DustPlayer(playerID)
}

func (s serverSuicideSink) RequestSuicide(ctx *enginecmd.Context, playerID model.PlayerID) error {
	if s.world == nil {
		return errors.New("suicide sink world is nil")
	}
	now := s.now
	if now == nil {
		now = time.Now
	}
	player, creature, err := s.world.PreparePlayerSuicide(playerID, now().Unix())
	if err != nil {
		return err
	}
	playerName := serverSuicidePlayerName(playerID, player)
	if err := s.removeFamilyMember(playerName, creature); err != nil {
		return err
	}
	if s.aliasStore != nil {
		if err := s.aliasStore.DeleteAliases(playerID); err != nil {
			return err
		}
	}
	if err := s.removePlayerFiles(playerID, player); err != nil {
		return err
	}
	if err := s.removeBankFiles(playerID, player); err != nil {
		return err
	}
	s.broadcast(ctx, playerName)
	s.log(now(), playerName)
	if err := s.world.DustPlayer(playerID); err != nil {
		return err
	}
	return nil
}

func (s serverSuicideSink) removeFamilyMember(playerName string, creature model.Creature) error {
	if strings.TrimSpace(s.root) == "" || !serverCreatureFlag(creature, "familyFlag", "PFAMIL") {
		return nil
	}
	familyID, ok := serverCreatureInt(creature, "familyID", "dailyExpndMax", "legacyDailyExpndMax")
	if !ok || familyID <= 0 {
		return nil
	}
	familyName := ""
	if s.world != nil {
		familyName, _ = s.world.FamilyDisplayName(familyID)
	}
	members, err := game.PersistFamilyMemberLeave(s.root, familyID, familyName, playerName)
	if err != nil {
		return err
	}
	if s.world != nil {
		if err := s.world.UpdateFamilyMembers(familyID, members); err != nil {
			s.logfOrDefault("[SUICIDE] WARN update family %d members after %s leave failed: %v", familyID, playerName, err)
		}
	}
	return nil
}

func (s serverSuicideSink) removePlayerFiles(playerID model.PlayerID, player model.Player) error {
	root := strings.TrimSpace(s.root)
	if root == "" {
		return nil
	}
	var paths []string
	if rel := strings.TrimSpace(player.Metadata.LegacyPath); rel != "" {
		if path, ok := serverSafeRootPath(root, rel); ok {
			paths = append(paths, path)
		}
	}
	for _, name := range serverSuicidePlayerNames(playerID, player) {
		paths = append(paths,
			filepath.Join(root, "player", krtext.FirstHangulBucket(name), name),
			filepath.Join(root, "player", "json", name+".json"),
		)
	}
	return serverRemoveFiles(paths)
}

func (s serverSuicideSink) removeBankFiles(playerID model.PlayerID, player model.Player) error {
	root := strings.TrimSpace(s.root)
	if root == "" {
		return nil
	}
	names := serverSuicidePlayerNames(playerID, player)
	if s.world != nil {
		for _, bankID := range serverSuicideBankIDs(playerID, names) {
			if bank, ok := s.world.Bank(bankID); ok && strings.TrimSpace(bank.OwnerName) != "" {
				names = append(names, bank.OwnerName)
			}
		}
	}
	paths := make([]string, 0, len(names)*2)
	for _, name := range serverUniqueStrings(names) {
		paths = append(paths,
			filepath.Join(root, "player", "bank", name),
			filepath.Join(root, "player", "bank", "json", name+".json"),
		)
	}
	return serverRemoveFiles(paths)
}

func (s serverSuicideSink) broadcast(ctx *enginecmd.Context, playerName string) {
	if ctx == nil || ctx.Values == nil {
		return
	}
	broadcast, ok := ctx.Values[game.ContextBroadcastKey].(func(session.Command) error)
	if !ok || broadcast == nil {
		return
	}
	if err := broadcast(session.Command{Write: fmt.Sprintf("\n### %s님이 자살신청을 하였습니다.\n", playerName)}); err != nil {
		s.logfOrDefault("[SUICIDE] WARN broadcast failed for %s: %v", playerName, err)
	}
}

func (s serverSuicideSink) log(t time.Time, playerName string) {
	s.logfOrDefault("[SUICIDE] %s : %s님이 자살신청을 하였습니다.", t.Format(time.RFC3339), playerName)
}

func (s serverSuicideSink) logfOrDefault(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func serverSuicidePlayerName(playerID model.PlayerID, player model.Player) string {
	for _, name := range serverSuicidePlayerNames(playerID, player) {
		return name
	}
	return string(playerID)
}

func serverSuicidePlayerNames(playerID model.PlayerID, player model.Player) []string {
	return serverUniqueStrings([]string{
		player.DisplayName,
		player.AccountName,
		strings.TrimPrefix(string(player.ID), "player:"),
		strings.TrimPrefix(string(playerID), "player:"),
		string(player.ID),
		string(playerID),
	})
}

func serverSuicideBankIDs(playerID model.PlayerID, names []string) []model.BankID {
	ids := []string{"bank:player:" + string(playerID)}
	for _, name := range names {
		ids = append(ids, "bank:player:"+name)
	}
	unique := serverUniqueStrings(ids)
	out := make([]model.BankID, 0, len(unique))
	for _, id := range unique {
		out = append(out, model.BankID(id))
	}
	return out
}

func serverDeathFinalizerPlayerID(ctx *enginecmd.Context, attacker model.Creature) model.PlayerID {
	if !attacker.PlayerID.IsZero() {
		return attacker.PlayerID
	}
	if ctx == nil {
		return ""
	}
	return model.PlayerID(strings.TrimSpace(ctx.ActorID))
}

func serverMarkDeathRewardPlayerDirty(world *state.World, playerID model.PlayerID) {
	if world == nil || playerID.IsZero() {
		return
	}
	if _, ok := world.Player(playerID); !ok {
		return
	}
	world.MarkPlayerDirty(playerID)
	world.QueueSave(playerID, "")
}

func serverSafeRootPath(root, rel string) (string, bool) {
	root = strings.TrimSpace(root)
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if root == "" || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	full := filepath.Join(root, rel)
	absRoot, rootErr := filepath.Abs(root)
	absFull, fullErr := filepath.Abs(full)
	if rootErr != nil || fullErr != nil {
		return "", false
	}
	if absFull == absRoot || strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
		return full, true
	}
	return "", false
}

func serverRemoveFiles(paths []string) error {
	for _, path := range serverUniqueStrings(paths) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("refusing to remove directory %q", path)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func serverCreatureFlag(creature model.Creature, keys ...string) bool {
	for _, key := range keys {
		if creature.Stats != nil && creature.Stats[key] != 0 {
			return true
		}
		if creature.Properties != nil && serverTruthy(creature.Properties[key]) {
			return true
		}
		for _, tag := range creature.Metadata.Tags {
			if strings.EqualFold(tag, key) {
				return true
			}
		}
	}
	return false
}

func serverCreatureInt(creature model.Creature, keys ...string) (int, bool) {
	for _, key := range keys {
		if creature.Stats != nil {
			if value, ok := creature.Stats[key]; ok {
				return value, true
			}
		}
		if creature.Properties != nil {
			if value, err := strconv.Atoi(strings.TrimSpace(creature.Properties[key])); err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

func serverTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func serverUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func serverDispatcher(inputs runtimeInputs) enginecmd.Dispatcher {
	groupMemory := game.NewGroupMemory()
	handlers := commandHandlers(inputs, groupMemory)
	wrappedHandlers := make(map[string]enginecmd.Handler, len(handlers))
	for k, h := range handlers {
		hCopy := h
		wrappedHandlers[k] = func(ctx *enginecmd.Context, resolved enginecmd.ResolvedCommand) (enginecmd.Status, error) {
			if ctx != nil && ctx.Values != nil {
				ctx.Values["game.groupMemory"] = groupMemory
			}
			return hCopy(ctx, resolved)
		}
	}
	return enginecmd.Dispatcher{
		Registry:   inputs.registry,
		Handlers:   wrappedHandlers,
		Special:    enginecmd.NewSpecialHandler(inputs.world, inputs.summary.Root),
		AliasStore: enginecmd.NewFileAliasStore(inputs.summary.Root, inputs.world),
	}
}

func commandHandlers(inputs runtimeInputs, optionalGroupMemory ...*game.GroupMemory) map[string]enginecmd.Handler {
	world := inputs.world
	var groupMemory *game.GroupMemory
	if len(optionalGroupMemory) > 0 {
		groupMemory = optionalGroupMemory[0]
	} else {
		groupMemory = game.NewGroupMemory()
	}
	move := game.NewGroupMoveHandler(world, groupMemory, enginecmd.NewMoveHandler(world))
	tellMemory := game.NewTellMemory()
	ignoreMemory := game.NewIgnoreMemory()
	familyMembershipRequests := game.NewFamilyMembershipRequests()
	marriageRequests := game.NewMarriageRequests()
	voteMemory := game.NewVoteMemory()
	aliasStore := enginecmd.NewFileAliasStore(inputs.summary.Root, world)
	deathFinalizer := func(ctx *enginecmd.Context, attacker, victim model.Creature) error {
		killerPlayerID := serverDeathFinalizerPlayerID(ctx, attacker)
		if inputs.getLoop != nil {
			if activeLoop := inputs.getLoop(); activeLoop != nil {
				_, _ = game.HandlePermanentCreatureDeath(activeLoop, killerPlayerID, victim.ID, time.Now().Unix())
			}
		}
		var err error
		if ctx == nil || ctx.ActorID == "" {
			_, err = world.FinalizeMonsterDeath(victim.ID)
			return err
		}
		snapshot, ok := groupMemory.Snapshot(ctx.ActorID)
		if !ok {
			_, err = world.FinalizeMonsterDeath(victim.ID)
			serverMarkDeathRewardPlayerDirty(world, killerPlayerID)
			return err
		}
		leaderID, followerIDs, ok := snapshot.CreatureSnapshot(world)
		if !ok {
			_, err = world.FinalizeMonsterDeath(victim.ID)
			serverMarkDeathRewardPlayerDirty(world, killerPlayerID)
			return err
		}
		_, err = world.FinalizeMonsterDeathWithOptions(victim.ID, state.FinalizeMonsterDeathOptions{
			RewardGroup: state.MonsterDeathRewardGroup{
				LeaderID:    leaderID,
				FollowerIDs: followerIDs,
			},
		})
		// B/C: Mark+Queue after combat reward (FinalizeMonsterDeath may have mutated gold/exp)
		serverMarkDeathRewardPlayerDirty(world, killerPlayerID)
		return err
	}
	attack := enginecmd.NewAttackHandlerWithDeathFinalizer(world, deathFinalizer)

	handlers := map[string]enginecmd.Handler{
		"look": enginecmd.NewLookHandler(world),
		"go":   move,
		"move": move,
		"get":  enginecmd.NewGetHandler(world),
		"drop": enginecmd.NewDropHandler(world),
		"quit": enginecmd.NewQuitHandlerWithOptions(world,
			enginecmd.WithQuitLowLevelSink(serverLowLevelQuitSink{
				world:      world,
				root:       inputs.summary.Root,
				aliasStore: aliasStore,
			}),
		),
		"inventory":          enginecmd.NewInventoryHandler(world),
		"wear":               enginecmd.NewWearHandler(world),
		"remove_obj":         enginecmd.NewRemoveObjectHandler(world),
		"equipment":          enginecmd.NewEquipmentHandler(world),
		"hold":               enginecmd.NewHoldHandler(world),
		"ready":              enginecmd.NewReadyHandler(world),
		"savegame":           enginecmd.NewSaveGameHandler(),
		"set_title":          enginecmd.NewSetTitleHandler(inputs.summary.Root, world),
		"clear_title":        enginecmd.NewClearTitleHandler(inputs.summary.Root, world),
		"info_obj":           enginecmd.NewAppraiseHandler(world),
		"obj_compare":        enginecmd.NewObjectCompareHandler(world),
		"health":             enginecmd.NewHealthHandler(world),
		"info":               enginecmd.NewInfoHandler(world),
		"where":              enginecmd.NewWhereHandler(world),
		"effect_flag_list":   enginecmd.NewEffectStatusHandler(world),
		"cast":               enginecmd.NewCastHandler(world, nil),
		"study":              enginecmd.NewStudyHandler(world),
		"train":              enginecmd.NewTrainHandler(world),
		"invince_train":      enginecmd.NewInvinceTrainHandler(world),
		"list":               enginecmd.NewShopListHandler(world),
		"buy":                enginecmd.NewShopBuyHandler(world),
		"sell":               enginecmd.NewShopSellHandler(world),
		"value":              enginecmd.NewShopValueHandler(world),
		"repair":             enginecmd.NewRepairHandler(world, nil),
		"drink":              enginecmd.NewDrinkHandler(world, nil),
		"zap":                enginecmd.NewZapHandler(world, nil),
		"use":                enginecmd.NewUseHandlerWithRoot(world, inputs.summary.Root, nil),
		"bank_inv":           enginecmd.NewBankInventoryHandler(world),
		"bank":               enginecmd.NewBankBalanceHandler(world),
		"deposit":            enginecmd.NewBankDepositHandler(world),
		"withdraw":           enginecmd.NewBankWithdrawHandler(world),
		"output_bank":        enginecmd.NewBankOutputHandler(world),
		"peek":               enginecmd.NewPeekHandler(world, nil),
		"track":              enginecmd.NewTrackHandler(world),
		"attack":             attack,
		"kick":               enginecmd.NewKickHandlerWithDeathFinalizer(world, deathFinalizer),
		"poison_mon":         enginecmd.NewPoisonMonHandlerWithDeathFinalizer(world, deathFinalizer),
		"up_dmg":             enginecmd.NewUpDamageHandler(world, nil),
		"magic_stop":         enginecmd.NewMagicStopHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"power":              enginecmd.NewPowerHandler(world, nil),
		"accurate":           enginecmd.NewAccurateHandler(world, nil),
		"absorb":             enginecmd.NewAbsorbHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"backstab":           enginecmd.NewBackstabHandlerWithDeathFinalizer(world, deathFinalizer),
		"bash":               enginecmd.NewBashHandlerWithDeathFinalizer(world, deathFinalizer),
		"circle":             enginecmd.NewCircleHandler(world),
		"invincible_kick":    enginecmd.NewInvincibleKickHandlerWithDeathFinalizer(world, deathFinalizer),
		"one_kill":           enginecmd.NewOneKillHandlerWithDeathFinalizer(world, deathFinalizer),
		"scratch":            enginecmd.NewScratchHandler(world),
		"eight":              enginecmd.NewEightHandlerWithDeathFinalizer(world, deathFinalizer),
		"nahan":              enginecmd.NewNahanHandlerWithDeathFinalizer(world, deathFinalizer),
		"red_eye":            enginecmd.NewRedEyeHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"thief_stat":         enginecmd.NewThiefStatHandler(world, nil),
		"poback":             enginecmd.NewPobackHandlerWithDeathFinalizer(world, deathFinalizer),
		"bnahan":             enginecmd.NewBnahanHandlerWithDeathFinalizer(world, deathFinalizer),
		"tagu":               enginecmd.NewTaguHandlerWithDeathFinalizer(world, deathFinalizer),
		"reflect":            enginecmd.NewReflectHandler(world, nil),
		"shadow":             enginecmd.NewShadowHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"chang":              enginecmd.NewChangHandlerWithDeathFinalizer(world, deathFinalizer),
		"sasal":              enginecmd.NewSasalHandlerWithDeathFinalizer(world, deathFinalizer),
		"rm_blind2":          enginecmd.NewRmBlind2Handler(world),
		"choi":               enginecmd.NewChoiHandlerWithDeathFinalizer(world, deathFinalizer),
		"turn":               enginecmd.NewTurnHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"teach":              enginecmd.NewTeachHandler(world),
		"steal":              enginecmd.NewStealHandler(world, nil),
		"search":             enginecmd.NewSearchHandler(world, nil),
		"hide":               enginecmd.NewHideHandler(world, nil),
		"set":                enginecmd.NewSetHandler(world),
		"clear":              enginecmd.NewClearHandler(world),
		"openexit":           enginecmd.NewOpenExitHandler(world),
		"closeexit":          enginecmd.NewCloseExitHandler(world),
		"unlock":             enginecmd.NewUnlockExitHandler(world),
		"lock":               enginecmd.NewLockExitHandler(world),
		"picklock":           enginecmd.NewPicklockHandler(world, nil),
		"flee":               enginecmd.NewFleeHandler(world, nil),
		"prt_time":           enginecmd.NewTimeHandlerWithWorld(world, nil),
		"prepare":            enginecmd.NewPrepareHandler(world),
		"guard":              enginecmd.NewGuardHandler(world),
		"meditate":           enginecmd.NewMeditateHandler(world, nil),
		"lion_scream":        enginecmd.NewLionScreamHandlerWithDeathFinalizer(world, deathFinalizer),
		"haste":              enginecmd.NewHasteHandler(world, nil),
		"pray":               enginecmd.NewPrayHandler(world, nil),
		"angel":              enginecmd.NewAngelHandler(world, nil),
		"return_square":      enginecmd.NewReturnSquareHandler(world),
		"who":                game.NewWhoHandler(world),
		"whois":              game.NewWhoisHandler(world),
		"pfinger":            game.NewPfingerHandler(world, inputs.summary.Root),
		"follow":             game.NewFollowHandler(world, groupMemory),
		"lose":               game.NewLoseHandler(world, groupMemory),
		"group":              game.NewGroupHandler(world, groupMemory),
		"gtalk":              game.NewGroupTalkHandler(world, groupMemory),
		"family_who":         game.NewFamilyWhoHandler(world),
		"family_talk":        game.NewFamilyTalkHandler(world),
		"family_news":        game.NewFamilyNewsHandler(world, inputs.summary.Root),
		"family":             game.NewFamilyJoinHandler(world, familyMembershipRequests),
		"boss_family":        game.NewFamilyJoinApproveHandler(world, inputs.summary.Root, familyMembershipRequests),
		"fm_dis":             game.NewFamilyJoinCancelHandler(world, familyMembershipRequests),
		"out_family":         game.NewFamilyLeaveHandler(world, inputs.summary.Root, familyMembershipRequests),
		"fm_out":             game.NewFamilyKickHandler(world, inputs.summary.Root, familyMembershipRequests),
		"invite":             game.NewInviteHandler(world),
		"marriage":           game.NewMarriageHandler(world, marriageRequests, inputs.summary.Root),
		"family_member":      game.NewFamilyMemberHandler(world),
		"list_family":        game.NewListFamilyHandler(world),
		"family_bank_inv":    game.NewFamilyBankInventoryHandler(world),
		"input_family_bank":  game.NewFamilyBankInventoryHandler(world),
		"output_family_bank": game.NewFamilyBankOutputHandler(world),
		"family_deposit":     game.NewFamilyDepositHandler(world),
		"family_withdraw":    game.NewFamilyWithdrawHandler(world),
		"call_war":           game.NewCallWarHandler(world),
		"give":               game.NewGiveHandler(world),
		"memo":               game.NewMemoHandler(world, inputs.summary.Root),
		"vote":               game.NewVoteHandler(world, voteMemory, inputs.summary.Root),
		"talk":               game.NewTalkHandlerWithRoot(world, inputs.summary.Root),
		"ignore":             game.NewIgnoreHandler(world, ignoreMemory),
		"send":               game.NewTellHandler(world, tellMemory, ignoreMemory),
		"resend":             game.NewReplyHandler(world, tellMemory, ignoreMemory),
		"say":                game.NewSayHandler(world),
		"broadsend":          game.NewBroadcastChatHandler(world),
		"broadsend2":         game.NewCheerHandler(world),
		"action":             game.NewActionHandler(world),
		"emote":              game.NewEmoteHandler(world),
		"yell":               game.NewYellHandler(world),
		"help":               enginecmd.NewHelpHandler(inputs.summary.Root, inputs.registry),
		"welcome":            enginecmd.NewWelcomeHandler(inputs.summary.Root),
		"look_board":         enginecmd.NewBoardLookHandler(world, inputs.summary.Root),
		"readscroll":         enginecmd.NewReadScrollHandler(world, inputs.summary.Root, nil),
		"writeboard":         enginecmd.NewBoardWriteHandler(world, inputs.summary.Root),
		"del_board":          enginecmd.NewBoardDeleteHandler(world, inputs.summary.Root),
		"postsend":           enginecmd.NewPostSendHandler(world, inputs.summary.Root),
		"postread":           enginecmd.NewPostReadHandler(world, inputs.summary.Root),
		"postdelete":         enginecmd.NewPostDeleteHandler(world, inputs.summary.Root),
		"trade":              enginecmd.NewTradeHandler(world),
		"trans_exp":          enginecmd.NewTransExpHandler(world),
		"m_send":             enginecmd.NewMarriageSendHandler(&marriageSendWorldWrapper{World: world, getLoop: inputs.getLoop}, inputs.summary.Root),
	}

	// 16차 포팅 추가 핸들러 등록
	handlers["description"] = enginecmd.NewDescriptionHandler(world)
	handlers["passwd"] = enginecmd.NewPasswdHandler(serverPasswordWorld{World: world}, enginecmd.WithPasswordSink(serverPasswordSink{world: world}))
	handlers["ply_alias"] = enginecmd.NewPlyAliasesHandler(aliasStore)
	handlers["suicide"] = enginecmd.NewPlySuicideHandler(world,
		enginecmd.WithSuicideSink(serverSuicideSink{world: world, root: inputs.summary.Root, aliasStore: aliasStore}),
		enginecmd.WithSuicideAliasStore(aliasStore),
	)
	handlers["burn"] = enginecmd.NewBurnHandlerWithRoot(world, inputs.summary.Root)
	handlers["purchase"] = enginecmd.NewMonsterPurchaseHandler(world)
	handlers["selection"] = enginecmd.NewMonsterSelectionHandler(world)
	handlers["forge"] = enginecmd.NewForgeHandler(world, nil)
	handlers["newforge"] = enginecmd.NewNewForgeHandler(world, nil)
	handlers["change_class"] = enginecmd.NewChangeClassHandler(world)

	// 17차 포팅 추가 핸들러 등록
	handlers["buy_states"] = enginecmd.NewBuyStatesHandler(world, nil)
	handlers["chg_name"] = enginecmd.NewChangeNameHandler(world)
	handlers["pledge"] = enginecmd.NewPledgeHandler(world)
	handlers["rescind"] = enginecmd.NewRescindHandler(world)
	handlers["sneak"] = enginecmd.NewSneakHandler(world, nil)
	handlers["dm_reload_rom"] = enginecmd.NewDMReloadRomHandler(world)
	handlers["dm_loadlockout"] = enginecmd.NewDMLoadLockoutHandler(world)
	handlers["dm_resave"] = enginecmd.NewDMResaveHandler(world)
	handlers["dm_rmstat"] = enginecmd.NewDMRmstatHandler(world)
	handlers["dm_teleport"] = enginecmd.NewDMTeleportHandler(world)
	handlers["dm_stat"] = enginecmd.NewDMStatHandler(world)
	handlers["dm_create_obj"] = enginecmd.NewDMCreateObjHandler(world)
	handlers["dm_create_crt"] = enginecmd.NewDMCreateCrtHandler(world)
	handlers["dm_perm"] = enginecmd.NewDMPermHandler(world)
	handlers["dm_invis"] = enginecmd.NewDMInvisHandler(world)
	handlers["dm_ac"] = enginecmd.NewDMAcHandler(world)
	handlers["dm_send"] = enginecmd.NewDMSendHandler(world)
	handlers["dm_echo"] = enginecmd.NewDMEchoHandler(world)
	handlers["dm_broadecho"] = enginecmd.NewDMBroadechoHandler(world)
	handlers["dm_nameroom"] = enginecmd.NewDMNameroomHandler(world)
	handlers["dm_spy"] = enginecmd.NewDMSpyHandler(world)
	handlers["dm_purge"] = enginecmd.NewDMPurgeHandler(world)
	handlers["dm_users"] = enginecmd.NewDMUsersHandler(world)
	handlers["dm_flush_crtobj"] = enginecmd.NewDMFlushCrtObjHandler(world)
	handlers["dm_flushsave"] = enginecmd.NewDMFlushsaveHandler(world)
	handlers["dm_shutdown"] = enginecmd.NewDMShutdownHandler(world)
	handlers["dm_force"] = enginecmd.NewDMForceHandler(&forceWorldWrapper{World: world, getLoop: inputs.getLoop})
	handlers["dm_log"] = enginecmd.NewDMLogHandler(world)
	handlers["dm_help"] = enginecmd.NewDMHelpHandler(inputs.summary.Root, world)
	handlers["dm_add_rom"] = enginecmd.NewDMAddRomHandler(world)
	handlers["dm_set"] = enginecmd.NewDMSetHandler(world)
	handlers["dm_finger"] = enginecmd.NewDMFingerHandler(world)
	handlers["dm_replace"] = enginecmd.NewDMReplaceHandler(world)
	handlers["dm_list"] = enginecmd.NewDMListHandler(world)
	handlers["dm_info"] = enginecmd.NewDMInfoHandler(world)
	handlers["dm_param"] = enginecmd.NewDMParamHandler(world)
	handlers["dm_silence"] = enginecmd.NewDMSilenceHandler(world)
	handlers["dm_group"] = enginecmd.NewDMGroupHandler(world)
	handlers["list_act"] = enginecmd.NewListActHandler(world)
	handlers["dm_save_all_ply"] = enginecmd.NewDMSaveAllPlyHandler(world)
	handlers["dm_append"] = enginecmd.NewDMAppendHandler(world)
	handlers["dm_prepend"] = enginecmd.NewDMPrependHandler(world)
	handlers["dm_cast"] = enginecmd.NewDMCastHandler(world)
	handlers["notepad"] = enginecmd.NewNotepadHandler(world, inputs.summary.Root)
	handlers["dm_delete"] = enginecmd.NewDMDeleteHandler(world)
	handlers["dm_obj_name"] = enginecmd.NewDMObjNameHandler(world)
	handlers["dm_crt_name"] = enginecmd.NewDMCrtNameHandler(world)
	handlers["dm_dust"] = enginecmd.NewDMDustHandler(world)
	handlers["dm_follow"] = enginecmd.NewDMFollowHandler(world)
	handlers["dm_attack"] = enginecmd.NewDMAttackHandler(world)
	handlers["list_enm"] = enginecmd.NewListEnmHandler(world)
	handlers["list_charm"] = enginecmd.NewListCharmHandler(world)

	// C 레거시 매핑 호환성 수정
	handlers["ply_aliases"] = handlers["ply_alias"]
	handlers["ply_suicide"] = handlers["suicide"]

	// DM Placeholder 핸들러 등록
	for k, h := range enginecmd.NewDMPlaceholderHandlers(world) {
		if _, exists := handlers[k]; !exists {
			handlers[k] = h
		}
	}

	return handlers
}

type marriageSendWorldWrapper struct {
	*state.World
	getLoop func() *game.Loop
}

type forceWorldWrapper struct {
	*state.World
	getLoop func() *game.Loop
}

func (w *forceWorldWrapper) ForcePlayerCommand(playerID model.PlayerID, cmd string) error {
	if w == nil || w.getLoop == nil {
		return fmt.Errorf("force player %s: game loop is not available", playerID)
	}
	loop := w.getLoop()
	if loop == nil {
		return fmt.Errorf("force player %s: game loop is not available", playerID)
	}
	for _, active := range loop.ActiveSessions() {
		if strings.EqualFold(active.ActorID, string(playerID)) {
			if loop.HasPendingLineHandler(active.ID) {
				return fmt.Errorf("force player %s: command input unavailable", playerID)
			}
			return loop.DispatchCommand(active.ID, playerID, cmd)
		}
	}
	return fmt.Errorf("force player %s: active session not found", playerID)
}

func (w *forceWorldWrapper) CanForcePlayerCommand(playerID model.PlayerID) bool {
	if w == nil || w.getLoop == nil {
		return false
	}
	loop := w.getLoop()
	if loop == nil {
		return false
	}
	for _, active := range loop.ActiveSessions() {
		if strings.EqualFold(active.ActorID, string(playerID)) {
			return !loop.HasPendingLineHandler(active.ID)
		}
	}
	return false
}

func (w *marriageSendWorldWrapper) ActiveSessions() []string {
	if w.getLoop == nil {
		return nil
	}
	loop := w.getLoop()
	if loop == nil {
		return nil
	}
	active := loop.ActiveSessions()
	ids := make([]string, len(active))
	for i, s := range active {
		ids[i] = string(s.ID)
	}
	return ids
}

func (w *marriageSendWorldWrapper) SessionActor(id string) (string, bool) {
	if w.getLoop == nil {
		return "", false
	}
	loop := w.getLoop()
	if loop == nil {
		return "", false
	}
	for _, s := range loop.ActiveSessions() {
		if string(s.ID) == id {
			return s.ActorID, true
		}
	}
	return "", false
}

func writeSummary(w io.Writer, cfg config, inputs runtimeInputs) {
	counts := inputs.summary.Counts

	fmt.Fprintf(w, "root: %s\n", inputs.summary.Root)
	fmt.Fprintf(w, "listen: %s\n", cfg.listen)
	if cfg.actor != "" {
		fmt.Fprintf(w, "actor: %s\n", cfg.actor)
	}
	fmt.Fprintf(w, "registry: %d commands\n", inputs.registryCommandCount)
	fmt.Fprintf(w, "world: %d rooms, %d players, %d creatures, %d objects, %d object prototypes\n",
		counts.Rooms,
		counts.Players,
		counts.Creatures,
		counts.ObjectInstances,
		counts.ObjectPrototypes,
	)
	fmt.Fprintf(w, "findings: %d warnings, %d errors\n", counts.Warnings, counts.Errors)
	if inputs.world == nil {
		fmt.Fprintln(w, "runtime world: missing")
		return
	}
	fmt.Fprintln(w, "runtime world: initialized")
}

func startWebSocketProxy(wsListen string, tcpAddr string, stdout io.Writer) {
	fmt.Fprintf(stdout, "websocket proxy listening: ws://%s -> tcp://%s\n", wsListen, tcpAddr)
	httpServer := &http.Server{
		Addr: wsListen,
		Handler: websocket.Handler(func(ws *websocket.Conn) {
			defer ws.Close()
			tcpConn, err := net.Dial("tcp", tcpAddr)
			if err != nil {
				log.Printf("WS Proxy dial error: %v", err)
				return
			}
			defer tcpConn.Close()

			errCh := make(chan error, 2)
			go func() {
				_, err := io.Copy(ws, tcpConn)
				errCh <- err
			}()
			go func() {
				_, err := io.Copy(tcpConn, ws)
				errCh <- err
			}()

			<-errCh
		}),
	}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WS Proxy HTTP server error: %v", err)
		}
	}()
}
