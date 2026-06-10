package state

import (
	"errors"
	"log"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"sync"

	"muhan/internal/world/load"
	"muhan/internal/world/model"
)

const (
	movePlayerDMClass = 13

	legacyObjectRandomEnchantmentFlagBit = 21

	stateCreatureDescriptionProperty = "description"
	stateCreaturePasswordHashKey     = "legacyPasswordHash"
	stateSuicidePendingTag           = "suicidePending"
	stateSuicideRequestedAtProperty  = "suicideRequestedAt"

	exitLTimeIntervalRawField = "ltime.interval"
	exitLTimeLTimeRawField    = "ltime.ltime"
	exitLTimeMiscRawField     = "ltime.misc"
)

// TrapState holds dynamic per-room trap simulation data for tick fidelity (P1-3).
// Static trap effects are executed by the movement trap handler; this runtime
// slot is reserved for dynamic counters/timers if deeper room.c traffic or
// disarm-window parity needs state that does not belong in static room data.
// Runtime only (not saved), matching legacy transient state.
type TrapState struct {
	TriggeredCount int
	LastTriggered  int64
	DisarmedUntil  int64 // unix time; 0 = armed

}

// World is a mutable runtime view of the loaded world data.
type World struct {
	roomsMu           sync.RWMutex
	playersMu         sync.RWMutex
	creaturesMu       sync.RWMutex
	objectsMu         sync.RWMutex
	banksMu           sync.RWMutex
	familiesMu        sync.RWMutex
	miscMu            sync.RWMutex
	rooms             map[model.RoomID]model.Room
	players           map[model.PlayerID]model.Player
	creatures         map[model.CreatureID]model.Creature
	families          map[int]model.Family
	banks             map[model.BankID]model.BankAccount
	objects           map[model.ObjectInstanceID]model.ObjectInstance
	prototypes        map[model.PrototypeID]model.ObjectPrototype
	marriageInvites   map[model.SpecialID][]string
	cooldowns         map[model.CreatureID]map[string]int64
	monsterDamage     map[model.CreatureID]map[model.CreatureID]int
	familyWar         FamilyWarSnapshot
	spies             map[model.PlayerID]model.PlayerID
	enemies           map[model.CreatureID][]string
	effectExpirations map[model.CreatureID]map[string]int64
	lockouts          []LockoutEntry

	// Tick & Simulation Fidelity extensions (historical P1-3 / Package 3/6 marker cleaned).
	// Small additions ONLY for new tick data as permitted:
	// - lightTimers: per-creature light source decay timers (supplements object
	//   "shotsCurrent" for carried lightsources; allows precise per-tick timing
	//   independent of 20s player update if needed in future).
	// - trapStates: optional dynamic per-room trap state (trigger counts, disarm
	//   timers, reset semantics) for deeper legacy room.c traffic behavior
	//   without polluting static room.Properties.
	// These are runtime-only (not persisted, reset on load), matching C statics
	// + active list side effects. Initialized empty in NewWorld.
	lightTimers map[model.CreatureID]map[string]int64
	trapStates  map[model.RoomID]TrapState

	BroadcastAllFunc         func(string) error
	UpdateActiveMonstersFunc func(t int64) error
	UpdatePlayerStatusesFunc func(t int64) error
	UpdateRandomSpawnsFunc   func(t int64) error
	UpdateTimeClockFunc      func(t int64) error
	UpdateTimedExitsFunc     func(t int64) error
	UpdateShutdownFunc       func(t int64) error
	RecalculateACFunc        func(model.CreatureID) error
	RecalculateTHACOFunc     func(model.CreatureID) error
	dbRoot                   string
	shutdownLTime            int64
	shutdownInterval         int64
	lastShutdownUpdate       int64
	lastActiveUpdate         int64
	lastPlayerUpdate         int64
	lastRandomUpdate         int64
	lastTimeUpdate           int64
	lastExitUpdate           int64
	legacyTime               int64
	randomUpdateInterval     int64
	txInterval               int64

	// B: Dirty tracking for efficient persistence (player/bank last change time).
	// Protected by dedicated dirtyMu (not world.mu) to:
	// - Prevent deadlocks when Mark*Dirty called from inside mutation critical sections (world.mu held).
	// - Reduce contention on hot world lock for high-frequency marks (gold, hp, inventory moves).
	playerDirty map[model.PlayerID]int64
	bankDirty   map[model.BankID]int64
	// D: Room floor objects dirty tracking (biggest remaining durability gap from review).
	// When objects move to/from a room (drop, get from ground, corpse drop, gold drop to room etc.)
	// we mark the room so its floor contents (including containers' contents) get persisted in sidecar.
	roomObjectDirty map[model.RoomID]int64
	// C: Board posts + family news dirty tracking (Package C runtime persistence).
	// Marked at mutation time (new post via appendBoardPost, toggle delete, family news change).
	// Enables FlushDirtyBoardsAndFamilyNews + sidecar JSON + startup restore (like B/D).
	boardDirty      map[string]int64 // legacy board dir e.g. "info", "family1", "user", "notice", "family"
	familyNewsDirty map[int]int64    // family ID (1..N)
	dirtyMu         sync.Mutex       // protects only the *Dirty maps

	// B: Minimal background save queue (C phase start)
	saveQueue chan saveRequest
}

