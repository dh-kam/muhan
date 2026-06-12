package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"muhan/internal/commandspec"
	enginecmd "muhan/internal/engine/command"
	"muhan/internal/engine/command/table"
	"muhan/internal/engine/game"
	"muhan/internal/session"
	"muhan/internal/world/load"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

type runtimeInputs struct {
	summary              load.Summary
	registryCommandCount int
	registry             commandspec.Registry
	world                *state.World
	getLoop              func() *game.Loop
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

	if cfg.showVersion {
		fmt.Fprintf(stdout, "muhan-server %s (commit=%s, built=%s)\n", version, commit, buildDate)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	slog.SetDefault(logger)

	if cfg.metricsListen != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			slog.Info(fmt.Sprintf("Prometheus metrics listening on %s", cfg.metricsListen))
			if err := http.ListenAndServe(cfg.metricsListen, mux); err != nil {
				slog.Error(fmt.Sprintf("Prometheus metrics server failed: %v", err))
			}
		}()
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
				slog.Error(fmt.Sprintf("[PERSIST] ERROR defer shutdown FlushActivePlayersAndBanks: %v", err))
			} else {
				slog.Info("[PERSIST] INFO defer shutdown full flush complete (players+banks+rooms)")
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
		slog.Warn(fmt.Sprintf("[SECURITY] WARN load lockouts failed: %v", err))
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
					slog.Warn(fmt.Sprintf("[PERSIST] WARN MergePlayerSave %s failed: %v", pid, err))
				}
			} else if err != nil {
				slog.Warn(fmt.Sprintf("[PERSIST] WARN LoadPlayer %s failed: %v", pid, err))
			}
			bankID := model.BankID("bank:player:" + string(pid))
			if bundle, ok, err := world.LoadBank(bankID); err == nil && ok {
				if err := world.MergeBankSave(bundle); err == nil {
					restoredBanks++
				} else {
					slog.Warn(fmt.Sprintf("[PERSIST] WARN MergeBankSave %s failed: %v", bankID, err))
				}
			} else if err != nil && !os.IsNotExist(err) {
				slog.Warn(fmt.Sprintf("[PERSIST] WARN LoadBank %s failed: %v", bankID, err))
			}
		}
		restoredBanks += restoreFamilyBankSidecars(absRoot, world)
		if restoredPlayers > 0 || restoredBanks > 0 {
			slog.Info(fmt.Sprintf("[PERSIST] INFO startup restore: %d players + %d banks from JSON sidecars", restoredPlayers, restoredBanks))
		} else {
			slog.Info("[PERSIST] INFO startup: no prior player/bank JSON sidecars found (fresh or legacy DB)")
		}

		// D: Restore room floor objects (dropped items, corpses, ground money etc.)
		restoredRooms := 0
		for rid := range summary.World.Rooms {
			if saved, ok, err := state.LoadRoomObjects(absRoot, rid); err == nil && ok {
				if err := world.MergeRoomObjectsSaveIntoWorld(saved); err == nil {
					restoredRooms++
				} else {
					slog.Warn(fmt.Sprintf("[PERSIST] WARN MergeRoomObjects %s failed: %v", rid, err))
				}
			} else if err != nil && !os.IsNotExist(err) {
				slog.Warn(fmt.Sprintf("[PERSIST] WARN LoadRoomObjects %s failed: %v", rid, err))
			}
		}
		if restoredRooms > 0 {
			slog.Info(fmt.Sprintf("[PERSIST] INFO startup restore: %d rooms with floor objects from sidecars", restoredRooms))
		}

		// C: Restore board posts + family news runtime sidecars (Package C complete)
		restoredBoards := 0
		knownBoardDirs := []string{"info", "notice", "user", "family", "family1", "family2", "family3", "family4"}
		for _, bdir := range knownBoardDirs {
			if saved, ok, err := state.LoadBoardPosts(absRoot, bdir); err == nil && ok {
				if err := world.MergeBoardPostsSaveIntoWorld(saved); err == nil {
					restoredBoards++
				} else {
					slog.Warn(fmt.Sprintf("[PERSIST] WARN MergeBoardPosts %s failed: %v", bdir, err))
				}
			} else if err != nil && !os.IsNotExist(err) {
				slog.Warn(fmt.Sprintf("[PERSIST] WARN LoadBoardPosts %s failed: %v", bdir, err))
			}
		}
		restoredFamNews := 0
		for fid := 1; fid <= 16; fid++ {
			if saved, ok, err := state.LoadFamilyNews(absRoot, fid); err == nil && ok {
				if err := world.MergeFamilyNewsSaveIntoWorld(saved); err == nil {
					restoredFamNews++
				} else {
					slog.Warn(fmt.Sprintf("[PERSIST] WARN MergeFamilyNews %d failed: %v", fid, err))
				}
			}
		}
		if restoredBoards > 0 || restoredFamNews > 0 {
			slog.Info(fmt.Sprintf("[PERSIST] INFO startup restore C: %d boards + %d family news sidecars", restoredBoards, restoredFamNews))
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
			slog.Warn(fmt.Sprintf("[PERSIST] WARN scan family bank sidecars failed: %v", err))
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
			slog.Warn(fmt.Sprintf("[PERSIST] WARN LoadFamilyBank %s failed: %v", bankID, err))
			continue
		}
		if !ok || !strings.HasPrefix(string(bundle.BankAccount.ID), "bank:family:") {
			continue
		}
		if err := world.MergeBankSave(bundle); err != nil {
			slog.Warn(fmt.Sprintf("[PERSIST] WARN MergeFamilyBank %s failed: %v", bundle.BankAccount.ID, err))
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
	// S3: 중복 로그인 감지를 위해 로그인 매니저에도 Loop 참조 연결
	login.getLoop = inputs.getLoop
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
		slog.Info(fmt.Sprintf("[PERSIST] INFO received OS signal %v: initiating graceful full flush + shutdown", sig))
		if inputs.world != nil {
			if err := inputs.world.FlushActivePlayersAndBanks(); err != nil {
				slog.Error(fmt.Sprintf("[PERSIST] ERROR signal shutdown FlushActivePlayersAndBanks: %v", err))
			} else {
				slog.Info("[PERSIST] INFO signal shutdown full flush complete (players+banks+rooms)")
			}
		}
		// Clean stop: close listener -> Accept errs -> serve returns -> defers run -> clean exit
		_ = listener.Close()
	}()

	// C3: 동시 접속 수 제한
	const maxTotalConns = 256
	const maxIPConns = 5
	var activeConns int64
	var ipConnsMu sync.Mutex
	ipConns := map[string]int{}

	var nextID uint64
	for {
		select {
		case err := <-loopErr:
			// B: Flush persistence on loop exit (crash or shutdown) - single reliable path
			if inputs.world != nil {
				if ferr := inputs.world.FlushActivePlayersAndBanks(); ferr != nil {
					slog.Error(fmt.Sprintf("[PERSIST] ERROR loopErr shutdown FlushActivePlayersAndBanks: %v", ferr))
				} else {
					slog.Info("[PERSIST] INFO loopErr shutdown full flush complete (players+banks+rooms)")
				}
			}
			return err
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			return err
		}

		remoteIP := serverRemoteHost(conn.RemoteAddr())

		// C3: 전체 동시 접속 수 확인
		if atomic.LoadInt64(&activeConns) >= maxTotalConns {
			_, _ = io.WriteString(conn, "\r\n서버가 만원입니다. 잠시 후 다시 접속해 주세요.\r\n")
			_ = conn.Close()
			fmt.Fprintf(stdout, "rejected: max connections %s\n", remoteIP)
			continue
		}

		// C3: IP별 동시 접속 수 확인
		ipConnsMu.Lock()
		if ipConns[remoteIP] >= maxIPConns {
			ipConnsMu.Unlock()
			_, _ = io.WriteString(conn, "\r\n같은 주소에서 너무 많은 접속이 있습니다.\r\n")
			_ = conn.Close()
			fmt.Fprintf(stdout, "rejected: per-ip limit %s\n", remoteIP)
			continue
		}
		ipConns[remoteIP]++
		ipConnsMu.Unlock()
		atomic.AddInt64(&activeConns, 1)

		lockoutMode, sitePassword := state.LockoutAllow, ""
		if inputs.world != nil {
			lockoutMode, sitePassword = inputs.world.CheckLockout(remoteIP)
		}
		if lockoutMode == state.LockoutDeny {
			_, _ = io.WriteString(conn, "\r\nYour site is locked out.\r\n")
			_ = conn.Close()
			atomic.AddInt64(&activeConns, -1)
			ipConnsMu.Lock()
			ipConns[remoteIP]--
			if ipConns[remoteIP] <= 0 {
				delete(ipConns, remoteIP)
			}
			ipConnsMu.Unlock()
			fmt.Fprintf(stdout, "rejected: lockout %s\n", remoteIP)
			continue
		}

		id := session.ID(fmt.Sprintf("s%d", atomic.AddUint64(&nextID, 1)))
		commands := make(chan session.Command, 16)
		if err := loop.RegisterSession(id, commands, actorID); err != nil {
			_ = conn.Close()
			atomic.AddInt64(&activeConns, -1)
			ipConnsMu.Lock()
			ipConns[remoteIP]--
			if ipConns[remoteIP] <= 0 {
				delete(ipConns, remoteIP)
			}
			ipConnsMu.Unlock()
			return err
		}

		s, err := session.New(id, conn, events, commands)
		if err != nil {
			loop.UnregisterSession(id)
			_ = conn.Close()
			atomic.AddInt64(&activeConns, -1)
			ipConnsMu.Lock()
			ipConns[remoteIP]--
			if ipConns[remoteIP] <= 0 {
				delete(ipConns, remoteIP)
			}
			ipConnsMu.Unlock()
			return err
		}

		fmt.Fprintf(stdout, "accepted: %s %s\n", id, conn.RemoteAddr())
		if actorID == "" {
			if lockoutMode == state.LockoutPassword {
				login.StartWithSitePassword(id, sitePassword, remoteIP)
				commands <- session.Command{Write: "\nA password is required to play from that site.\nPlease enter site password: "}
			} else {
				login.Start(id, remoteIP)
				commands <- session.Command{Write: loginNamePrompt}
			}
		} else {
			commands <- session.Command{Write: "무한에 접속했습니다.\n", Prompt: "> "}
		}
		connRemoteIP := remoteIP // capture for goroutine
		go func() {
			defer func() {
				// C3: 세션 종료 시 카운터 감소
				atomic.AddInt64(&activeConns, -1)
				ipConnsMu.Lock()
				ipConns[connRemoteIP]--
				if ipConns[connRemoteIP] <= 0 {
					delete(ipConns, connRemoteIP)
				}
				ipConnsMu.Unlock()
			}()
			if err := s.Run(ctx); err != nil {
				fmt.Fprintf(stdout, "session %s ended: %v\n", id, err)
			}
		}()
	}
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
