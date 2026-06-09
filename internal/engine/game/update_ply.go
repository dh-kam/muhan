package game

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"muhan/internal/krtext"
	"muhan/internal/persist/cbin"
	"muhan/internal/persist/legacykr"
	"muhan/internal/session"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

// UpdatePlyWorld is the interface required by UpdatePlayerStatuses.
type UpdatePlyWorld interface {
	ActiveSessions() []ActiveSession
	SessionActor(sessionID session.ID) (model.CreatureID, model.PlayerID, bool)
	SessionLastInputTime(sessionID session.ID) (int64, bool)
	DisconnectSession(sessionID session.ID) error
	WriteToSession(sessionID session.ID, text string, isPrompt bool) error
	Creature(creatureID model.CreatureID) (model.Creature, bool)
	Player(playerID model.PlayerID) (model.Player, bool)
	SetCreatureStat(creatureID model.CreatureID, name string, val int) error
	RecalculateAC(creatureID model.CreatureID) error
	RecalculateTHACO(creatureID model.CreatureID) error
	UpdatePlayerTags(playerID model.PlayerID, add, remove []string) (model.Player, error)
	UseCreatureCooldown(creatureID model.CreatureID, key string, nowUnix int64, intervalSeconds int64) (int64, bool, error)
	SetCreatureCooldown(creatureID model.CreatureID, key string, nowUnix int64, intervalSeconds int64) error
	Room(roomID model.RoomID) (model.Room, bool)
	MovePlayerToRoom(playerID model.PlayerID, roomID model.RoomID) error
	BroadcastRoom(roomID model.RoomID, excludeSessionID session.ID, text string) error
	BroadcastAll(text string) error
	SetObjectProperty(objectID model.ObjectInstanceID, key string, value string) (model.ObjectInstance, error)
	Object(objectID model.ObjectInstanceID) (model.ObjectInstance, bool)
	ObjectPrototype(id model.PrototypeID) (model.ObjectPrototype, bool)
	SavePlayer(playerID model.PlayerID) error

	GetEffectExpiration(creatureID model.CreatureID, tag string) (int64, bool)
	SetEffectExpiration(creatureID model.CreatureID, tag string, expires int64)
	DeleteEffectExpiration(creatureID model.CreatureID, tag string)
}

// PermanentCreatureDeathWorld is the core surface needed to mirror the
// legacy die_perm_crt() side effects without tying command handlers to state.
type PermanentCreatureDeathWorld interface {
	ActiveSessions() []ActiveSession
	Player(playerID model.PlayerID) (model.Player, bool)
	Creature(creatureID model.CreatureID) (model.Creature, bool)
	Room(roomID model.RoomID) (model.Room, bool)
	BroadcastRoom(roomID model.RoomID, excludeSessionID session.ID, text string) error
	WriteToSession(sessionID session.ID, text string, isPrompt bool) error
	SetCreatureStat(creatureID model.CreatureID, name string, val int) error
}

// PermanentCreatureDeathResult reports which die_perm_crt() side effects were
// applied. Callers can use this for smoke tests, logging, or follow-up hooks.
type PermanentCreatureDeathResult struct {
	Permanent                     bool
	RespawnMarked                 bool
	DeathDescriptionBroadcast     bool
	QuestNumber                   int
	QuestCompleted                bool
	QuestAlreadyCompleted         bool
	QuestExperience               int
	SummonRequested               bool
	SummonedCreatureID            model.CreatureID
	PermanentDeathEventHookCalled bool
}

// PermanentCreatureDeathEvent is emitted to an optional world hook after
// handling C die_perm_crt() parity work.
type PermanentCreatureDeathEvent struct {
	WhenUnix                  int64
	KillerPlayerID            model.PlayerID
	KillerCreatureID          model.CreatureID
	DeadCreatureID            model.CreatureID
	RoomID                    model.RoomID
	RespawnMarked             bool
	DeathDescriptionBroadcast bool
	QuestNumber               int
	QuestCompleted            bool
	QuestAlreadyCompleted     bool
	QuestExperience           int
	SummonedCreatureID        model.CreatureID
}

// Define constants
const (
	legacyClassAssassin   = 1
	legacyClassBarbarian  = 2
	legacyClassCleric     = 3
	legacyClassFighter    = 4
	legacyClassMage       = 5
	legacyClassPaladin    = 6
	legacyClassRanger     = 7
	legacyClassThief      = 8
	legacyClassInvincible = 9
	legacyClassBulsa      = 11
)

var thacoList = [][]int{
	{20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20}, // 0
	{18, 18, 18, 17, 17, 16, 16, 15, 15, 14, 14, 13, 13, 12, 12, 11, 10, 10, 9, 9},   // 1 Assassin
	{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 3, 2},          // 2 Barbarian
	{20, 20, 19, 18, 18, 17, 16, 16, 15, 14, 14, 13, 13, 12, 12, 11, 10, 10, 9, 8},   // 3 Cleric
	{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 3, 3},          // 4 Fighter
	{20, 20, 19, 19, 18, 18, 18, 17, 17, 16, 16, 16, 15, 15, 14, 14, 14, 13, 13, 11}, // 5 Mage
	{19, 19, 18, 18, 17, 16, 16, 15, 15, 14, 14, 13, 13, 12, 11, 11, 10, 9, 8, 7},    // 6 Paladin
	{19, 19, 18, 17, 16, 16, 15, 15, 14, 14, 13, 12, 12, 11, 11, 10, 9, 9, 8, 7},     // 7 Ranger
	{20, 20, 19, 19, 18, 18, 17, 17, 16, 16, 15, 15, 14, 14, 13, 13, 12, 12, 11, 11}, // 8 Thief
	{15, 15, 14, 14, 13, 13, 12, 12, 11, 11, 10, 10, 9, 9, 8, 8, 7, 6, 5, 5},         // 9 Invincible
	{12, 12, 11, 11, 10, 10, 9, 9, 8, 8, 7, 7, 6, 6, 5, 5, 4, 4, 3, 0},               // 10 Caretaker
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},                     // 11 Bulsa
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},                     // 12
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},                     // 13 DM
}

var bonusTable = []int{
	-4, -4, -4, -3, -3, -2, -2, -1, -1, -1,
	0, 0, 0, 0, 1, 1, 1, 2, 2, 2,
	3, 3, 3, 3, 4, 4, 4, 4, 4, 5,
	5, 5, 5, 5, 5, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9,
}

func legacyStatBonus(stat int) int {
	if stat < 0 {
		return -4
	}
	if stat >= len(bonusTable) {
		return 9
	}
	return bonusTable[stat]
}

// UpdatePlayerStatuses checks idle timeouts and runs player ticks.
func UpdatePlayerStatuses(world UpdatePlyWorld, t int64) {
	for _, active := range world.ActiveSessions() {
		sessionID := active.ID
		actorID := active.ActorID
		if actorID == "" {
			continue
		}

		creatureID, playerID, ok := world.SessionActor(sessionID)
		if !ok || playerID.IsZero() {
			continue
		}

		c, ok := world.Creature(creatureID)
		if !ok {
			continue
		}

		class := creatureClass(c)
		if class != legacyClassDM {
			if ltime, ok := world.SessionLastInputTime(sessionID); ok {
				if t-ltime > 300 {
					_ = world.WriteToSession(sessionID, "\r\n입력없이 5분이상 유지하면 접속이 끊어집니다.\r\n", false)
					_ = world.DisconnectSession(sessionID)
					continue
				}
			}
		}

		if err := tickPlayer(world, playerID, creatureID, sessionID, t); err != nil {
			// Failures in tick are non-fatal to other players
			println("ERROR ticking player:", playerID, err.Error())
		}
	}
}