type saveRequest struct {
	playerID model.PlayerID
	bankID   model.BankID
	boardDir string // C: for board posts sidecar
	familyID int    // C: for family news sidecar
	done     chan struct{}
}

// BroadcastAll broadcasts a message to all connected sessions.
func (w *World) BroadcastAll(message string) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.BroadcastAllFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(message)
	}
	return nil
}

// SetDBRoot sets the database root path for dynamic loading.
func (w *World) SetDBRoot(root string) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.dbRoot = root
}

// DBRoot returns the database root path.
func (w *World) DBRoot() string {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.dbRoot
}

var (
	ErrFamilyWarInvalidFamily = errors.New("family war: family id must be positive")
	ErrFamilyWarSelf          = errors.New("family war: family cannot declare war on itself")
	ErrFamilyWarActive        = errors.New("family war: war is already active")
	ErrFamilyWarPending       = errors.New("family war: another war request is pending")
	ErrFamilyWarNoPending     = errors.New("family war: no pending war request")
	ErrFamilyWarNotRequester  = errors.New("family war: family did not request the pending war")
	ErrFamilyWarNotTarget     = errors.New("family war: family is not the target of the pending war")
)

// FamilyWarPair identifies two legacy family numbers participating in a war
// transition. For pending requests First is the caller and Second is the target.
// For active wars the order preserves the legacy AT_WAR encoding.
type FamilyWarPair struct {
	First  int
	Second int
}

// IsZero reports whether pair is unset.
func (p FamilyWarPair) IsZero() bool {
	return p.First == 0 && p.Second == 0
}

// FamilyWarSnapshot is the runtime equivalent of legacy AT_WAR/CALLWAR1/CALLWAR2.
type FamilyWarSnapshot struct {
	Active  FamilyWarPair
	Pending FamilyWarPair
}

// HasPending reports whether a family war request is waiting for acceptance.
func (s FamilyWarSnapshot) HasPending() bool {
	return !s.Pending.IsZero()
}

// New returns a runtime world state copied from src.
func New(src *load.World) *World {
	return NewWorld(src)
}

// NewWorld returns a runtime world state copied from src.
func NewWorld(src *load.World) *World {
	w := &World{
		rooms:             map[model.RoomID]model.Room{},
		players:           map[model.PlayerID]model.Player{},
		creatures:         map[model.CreatureID]model.Creature{},
		families:          map[int]model.Family{},
		banks:             map[model.BankID]model.BankAccount{},
		objects:           map[model.ObjectInstanceID]model.ObjectInstance{},
		prototypes:        map[model.PrototypeID]model.ObjectPrototype{},
		marriageInvites:   map[model.SpecialID][]string{},
		cooldowns:         map[model.CreatureID]map[string]int64{},
		monsterDamage:     map[model.CreatureID]map[model.CreatureID]int{},
		spies:             map[model.PlayerID]model.PlayerID{},
		enemies:           map[model.CreatureID][]string{},
		effectExpirations: map[model.CreatureID]map[string]int64{},

		lightTimers:          map[model.CreatureID]map[string]int64{},
		trapStates:           map[model.RoomID]TrapState{},
		randomUpdateInterval: 20,
		txInterval:           3600,

		playerDirty:     make(map[model.PlayerID]int64),
		bankDirty:       make(map[model.BankID]int64),
		roomObjectDirty: make(map[model.RoomID]int64),

		boardDirty:      make(map[string]int64),
		familyNewsDirty: make(map[int]int64),

		saveQueue: make(chan saveRequest, 128),
	}
	go w.backgroundSaver()
	if src == nil {
		return w
	}

	for id, room := range src.Rooms {
		w.rooms[id] = cloneRoom(room)
	}
	for id, player := range src.Players {
		w.players[id] = clonePlayer(player)
	}
	for id, creature := range src.Creatures {
		w.creatures[id] = cloneCreature(creature)
	}
	for id, family := range src.Families {
		w.families[id] = cloneFamily(family)
	}
	for id, account := range src.Banks {
		w.banks[id] = cloneBankAccount(account)
	}
	for id, object := range src.Objects {
		w.objects[id] = cloneObject(object)
	}
	for id, proto := range src.ObjectPrototypes {
		w.prototypes[id] = cloneObjectPrototype(proto)
	}
	for id, names := range src.MarriageInvites {
		w.marriageInvites[id] = slices.Clone(names)
	}
	w.reconcileRoomOccupants()
	return w
}

