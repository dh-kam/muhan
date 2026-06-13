package game

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	enginecmd "github.com/0xc0de1ab/muhan/internal/engine/command"
	"github.com/0xc0de1ab/muhan/internal/metrics"
	"github.com/0xc0de1ab/muhan/internal/persist/legacykr"
	"github.com/0xc0de1ab/muhan/internal/session"
	"github.com/0xc0de1ab/muhan/internal/world/model"
	"github.com/0xc0de1ab/muhan/internal/world/state"
)

var (
	ErrSessionNotFound = errors.New("game: session not found")
	ErrSessionExists   = errors.New("game: session already registered")
)

type PromptFunc func(session.ID, *enginecmd.Context, enginecmd.Status) string

type ErrorFormatter func(error) string

type PromptPolicy func(enginecmd.Status, error) bool

type UnauthenticatedLineHandler func(context.Context, session.ID, string) (UnauthenticatedLineResult, error)

type UnauthenticatedLineResult struct {
	ActorID string
	Command session.Command
	Pending enginecmd.PendingLineHandler
}

type RoomBroadcastWorld interface {
	Player(model.PlayerID) (model.Player, bool)
}

type Option func(*Loop)

type Loop struct {
	dispatcher enginecmd.Dispatcher
	prompt     PromptFunc
	promptFor  PromptPolicy
	formatErr  ErrorFormatter
	unauth     UnauthenticatedLineHandler
	values     map[string]any
	roomWorld  RoomBroadcastWorld
	world      *state.World

	mu            sync.RWMutex
	sessions      map[session.ID]binding
	pending       map[session.ID]enginecmd.PendingLineHandler
	lastInputTime map[session.ID]int64
	lastCommand   map[session.ID]string
	activeEffects map[model.CreatureID]map[string]int64
}

type binding struct {
	commands chan<- session.Command
	actorID  string
}

func NewLoop(dispatcher enginecmd.Dispatcher, opts ...Option) *Loop {
	l := &Loop{
		dispatcher:    dispatcher,
		sessions:      map[session.ID]binding{},
		pending:       map[session.ID]enginecmd.PendingLineHandler{},
		lastInputTime: map[session.ID]int64{},
		lastCommand:   map[session.ID]string{},
		activeEffects: map[model.CreatureID]map[string]int64{},
		formatErr: func(err error) string {
			if err == nil {
				return ""
			}
			return err.Error() + "\n"
		},
		promptFor: func(status enginecmd.Status, err error) bool {
			return status == enginecmd.StatusPrompt
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(l)
		}
	}
	return l
}

func WithPrompt(prompt PromptFunc) Option {
	return func(l *Loop) {
		l.prompt = prompt
	}
}

func WithErrorFormatter(format ErrorFormatter) Option {
	return func(l *Loop) {
		l.formatErr = format
	}
}

func WithPromptPolicy(policy PromptPolicy) Option {
	return func(l *Loop) {
		l.promptFor = policy
	}
}

func WithUnauthenticatedLineHandler(handler UnauthenticatedLineHandler) Option {
	return func(l *Loop) {
		l.unauth = handler
	}
}

func WithCommandContextValues(values map[string]any) Option {
	return func(l *Loop) {
		if len(values) == 0 {
			return
		}
		if l.values == nil {
			l.values = map[string]any{}
		}
		for key, value := range values {
			l.values[key] = value
		}
	}
}

func WithRoomBroadcastWorld(world RoomBroadcastWorld) Option {
	return func(l *Loop) {
		l.roomWorld = world
	}
}