func tickPlayer(world UpdatePlyWorld, playerID model.PlayerID, creatureID model.CreatureID, sessionID session.ID, t int64) error {
	if err := updateStatusExpirations(world, playerID, creatureID, sessionID, t); err != nil {
		return err
	}

	c, ok := world.Creature(creatureID)
	if !ok {
		return nil
	}

	// Auto-save check: Every 600 seconds
	_, saveUsable, _ := world.UseCreatureCooldown(creatureID, "effect:duration:PSAVE", t, 600)
	if saveUsable {
		_ = world.SavePlayer(playerID)
	}

	// Light decay check
	slot, obj, ok := hasLight(c, world)
	if ok && slot != "" {
		legacyType := objectLegacyType(world, obj)
		if legacyType == 12 { // LIGHTSOURCE
			charges, _ := objectFirstIntProperty(world, obj, "charges", "shotsCurrent", "shotscur")
			newCharges := charges - 1
			if newCharges < 0 {
				newCharges = 0
			}
			_, _ = world.SetObjectProperty(obj.ID, "shotsCurrent", strconv.Itoa(newCharges))
			if newCharges < 1 {
				objName := objectDisplayName(world, obj)
				_ = world.WriteToSession(sessionID, "\n당신의 "+objName+krtext.Particle(objName, '1')+" 꺼졌습니다.\n", false)
				playerName := creatureName(c)
				_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 "+objName+krtext.Particle(objName, '1')+" 꺼졌습니다.\n")
			}
		}
	}

	// HP/MP healing and poison/disease/harm room ticks
	_, healsUsable, _ := world.UseCreatureCooldown(creatureID, "effect:duration:HEALS", t, 0)
	if healsUsable {
		roomID := c.RoomID
		if roomID.IsZero() {
			return nil
		}
		room, ok := world.Room(roomID)
		if !ok {
			return nil
		}

		con := creatureStat(c, "constitution")
		intelligence := creatureStat(c, "intelligence")
		piety := creatureStat(c, "piety")
		class := creatureClass(c)

		ill := false
		if creatureHasAnyFlag(c, "PPOISN", "poison") || creatureHasAnyFlag(c, "PDISEA", "disease") {
			ill = true
		}

		hpCur := creatureStat(c, "hpCurrent")
		hpMax := creatureStat(c, "hpMax")
		mpCur := creatureStat(c, "mpCurrent")
		mpMax := creatureStat(c, "mpMax")

		var nextInterval int64 = 5

		if !roomHasAnyFlag(room, "RPHARM", "harm") && !ill {
			hpAdd := 5 + legacyStatBonus(con)
			if class == legacyClassBarbarian {
				hpAdd += 2
			}
			if hpAdd < 4 {
				hpAdd = 4
			}

			mpAdd := 5
			if class == legacyClassMage {
				mpAdd += 2
			}
			if intelligence > 17 {
				mpAdd += 1
			}
			if mpAdd < 4 {
				mpAdd = 4
			}

			newHp := hpCur + hpAdd
			newMp := mpCur + mpAdd

			nextInterval = 5

			if roomHasAnyFlag(room, "RHEALR", "healr", "healing") {
				newHp += 10
				newMp += 10
				nextInterval /= 3
				if nextInterval < 1 {
					nextInterval = 1
				}
			}

			if newHp > hpMax {
				newHp = hpMax
			}
			if newMp > mpMax {
				newMp = mpMax
			}

			_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
			_ = world.SetCreatureStat(c.ID, "mpCurrent", newMp)

		} else if !roomHasAnyFlag(room, "RPHARM", "harm") && ill {
			if creatureHasAnyFlag(c, "PPOISN", "poison") {
				_ = world.WriteToSession(sessionID, ansiCode(c, 5)+ansiCode(c, 31)+"\n독이 당신의 핏줄로 스며듭니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
				damage := mrand(1, hpMax/20) - legacyStatBonus(con)
				if damage < 1 {
					damage = 1
				}
				newHp := hpCur - damage
				_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
				hpCur = newHp

				nextInterval = int64(30 - 3*legacyStatBonus(con))
				if nextInterval < 1 {
					nextInterval = 1
				}

				if newHp < 1 {
					return handlePlayerDeath(world, playerID, c, room)
				}
			}
			if creatureHasAnyFlag(c, "PDISEA", "disease") {
				_ = world.WriteToSession(sessionID, ansiCode(c, 5)+ansiCode(c, 31)+"\n병이 당신의 마음을 잠식합니다."+ansiCode(c, 0), false)
				_ = world.SetCreatureCooldown(c.ID, "cooldown:attack", t, int64(dice(1, 6, 3)))
				_ = world.WriteToSession(sessionID, ansiCode(c, 34)+"몸이 피로해 집니다.\n"+ansiCode(c, 37)+ansiCode(c, 0), false)

				damage := mrand(1, 6) - legacyStatBonus(con)
				if damage < 1 {
					damage = 1
				}
				newHp := hpCur - damage
				_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
				hpCur = newHp

				nextInterval = int64(30 - 3*legacyStatBonus(con))
				if nextInterval < 1 {
					nextInterval = 1
				}

				if newHp < 1 {
					return handlePlayerDeath(world, playerID, c, room)
				}
			}
		} else {
			prot := true

			if roomHasAnyFlag(room, "RPPOIS", "pois", "poison") {
				_ = world.WriteToSession(sessionID, ansiCode(c, 5)+ansiCode(c, 32)+"\n독기운이 당신을 중독시킵니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
				_, _ = world.UpdatePlayerTags(playerID, []string{"PPOISN"}, nil)
			}

			if roomHasAnyFlag(room, "RPBEFU", "befu", "befuddle") {
				_ = world.WriteToSession(sessionID, "\n방이 빙글빙글 도는것 감습니다.\n이제 정신을 차립니다.", false)
				befInterval := dice(2, 6, 0)
				if befInterval < 6 {
					befInterval = 6
				}
				_ = world.SetCreatureCooldown(c.ID, "cooldown:attack", t, int64(befInterval))
			}

			c, _ = world.Creature(creatureID)
			hpCur = creatureStat(c, "hpCurrent")

			if creatureHasAnyFlag(c, "PPOISN", "poison") {
				_ = world.WriteToSession(sessionID, ansiCode(c, 5)+ansiCode(c, 31)+"\n독이 당신의 핏줄로 스며듭니다."+ansiCode(c, 0)+ansiCode(c, 37), false)
				damage := mrand(1, 4) - legacyStatBonus(con)
				if damage < 1 {
					damage = 1
				}
				newHp := hpCur - damage
				_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
				hpCur = newHp
				if newHp < 1 {
					return handlePlayerDeath(world, playerID, c, room)
				}
			}

			if creatureHasAnyFlag(c, "PDISEA", "disease") {
				_ = world.WriteToSession(sessionID, ansiCode(c, 5)+ansiCode(c, 31)+"\n병이 당신의 마음을 잠식합니다."+ansiCode(c, 0), false)
				_ = world.SetCreatureCooldown(c.ID, "cooldown:attack", t, int64(dice(1, 6, 3)))
				_ = world.WriteToSession(sessionID, ansiCode(c, 34)+"\n당신은 신경질적으로 됩니다."+ansiCode(c, 0), false)

				damage := mrand(1, 6) - legacyStatBonus(con)
				if damage < 1 {
					damage = 1
				}
				newHp := hpCur - damage
				_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
				hpCur = newHp

				nextInterval = int64(30 - 3*legacyStatBonus(con))
				if nextInterval < 1 {
					nextInterval = 1
				}

				if newHp < 1 {
					return handlePlayerDeath(world, playerID, c, room)
				}
			}

			if roomHasAnyFlag(room, "RPMPDR", "mpdr", "mpdrain") {
				drain := mpCur
				if drain > 3 {
					drain = 3
				}
				_ = world.SetCreatureStat(c.ID, "mpCurrent", mpCur-drain)
			} else if !ill {
				mpAdd := 2
				if class == legacyClassMage {
					mpAdd += 2
				}
				if intelligence > 17 {
					mpAdd += 1
				}
				if mpAdd < 1 {
					mpAdd = 1
				}
				newMp := mpCur + mpAdd
				if newMp > mpMax {
					newMp = mpMax
				}
				_ = world.SetCreatureStat(c.ID, "mpCurrent", newMp)
			}

			if roomHasAnyFlag(room, "RFIRER", "firer", "fire") && !creatureHasAnyFlag(c, "PRFIRE", "resist_fire") {
				_ = world.WriteToSession(sessionID, "\n뜨거운 기운이 당신을 태웁니다.", false)
				prot = false
			} else if roomHasAnyFlag(room, "RWATER", "water") && !creatureHasAnyFlag(c, "PBRWAT", "breath_water") {
				_ = world.WriteToSession(sessionID, "\n물이 당신의 폐로 흘러듭니다.", false)
				prot = false
			} else if roomHasAnyFlag(room, "REARTH", "earth") && !creatureHasAnyFlag(c, "PSSHLD", "shadow_shield") {
				_ = world.WriteToSession(sessionID, "\n흙이 무너져 당신을 덮칩니다.", false)
				prot = false
			} else if roomHasAnyFlag(room, "RWINDR", "windr", "wind") && !creatureHasAnyFlag(c, "PRCOLD", "resist_cold") {
				_ = world.WriteToSession(sessionID, ansiCode(c, 34)+"\n차가운 기운이 뼈속까지 스며듭니다."+ansiCode(c, 37), false)
				prot = false
			} else if !roomHasAnyFlag(room, "RWINDR", "windr", "wind") &&
				!roomHasAnyFlag(room, "REARTH", "earth") &&
				!roomHasAnyFlag(room, "RFIRER", "firer", "fire") &&
				!roomHasAnyFlag(room, "RWATER", "water") &&
				!roomHasAnyFlag(room, "RPPOIS", "pois", "poison") &&
				!roomHasAnyFlag(room, "RPBEFU", "befu", "befuddle") &&
				!roomHasAnyFlag(room, "RPMPDR", "mpdr", "mpdrain") {

				_ = world.WriteToSession(sessionID, ansiCode(c, 1)+ansiCode(c, 35)+"\n보이지않는 무엇이 당신의 생명력을 빨아들입니다."+ansiCode(c, 0)+ansiCode(c, 37), false)
				prot = false
			}

			if !prot {
				conBonus := legacyStatBonus(con)
				if conBonus > 2 {
					conBonus = 2
				}
				damage := 8 - conBonus
				newHp := hpCur - damage
				_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
				if newHp < 1 {
					return handlePlayerDeath(world, playerID, c, room)
				}
			} else if !ill {
				hpAdd := 3 + legacyStatBonus(con)
				if class == legacyClassBarbarian {
					hpAdd += 2
				}
				if hpAdd < 1 {
					hpAdd = 1
				}
				newHp := hpCur + hpAdd
				if newHp > hpMax {
					newHp = hpMax
				}
				_ = world.SetCreatureStat(c.ID, "hpCurrent", newHp)
			}

			nextInterval = 5 - 3*int64(legacyStatBonus(piety))
			if nextInterval < 1 {
				nextInterval = 1
			}

			_ = world.RecalculateTHACO(c.ID)
			_ = world.RecalculateAC(c.ID)
		}

		_ = world.SetCreatureCooldown(creatureID, "effect:duration:HEALS", t, nextInterval)
	}

	// Wimpy check
	if latest, ok := world.Creature(creatureID); ok && creatureHasAnyFlag(latest, "PWIMPY") {
		wimpyValue := latest.Stats["wimpyValue"]
		if wimpyValue == 0 {
			wimpyValue = 10
		}
		hpCur := latest.Stats["hpCurrent"]
		if hpCur > 0 && hpCur <= wimpyValue {
			roomID := latest.RoomID
			if !roomID.IsZero() {
				if room, ok := world.Room(roomID); ok {
					// Check if there is an active/visible threat in the room to avoid spamming flee
					hasThreat := false
					for _, cid := range room.CreatureIDs {
						if cid.IsZero() || cid == latest.ID {
							continue
						}
						if crt, ok := world.Creature(cid); ok {
							if crt.Kind != model.CreatureKindPlayer && crt.Stats["hpCurrent"] > 0 {
								hasThreat = true
								break
							}
						}
					}
					if hasThreat {
						if disp, ok := world.(interface {
							DispatchCommand(sessionID session.ID, playerID model.PlayerID, line string) error
						}); ok {
							_ = disp.DispatchCommand(sessionID, playerID, "도망")
						}
					}
				}
			}
		}
	}

	return nil
}

func handlePlayerDeath(world UpdatePlyWorld, playerID model.PlayerID, c model.Creature, room model.Room) error {
	hpMax := creatureStat(c, "hpMax")
	mpMax := creatureStat(c, "mpMax")
	mpCur := creatureStat(c, "mpCurrent")

	if err := world.SetCreatureStat(c.ID, "hpCurrent", hpMax); err != nil {
		return err
	}
	newMp := mpCur
	if mpMax/10 > newMp {
		newMp = mpMax / 10
	}
	if err := world.SetCreatureStat(c.ID, "mpCurrent", newMp); err != nil {
		return err
	}

	if _, err := world.UpdatePlayerTags(playerID, nil, []string{"PPOISN", "poison", "PDISEA", "disease"}); err != nil {
		return err
	}

	if err := world.MovePlayerToRoom(playerID, model.RoomID("room:1008")); err != nil {
		return err
	}

	_ = world.SavePlayer(playerID)

	sessionID := playerSessionID(world, playerID)
	if sessionID != "" {
		_ = world.WriteToSession(sessionID, "당신은 죽으면서 몇가지 물건을 떨어뜨렸습니다.\n", false)
	}

	if !roomHasAnyFlag(room, "RSUVIV", "survival") {
		playerName := creatureName(c)
		_ = world.BroadcastAll(fmt.Sprintf("\n### 애석하게도 %s님이 죽었습니다.", playerName))
	}

	// Package E: tight integration - boss death ends active family war (C parity with check_war_one + AT_WAR reset in creature.c:die)
	// Uses type assert to avoid expanding UpdatePlyWorld interface; restart-safe (war is runtime only).
	if creatureHasAnyFlag(c, "PFMBOS", "familyBoss", "fmbos", "PFMBOS") {
		fam := 0
		for _, k := range []string{"familyID", "dailyExpndMax", "legacyDailyExpndMax"} {
			if v := creatureStat(c, k); v > 0 {
				fam = v
				break
			}
		}
		if fam > 0 {
			if warWorld, ok := world.(interface{ FamilyAtWar(int) bool }); ok && warWorld.FamilyAtWar(fam) {
				_ = EndWar(world, "boss_death")
				famName := familyDisplayNameFrom(world, fam)
				_ = world.BroadcastAll(fmt.Sprintf("\n### [%s] 패거리의 문주가 죽었습니다.", famName))
				_ = world.BroadcastAll(fmt.Sprintf("\n### [%s] 패거리는 전쟁에서 패했습니다.", famName))
			}
		}
	}

	return nil
}

// HandlePermanentCreatureDeath mirrors the C die_perm_crt() post-processing
// that is safe to express at the game layer:
//   - updates the room permanent monster ltime when room properties expose a
//     perm_mon/permanentCreature slot,
//   - broadcasts MDEATH/deathDescription text,
//   - grants questnum rewards to the killing player,
//   - spawns the MSUMMO special creature through the world's spawn hook.
func HandlePermanentCreatureDeath(world PermanentCreatureDeathWorld, killerPlayerID model.PlayerID, deadCreatureID model.CreatureID, nowUnix int64) (PermanentCreatureDeathResult, error) {
	var result PermanentCreatureDeathResult
	if world == nil || deadCreatureID.IsZero() {
		return result, nil
	}

	dead, ok := world.Creature(deadCreatureID)
	if !ok {
		return result, nil
	}
	if !creatureHasAnyFlag(dead, "MPERMT", "permanent") {
		return result, nil
	}
	result.Permanent = true

	var killerCreature model.Creature
	var killerCreatureID model.CreatureID
	if !killerPlayerID.IsZero() {
		if player, ok := world.Player(killerPlayerID); ok {
			killerCreatureID = player.CreatureID
			if killerCreatureID.IsZero() {
				killerCreatureID = model.CreatureID(killerPlayerID)
			}
			killerCreature, _ = world.Creature(killerCreatureID)
		}
	}

	if room, ok := world.Room(dead.RoomID); ok {
		marked, err := markPermanentCreatureRespawn(world, room, dead, nowUnix)
		if err != nil {
			return result, err
		}
		result.RespawnMarked = marked
	}

	broadcasted, err := broadcastPermanentCreatureDeathDescription(world, dead)
	if err != nil {
		return result, err
	}
	result.DeathDescriptionBroadcast = broadcasted

	if !killerCreature.ID.IsZero() {
		questResult, err := applyPermanentCreatureQuestReward(world, killerPlayerID, killerCreature, dead)
		if err != nil {
			return result, err
		}
		result.QuestNumber = questResult.questNumber
		result.QuestCompleted = questResult.completed
		result.QuestAlreadyCompleted = questResult.alreadyCompleted
		result.QuestExperience = questResult.experience
	}

	summoned, summonedID, err := summonPermanentCreatureDeathFollower(world, killerCreature, dead)
	if err != nil {
		return result, err
	}
	result.SummonRequested = summoned
	result.SummonedCreatureID = summonedID

	if recorder, ok := world.(interface {
		RecordPermanentCreatureDeath(PermanentCreatureDeathEvent) error
	}); ok {
		err := recorder.RecordPermanentCreatureDeath(PermanentCreatureDeathEvent{
			WhenUnix:                  nowUnix,
			KillerPlayerID:            killerPlayerID,
			KillerCreatureID:          killerCreatureID,
			DeadCreatureID:            dead.ID,
			RoomID:                    dead.RoomID,
			RespawnMarked:             result.RespawnMarked,
			DeathDescriptionBroadcast: result.DeathDescriptionBroadcast,
			QuestNumber:               result.QuestNumber,
			QuestCompleted:            result.QuestCompleted,
			QuestAlreadyCompleted:     result.QuestAlreadyCompleted,
			QuestExperience:           result.QuestExperience,
			SummonedCreatureID:        result.SummonedCreatureID,
		})
		if err != nil {
			return result, err
		}
		result.PermanentDeathEventHookCalled = true
	}

	return result, nil
}

func markPermanentCreatureRespawn(world PermanentCreatureDeathWorld, room model.Room, dead model.Creature, nowUnix int64) (bool, error) {
	updater, ok := world.(interface {
		UpdateRoomProperty(model.RoomID, string, string) error
	})
	if !ok || room.ID.IsZero() {
		return false, nil
	}

	if key := strings.TrimSpace(dead.Properties["permanentRespawnLTimeKey"]); key != "" {
		return true, updater.UpdateRoomProperty(room.ID, key, strconv.FormatInt(nowUnix, 10))
	}

	for _, prefix := range []string{"perm_mon", "permMon", "permanentCreature"} {
		for i := 0; i < 10; i++ {
			if !permanentCreatureSlotMatches(room.Properties, prefix, i, dead, nowUnix) {
				continue
			}
			key := permanentCreatureSlotExistingKey(room.Properties, prefix, i, "ltime")
			if key == "" {
				key = fmt.Sprintf("%s.%d.ltime", prefix, i)
			}
			return true, updater.UpdateRoomProperty(room.ID, key, strconv.FormatInt(nowUnix, 10))
		}
	}

	if marked, err := markPermanentCreatureRespawnFromLegacyRoom(world, updater, room, dead, nowUnix); marked || err != nil {
		return marked, err
	}

	return false, nil
}

func markPermanentCreatureRespawnFromLegacyRoom(world PermanentCreatureDeathWorld, updater interface {
	UpdateRoomProperty(model.RoomID, string, string) error
}, room model.Room, dead model.Creature, nowUnix int64) (bool, error) {
	root := permanentDeathDBRoot(world)
	if root == "" {
		return false, nil
	}
	slots, err := legacyRoomPermanentCreatureSlots(root, room)
	if err != nil {
		return false, err
	}
	for _, slot := range slots {
		if slot.Misc <= 0 || slot.LTime+slot.Interval > nowUnix {
			continue
		}
		template, found, err := readLegacyCreaturePrototype(root, slot.Misc)
		if err != nil {
			return false, err
		}
		if !legacyPermanentCreatureSlotMatchesDead(slot, template, found, dead) {
			continue
		}
		prefix := fmt.Sprintf("perm_mon.%d", slot.Index)
		if err := updater.UpdateRoomProperty(room.ID, prefix+".misc", strconv.Itoa(slot.Misc)); err != nil {
			return false, err
		}
		if err := updater.UpdateRoomProperty(room.ID, prefix+".interval", strconv.FormatInt(slot.Interval, 10)); err != nil {
			return false, err
		}
		if found && template.Name != "" {
			if err := updater.UpdateRoomProperty(room.ID, prefix+".name", template.Name); err != nil {
				return false, err
			}
		}
		return true, updater.UpdateRoomProperty(room.ID, prefix+".ltime", strconv.FormatInt(nowUnix, 10))
	}
	return false, nil
}

func legacyPermanentCreatureSlotMatchesDead(slot legacyPermanentCreatureSlot, template legacyCreaturePrototype, found bool, dead model.Creature) bool {
	if found && normalizeCreatureName(template.Name) == normalizeCreatureName(creatureName(dead)) {
		return true
	}
	deadNumber := permanentCreatureLegacyNumber(dead)
	return deadNumber > 0 && deadNumber == slot.Misc
}

func permanentCreatureSlotMatches(properties map[string]string, prefix string, index int, dead model.Creature, nowUnix int64) bool {
	if len(properties) == 0 {
		return false
	}
	if permanentCreatureSlotFuture(properties, prefix, index, nowUnix) {
		return false
	}

	deadName := normalizeCreatureName(creatureName(dead))
	for _, field := range []string{"name", "displayName"} {
		if value, ok := permanentCreatureSlotProperty(properties, prefix, index, field); ok && normalizeCreatureName(value) == deadName {
			return true
		}
	}

	deadID := strings.TrimSpace(string(dead.ID))
	for _, field := range []string{"id", "creatureID", "creatureId"} {
		if value, ok := permanentCreatureSlotProperty(properties, prefix, index, field); ok && strings.TrimSpace(value) == deadID {
			return true
		}
	}

	deadPrototypeNumber := permanentCreatureLegacyNumber(dead)
	if deadPrototypeNumber > 0 {
		for _, field := range []string{"misc", "prototype", "prototypeNumber", "legacyNumber"} {
			if value, ok := permanentCreatureSlotProperty(properties, prefix, index, field); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n == deadPrototypeNumber {
					return true
				}
			}
		}
	}

	return false
}

func permanentCreatureSlotFuture(properties map[string]string, prefix string, index int, nowUnix int64) bool {
	ltime, hasLTime := permanentCreatureSlotInt(properties, prefix, index, "ltime")
	interval, hasInterval := permanentCreatureSlotInt(properties, prefix, index, "interval")
	if !hasLTime || !hasInterval || interval <= 0 {
		return false
	}
	return ltime+interval > nowUnix
}

func permanentCreatureSlotInt(properties map[string]string, prefix string, index int, field string) (int64, bool) {
	value, ok := permanentCreatureSlotProperty(properties, prefix, index, field)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func permanentCreatureSlotProperty(properties map[string]string, prefix string, index int, field string) (string, bool) {
	key := permanentCreatureSlotExistingKey(properties, prefix, index, field)
	if key == "" {
		return "", false
	}
	return properties[key], true
}

func permanentCreatureSlotExistingKey(properties map[string]string, prefix string, index int, field string) string {
	for _, key := range []string{
		fmt.Sprintf("%s.%d.%s", prefix, index, field),
		fmt.Sprintf("%s[%d].%s", prefix, index, field),
		fmt.Sprintf("%s.%02d.%s", prefix, index, field),
		fmt.Sprintf("%s[%02d].%s", prefix, index, field),
	} {
		if _, ok := properties[key]; ok {
			return key
		}
	}
	return ""
}

func permanentCreatureLegacyNumber(creature model.Creature) int {
	for _, key := range []string{"legacyNumber", "prototypeNumber", "sourceNumber", "misc"} {
		if val := creatureStat(creature, key); val > 0 {
			return val
		}
		if creature.Properties != nil {
			if n, err := strconv.Atoi(strings.TrimSpace(creature.Properties[key])); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func normalizeCreatureName(name string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
}

func broadcastPermanentCreatureDeathDescription(world PermanentCreatureDeathWorld, dead model.Creature) (bool, error) {
	text, ok, err := permanentCreatureDeathDescription(world, dead)
	if err != nil || !ok {
		return false, err
	}
	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return false, nil
	}
	if !strings.HasPrefix(text, "\n") {
		text = "\n" + text
	}
	return true, world.BroadcastRoom(dead.RoomID, "", text)
}

func permanentCreatureDeathDescription(world PermanentCreatureDeathWorld, dead model.Creature) (string, bool, error) {
	if provider, ok := world.(interface {
		CreatureDeathDescription(model.Creature) (string, bool, error)
	}); ok {
		text, found, err := provider.CreatureDeathDescription(dead)
		if err != nil || found {
			return text, found, err
		}
	}

	if dead.Properties != nil {
		for _, key := range []string{"deathDescriptionText", "mdeathDescription", "deathDescriptionBody"} {
			if text := strings.TrimSpace(dead.Properties[key]); text != "" {
				return text, true, nil
			}
		}
	}

	if !creatureHasAnyFlag(dead, "MDEATH", "deathDescription") {
		return "", false, nil
	}
	if text, found, err := permanentCreatureDeathDescriptionFromLegacyRoot(world, dead); err != nil || found {
		return text, found, err
	}
	return "", false, nil
}

func permanentCreatureDeathDescriptionFromLegacyRoot(world PermanentCreatureDeathWorld, dead model.Creature) (string, bool, error) {
	root := permanentDeathDBRoot(world)
	if root == "" {
		return "", false, nil
	}
	candidates := legacyDeathDescriptionFilenameCandidates(dead)
	for _, filename := range candidates {
		path := filepath.Join(root, "objmon", "ddesc", filename)
		data, err := os.ReadFile(path)
		if err == nil {
			text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Path: path, Field: "ddesc"}, data)
			if err != nil {
				return "", false, err
			}
			return text, true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("read death description %q: %w", path, err)
		}
	}
	return "", false, nil
}

func legacyDeathDescriptionFilenameCandidates(dead model.Creature) []string {
	level := creatureLegacyLevel(dead)
	if level <= 0 {
		return nil
	}
	names := []string{dead.DisplayName}
	for _, key := range []string{"name", "legacyRecordName", "key[0]", "key[1]", "key[2]"} {
		names = append(names, dead.Properties[key])
	}
	if keywords := strings.TrimSpace(dead.Properties["keywords"]); keywords != "" {
		names = append(names, strings.Split(keywords, "\n")...)
	}

	seen := map[string]struct{}{}
	filenames := make([]string, 0, len(names))
	for _, name := range names {
		filename, ok := legacyDeathDescriptionFilename(name, level)
		if !ok {
			continue
		}
		if _, exists := seen[filename]; exists {
			continue
		}
		seen[filename] = struct{}{}
		filenames = append(filenames, filename)
	}
	return filenames
}

func legacyDeathDescriptionFilename(name string, level int) (string, bool) {
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "_")
	if name == "" || level <= 0 {
		return "", false
	}
	return name + "_" + strconv.Itoa(level), true
}

func creatureLegacyLevel(creature model.Creature) int {
	if creature.Level > 0 {
		return creature.Level
	}
	for _, key := range []string{"level", "legacyLevel"} {
		if val := creatureStat(creature, key); val > 0 {
			return val
		}
		if creature.Properties != nil {
			if n, err := strconv.Atoi(strings.TrimSpace(creature.Properties[key])); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

type permanentQuestRewardResult struct {
	questNumber      int
	completed        bool
	alreadyCompleted bool
	experience       int
}

func applyPermanentCreatureQuestReward(world PermanentCreatureDeathWorld, killerPlayerID model.PlayerID, killer model.Creature, dead model.Creature) (permanentQuestRewardResult, error) {
	questNum := permanentCreatureQuestNumber(dead)
	result := permanentQuestRewardResult{questNumber: questNum}
	if questNum < 1 {
		return result, nil
	}

	sessionID := playerSessionID(world, killerPlayerID)
	if creatureQuestCompleted(killer, questNum) {
		result.alreadyCompleted = true
		if sessionID != "" {
			_ = world.WriteToSession(sessionID, "\n당신은 이미 임무를 달성하여 경험치를 받을 자격이 없습니다.\n", false)
		}
		return result, nil
	}

	if setter, ok := world.(interface {
		SetCreatureProperty(model.CreatureID, string, string) (model.Creature, error)
	}); ok {
		if _, err := setter.SetCreatureProperty(killer.ID, questCompletionKey(questNum), "1"); err != nil {
			return result, err
		}
	}

	xp := getQuestExpLocal(questNum - 1)
	if xp > 0 {
		if err := world.SetCreatureStat(killer.ID, "experience", creatureStat(killer, "experience")+xp); err != nil {
			return result, err
		}
		if err := addPermanentCreatureDeathProficiency(world, killer, xp); err != nil {
			return result, err
		}
	}

	if sessionID != "" {
		_ = world.WriteToSession(sessionID, "\n축하합니다. 당신은 임무를 달성하였습니다.\n", false)
		if xp > 0 {
			_ = world.WriteToSession(sessionID, fmt.Sprintf("당신은 경험치 %d을 얻었습니다.\n", xp), false)
		}
	}

	result.completed = true
	result.experience = xp
	return result, nil
}

func permanentCreatureQuestNumber(creature model.Creature) int {
	for _, key := range []string{"questNumber", "questnum", "questNum"} {
		if val := creatureStat(creature, key); val > 0 {
			return val
		}
		if creature.Properties != nil {
			if n, err := strconv.Atoi(strings.TrimSpace(creature.Properties[key])); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func addPermanentCreatureDeathProficiency(world PermanentCreatureDeathWorld, creature model.Creature, exp int) error {
	setter, ok := world.(interface {
		SetCreatureProperty(model.CreatureID, string, string) (model.Creature, error)
	})
	if !ok || exp <= 0 {
		return nil
	}
	part := exp / 9
	if part <= 0 {
		return nil
	}
	for _, key := range []string{"proficiency/sharp", "proficiency/thrust", "proficiency/blunt", "proficiency/pole", "proficiency/missile"} {
		val := 0
		if creature.Properties != nil {
			val, _ = strconv.Atoi(strings.TrimSpace(creature.Properties[key]))
		}
		if _, err := setter.SetCreatureProperty(creature.ID, key, strconv.Itoa(val+part)); err != nil {
			return err
		}
	}
	return nil
}

func summonPermanentCreatureDeathFollower(world PermanentCreatureDeathWorld, killer model.Creature, dead model.Creature) (bool, model.CreatureID, error) {
	if !creatureHasAnyFlag(dead, "MSUMMO", "summoner", "summon") {
		return false, "", nil
	}
	protoID := permanentCreatureSummonPrototypeID(dead)
	if protoID.IsZero() {
		return true, "", nil
	}
	spawner, ok := world.(interface {
		SpawnCreature(model.CreatureID, model.RoomID, bool) (model.CreatureID, error)
	})
	if !ok {
		return true, "", nil
	}
	if err := hydratePermanentCreatureSummonPrototype(world, protoID); err != nil {
		return true, "", err
	}
	summonedID, err := spawner.SpawnCreature(protoID, dead.RoomID, true)
	if err != nil {
		return true, "", err
	}
	if !killer.ID.IsZero() {
		if enemySetter, ok := world.(interface {
			AddCreatureEnemy(model.CreatureID, string) error
		}); ok {
			_ = enemySetter.AddCreatureEnemy(summonedID, creatureName(killer))
		} else if enemySetter, ok := world.(interface {
			AddEnemy(model.CreatureID, model.CreatureID) (bool, error)
		}); ok {
			_, _ = enemySetter.AddEnemy(summonedID, killer.ID)
		}
	}
	return true, summonedID, nil
}

func permanentCreatureSummonPrototypeID(creature model.Creature) model.CreatureID {
	if creature.Properties != nil {
		for _, key := range []string{"summonPrototypeID", "summonCreatureID", "legacySummonPrototypeID"} {
			if val := strings.TrimSpace(creature.Properties[key]); val != "" {
				return model.CreatureID(val)
			}
		}
	}
	special, ok := creatureStatValue(creature, "special")
	if !ok || special < 0 {
		return ""
	}
	return legacyCreaturePrototypeID(special)
}

func legacyCreaturePrototypeID(number int) model.CreatureID {
	if number < 0 {
		return ""
	}
	return model.CreatureID(fmt.Sprintf("creature:m%02d:%d", number/100, number%100))
}

type legacyCreaturePrototype struct {
	Number      int
	ID          model.CreatureID
	Name        string
	Description string
	Level       int
	Special     int
	QuestNumber int
	Flags       []byte
	Carry       [10]int
	Creature    model.Creature
}

const (
	legacyCreatureLevelOff        = 318
	legacyCreatureTypeOff         = 319
	legacyCreatureClassOff        = 320
	legacyCreatureRaceOff         = 321
	legacyCreatureNumWanderOff    = 322
	legacyCreatureAlignmentOff    = 324
	legacyCreatureStrengthOff     = 326
	legacyCreatureDexterityOff    = 327
	legacyCreatureConstitutionOff = 328
	legacyCreatureIntelligenceOff = 329
	legacyCreaturePietyOff        = 330
	legacyCreatureHPMaxOff        = 332
	legacyCreatureHPCurOff        = 334
	legacyCreatureMPMaxOff        = 336
	legacyCreatureMPCurOff        = 338
	legacyCreatureArmorOff        = 340
	legacyCreatureThacoOff        = 341
	legacyCreatureExperienceOff   = 344
	legacyCreatureGoldOff         = 348
	legacyCreatureNDiceOff        = 352
	legacyCreatureSDiceOff        = 354
	legacyCreaturePDiceOff        = 356
	legacyCreatureSpecialOff      = 358
	legacyCreatureQuestNumberOff  = 436
	legacyRoomPermMonOff          = 216
	legacyRoomPermMonCount        = 10
)

func readLegacyCreaturePrototype(root string, number int) (legacyCreaturePrototype, bool, error) {
	if strings.TrimSpace(root) == "" || number < 0 {
		return legacyCreaturePrototype{}, false, nil
	}
	path := filepath.Join(root, "objmon", fmt.Sprintf("m%02d", number/100))
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return legacyCreaturePrototype{}, false, nil
		}
		return legacyCreaturePrototype{}, false, fmt.Errorf("read legacy creature prototype %d: %w", number, err)
	}
	index := number % 100
	offset := index * cbin.CreatureSize
	if offset < 0 || offset+cbin.CreatureSize > len(data) {
		return legacyCreaturePrototype{}, false, nil
	}
	record, err := cbin.DecodeCreatureRecord(data[offset : offset+cbin.CreatureSize])
	if err != nil {
		return legacyCreaturePrototype{}, false, fmt.Errorf("decode legacy creature prototype %d: %w", number, err)
	}
	name := ""
	if record.Name.Err == nil {
		name = strings.TrimSpace(record.Name.Text)
	}
	id := legacyCreaturePrototypeID(number)
	description := legacyCreatureText(record.Description)
	stats := legacyCreatureStats(record.Raw)
	stats["legacyNumber"] = number
	var carry [10]int
	for i, value := range record.Carry {
		carry[i] = int(value)
		if value != 0 {
			stats[fmt.Sprintf("carry[%d]", i)] = int(value)
		}
	}
	properties := legacyCreatureProperties(record)
	rawFields := legacyCreatureRawFields(record)
	tags := legacyCreatureFlagTags(record.Flags[:])
	creatureName := name
	if creatureName == "" {
		creatureName = string(id)
	}
	creature := model.Creature{
		ID:          id,
		Kind:        model.CreatureKindMonster,
		DisplayName: creatureName,
		Description: description,
		Level:       legacyRawUint8(record.Raw, legacyCreatureLevelOff),
		Stats:       stats,
		Properties:  properties,
		Metadata: model.Metadata{
			Source:         "legacy",
			LegacyKind:     "creaturePrototype",
			LegacyID:       fmt.Sprintf("m%02d:%d", number/100, number%100),
			LegacyPath:     filepath.ToSlash(filepath.Join("objmon", fmt.Sprintf("m%02d", number/100))),
			LegacyEncoding: "euc-kr/cp949",
			RecordIndex:    index,
			RecordOffset:   int64(offset),
			RawFields:      rawFields,
			Tags:           tags,
		},
	}
	return legacyCreaturePrototype{
		Number:      number,
		ID:          id,
		Name:        name,
		Description: description,
		Level:       creature.Level,
		Special:     stats["special"],
		QuestNumber: stats["questNumber"],
		Flags:       append([]byte(nil), record.Flags[:]...),
		Carry:       carry,
		Creature:    creature,
	}, true, nil
}

func hydratePermanentCreatureSummonPrototype(world PermanentCreatureDeathWorld, protoID model.CreatureID) error {
	updater, ok := world.(interface {
		UpdateCreature(model.Creature) error
	})
	if !ok {
		return nil
	}
	number, ok := legacyCreatureNumberFromPrototypeID(protoID)
	if !ok {
		return nil
	}
	root := permanentDeathDBRoot(world)
	if root == "" {
		return nil
	}
	template, found, err := readLegacyCreaturePrototype(root, number)
	if err != nil || !found {
		return err
	}
	return updater.UpdateCreature(template.Creature)
}

func legacyCreatureNumberFromPrototypeID(id model.CreatureID) (int, bool) {
	value := strings.TrimSpace(string(id))
	if !strings.HasPrefix(value, "creature:m") {
		return 0, false
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 || !strings.HasPrefix(parts[1], "m") {
		return 0, false
	}
	fileNum, err := strconv.Atoi(strings.TrimPrefix(parts[1], "m"))
	if err != nil || fileNum < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil || index < 0 {
		return 0, false
	}
	return fileNum*100 + index, true
}

func legacyCreatureStats(raw []byte) map[string]int {
	return map[string]int{
		"legacyType":   legacyRawInt8(raw, legacyCreatureTypeOff),
		"class":        legacyRawInt8(raw, legacyCreatureClassOff),
		"race":         legacyRawInt8(raw, legacyCreatureRaceOff),
		"numWander":    legacyRawInt8(raw, legacyCreatureNumWanderOff),
		"alignment":    legacyRawInt16(raw, legacyCreatureAlignmentOff),
		"strength":     legacyRawInt8(raw, legacyCreatureStrengthOff),
		"dexterity":    legacyRawInt8(raw, legacyCreatureDexterityOff),
		"constitution": legacyRawInt8(raw, legacyCreatureConstitutionOff),
		"intelligence": legacyRawInt8(raw, legacyCreatureIntelligenceOff),
		"piety":        legacyRawInt8(raw, legacyCreaturePietyOff),
		"hpMax":        legacyRawInt16(raw, legacyCreatureHPMaxOff),
		"hpCurrent":    legacyRawInt16(raw, legacyCreatureHPCurOff),
		"mpMax":        legacyRawInt16(raw, legacyCreatureMPMaxOff),
		"mpCurrent":    legacyRawInt16(raw, legacyCreatureMPCurOff),
		"armor":        legacyRawInt8(raw, legacyCreatureArmorOff),
		"thaco":        legacyRawInt8(raw, legacyCreatureThacoOff),
		"experience":   int(legacyRawInt32(raw, legacyCreatureExperienceOff)),
		"gold":         int(legacyRawInt32(raw, legacyCreatureGoldOff)),
		"nDice":        legacyRawInt16(raw, legacyCreatureNDiceOff),
		"sDice":        legacyRawInt16(raw, legacyCreatureSDiceOff),
		"pDice":        legacyRawInt16(raw, legacyCreaturePDiceOff),
		"special":      legacyRawInt16(raw, legacyCreatureSpecialOff),
		"questNumber":  legacyRawInt8(raw, legacyCreatureQuestNumberOff),
	}
}

func legacyCreatureProperties(record cbin.CreatureRecord) map[string]string {
	properties := map[string]string{}
	if text := legacyCreatureText(record.Talk); text != "" {
		properties["legacyTalk"] = text
	}
	if text := legacyCreatureText(record.Password); text != "" {
		properties["legacyPassword"] = text
	}
	keywords := make([]string, 0, len(record.Keys))
	for _, key := range record.Keys {
		if text := legacyCreatureText(key); text != "" {
			keywords = append(keywords, text)
		}
	}
	if len(keywords) > 0 {
		properties["keywords"] = strings.Join(keywords, "\n")
	}
	if len(properties) == 0 {
		return nil
	}
	return properties
}

func legacyCreatureText(field cbin.TextField) string {
	if field.Err != nil {
		return ""
	}
	return strings.TrimSpace(field.Text)
}

func legacyCreatureRawFields(record cbin.CreatureRecord) map[string][]byte {
	fields := map[string][]byte{
		"flags": append([]byte(nil), record.Flags[:]...),
	}
	for i, carry := range record.Carry {
		if carry == 0 {
			continue
		}
		var raw [2]byte
		binary.LittleEndian.PutUint16(raw[:], uint16(carry))
		fields[fmt.Sprintf("carry[%d]", i)] = append([]byte(nil), raw[:]...)
	}
	return fields
}

func legacyCreatureFlagTags(flags []byte) []string {
	names := []string{
		"MPERMT", "MHIDDN", "MINVIS", "MTOMEN", "MDROPS", "MNOPRE", "MAGGRE", "MGUARD",
		"MBLOCK", "MFOLLO", "MFLEER", "MSCAVE", "MMALES", "MPOISS", "MUNDED", "MUNSTL",
		"MPOISN", "MMAGIC", "MHASSC", "MBRETH", "MMGONL", "MDINVI", "MENONL", "MTALKS",
		"MUNKIL", "MNRGLD", "MTLKAG", "MRMAGI", "MBRWP1", "MBRWP2", "MENEDR", "MKNGDM",
		"MPLDGK", "MRSCND", "MDISEA", "MDISIT", "MPURIT", "MTRADE", "MPGUAR", "MGAGGR",
		"MEAGGR", "MDEATH", "MMAGIO", "MRBEFD", "MNOCIR", "MBLNDR", "MDMFOL", "MFEARS",
		"MSILNC", "MBLIND", "MCHARM", "MBEFUD", "MKNDM1", "MKNDM2", "MKNDM3", "MKNDM4",
		"MKING1", "MKING2", "MKING3", "MKING4", "MSAYTLK", "MSUMMO", "MNOCHA",
	}
	tags := make([]string, 0)
	for bit, name := range names {
		if bit/8 < len(flags) && flags[bit/8]&(1<<uint(bit%8)) != 0 {
			tags = append(tags, name)
		}
	}
	return tags
}

type legacyPermanentCreatureSlot struct {
	Index    int
	Interval int64
	LTime    int64
	Misc     int
}

func legacyRoomPermanentCreatureSlots(root string, room model.Room) ([]legacyPermanentCreatureSlot, error) {
	path := legacyRoomFilePath(root, room)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read legacy room %q: %w", path, err)
	}
	if len(data) < legacyRoomPermMonOff+legacyRoomPermMonCount*cbin.LasttimeSize {
		return nil, nil
	}
	slots := make([]legacyPermanentCreatureSlot, 0, legacyRoomPermMonCount)
	for i := 0; i < legacyRoomPermMonCount; i++ {
		offset := legacyRoomPermMonOff + i*cbin.LasttimeSize
		misc := legacyRawInt16(data, offset+8)
		if misc == 0 {
			continue
		}
		slots = append(slots, legacyPermanentCreatureSlot{
			Index:    i,
			Interval: int64(legacyRawInt32(data, offset)),
			LTime:    int64(legacyRawInt32(data, offset+4)),
			Misc:     misc,
		})
	}
	return slots, nil
}

func legacyRoomFilePath(root string, room model.Room) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if path := strings.TrimSpace(room.Metadata.LegacyPath); path != "" {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(root, filepath.FromSlash(path))
	}
	number := legacyRoomNumberFromID(room.ID)
	if number < 0 {
		return ""
	}
	return filepath.Join(root, "rooms", fmt.Sprintf("r%02d", number/1000), fmt.Sprintf("r%05d", number))
}

func legacyRoomNumberFromID(roomID model.RoomID) int {
	value := strings.TrimSpace(string(roomID))
	value = strings.TrimPrefix(value, "room:")
	value = strings.TrimPrefix(value, "r")
	if value == "" {
		return -1
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

func permanentDeathDBRoot(world PermanentCreatureDeathWorld) string {
	if provider, ok := world.(interface{ DBRoot() string }); ok {
		return strings.TrimSpace(provider.DBRoot())
	}
	return ""
}

func legacyRawUint8(data []byte, off int) int {
	if off < 0 || off >= len(data) {
		return 0
	}
	return int(data[off])
}

func legacyRawInt8(data []byte, off int) int {
	if off < 0 || off >= len(data) {
		return 0
	}
	return int(int8(data[off]))
}

func legacyRawInt16(data []byte, off int) int {
	if off < 0 || off+2 > len(data) {
		return 0
	}
	return int(int16(binary.LittleEndian.Uint16(data[off : off+2])))
}

func legacyRawInt32(data []byte, off int) int32 {
	if off < 0 || off+4 > len(data) {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(data[off : off+4]))
}

func (l *Loop) SetCreatureProperty(creatureID model.CreatureID, key string, value string) (model.Creature, error) {
	if l.world == nil {
		return model.Creature{}, fmt.Errorf("world is nil")
	}
	return l.world.SetCreatureProperty(creatureID, key, value)
}

func (l *Loop) UpdateRoomProperty(roomID model.RoomID, key string, value string) error {
	if l.world == nil {
		return fmt.Errorf("world is nil")
	}
	return l.world.UpdateRoomProperty(roomID, key, value)
}

func (l *Loop) SpawnCreature(protoID model.CreatureID, roomID model.RoomID, carryItems bool) (model.CreatureID, error) {
	if l.world == nil {
		return "", fmt.Errorf("world is nil")
	}
	return l.world.SpawnCreature(protoID, roomID, carryItems)
}

func (l *Loop) DBRoot() string {
	if l == nil || l.world == nil {
		return ""
	}
	return l.world.DBRoot()
}

func updateStatusExpirations(world UpdatePlyWorld, playerID model.PlayerID, creatureID model.CreatureID, sessionID session.ID, t int64) error {
	c, ok := world.Creature(creatureID)
	if !ok {
		return nil
	}

	// Enchantment item expiration check
	var itemsToCheck []model.ObjectInstanceID
	for _, objID := range c.Inventory.ObjectIDs {
		if !objID.IsZero() {
			itemsToCheck = append(itemsToCheck, objID)
		}
	}
	for _, objID := range c.Equipment {
		if !objID.IsZero() {
			itemsToCheck = append(itemsToCheck, objID)
		}
	}

	for _, objID := range itemsToCheck {
		obj, ok := world.Object(objID)
		if !ok {
			continue
		}
		if expireStr, ok := obj.Properties["enchant_expire_at"]; ok {
			if expireVal, err := strconv.ParseInt(expireStr, 10, 64); err == nil {
				if t >= expireVal {
					// Restore original stats if backed up
					origProps := []string{"armor", "pDice", "shotsMax", "shotsCurrent", "value", "adjustment"}
					for _, prop := range origProps {
						origKey := "orig_" + prop
						if origVal, ok := obj.Properties[origKey]; ok {
							_, _ = world.SetObjectProperty(obj.ID, prop, origVal)
							_, _ = world.SetObjectProperty(obj.ID, origKey, "")
						}
					}
					_, _ = world.SetObjectProperty(obj.ID, "enchant_expire_at", "")

					// Remove tags
					if tagUpdater, ok := world.(interface {
						UpdateObjectTags(model.ObjectInstanceID, []string, []string) (model.ObjectInstance, error)
					}); ok {
						_, _ = tagUpdater.UpdateObjectTags(obj.ID, nil, []string{"enchanted", "oencha"})
					}

					// Message to player
					objName := obj.DisplayNameOverride
					if objName == "" {
						if proto, ok := world.ObjectPrototype(obj.PrototypeID); ok {
							objName = proto.DisplayName
						}
					}
					if objName == "" {
						objName = "장비"
					}
					_ = world.WriteToSession(sessionID, ansiCode(c, 32)+fmt.Sprintf("\n%s에 깃든 주술의 기운이 사라졌습니다.\n", objName)+ansiCode(c, 37)+ansiCode(c, 0), false)

					_ = world.RecalculateAC(c.ID)
					_ = world.RecalculateTHACO(c.ID)
				}
			}
		}
	}

	level := creatureStat(c, "level")

	checkExpiry := func(tag string, defaultDur int64, onExpire func() error) error {
		if !creatureHasAnyFlag(c, tag) {
			world.DeleteEffectExpiration(c.ID, tag)
			return nil
		}
		expires, ok := world.GetEffectExpiration(c.ID, tag)
		if !ok {
			world.SetEffectExpiration(c.ID, tag, t+defaultDur)
			return nil
		}
		if t >= expires {
			world.DeleteEffectExpiration(c.ID, tag)
			aliases := []string{tag}
			if extra, ok := state.FlagAliasMap[strings.ToLower(tag)]; ok {
				aliases = append(aliases, extra...)
			}
			_, err := world.UpdatePlayerTags(playerID, nil, aliases)
			if err != nil {
				return err
			}
			if tagUpdater, ok := world.(interface {
				UpdateCreatureTags(model.CreatureID, []string, []string) (model.Creature, error)
			}); ok {
				_, err = tagUpdater.UpdateCreatureTags(c.ID, nil, aliases)
				if err != nil {
					return err
				}
			}
			return onExpire()
		}
		return nil
	}

	class := creatureClass(c)

	// PHASTE
	hasteDur := int64(120 + 60*(((level+3)/4)/5))
	if err := checkExpiry("PHASTE", hasteDur, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n당신의 몸이 느려졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		dex := creatureStat(c, "dexterity")
		_ = world.SetCreatureStat(c.ID, "dexterity", dex-15)
		return world.RecalculateAC(c.ID)
	}); err != nil {
		return err
	}

	// PPOWER
	powerDur := int64(120 + 60*(((level+3)/4)/5))
	if err := checkExpiry("PPOWER", powerDur, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n당신의 힘이 약해졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		str := creatureStat(c, "strength")
		_ = world.SetCreatureStat(c.ID, "strength", str-3)
		return world.RecalculateAC(c.ID)
	}); err != nil {
		return err
	}

	// PUPDMG
	if err := checkExpiry("PUPDMG", 120, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n당신의 기가 빠져나갑니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		pdice := creatureStat(c, "pdice")
		hpmax := creatureStat(c, "hpMax")
		mpmax := creatureStat(c, "mpMax")
		if class < legacyClassInvincible {
			_ = world.SetCreatureStat(c.ID, "pdice", pdice-2)
			_ = world.SetCreatureStat(c.ID, "hpMax", hpmax-50)
			_ = world.SetCreatureStat(c.ID, "mpMax", mpmax-20)
		} else {
			_ = world.SetCreatureStat(c.ID, "pdice", pdice-3)
			_ = world.SetCreatureStat(c.ID, "hpMax", hpmax-100)
			_ = world.SetCreatureStat(c.ID, "mpMax", mpmax-100)
		}
		return world.RecalculateAC(c.ID)
	}); err != nil {
		return err
	}

	// PSLAYE
	slayeDur := int64(150 + 60*(((level+3)/4)/5))
	if err := checkExpiry("PSLAYE", slayeDur, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n당신의 무기가 살기를 잃었습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		_ = world.RecalculateTHACO(c.ID)
		return world.RecalculateAC(c.ID)
	}); err != nil {
		return err
	}

	// PMEDIT
	meditDur := int64(150 + 60*(((level+3)/4)/5))
	if err := checkExpiry("PMEDIT", meditDur, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n참선의 영향력이 떨어졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		intel := creatureStat(c, "intelligence")
		_ = world.SetCreatureStat(c.ID, "intelligence", intel-3)
		return world.RecalculateAC(c.ID)
	}); err != nil {
		return err
	}

	// PPRAYD
	if err := checkExpiry("PPRAYD", 500, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n당신의 믿음이 약해졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		piety := creatureStat(c, "piety")
		_ = world.SetCreatureStat(c.ID, "piety", piety-5)
		return nil
	}); err != nil {
		return err
	}

	// PINVIS
	if class < legacyClassDM {
		if err := checkExpiry("PINVIS", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 35)+"\n당신은 이제 눈에 보입니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			return nil
		}); err != nil {
			return err
		}
	}

	// PDINVI
	if class < legacyClassDM {
		if err := checkExpiry("PDINVI", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 35)+"\n당신의 눈이 침침해졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			return nil
		}); err != nil {
			return err
		}
	}

	// PDMAGI
	if class < legacyClassDM {
		if err := checkExpiry("PDMAGI", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 35)+"\n당신의 감지력이 떨어졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			return nil
		}); err != nil {
			return err
		}
	}

	// PHIDDN
	if err := checkExpiry("PHIDDN", 300, func() error {
		return nil
	}); err != nil {
		return err
	}

	// PPROTE
	if err := checkExpiry("PPROTE", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n당신의 보호력이 떨어졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return world.RecalculateAC(c.ID)
	}); err != nil {
		return err
	}

	// PLEVIT
	if class < legacyClassDM {
		if err := checkExpiry("PLEVIT", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 35)+"\n당신은 땅에 내려섰습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			return nil
		}); err != nil {
			return err
		}
	}

	// PBLESS
	if err := checkExpiry("PBLESS", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n축복력이 떨어졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return world.RecalculateTHACO(c.ID)
	}); err != nil {
		return err
	}

	// PRFIRE
	if err := checkExpiry("PRFIRE", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n당신의 피부가 돌아왔습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PRCOLD
	if err := checkExpiry("PRCOLD", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 1)+ansiCode(c, 33)+"\n차가운 기운이 몸을 휩쌉니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PBRWAT
	if err := checkExpiry("PBRWAT", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 1)+ansiCode(c, 34)+"\n당신의 폐가 줄어들었습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PSSHLD
	if err := checkExpiry("PSSHLD", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 1)+ansiCode(c, 32)+"\n당신의 주술 방패가 사라졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PFLYSP
	if class < legacyClassDM {
		if err := checkExpiry("PFLYSP", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n당신은 더이상 날수 없습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			return nil
		}); err != nil {
			return err
		}
	}

	// PRMAGI
	if err := checkExpiry("PRMAGI", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 1)+ansiCode(c, 35)+"\n마법의 방어력이 사라졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PSILNC
	if err := checkExpiry("PSILNC", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n당신의 목소리를 되찾았습니다!"+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PFEARS
	if err := checkExpiry("PFEARS", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n당신은 용기를 되찾았습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PKNOWA
	if class < legacyClassDM {
		if err := checkExpiry("PKNOWA", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 36)+"\n당신의 분별력이 감퇴되었습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			return nil
		}); err != nil {
			return err
		}
	}

	// PLIGHT
	if class < legacyClassDM {
		if err := checkExpiry("PLIGHT", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n마법의 빛이 사라졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			playerName := creatureName(c)
			_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 마법의 빛이 사라졌습니다.")
			return nil
		}); err != nil {
			return err
		}
	}

	// PCHARM
	if err := checkExpiry("PCHARM", 1200, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n당신의 행동이 정상적으로 되었습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		return nil
	}); err != nil {
		return err
	}

	// PANGEL
	if class < legacyClassDM {
		if err := checkExpiry("PANGEL", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n정령이 당신의 몸에서 떠나갑니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			playerName := creatureName(c)
			_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 정령이 사라졌습니다..")
			return nil
		}); err != nil {
			return err
		}
	}

	// PREFLECT
	if class < legacyClassDM {
		if err := checkExpiry("PREFLECT", 1200, func() error {
			_ = world.WriteToSession(sessionID, ansiCode(c, 32)+"\n당신의 반탄강기가 풀렸습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
			playerName := creatureName(c)
			_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 반탄강기가 풀렸습니다.")
			_ = world.RecalculateAC(c.ID)
			_ = world.RecalculateTHACO(c.ID)
			return nil
		}); err != nil {
			return err
		}
	}

	// PSHADOW
	if err := checkExpiry("PSHADOW", 300, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 36)+"\n당신의 분신들이 사라졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		playerName := creatureName(c)
		_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 분신들이 사라졌습니다.")
		_ = world.SetCreatureStat(c.ID, "shadowClones", 0)
		_, _ = world.UpdatePlayerTags(playerID, nil, []string{"shadow", "shadowClone"})
		_ = world.RecalculateAC(c.ID)
		_ = world.RecalculateTHACO(c.ID)
		return nil
	}); err != nil {
		return err
	}

	// PABSORB
	if err := checkExpiry("PABSORB", 600, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 35)+"\n당신의 흡성대법 기운이 사라졌습니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		playerName := creatureName(c)
		_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 흡성대법 기운이 사라졌습니다.")
		_, _ = world.UpdatePlayerTags(playerID, nil, []string{"absorb"})
		_ = world.RecalculateAC(c.ID)
		_ = world.RecalculateTHACO(c.ID)
		return nil
	}); err != nil {
		return err
	}

	// PCHOI
	if err := checkExpiry("PCHOI", 120, func() error {
		_ = world.WriteToSession(sessionID, ansiCode(c, 33)+"\n최루탄의 매운 기운이 가셨습니다. 이제 앞이 잘 보입니다."+ansiCode(c, 37)+ansiCode(c, 0), false)
		playerName := creatureName(c)
		_ = world.BroadcastRoom(c.RoomID, sessionID, "\n"+playerName+"의 최루탄 효과가 사라졌습니다.")
		_, _ = world.UpdatePlayerTags(playerID, nil, []string{"choi"})
		_ = world.RecalculateAC(c.ID)
		_ = world.RecalculateTHACO(c.ID)
		return nil
	}); err != nil {
		return err
	}

	return nil
}