// Families returns all legacy family registry rows sorted by slot and id.
func (w *World) Families() []model.Family {
	if w == nil {
		return nil
	}
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)

	families := make([]model.Family, 0, len(w.families))
	for _, family := range w.families {
		families = append(families, cloneFamily(family))
	}
	slices.SortStableFunc(families, func(a, b model.Family) int {
		if a.Slot != b.Slot {
			return a.Slot - b.Slot
		}
		return a.ID - b.ID
	})
	return families
}

type roomPermanentSlot struct {
	index    int
	misc     int
	ltime    int64
	interval int64
}

type roomPermanentGroup struct {
	misc  int
	count int
}

func (w *World) applyRandomEnchantIfNeededLocked(object *model.ObjectInstance) {
	if object == nil || !w.objectHasRandomEnchantLocked(*object) {
		return
	}
	w.applyLegacyRandomEnchantRollLocked(object, rand.Intn(100)+1)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func stateProficiencyRank(value int, table [12]int64) int {
	rank := 100
	i := 10
	for i = 0; i < 11; i++ {
		if int64(value) < table[i+1] {
			rank = 10 * i
			break
		}
	}
	if table[i+1] > table[i] {
		rank += int((int64(value) - table[i]) * 10 / (table[i+1] - table[i]))
	}
	return rank
}

var stateLegacyBonusTable = [...]int{
	-4, -4, -4, -3, -3, -2, -2, -1, -1, -1,
	0, 0, 0, 0, 1, 1, 1, 2, 2, 2,
	3, 3, 3, 3, 4, 4, 4, 4, 4, 5,
	5, 5, 5, 5, 5, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	9, 9, 9, 9, 9, 9,
}

var stateLegacyTHACOList = [...][20]int{
	{20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20},
	{18, 18, 18, 17, 17, 16, 16, 15, 15, 14, 14, 13, 13, 12, 12, 11, 10, 10, 9, 9},
	{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 3, 2},
	{20, 20, 19, 18, 18, 17, 16, 16, 15, 14, 14, 13, 13, 12, 12, 11, 10, 10, 9, 8},
	{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 3, 3},
	{20, 20, 19, 19, 18, 18, 18, 17, 17, 16, 16, 16, 15, 15, 14, 14, 14, 13, 13, 11},
	{19, 19, 18, 18, 17, 16, 16, 15, 15, 14, 14, 13, 13, 12, 11, 11, 10, 9, 8, 7},
	{19, 19, 18, 17, 16, 16, 15, 15, 14, 14, 13, 12, 12, 11, 11, 10, 9, 9, 8, 7},
	{20, 20, 19, 19, 18, 18, 17, 17, 16, 16, 15, 15, 14, 14, 13, 13, 12, 12, 11, 11},
	{15, 15, 14, 14, 13, 13, 12, 12, 11, 11, 10, 10, 9, 9, 8, 8, 7, 6, 5, 5},
	{12, 12, 11, 11, 10, 10, 9, 9, 8, 8, 7, 7, 6, 6, 5, 5, 4, 4, 3, 0},
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
}

func rawFieldInt64(raw []byte, width int) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	if text, ok := rawASCIIInteger(raw); ok {
		value, err := strconv.ParseInt(text, 10, 64)
		if err == nil {
			return value, true
		}
	}
	switch width {
	case 2:
		if len(raw) >= 2 {
			return int64(int16(uint16(raw[0]) | uint16(raw[1])<<8)), true
		}
	case 4:
		if len(raw) >= 4 {
			return int64(int32(uint32(raw[0]) | uint32(raw[1])<<8 | uint32(raw[2])<<16 | uint32(raw[3])<<24)), true
		}
	}
	return 0, false
}

func rawASCIIInteger(raw []byte) (string, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", false
	}
	hasDigit := false
	for i, r := range text {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r == '-' || r == '+') && i == 0:
		default:
			return "", false
		}
	}
	return text, hasDigit
}

func int32RawField(value int64) []byte {
	v := int32(value)
	return []byte{
		byte(v),
		byte(v >> 8),
		byte(v >> 16),
		byte(v >> 24),
	}
}