func WithWorld(world *state.World) Option {
	return func(l *Loop) {
		l.world = world
		if world != nil {
			if world.RecalculateACFunc == nil {
				world.RecalculateACFunc = func(creatureID model.CreatureID) error {
					return l.RecalculateAC(creatureID)
				}
			}
			if world.RecalculateTHACOFunc == nil {
				world.RecalculateTHACOFunc = func(creatureID model.CreatureID) error {
					return l.RecalculateTHACO(creatureID)
				}
			}
			if world.UpdatePlayerStatusesFunc == nil {
				world.UpdatePlayerStatusesFunc = func(t int64) error {
					UpdatePlayerStatuses(l, t)
					return nil
				}
			}
			if world.UpdateActiveMonstersFunc == nil {
				world.UpdateActiveMonstersFunc = func(t int64) error {
					UpdateActiveMonsters(&loopUpdateActiveWorld{l: l, w: world}, t)
					return nil
				}
			}
			if world.UpdateRandomSpawnsFunc == nil {
				world.UpdateRandomSpawnsFunc = func(t int64) error {
					UpdateRandomSpawns(world, t)
					return nil
				}
			}
			if world.UpdateTimeClockFunc == nil {
				world.UpdateTimeClockFunc = func(t int64) error {
					UpdateTimeClock(world, t)
					return nil
				}
			}
			if world.UpdateShutdownFunc == nil {
				world.UpdateShutdownFunc = func(t int64) error {
					UpdateShutdown(&loopShutdownWorld{l: l, w: world}, t)
					return nil
				}
			}
		}
	}
}

func (l *Loop) RegisterSession(id session.ID, commands chan<- session.Command, actorID string) error {
	if l == nil {
		return errors.New("game: nil loop")
	}
	if id == "" {
		return errors.New("game: session id is required")
	}
	if commands == nil {
		return errors.New("game: command channel is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.sessions[id]; ok {
		return fmt.Errorf("%w %q", ErrSessionExists, id)
	}
	l.sessions[id] = binding{commands: commands, actorID: actorID}
	l.lastInputTime[id] = time.Now().Unix()
	metrics.ActiveSessions.Inc()
	return nil
}

func (l *Loop) BindActor(id session.ID, actorID string) error {
	if l == nil {
		return errors.New("game: nil loop")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.sessions[id]
	if !ok {
		return fmt.Errorf("%w %q", ErrSessionNotFound, id)
	}
	b.actorID = actorID
	l.sessions[id] = b
	return nil
}

func (l *Loop) UnregisterSession(id session.ID) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessions, id)
	delete(l.pending, id)
	delete(l.lastInputTime, id)
	delete(l.lastCommand, id)
	metrics.ActiveSessions.Dec()
}

func (l *Loop) Run(ctx context.Context, events <-chan session.Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			l.Tick()
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := l.HandleEvent(ctx, event); err != nil {
				log.Printf("[LOOP] ERROR HandleEvent session=%s kind=%s: %v", event.SessionID, event.Kind, err)
			}
		}
	}
}

func (l *Loop) Tick() {
	l.TickAt(time.Now().Unix())
}

func (l *Loop) TickAt(t int64) {
	// Delegate ALL periodic world updates (active 1s, player 20s, random ~20s,
	// time 150s, exits 3600s, shutdown 30s) to TickWorld / the 6 Func hooks.
	// This ensures exact C update.c cadences (update_game -> update_users@20s etc).
	// Previously an unconditional UpdatePlayerStatuses here caused light decay,
	// wimpy checks and similar to run at 1Hz instead of 20s (fidelity bug for
	// object decay timing, poison/DoT, regen details).
	if l.world != nil {
		if err := l.world.TickWorld(t); err != nil {
			log.Printf("[LOOP] ERROR TickWorld t=%d: %v", t, err)
		}
	}

	// B: Smarter periodic persistence (dirty + activity based)
	// Only flush players/banks that were actually modified since last check.
	// Combined with activity check for efficiency.
	if l.world != nil && t%300 == 0 {
		hasRecentActivity := false
		for _, last := range l.lastInputTime {
			if t-last < 600 {
				hasRecentActivity = true
				break
			}
		}
		if hasRecentActivity {
			// Use dirty-based flush when possible (B big expansion)
			if err := l.world.FlushDirtyPlayersAndBanks(t - 600); err != nil {
				log.Printf("[PERSIST] WARN periodic FlushDirtyPlayersAndBanks (since %d): %v", t-600, err)
			}
			// D: Also flush any rooms whose floor objects changed (drops, pickups, deaths)
			if err := l.world.FlushDirtyRoomObjects(t - 600); err != nil {
				log.Printf("[PERSIST] WARN periodic FlushDirtyRoomObjects (since %d): %v", t-600, err)
			}
			// C: Board posts + family news dirty flush (Package C)
			if err := l.world.FlushDirtyBoardsAndFamilyNews(t - 600); err != nil {
				log.Printf("[PERSIST] WARN periodic FlushDirtyBoardsAndFamilyNews (since %d): %v", t-600, err)
			}
		}
	}

	// Note: player statuses (incl. idle kicks, light decay on carried lightsources,
	// HP/MP natural regen + poison/DoT/harm rooms) now strictly at 20s via schedule,
	// matching C update_users + update_ply.
}