// Helpers
func creatureStat(c model.Creature, key string) int {
	value, _ := creatureStatValue(c, key)
	return value
}

func creatureStatValue(c model.Creature, key string) (int, bool) {
	if c.Stats != nil {
		if value, ok := c.Stats[key]; ok {
			return value, true
		}
	}
	if c.Properties != nil {
		if raw, ok := c.Properties[key]; ok {
			return parseObjectInt(raw)
		}
	}
	target := normalizeFlagName(key)
	if target == "" {
		return 0, false
	}
	for statKey, value := range c.Stats {
		if normalizeFlagName(statKey) == target {
			return value, true
		}
	}
	for propertyKey, raw := range c.Properties {
		if normalizeFlagName(propertyKey) == target {
			return parseObjectInt(raw)
		}
	}
	return 0, false
}

func creatureClass(c model.Creature) int {
	return creatureStat(c, "class")
}

func creatureName(c model.Creature) string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return "무명"
}

func cleanDisplayText(text string) string {
	return strings.TrimSpace(text)
}

func looksLikeInternalObjectID(name string) bool {
	return strings.HasPrefix(name, "object:") || strings.HasPrefix(name, "objinst:")
}

func firstObjectKeyName(properties map[string]string) string {
	for _, key := range []string{"key[0]", "key[1]", "key[2]"} {
		if name := cleanDisplayText(properties[key]); name != "" {
			return name
		}
	}
	return ""
}