func addMetadataTags(tags []string, add []string) []string {
	out := slices.Clone(tags)
	seen := make(map[string]struct{}, len(out)+len(add))
	for _, tag := range out {
		if normalized := normalizeFlagName(tag); normalized != "" {
			seen[normalized] = struct{}{}
		}
	}
	for _, tag := range add {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		normalized := normalizeFlagName(tag)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		out = append(out, tag)
		seen[normalized] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FinalizeMonsterDeathOptions carries death-finalization inputs that are not
// owned by world state.
type FinalizeMonsterDeathOptions struct {
	RewardGroup MonsterDeathRewardGroup
}

// MonsterDeathRewardGroup is a snapshot of the final attacker's group at the
// moment a monster dies. FollowerIDs should contain followers only; LeaderID is
// counted separately.
type MonsterDeathRewardGroup struct {
	LeaderID    model.CreatureID
	FollowerIDs []model.CreatureID
}

func (w *World) pruneCharmReferencesLocked(target model.Creature) {
	remove := charmReferenceTags(target)
	if len(remove) == 0 {
		return
	}
	for id, creature := range w.creatures {
		nextTags := removeMetadataTags(creature.Metadata.Tags, remove)
		if slices.Equal(nextTags, creature.Metadata.Tags) {
			continue
		}
		creature.Metadata.Tags = nextTags
		w.creatures[id] = creature
		if !creature.PlayerID.IsZero() {
			w.MarkPlayerDirty(creature.PlayerID)
		}
	}
	for id, player := range w.players {
		nextTags := removeMetadataTags(player.Metadata.Tags, remove)
		if slices.Equal(nextTags, player.Metadata.Tags) {
			continue
		}
		player.Metadata.Tags = nextTags
		w.players[id] = player
		w.MarkPlayerDirty(id)
	}
}

func charmReferenceTags(target model.Creature) []string {
	var tags []string
	if name := strings.TrimSpace(target.DisplayName); name != "" {
		tags = append(tags, "charm:"+name)
	}
	if !target.ID.IsZero() {
		tags = append(tags, "charmID:"+string(target.ID))
	}
	return tags
}

var weaponProficiencyStatKeys = [...]string{
	"proficiencySharp",
	"proficiencyThrust",
	"proficiencyBlunt",
	"proficiencyPole",
	"proficiencyMissile",
}

var weaponProficiencyPropertyKeys = [...]string{
	"sharp",
	"thrust",
	"blunt",
	"pole",
	"missile",
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func hasAnyNormalizedFlag(flags []string, names ...string) bool {
	targets := normalizedFlagSet(names...)
	for _, flag := range flags {
		if _, ok := targets[normalizeFlagName(flag)]; ok {
			return true
		}
	}
	return false
}

func normalizedFlagSet(names ...string) map[string]struct{} {
	targets := make(map[string]struct{})
	for _, name := range ExpandFlagNames(names...) {
		targets[name] = struct{}{}
	}
	return targets
}

// FlagAliasMap maps normalized flag/tag names to their list of aliases.
var FlagAliasMap = map[string][]string{
	"poison":   {"poison", "poisoned", "ppoisn", "mpoisn"},
	"poisoned": {"poison", "poisoned", "ppoisn", "mpoisn"},
	"ppoisn":   {"poison", "poisoned", "ppoisn", "mpoisn"},
	"mpoisn":   {"poison", "poisoned", "ppoisn", "mpoisn"},

	"disease":  {"disease", "diseased", "pdisea", "mdisea"},
	"diseased": {"disease", "diseased", "pdisea", "mdisea"},
	"pdisea":   {"disease", "diseased", "pdisea", "mdisea"},
	"mdisea":   {"disease", "diseased", "pdisea", "mdisea"},

	"blind":   {"blind", "blinded", "pblind", "mblind"},
	"blinded": {"blind", "blinded", "pblind", "mblind"},
	"pblind":  {"blind", "blinded", "pblind", "mblind"},
	"mblind":  {"blind", "blinded", "pblind", "mblind"},

	"fear":    {"fear", "fearful", "pfears", "mfears"},
	"fearful": {"fear", "fearful", "pfears", "mfears"},
	"pfears":  {"fear", "fearful", "pfears", "mfears"},
	"mfears":  {"fear", "fearful", "pfears", "mfears"},

	"charm":   {"charm", "charmed", "pcharm", "mcharm"},
	"charmed": {"charm", "charmed", "pcharm", "mcharm"},
	"pcharm":  {"charm", "charmed", "pcharm", "mcharm"},
	"mcharm":  {"charm", "charmed", "pcharm", "mcharm"},

	"silence":  {"silence", "silenced", "psilnc", "msilnc"},
	"silenced": {"silence", "silenced", "psilnc", "msilnc"},
	"psilnc":   {"silence", "silenced", "psilnc", "msilnc"},
	"msilnc":   {"silence", "silenced", "psilnc", "msilnc"},

	"befuddle":  {"befuddle", "befuddled"},
	"befuddled": {"befuddle", "befuddled"},

	"hidden": {"hidden", "phiddn", "mhiddn"},
	"phiddn": {"hidden", "phiddn", "mhiddn"},
	"mhiddn": {"hidden", "phiddn", "mhiddn"},

	"invisible":    {"invisible", "invisibility", "pinvis", "minvis"},
	"invisibility": {"invisible", "invisibility", "pinvis", "minvis"},
	"pinvis":       {"invisible", "invisibility", "pinvis", "minvis"},
	"minvis":       {"invisible", "invisibility", "pinvis", "minvis"},

	"detectinvisible": {"detectinvisible", "detectinvis", "pdinvi", "mdinvi"},
	"detectinvis":     {"detectinvisible", "detectinvis", "pdinvi", "mdinvi"},
	"pdinvi":          {"detectinvisible", "detectinvis", "pdinvi", "mdinvi"},
	"mdinvi":          {"detectinvisible", "detectinvis", "pdinvi", "mdinvi"},

	"bless":   {"bless", "blessed", "pbless"},
	"blessed": {"bless", "blessed", "pbless"},
	"pbless":  {"bless", "blessed", "pbless"},

	"light":  {"light", "plight"},
	"plight": {"light", "plight"},

	"protection": {"protection", "protect", "protected", "pprote"},
	"protect":    {"protection", "protect", "protected", "pprote"},
	"protected":  {"protection", "protect", "protected", "pprote"},
	"pprote":     {"protection", "protect", "protected", "pprote"},

	"resistfire":     {"resistfire", "fireresistance", "prfire"},
	"fireresistance": {"resistfire", "fireresistance", "prfire"},
	"prfire":         {"resistfire", "fireresistance", "prfire"},

	"resistcold":     {"resistcold", "coldresistance", "prcold"},
	"coldresistance": {"resistcold", "coldresistance", "prcold"},
	"prcold":         {"resistcold", "coldresistance", "prcold"},

	"earthshield": {"earthshield", "stoneshield", "psshld"},
	"stoneshield": {"earthshield", "stoneshield", "psshld"},
	"psshld":      {"earthshield", "stoneshield", "psshld"},

	"resistmagic":     {"resistmagic", "magicresistance", "prmagi", "mrmagi"},
	"magicresistance": {"resistmagic", "magicresistance", "prmagi", "mrmagi"},
	"prmagi":          {"resistmagic", "magicresistance", "prmagi", "mrmagi"},
	"mrmagi":          {"resistmagic", "magicresistance", "prmagi", "mrmagi"},

	"prepared": {"prepared", "prepare", "pprepa"},
	"prepare":  {"prepared", "prepare", "pprepa"},
	"pprepa":   {"prepared", "prepare", "pprepa"},

	"levitate":   {"levitate", "levitation", "plevit"},
	"levitation": {"levitate", "levitation", "plevit"},
	"plevit":     {"levitate", "levitation", "plevit"},

	"fly":    {"fly", "flying", "pflysp"},
	"flying": {"fly", "flying", "pflysp"},
	"pflysp": {"fly", "flying", "pflysp"},

	"breathewater":   {"breathewater", "waterbreathing", "pbrwat"},
	"waterbreathing": {"breathewater", "waterbreathing", "pbrwat"},
	"pbrwat":         {"breathewater", "waterbreathing", "pbrwat"},

	"detectmagic": {"detectmagic", "dmagic", "pdmagi"},
	"dmagic":      {"detectmagic", "dmagic", "pdmagi"},
	"pdmagi":      {"detectmagic", "dmagic", "pdmagi"},

	"haste":  {"haste", "hasted", "phaste"},
	"hasted": {"haste", "hasted", "phaste"},
	"phaste": {"haste", "hasted", "phaste"},

	"prayer": {"prayer", "prayed", "pprayd"},
	"prayed": {"prayer", "prayed", "pprayd"},
	"pprayd": {"prayer", "prayed", "pprayd"},

	"meditate":   {"meditate", "meditation", "pmedit"},
	"meditation": {"meditate", "meditation", "pmedit"},
	"pmedit":     {"meditate", "meditation", "pmedit"},

	"power":  {"power", "ppower"},
	"ppower": {"power", "ppower"},

	"knowalignment":  {"knowalignment", "alignmentsense", "pknowa"},
	"alignmentsense": {"knowalignment", "alignmentsense", "pknowa"},
	"pknowa":         {"knowalignment", "alignmentsense", "pknowa"},

	"updamage": {"updamage", "updmg", "pupdmg"},
	"updmg":    {"updamage", "updmg", "pupdmg"},
	"pupdmg":   {"updamage", "updmg", "pupdmg"},

	"reflect":    {"reflect", "reflection", "preflect"},
	"reflection": {"reflect", "reflection", "preflect"},
	"preflect":   {"reflect", "reflection", "preflect"},

	"slayer": {"slayer", "slay", "pslaye"},
	"slay":   {"slayer", "slay", "pslaye"},
	"pslaye": {"slayer", "slay", "pslaye"},

	"angel":  {"angel", "pangel"},
	"pangel": {"angel", "pangel"},

	"married":  {"married", "marriage", "pmarri"},
	"marriage": {"married", "marriage", "pmarri"},
	"pmarri":   {"married", "marriage", "pmarri"},
}

var legacyCreatureFlagAliasGroups = [][]string{
	{"MPERMT", "permanent"},
	{"MHIDDN", "hidden"},
	{"MINVIS", "invisible"},
	{"MTOMEN", "manToMenPlural"},
	{"MDROPS", "noPluralSuffix"},
	{"MNOPRE", "noPrefix"},
	{"MAGGRE", "aggressive"},
	{"MGUARD", "guardTreasure"},
	{"MBLOCK", "blocksExits"},
	{"MFOLLO", "followsAttacker"},
	{"MFLEER", "flees"},
	{"MSCAVE", "scavenger"},
	{"MMALES", "male"},
	{"MPOISS", "poisoner"},
	{"MUNDED", "undead"},
	{"MUNSTL", "cannotSteal"},
	{"MPOISN", "poisoned", "poison"},
	{"MMAGIC", "magicUser", "magic"},
	{"MHASSC", "hasScavenged"},
	{"MBRETH", "breathWeapon", "breath"},
	{"MMGONL", "magicOnly"},
	{"MDINVI", "detectInvisible", "detectInvis"},
	{"MENONL", "magicOrEnchantedOnly"},
	{"MTALKS", "talks"},
	{"MUNKIL", "unkillable"},
	{"MNRGLD", "fixedGold"},
	{"MTLKAG", "talkAggressive"},
	{"MRMAGI", "resistMagic", "magicResistance"},
	{"MBRWP1", "breathWeaponType1"},
	{"MBRWP2", "breathWeaponType2"},
	{"MENEDR", "energyDrain"},
	{"MKNGDM", "kingdom"},
	{"MPLDGK", "pledgeKingdom"},
	{"MRSCND", "rescindKingdom"},
	{"MDISEA", "disease", "diseased"},
	{"MDISIT", "dissolveItems"},
	{"MPURIT", "purchaseItems"},
	{"MTRADE", "tradeItems"},
	{"MPGUAR", "passiveExitGuard"},
	{"MGAGGR", "goodAggressive"},
	{"MEAGGR", "evilAggressive"},
	{"MDEATH", "deathDescription"},
	{"MMAGIO", "magicPercent"},
	{"MRBEFD", "resistStunOnly"},
	{"MNOCIR", "cannotCircle"},
	{"MBLNDR", "blind"},
	{"MDMFOL", "followDM"},
	{"MFEARS", "fearful", "fear"},
	{"MSILNC", "silenced", "silence"},
	{"MBLIND", "blinded"},
	{"MCHARM", "charmed", "charm"},
	{"MBEFUD", "befuddled", "befuddle"},
	{"MKNDM1", "kingdom1"},
	{"MKNDM2", "kingdom2"},
	{"MKNDM3", "kingdom3"},
	{"MKNDM4", "kingdom4"},
	{"MKING1", "king1"},
	{"MKING2", "king2"},
	{"MKING3", "king3"},
	{"MKING4", "king4"},
	{"MSAYTLK", "sayTalk"},
	{"MSUMMO", "summoner", "summon"},
	{"MNOCHA", "noCharm"},
}

var legacyRoomFlagAliasGroups = [][]string{
	{"RSHOPP", "shoppe", "shop"},
	{"RDUMPR", "dump"},
	{"RPAWNS", "pawnShop", "pawn", "pawns"},
	{"RTRAIN", "train", "training"},
	{"RTRAIN4", "trainingBit4", "trainBit4"},
	{"RTRAIN5", "trainingBit5", "trainBit5"},
	{"RTRAIN6", "trainingBit6", "trainBit6"},
	{"RREPAI", "repair", "repairShop"},
	{"RDARKR", "darkAlways"},
	{"RDARKN", "darkNight"},
	{"RPOSTO", "postOffice"},
	{"RNOKIL", "noPlayerKill", "noKill"},
	{"RNOTEL", "noTeleport"},
	{"RHEALR", "healFast"},
	{"RONEPL", "onePlayer"},
	{"RTWOPL", "twoPlayers"},
	{"RTHREE", "threePlayers"},
	{"RNOMAG", "noMagic"},
	{"RPTRAK", "permanentTracks", "permTrack"},
	{"REARTH", "earth"},
	{"RWINDR", "wind"},
	{"RFIRER", "fire"},
	{"RWATER", "water"},
	{"RPLWAN", "playerWander", "groupWander"},
	{"RPHARM", "playerHarm"},
	{"RPPOIS", "playerPoison"},
	{"RPMPDR", "RPMPRD", "playerMPDrain"},
	{"RPBEFU", "playerBefuddle", "confusion"},
	{"RNOLEA", "noSummonOut", "noSummon"},
	{"RPLDGK", "pledge"},
	{"RRSCND", "rescind"},
	{"RNOPOT", "noPotion"},
	{"RPMEXT", "magicExtend", "pmagic"},
	{"RNOLOG", "noLog"},
	{"RELECT", "election"},
	{"RFORGE", "forge"},
	{"RSUVIV", "survival"},
	{"RFAMIL", "family"},
	{"RONFML", "onlyFamily", "familyOnly"},
	{"RBANK", "bank"},
	{"RMARRI", "marriage"},
	{"RONMAR", "onlyMarried", "marriedOnly"},
	{"RCAST", "cast"},
	{"RDEPOT", "depot"},
}

var legacyExitFlagAliasGroups = [][]string{
	{"XSECRT", "XSECRET", "secret"},
	{"XINVIS", "invisible"},
	{"XLOCKD", "XLOCKED", "locked"},
	{"XCLOSD", "XCLOSED", "closed"},
	{"XLOCKS", "lockable"},
	{"XCLOSS", "closable"},
	{"XUNPCK", "unpickable"},
	{"XNAKED", "naked"},
	{"XCLIMB", "climb"},
	{"XREPEL", "repel"},
	{"XDCLIM", "hardClimb", "difficultClimb"},
	{"XFLYSP", "fly"},
	{"XFEMAL", "femaleOnly"},
	{"XMALES", "maleOnly"},
	{"XPLDGK", "pledgeOnly"},
	{"XKNGDM", "kingdomSelector"},
	{"XNGHTO", "nightOnly"},
	{"XDAYON", "XDATON", "dayOnly"},
	{"XPGUAR", "guarded"},
	{"XNOSEE", "noSee"},
	{"XKNDM1", "kingdom1"},
	{"XKNDM2", "kingdom2"},
}

var legacyObjectFlagAliasGroups = [][]string{
	{"OPERMT", "permanent"},
	{"OHIDDN", "hidden"},
	{"OINVIS", "invisible"},
	{"OSOMEA", "somePrefix"},
	{"ODROPS", "noPluralSuffix"},
	{"ONOPRE", "noPrefix"},
	{"OCONTN", "container"},
	{"OWTLES", "weightless"},
	{"OTEMPP", "temporaryPermanent", "tempPermanent"},
	{"OPERM2", "inventoryPermanent"},
	{"ONOMAG", "noMage"},
	{"OLIGHT", "lightSource"},
	{"OGOODO", "goodOnly"},
	{"OEVILO", "evilOnly"},
	{"OENCHA", "enchanted"},
	{"ONOFIX", "noRepair"},
	{"OCLIMB", "climbGear", "climbing"},
	{"ONOTAK", "noTake", "notTake"},
	{"OSCENE", "scenery", "scene"},
	{"OSIZE1", "sizeSmall"},
	{"OSIZE2", "sizeLarge"},
	{"ORENCH", "randomEnchantment", "randEnch"},
	{"OCURSE", "cursed"},
	{"OWEARS", "worn"},
	{"OUSEFL", "useFromFloor"},
	{"OCNDES", "containerDevours", "devours"},
	{"ONOMAL", "femaleOnly", "noMale"},
	{"ONOFEM", "maleOnly", "noFemale"},
	{"ODDICE", "damageDice"},
	{"OPLDGK", "pledgeOnly", "organization"},
	{"OKNGDM", "kingdomBound", "kngdm"},
	{"OCLSEL", "classSelective", "clsSel"},
	{"OASSNO", "classAssassin", "assassinUsable"},
	{"OBARBO", "classBarbarian", "barbarianUsable"},
	{"OCLERO", "classCleric", "clericUsable"},
	{"OFIGHO", "classFighter", "fighterUsable"},
	{"OMAGEO", "classMage", "mageUsable"},
	{"OPALAO", "classPaladin", "paladinUsable"},
	{"ORNGRO", "classRanger", "rangerUsable"},
	{"OTHIEO", "classThief", "thiefUsable"},
	{"OVBEFD", "stunLengthDice"},
	{"ONSHAT", "neverShatter"},
	{"OALCRT", "alwaysCritical"},
	{"OCNAME", "customName"},
	{"OSPECI", "specialItem"},
	{"OMARRI", "marriageOnly"},
	{"OEVENT", "eventItem", "event"},
	{"ONAMED", "named"},
	{"ONOBUN", "noBurn", "noburn"},
	{"OWHELD", "held"},
}

var legacyCreatureFlagAliasIndex = buildLegacyAliasIndex(legacyCreatureFlagAliasGroups)

var legacyRoomFlagAliasIndex = buildLegacyAliasIndex(legacyRoomFlagAliasGroups)

var legacyExitFlagAliasIndex = buildLegacyAliasIndex(legacyExitFlagAliasGroups)

var legacyObjectFlagAliasIndex = buildLegacyAliasIndex(legacyObjectFlagAliasGroups)

// ExpandFlagNames expands the given flag/tag names to include all known aliases.
func ExpandFlagNames(names ...string) []string {
	var result []string
	seen := make(map[string]struct{})
	for _, name := range names {
		normalized := normalizeFlagName(name)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; !ok {
			seen[normalized] = struct{}{}
			result = append(result, normalized)
		}
		if aliases, ok := FlagAliasMap[normalized]; ok {
			for _, alias := range aliases {
				normAlias := normalizeFlagName(alias)
				if _, ok := seen[normAlias]; !ok {
					seen[normAlias] = struct{}{}
					result = append(result, normAlias)
				}
			}
		}
		if aliases, ok := legacyCreatureFlagAliasIndex[normalized]; ok {
			for _, alias := range aliases {
				normAlias := normalizeFlagName(alias)
				if _, ok := seen[normAlias]; !ok {
					seen[normAlias] = struct{}{}
					result = append(result, normAlias)
				}
			}
		}
		if aliases, ok := legacyRoomFlagAliasIndex[normalized]; ok {
			for _, alias := range aliases {
				normAlias := normalizeFlagName(alias)
				if _, ok := seen[normAlias]; !ok {
					seen[normAlias] = struct{}{}
					result = append(result, normAlias)
				}
			}
		}
		if aliases, ok := legacyExitFlagAliasIndex[normalized]; ok {
			for _, alias := range aliases {
				normAlias := normalizeFlagName(alias)
				if _, ok := seen[normAlias]; !ok {
					seen[normAlias] = struct{}{}
					result = append(result, normAlias)
				}
			}
		}
		if aliases, ok := legacyObjectFlagAliasIndex[normalized]; ok {
			for _, alias := range aliases {
				normAlias := normalizeFlagName(alias)
				if _, ok := seen[normAlias]; !ok {
					seen[normAlias] = struct{}{}
					result = append(result, normAlias)
				}
			}
		}
	}
	return result
}

func propertyFlagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func propertyFlagValueHasAnyToken(value string, targets map[string]struct{}) bool {
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' '
	}) {
		if _, ok := targets[normalizeFlagName(token)]; ok {
			return true
		}
	}
	return false
}

func normalizeFlagName(flag string) string {
	flag = strings.ToLower(strings.TrimSpace(flag))
	flag = strings.ReplaceAll(flag, "-", "")
	flag = strings.ReplaceAll(flag, "_", "")
	flag = strings.ReplaceAll(flag, " ", "")
	return flag
}

func parseStateInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	return parsed, err == nil
}