func (l *Loop) HandleEvent(ctx context.Context, event session.Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if l == nil {
		return errors.New("game: nil loop")
	}

	switch event.Kind {
	case session.EventLine:
		return l.handleLine(ctx, event)
	case session.EventClosed:
		l.UnregisterSession(event.SessionID)
		return nil
	case session.EventError:
		l.UnregisterSession(event.SessionID)
		return nil
	default:
		return fmt.Errorf("game: unknown session event kind %q", event.Kind)
	}
}

func (l *Loop) handleLine(ctx context.Context, event session.Event) error {
	l.mu.Lock()
	l.lastInputTime[event.SessionID] = time.Now().Unix()
	l.mu.Unlock()

	b, ok := l.sessionBinding(event.SessionID)
	if !ok {
		return fmt.Errorf("%w %q", ErrSessionNotFound, event.SessionID)
	}
	if b.actorID == "" && l.unauth != nil {
		result, err := l.unauth(ctx, event.SessionID, event.Line)
		out := result.Command.Write
		if err != nil && l.formatErr != nil {
			out += l.formatErr(err)
		}
		cmd := result.Command
		cmd.Write = out
		if result.ActorID != "" {
			if bindErr := l.BindActor(event.SessionID, result.ActorID); bindErr != nil {
				return bindErr
			}
		}
		if result.Pending != nil {
			l.setPendingLineHandler(event.SessionID, result.Pending)
		}
		if isNoopCommand(cmd) {
			return nil
		}
		return sendCommand(ctx, b.commands, cmd)
	}

	if pending, ok := l.pendingLineHandler(event.SessionID); ok {
		commandCtx := l.newCommandContext(ctx, event.SessionID, b.actorID)
		status, err := pending(commandCtx, event.Line)
		return l.sendCommandResult(ctx, event.SessionID, b.commands, commandCtx, status, err)
	}

	commandCtx := l.newCommandContext(ctx, event.SessionID, b.actorID)
	if l.legacyHexLineEnabled(b.actorID) {
		commandCtx.WriteString(legacyHexLine(event.Line))
	}

	line, repeated := l.resolveLegacyLastCommand(event.SessionID, event.Line)
	if repeated && line == "" {
		return l.sendCommandResult(ctx, event.SessionID, b.commands, commandCtx, enginecmd.StatusPrompt, nil)
	}

	status, err := l.dispatcher.DispatchLine(commandCtx, line)
	return l.sendCommandResult(ctx, event.SessionID, b.commands, commandCtx, status, err)
}

func (l *Loop) legacyHexLineEnabled(actorID string) bool {
	if l == nil || l.world == nil || strings.TrimSpace(actorID) == "" {
		return false
	}
	playerID := model.PlayerID(actorID)
	if player, ok := l.world.Player(playerID); ok {
		if legacyMetadataFlag(player.Metadata, "PHEXLN") {
			return true
		}
		if !player.CreatureID.IsZero() {
			if creature, ok := l.world.Creature(player.CreatureID); ok {
				return legacyCreatureFlag(creature, "PHEXLN")
			}
		}
		return false
	}
	creature, ok := l.world.Creature(model.CreatureID(actorID))
	return ok && legacyCreatureFlag(creature, "PHEXLN")
}