func objectDisplayName(world UpdatePlyWorld, object model.ObjectInstance) string {
	if name := cleanDisplayText(object.DisplayNameOverride); name != "" {
		return name
	}
	if name := cleanDisplayText(object.Properties["name"]); name != "" {
		return name
	}
	if !object.PrototypeID.IsZero() {
		if proto, ok := world.ObjectPrototype(object.PrototypeID); ok {
			if name := cleanDisplayText(proto.DisplayName); name != "" && !looksLikeInternalObjectID(name) {
				return name
			}
			if name := cleanDisplayText(proto.Properties["name"]); name != "" {
				return name
			}
			if name := firstObjectKeyName(proto.Properties); name != "" {
				return name
			}
		}
	}
	if name := firstObjectKeyName(object.Properties); name != "" {
		return name
	}
	return string(object.ID)
}

func parseObjectInt(val string) (int, bool) {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0, false
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return 0, false
	}
	return i, true
}

func playerSessionID(world interface {
	ActiveSessions() []ActiveSession
}, playerID model.PlayerID) session.ID {
	for _, active := range world.ActiveSessions() {
		if active.ActorID == string(playerID) {
			return active.ID
		}
	}
	return ""
}

func creatureHasAnyFlag(creature model.Creature, names ...string) bool {
	if hasAnyNormalizedFlag(creature.Metadata.Tags, names...) {
		return true
	}
	if creatureHasAnyLegacyRawFlag(creature, names...) {
		return true
	}
	targets := normalizedFlagSet(names...)
	for key, value := range creature.Stats {
		if value == 0 {
			continue
		}
		if _, ok := targets[normalizeFlagName(key)]; ok {
			return true
		}
	}
	for key, value := range creature.Properties {
		normalizedKey := normalizeFlagName(key)
		if _, ok := targets[normalizedKey]; ok && propertyFlagEnabled(value) {
			return true
		}
		for _, token := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ';' || r == '|' || r == ' '
		}) {
			if _, ok := targets[normalizeFlagName(token)]; ok {
				return true
			}
		}
	}
	return false
}