func appendIDOnce[T comparable](ids []T, id T) []T {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func cloneMetadata(metadata model.Metadata) model.Metadata {
	metadata.RawFields = cloneRawFields(metadata.RawFields)
	metadata.Tags = slices.Clone(metadata.Tags)
	metadata.Notes = slices.Clone(metadata.Notes)
	metadata.PrototypeResolution = clonePrototypeResolution(metadata.PrototypeResolution)
	return metadata
}

func cloneRawFields(fields map[string][]byte) map[string][]byte {
	if fields == nil {
		return nil
	}
	out := make(map[string][]byte, len(fields))
	for key, value := range fields {
		out[key] = slices.Clone(value)
	}
	return out
}

// FlushSaveQueue waits until all save requests already queued for this world
// have been processed by the background saver.
func (w *World) FlushSaveQueue() {
	if w == nil || w.saveQueue == nil {
		return
	}
	done := make(chan struct{})
	w.saveQueue <- saveRequest{done: done}
	<-done
}

// QueueSave enqueues a save request (non-blocking best effort).
func (w *World) QueueSave(playerID model.PlayerID, bankID model.BankID) {
	select {
	case w.saveQueue <- saveRequest{playerID: playerID, bankID: bankID}:
	default:

		log.Printf("[PERSIST] WARN QueueSave fallback sync (queue full) for player=%s bank=%s", playerID, bankID)
		if !playerID.IsZero() {
			if err := w.SavePlayer(playerID); err != nil {
				log.Printf("[PERSIST] ERROR fallback SavePlayer %s: %v", playerID, err)
			}
		}
		if bankID != "" {
			if err := w.SaveBank(bankID); err != nil {
				log.Printf("[PERSIST] ERROR fallback SaveBank %s: %v", bankID, err)
			}
		}
	}
}

func (w *World) lockDomains(players, rooms, creatures, objects, banks, families, misc bool) {
	if misc { w.miscMu.Lock() }
	if families { w.familiesMu.Lock() }
	if banks { w.banksMu.Lock() }
	if objects { w.objectsMu.Lock() }
	if creatures { w.creaturesMu.Lock() }
	if rooms { w.roomsMu.Lock() }
	if players { w.playersMu.Lock() }
}

func (w *World) unlockDomains(players, rooms, creatures, objects, banks, families, misc bool) {
	if players { w.playersMu.Unlock() }
	if rooms { w.roomsMu.Unlock() }
	if creatures { w.creaturesMu.Unlock() }
	if objects { w.objectsMu.Unlock() }
	if banks { w.banksMu.Unlock() }
	if families { w.familiesMu.Unlock() }
	if misc { w.miscMu.Unlock() }
}

func (w *World) rLockDomains(players, rooms, creatures, objects, banks, families, misc bool) {
	if misc { w.miscMu.RLock() }
	if families { w.familiesMu.RLock() }
	if banks { w.banksMu.RLock() }
	if objects { w.objectsMu.RLock() }
	if creatures { w.creaturesMu.RLock() }
	if rooms { w.roomsMu.RLock() }
	if players { w.playersMu.RLock() }
}

func (w *World) rUnlockDomains(players, rooms, creatures, objects, banks, families, misc bool) {
	if players { w.playersMu.RUnlock() }
	if rooms { w.roomsMu.RUnlock() }
	if creatures { w.creaturesMu.RUnlock() }
	if objects { w.objectsMu.RUnlock() }
	if banks { w.banksMu.RUnlock() }
	if families { w.familiesMu.RUnlock() }
	if misc { w.miscMu.RUnlock() }
}