func legacyHexLine(line string) string {
	encoded, err := legacykr.EncodeEUCKR(line)
	if err != nil {
		encoded = []byte(line)
	}
	var b strings.Builder
	for _, c := range encoded {
		fmt.Fprintf(&b, "%02X", c)
	}
	b.WriteByte('\n')
	return b.String()
}

func legacyCreatureFlag(creature model.Creature, name string) bool {
	return creatureHasAnyFlag(creature, name)
}

func legacyMetadataFlag(metadata model.Metadata, name string) bool {
	return hasAnyNormalizedFlag(metadata.Tags, name)
}

func legacyTruthyProperty(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func (l *Loop) resolveLegacyLastCommand(id session.ID, line string) (string, bool) {
	if l == nil {
		return line, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	repeated := false
	resolved := line
	switch {
	case line == "!":
		repeated = true
		resolved = l.lastCommand[id]
	case strings.HasPrefix(line, "!"):
		repeated = true
		resolved = legacyTruncateLastCommand(l.lastCommand[id]+line[1:], 79)
	}

	if resolved != "" {
		l.lastCommand[id] = legacyLastCommandText(resolved)
	}
	return resolved, repeated
}

func legacyLastCommandText(line string) string {
	return legacyTruncateLastCommand(strings.TrimLeft(line, " "), 79)
}

func legacyTruncateLastCommand(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range text {
		part := string(r)
		size := len(part)
		if encoded, err := legacykr.EncodeEUCKR(part); err == nil {
			size = len(encoded)
		}
		if used+size > limit {
			break
		}
		b.WriteRune(r)
		used += size
	}
	return b.String()
}

func (l *Loop) sendCommandResult(ctx context.Context, id session.ID, commands chan<- session.Command, commandCtx *enginecmd.Context, status enginecmd.Status, err error) error {
	out := commandCtx.OutputString()
	if err != nil && l.formatErr != nil {
		out += l.formatErr(err)
	}

	cmd := session.Command{
		Write: out,
		Close: status == enginecmd.StatusDisconnect,
	}
	if l.prompt != nil && l.promptFor != nil && l.promptFor(status, err) {
		cmd.Prompt = l.prompt(id, commandCtx, status)
	}
	if isNoopCommand(cmd) {
		return nil
	}
	return sendCommand(ctx, commands, cmd)
}

func (l *Loop) setPendingLineHandler(id session.ID, handler enginecmd.PendingLineHandler) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if handler == nil {
		delete(l.pending, id)
		return
	}
	l.pending[id] = handler
}

func (l *Loop) pendingLineHandler(id session.ID) (enginecmd.PendingLineHandler, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	handler, ok := l.pending[id]
	return handler, ok
}

func (l *Loop) HasPendingLineHandler(id session.ID) bool {
	_, ok := l.pendingLineHandler(id)
	return ok
}

func isNoopCommand(cmd session.Command) bool {
	return cmd.Write == "" && cmd.Prompt == "" && !cmd.Close && cmd.SetCallback == nil
}

func sendCommand(ctx context.Context, commands chan<- session.Command, cmd session.Command) error {
	select {
	case commands <- cmd:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type loopShutdownWorld struct {
	l *Loop
	w *state.World
}

func (a *loopShutdownWorld) ShutdownSchedule() (ltime int64, interval int64) {
	return a.w.ShutdownSchedule()
}

func (a *loopShutdownWorld) LastShutdownUpdate() int64 {
	return a.w.LastShutdownUpdate()
}

func (a *loopShutdownWorld) SetLastShutdownUpdate(t int64) {
	a.w.SetLastShutdownUpdate(t)
}

func (a *loopShutdownWorld) BroadcastAll(message string) error {
	return a.w.BroadcastAll(message)
}

func (a *loopShutdownWorld) SaveAllPlayers() error {
	return a.w.SaveAllPlayers()
}

func (a *loopShutdownWorld) ResaveAllRooms(permOnly bool) error {
	return a.w.ResaveAllRooms(permOnly)
}

func (a *loopShutdownWorld) FlushActivePlayersAndBanks() error {
	// Delegate to world's FlushActive which is now the single reliable shutdown flush path
	// (players + banks + full room floor objects incl. runtime non-perm).
	return a.w.FlushActivePlayersAndBanks()
}

func (a *loopShutdownWorld) DisconnectAll() {
	a.l.mu.Lock()
	defer a.l.mu.Unlock()
	for _, b := range a.l.sessions {
		select {
		case b.commands <- session.Command{Close: true}:
		default:
		}
	}
}

func (a *loopShutdownWorld) Terminate() {
	os.Exit(0)
}

func (l *Loop) SessionLastInputTime(sessionID session.ID) (int64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	t, ok := l.lastInputTime[sessionID]
	return t, ok
}

func (l *Loop) DisconnectSession(sessionID session.ID) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	b, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	select {
	case b.commands <- session.Command{Close: true}:
		return nil
	default:
		return fmt.Errorf("failed to send close command to session %s", sessionID)
	}
}

func (l *Loop) WriteToSession(sessionID session.ID, text string, isPrompt bool) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	b, ok := l.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	cmd := session.Command{Write: text}
	select {
	case b.commands <- cmd:
		return nil
	default:
		return fmt.Errorf("failed to send command to session %s", sessionID)
	}
}

func (l *Loop) Creature(creatureID model.CreatureID) (model.Creature, bool) {
	if l.world == nil {
		return model.Creature{}, false
	}
	return l.world.Creature(creatureID)
}

func (l *Loop) Player(playerID model.PlayerID) (model.Player, bool) {
	if l.world == nil {
		return model.Player{}, false
	}
	return l.world.Player(playerID)
}

func (l *Loop) SetCreatureStat(creatureID model.CreatureID, name string, val int) error {
	if l.world == nil {
		return fmt.Errorf("world is nil")
	}
	return l.world.SetCreatureStat(creatureID, name, val)
}

func (l *Loop) RecalculateAC(creatureID model.CreatureID) error {
	c, ok := l.Creature(creatureID)
	if !ok {
		return fmt.Errorf("creature %s not found", creatureID)
	}
	ac := computeAC(c, l)
	return l.SetCreatureStat(creatureID, "armor", ac)
}

func (l *Loop) RecalculateTHACO(creatureID model.CreatureID) error {
	c, ok := l.Creature(creatureID)
	if !ok {
		return fmt.Errorf("creature %s not found", creatureID)
	}
	thaco := computeTHACO(c, l)
	return l.SetCreatureStat(creatureID, "thaco", thaco)
}

func (l *Loop) UpdatePlayerTags(playerID model.PlayerID, add, remove []string) (model.Player, error) {
	if l.world == nil {
		return model.Player{}, fmt.Errorf("world is nil")
	}
	return l.world.UpdatePlayerTags(playerID, add, remove)
}

func (l *Loop) UseCreatureCooldown(creatureID model.CreatureID, key string, nowUnix int64, intervalSeconds int64) (int64, bool, error) {
	if l.world == nil {
		return 0, false, fmt.Errorf("world is nil")
	}
	return l.world.UseCreatureCooldown(creatureID, key, nowUnix, intervalSeconds)
}

func (l *Loop) SetCreatureCooldown(creatureID model.CreatureID, key string, nowUnix int64, intervalSeconds int64) error {
	if l.world == nil {
		return fmt.Errorf("world is nil")
	}
	return l.world.SetCreatureCooldown(creatureID, key, nowUnix, intervalSeconds)
}

func (l *Loop) Room(roomID model.RoomID) (model.Room, bool) {
	if l.world == nil {
		return model.Room{}, false
	}
	return l.world.Room(roomID)
}

func (l *Loop) AllRoomIDs() []model.RoomID {
	if l.world == nil {
		return nil
	}
	return l.world.AllRoomIDs()
}

func (l *Loop) MovePlayerToRoom(playerID model.PlayerID, roomID model.RoomID) error {
	if l.world == nil {
		return fmt.Errorf("world is nil")
	}
	return l.world.MovePlayerToRoom(playerID, roomID)
}

func (l *Loop) BroadcastRoom(roomID model.RoomID, excludeSessionID session.ID, text string) error {
	return l.RoomBroadcast(context.Background(), roomID, excludeSessionID, text)
}

func (l *Loop) BroadcastAll(text string) error {
	return l.Broadcast(context.Background(), session.Command{Write: text})
}

func (l *Loop) SetObjectProperty(objectID model.ObjectInstanceID, key string, value string) (model.ObjectInstance, error) {
	if l.world == nil {
		return model.ObjectInstance{}, fmt.Errorf("world is nil")
	}
	return l.world.SetObjectProperty(objectID, key, value)
}

func (l *Loop) Object(objectID model.ObjectInstanceID) (model.ObjectInstance, bool) {
	if l.world == nil {
		return model.ObjectInstance{}, false
	}
	return l.world.Object(objectID)
}

func (l *Loop) ObjectPrototype(id model.PrototypeID) (model.ObjectPrototype, bool) {
	if l.world == nil {
		return model.ObjectPrototype{}, false
	}
	return l.world.ObjectPrototype(id)
}

func (l *Loop) SavePlayer(playerID model.PlayerID) error {
	if l.world == nil {
		return nil
	}
	return l.world.SavePlayer(playerID)
}

func (l *Loop) GetEffectExpiration(creatureID model.CreatureID, tag string) (int64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	m, ok := l.activeEffects[creatureID]
	if !ok {
		return 0, false
	}
	expires, ok := m[tag]
	return expires, ok
}

func (l *Loop) SetEffectExpiration(creatureID model.CreatureID, tag string, expires int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.activeEffects[creatureID]
	if !ok {
		m = map[string]int64{}
		l.activeEffects[creatureID] = m
	}
	m[tag] = expires
}

func (l *Loop) DeleteEffectExpiration(creatureID model.CreatureID, tag string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.activeEffects[creatureID]
	if ok {
		delete(m, tag)
		if len(m) == 0 {
			delete(l.activeEffects, creatureID)
		}
	}
}

func (l *Loop) SessionActor(sessionID session.ID) (model.CreatureID, model.PlayerID, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	b, ok := l.sessions[sessionID]
	if !ok || b.actorID == "" {
		return "", "", false
	}
	playerID := model.PlayerID(b.actorID)
	if l.world != nil {
		if p, ok := l.world.Player(playerID); ok && !p.CreatureID.IsZero() {
			return p.CreatureID, playerID, true
		}
	}
	return model.CreatureID(playerID), playerID, true
}

func (l *Loop) DispatchCommand(sessionID session.ID, playerID model.PlayerID, line string) error {
	l.mu.RLock()
	b, ok := l.sessions[sessionID]
	l.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	commandCtx := l.newCommandContext(context.Background(), sessionID, string(playerID))
	status, err := l.dispatcher.DispatchLine(commandCtx, line)
	return l.sendCommandResult(context.Background(), sessionID, b.commands, commandCtx, status, err)
}

type loopUpdateActiveWorld struct {
	l *Loop
	w *state.World
}

func (w *loopUpdateActiveWorld) DispatchCommand(sessionID session.ID, playerID model.PlayerID, line string) error {
	return w.l.DispatchCommand(sessionID, playerID, line)
}

// UpdateActiveMonsters is implemented in update_active.go