func creatureHasAnyLegacyRawFlag(creature model.Creature, names ...string) bool {
	flags := creature.Metadata.RawFields["flags"]
	if len(flags) == 0 {
		return false
	}
	bits := map[string]int{
		normalizeFlagName("MPERMT"):           0,
		normalizeFlagName("permanent"):        0,
		normalizeFlagName("MDEATH"):           41,
		normalizeFlagName("deathDescription"): 41,
		normalizeFlagName("MSUMMO"):           61,
		normalizeFlagName("summoner"):         61,
		normalizeFlagName("summon"):           61,
	}
	for _, name := range names {
		bit, ok := bits[normalizeFlagName(name)]
		if !ok {
			continue
		}
		if bit/8 < len(flags) && flags[bit/8]&(1<<uint(bit%8)) != 0 {
			return true
		}
	}
	return false
}

func roomHasAnyFlag(room model.Room, names ...string) bool {
	if hasAnyNormalizedFlag(room.Metadata.Tags, names...) {
		return true
	}
	if len(room.Properties) == 0 {
		return false
	}
	targets := normalizedFlagSet(names...)
	for key, value := range room.Properties {
		normalizedKey := normalizeFlagName(key)
		if _, ok := targets[normalizedKey]; ok && propertyFlagEnabled(value) {
			return true
		}
		for _, token := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ';' || r == '|' || r == ' '
		}) {
			if _, ok := targets[normalizeFlagName(token)]; ok {
				return true
			}
		}
	}
	return false
}

func hasAnyNormalizedFlag(tags []string, names ...string) bool {
	if len(tags) == 0 || len(names) == 0 {
		return false
	}
	targets := normalizedFlagSet(names...)
	for _, tag := range tags {
		if _, ok := targets[normalizeFlagName(tag)]; ok {
			return true
		}
	}
	return false
}

func normalizedFlagSet(names ...string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, name := range state.ExpandFlagNames(names...) {
		set[name] = struct{}{}
	}
	return set
}

func normalizeFlagName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, " ", "")
	return name
}

func mrand(min, max int) int {
	if max <= min {
		return min
	}
	return rand.Intn(max-min+1) + min
}

func dice(num, size, plus int) int {
	sum := 0
	for i := 0; i < num; i++ {
		sum += rand.Intn(size) + 1
	}
	return sum + plus
}

func hasLight(c model.Creature, world UpdatePlyWorld) (string, model.ObjectInstance, bool) {
	slots := []string{
		"body", "arms", "legs", "hands", "head", "feet", "shield", "face",
		"neck1", "neck2", "held", "wield",
		"finger1", "finger2", "finger3", "finger4", "finger5", "finger6", "finger7", "finger8",
	}
	for _, slot := range slots {
		if objID := c.Equipment[slot]; !objID.IsZero() {
			if obj, ok := world.Object(objID); ok {
				if objectHasAnyTag(world, obj, "OLIGHT", "light") {
					legacyType := objectLegacyType(world, obj)
					if legacyType == 12 { // LIGHTSOURCE
						charges, ok := objectFirstIntProperty(world, obj, "charges", "shotsCurrent", "shotscur")
						if !ok || charges > 0 {
							return slot, obj, true
						}
					} else {
						return slot, obj, true
					}
				}
			}
		}
	}
	if creatureHasAnyFlag(c, "PLIGHT", "light") {
		return "", model.ObjectInstance{}, true
	}
	return "", model.ObjectInstance{}, false
}

func objectHasAnyTag(world UpdatePlyWorld, obj model.ObjectInstance, tags ...string) bool {
	if hasAnyNormalizedFlag(obj.Metadata.Tags, tags...) {
		return true
	}
	targets := normalizedFlagSet(tags...)
	if talkPropertiesHaveAnyFlag(obj.Properties, targets) {
		return true
	}
	if !obj.PrototypeID.IsZero() {
		if proto, ok := world.ObjectPrototype(obj.PrototypeID); ok {
			if hasAnyNormalizedFlag(proto.Metadata.Tags, tags...) {
				return true
			}
			if talkPropertiesHaveAnyFlag(proto.Properties, targets) {
				return true
			}
		}
	}
	return false
}

func objectLegacyType(world UpdatePlyWorld, object model.ObjectInstance) int {
	if value, ok := objectIntProperty(world, object, "type"); ok {
		return value
	}
	return -1
}

func objectIntProperty(world UpdatePlyWorld, object model.ObjectInstance, key string) (int, bool) {
	if value, ok := parseObjectInt(object.Properties[key]); ok {
		return value, true
	}
	if !object.PrototypeID.IsZero() {
		if proto, ok := world.ObjectPrototype(object.PrototypeID); ok {
			if value, ok := parseObjectInt(proto.Properties[key]); ok {
				return value, true
			}
		}
	}
	return 0, false
}

func objectFirstIntProperty(world UpdatePlyWorld, object model.ObjectInstance, keys ...string) (int, bool) {
	for _, key := range keys {
		if value, ok := parseObjectInt(object.Properties[key]); ok {
			return value, true
		}
	}
	if object.PrototypeID.IsZero() {
		return 0, false
	}
	proto, ok := world.ObjectPrototype(object.PrototypeID)
	if !ok {
		return 0, false
	}
	for _, key := range keys {
		if value, ok := parseObjectInt(proto.Properties[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func ansiCode(c model.Creature, colorCode int) string {
	if !creatureHasAnyFlag(c, "ANSIC", "PANSIC") {
		return ""
	}
	if colorCode == 0 {
		return "\x1b[0m"
	}
	bright := 0
	if creatureHasAnyFlag(c, "PBRIGH", "bright") && colorCode != 37 {
		bright = 1
	}
	return fmt.Sprintf("\x1b[%d;%dm", bright, colorCode)
}

func computeAC(c model.Creature, world UpdatePlyWorld) int {
	ac := 100

	con := creatureStat(c, "constitution")
	if con > 95 {
		ac -= 5 * legacyStatBonus(90)
	} else {
		ac -= 5 * (legacyStatBonus(con) + 4)
	}

	dex := creatureStat(c, "dexterity")
	if dex > 95 {
		ac -= 2 * legacyStatBonus(90)
	} else {
		ac -= 2 * (legacyStatBonus(dex) + 4)
	}

	slots := []string{
		"body", "arms", "legs", "hands", "head", "feet", "shield", "face",
		"neck1", "neck2", "held", "wield",
		"finger1", "finger2", "finger3", "finger4", "finger5", "finger6", "finger7", "finger8",
	}
	for _, slot := range slots {
		if objID := c.Equipment[slot]; !objID.IsZero() {
			if obj, ok := world.Object(objID); ok {
				if armorVal, ok := objectIntProperty(world, obj, "armor"); ok {
					ac -= armorVal
				}
			}
		}
	}

	if creatureHasAnyFlag(c, "PPROTE", "protect") {
		ac -= 10
	}
	if creatureHasAnyFlag(c, "PREFLECT", "reflect", "reflection") {
		ac -= 15
	}
	if creatureHasAnyFlag(c, "PSHADOW", "shadow", "shadowClone") {
		ac -= 20
	}
	if creatureHasAnyFlag(c, "PABSORB", "absorb") {
		ac -= 10
	}
	if creatureHasAnyFlag(c, "PCHOI", "choi") {
		ac += 20
	}

	if creatureClass(c) >= legacyClassBulsa {
		ac -= 10
		if con > 45 {
			ac -= (con - 45)
		}
	}

	if ac < -127 {
		ac = -127
	} else if ac > 127 {
		ac = 127
	}
	return ac
}

func computeTHACO(c model.Creature, world UpdatePlyWorld) int {
	level := creatureStat(c, "level")
	n := (level + 3) / 4
	if n > 20 {
		n = 19
	} else if n > 0 {
		n -= 1
	} else {
		n = 0
	}

	class := creatureClass(c)
	var thaco int
	if class >= 0 && class < len(thacoList) {
		thaco = thacoList[class][n]
	} else {
		thaco = 20
	}

	var weapon model.ObjectInstance
	wielded := false
	for slot, objID := range c.Equipment {
		if (strings.EqualFold(slot, "wield") || strings.EqualFold(slot, "weapon")) && !objID.IsZero() {
			if obj, ok := world.Object(objID); ok {
				weapon = obj
				wielded = true
				break
			}
		}
	}
	if wielded {
		if adj, ok := objectIntProperty(world, weapon, "adjustment"); ok {
			thaco -= adj
		}
	}

	thaco -= modProfic(c, world)

	m := 0
	// C sums SHARP..POLE, then mprofic(0)..FIRE. mprofic(0) mirrors
	// the legacy realm[-1] memory layout and reads missile proficiency.
	for i := 0; i < 4; i++ {
		m += profic(c, i)
	}
	for j := 0; j < 4; j++ {
		m += mprofic(c, j)
	}
	m /= 50
	thaco -= m

	if creatureHasAnyFlag(c, "PBLESS", "bless") {
		thaco -= 3
	}
	if creatureHasAnyFlag(c, "PREFLECT", "reflect", "reflection") {
		thaco -= 1
	}
	if creatureHasAnyFlag(c, "PSHADOW", "shadow", "shadowClone") {
		thaco -= 3
	}
	if creatureHasAnyFlag(c, "PABSORB", "absorb") {
		thaco -= 2
	}
	if creatureHasAnyFlag(c, "PCHOI", "choi") {
		thaco += 5
	}
	if creatureHasAnyFlag(c, "PSLAYE", "slaye", "accurate", "slayer") {
		thaco -= 3
	}
	if class == legacyClassDM {
		thaco -= 60
	}
	if class == legacyClassBulsa {
		thaco -= 14
	}

	return thaco
}

func mprofic(c model.Creature, index int) int {
	var n int
	if index == 0 {
		n = creatureProficiency(c, 4)
	} else {
		realmIdx := -1
		switch index - 1 {
		case 0: // Fire
			realmIdx = 2
		case 1: // Wind
			realmIdx = 1
		case 2: // Earth
			realmIdx = 0
		case 3: // Water
			realmIdx = 3
		}
		if realmIdx != -1 {
			n = creatureRealm(c, realmIdx)
		}
	}

	var profArray [12]int64
	class := creatureClass(c)
	switch class {
	case legacyClassMage, legacyClassInvincible, legacyClassCaretaker, legacyClassBulsa, legacyClassSubDM, legacyClassDM:
		profArray = [12]int64{0, 1024, 2048, 4096, 8192, 16384, 35768, 85536, 140000, 459410, 2073306, 500000000}
	case legacyClassCleric:
		profArray = [12]int64{0, 1024, 4092, 8192, 16384, 32768, 70536, 119000, 226410, 709410, 2973307, 500000000}
	case legacyClassPaladin, legacyClassRanger:
		profArray = [12]int64{0, 1024, 8192, 16384, 32768, 65536, 105000, 165410, 287306, 809410, 3538232, 500000000}
	default:
		profArray = [12]int64{0, 1024, 40000, 80000, 120000, 160000, 205000, 222000, 380000, 965410, 5495000, 500000000}
	}

	var i int
	var prof int
	for i = 0; i < 11; i++ {
		if int64(n) < profArray[i+1] {
			prof = 10 * i
			break
		}
	}
	if profArray[i+1] > profArray[i] {
		prof += int((int64(n) - profArray[i]) * 10 / (profArray[i+1] - profArray[i]))
	}
	return prof
}

func profic(c model.Creature, index int) int {
	n := creatureProficiency(c, index)
	var profArray [12]int64
	class := creatureClass(c)
	switch class {
	case legacyClassFighter, legacyClassInvincible, legacyClassCaretaker, legacyClassBulsa, legacyClassSubDM, legacyClassDM:
		profArray = [12]int64{0, 768, 1024, 1440, 1910, 16000, 31214, 167000, 268488, 695000, 934808, 500000000}
	case legacyClassBarbarian:
		profArray = [12]int64{0, 1536, 2048, 2880, 3820, 32000, 62428, 334000, 536976, 1390000, 1869616, 500000000}
	case legacyClassThief, legacyClassRanger:
		profArray = [12]int64{0, 2304, 3072, 4320, 5730, 48000, 93642, 501000, 805464, 2085000, 2804424, 500000000}
	case legacyClassCleric, legacyClassPaladin, legacyClassAssassin:
		profArray = [12]int64{0, 3072, 4096, 5076, 7640, 64000, 124856, 668000, 1073952, 2780000, 3939232, 500000000}
	default:
		profArray = [12]int64{0, 5376, 7168, 10080, 13370, 112000, 218498, 1169000, 1879416, 4865000, 6543656, 500000000}
	}

	var i int
	var prof int
	for i = 0; i < 11; i++ {
		if int64(n) < profArray[i+1] {
			prof = 10 * i
			break
		}
	}
	if profArray[i+1] > profArray[i] {
		prof += int((int64(n) - profArray[i]) * 10 / (profArray[i+1] - profArray[i]))
	}
	return prof
}

func modProfic(c model.Creature, world UpdatePlyWorld) int {
	var amt int
	class := creatureClass(c)
	switch class {
	case legacyClassFighter, legacyClassBarbarian, legacyClassInvincible, legacyClassCaretaker:
		amt = 20
	case legacyClassRanger, legacyClassPaladin:
		amt = 25
	case legacyClassThief, legacyClassAssassin, legacyClassCleric:
		amt = 30
	default:
		amt = 40
	}

	var weapon model.ObjectInstance
	wielded := false
	for slot, objID := range c.Equipment {
		if (strings.EqualFold(slot, "wield") || strings.EqualFold(slot, "weapon")) && !objID.IsZero() {
			if obj, ok := world.Object(objID); ok {
				weapon = obj
				wielded = true
				break
			}
		}
	}

	if wielded {
		wtype, ok := objectIntProperty(world, weapon, "type")
		if ok && wtype >= 0 && wtype <= 4 {
			return profic(c, wtype) / amt
		}
	}
	return profic(c, 2) / amt // blunt
}

var legacyWeaponProficiencyStatKeys = [...]string{
	"proficiencySharp",
	"proficiencyThrust",
	"proficiencyBlunt",
	"proficiencyPole",
	"proficiencyMissile",
}

var legacyWeaponProficiencyPropertyKeys = [...]string{
	"sharp",
	"thrust",
	"blunt",
	"pole",
	"missile",
}

func creatureProficiency(c model.Creature, idx int) int {
	fallbackKeys := make([]string, 0, 12)
	if idx >= 0 && idx < len(legacyWeaponProficiencyStatKeys) {
		part := legacyWeaponProficiencyPropertyKeys[idx]
		fallbackKeys = append(fallbackKeys,
			legacyWeaponProficiencyStatKeys[idx],
			fmt.Sprintf("proficiency/%s", part),
			fmt.Sprintf("proficiency.%s", part),
			fmt.Sprintf("proficiency_%s", part),
		)
	}
	fallbackKeys = append(fallbackKeys,
		fmt.Sprintf("proficiency/%d", idx),
		fmt.Sprintf("proficiency.%d", idx),
		fmt.Sprintf("proficiency_%d", idx),
		fmt.Sprintf("proficiency%d", idx),
	)
	for _, key := range fallbackKeys {
		if val, ok := c.Stats[key]; ok {
			return val
		}
		if valStr, ok := c.Properties[key]; ok {
			if val, err := strconv.Atoi(strings.TrimSpace(valStr)); err == nil {
				return val
			}
		}
	}
	return 0
}

func creatureRealm(c model.Creature, idx int) int {
	keys := []string{"realmEarth", "realmWind", "realmFire", "realmWater"}
	if idx >= 0 && idx < len(keys) {
		if val, ok := c.Stats[keys[idx]]; ok {
			return val
		}
	}
	indexKeys := []string{
		fmt.Sprintf("realm/%d", idx+1),
		fmt.Sprintf("realm.%d", idx+1),
		fmt.Sprintf("realm_%d", idx+1),
		fmt.Sprintf("realm%d", idx+1),
	}
	for _, key := range indexKeys {
		if val, ok := c.Stats[key]; ok {
			return val
		}
		if valStr, ok := c.Properties[key]; ok {
			if val, err := strconv.Atoi(strings.TrimSpace(valStr)); err == nil {
				return val
			}
		}
	}
	return 0
}
