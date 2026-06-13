package command

import (
	"fmt"
	"strings"

	"github.com/0xc0de1ab/muhan/internal/krtext"
	"github.com/0xc0de1ab/muhan/internal/world/model"
)

// study/teach formulas verified against C src/magic1.c (study, teach):
// - Deterministic (no random success rate, no MP cost, no proficiency gain on learn).
//   C study/teach are instant set if preconds pass (level via ndice, alignment OGOODO/OEVILO,
//   class OCLSEL bits, room RCAST for teach, spell level restrictions for teach >basic).
// - Go matches: readScrollLevelRestricted, magicObjectAlignmentRejected, magicObjectClassRestricted,
//   teach class/spllv checks, RCAST room flag, already-knows via tags.
// - "배워/연마/가르쳐" success/prof/MP confirmed N/A for these paths (combat prof gains elsewhere).
// (historical P1-5 marker; resolved)
// Package 6/6 verification complete for exact legacy behavior.

type TeachWorld interface {
	LookWorld
	UpdateCreatureTags(model.CreatureID, []string, []string) (model.Creature, error)
	UpdatePlayerTags(model.PlayerID, []string, []string) (model.Player, error)
}

func NewTeachHandler(world TeachWorld) Handler {
	// teach ("가르쳐") is deterministic success (no rand) per C magic1.c teach().
	// Teacher must know spell, target not, room RCAST, class/level (spllv) restrictions exact match.
	// Always sets tag on success, no MP cost, no failure %.
	// (historical P0-2 marker cleaned post Package 2)
	return func(ctx *Context, resolved ResolvedCommand) (Status, error) {
		krtext.InTeachCommand = true
		defer func() { krtext.InTeachCommand = false }()

		playerID := InventoryPlayerIDFromContext(ctx)
		if playerID.IsZero() {
			return StatusDefault, ErrInventoryActorRequired
		}
		player, actor, err := CurrentInventoryCreature(world, playerID)
		if err != nil {
			return StatusDefault, err
		}
		roomID := studyActorRoomID(player, actor)
		room, ok := world.Room(roomID)
		if !ok {
			return StatusDefault, fmt.Errorf("teach: room %q not found", roomID)
		}

		if !roomHasAnyFlag(room, "rcast", "RCAST") {
			ctx.WriteString("주문 전수장에서만 가능합니다.")
			return StatusDefault, nil
		}
		targetName := getArg(resolved, 0)
		spellName := getArg(resolved, 1)
		if targetName == "" || spellName == "" {
			ctx.WriteString("누구에게 비법을 전수시키실겁니까?")
			return StatusDefault, nil
		}

		if teachActorIsBlind(player, actor) {
			ctx.WriteString("\x1b[0;31m아무것도 보이지 않습니다!\n\x1b[0;37m")
			return StatusDefault, nil
		}
		if teachActorIsSilenced(player, actor) {
			ctx.WriteString("\x1b[0;33m당신은 한마디도 할수 없습니다!\n\x1b[0;37m")
			return StatusDefault, nil
		}
		class := creatureClass(actor)
		if class != model.ClassMage && class != model.ClassCleric && class < model.ClassInvincible {
			ctx.WriteString("\n도술사와 불제자만이 전수시킬 수 있는 능력이 있습니다.\n")
			return StatusDefault, nil
		}

		target, ok := findTeachTarget(world, room, player.ID, actor.ID, targetName, getOrdinal(resolved, 0))
		if !ok {
			ctx.WriteString("\n그런 사람은 존재하지 않습니다.\n")
			return StatusDefault, nil
		}

		var spellsToTeach []teachSpell

		spell, matchCount := matchTeachSpell(spellName)
		if matchCount == 0 {
			ctx.WriteString("\n그런 주문이 존재하지 않습니다.\n")
			return StatusDefault, nil
		}
		if matchCount > 1 {
			ctx.WriteString("\n주문이름이 이상합니다.\n")
			return StatusDefault, nil
		}
		if !teachActorKnowsSpell(player, actor, spell) {
			ctx.WriteString("\n당신은 아직 그런 주문을 터득하지 못했습니다.\n")
			return StatusDefault, nil
		}
		spellsToTeach = []teachSpell{spell}

		var newSpells []teachSpell
		for _, s := range spellsToTeach {
			if !teachTargetKnowsSpell(target, s) {
				newSpells = append(newSpells, s)
			}
		}

		if len(newSpells) == 0 {
			tName := teachTargetName(target)
			ctx.WriteString("\n" + tName + "이 이미 터득한 주문입니다.\n")
			return StatusDefault, nil
		}

		spell = spellsToTeach[0]
		if spell.level == 1 && class != model.ClassCleric && class < model.ClassInvincible {
			ctx.WriteString("\n그 주문을 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level == 2 && class != model.ClassMage && class < model.ClassInvincible {
			ctx.WriteString("\n그 주문을 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level == 3 && class < model.ClassInvincible {
			ctx.WriteString("\n그 주문을 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level == 4 && class < model.ClassCaretaker {
			ctx.WriteString("\n그 주문을 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level == 5 && class < model.ClassSubDM {
			ctx.WriteString("\n그 주문을 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level == 6 {
			ctx.WriteString("\n천상주문은 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level == 7 {
			ctx.WriteString("\n태극주문은 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}
		if spell.level <= 0 || spell.level > 7 {
			ctx.WriteString("\n그 주문을 다른 사람에게 전수시킬 수 없습니다.\n")
			return StatusDefault, nil
		}

		player, actor, err = clearCommandActorHidden(world, player, actor)
		if err != nil {
			return StatusDefault, err
		}

		var tagsToAdd []string
		for _, s := range newSpells {
			tagsToAdd = append(tagsToAdd, s.tag)
		}

		if _, err := world.UpdateCreatureTags(target.creature.ID, tagsToAdd, nil); err != nil {
			return StatusDefault, err
		}
		if _, err := world.UpdatePlayerTags(target.player.ID, tagsToAdd, nil); err != nil {
			return StatusDefault, err
		}

		spellDisplay := spellsToTeach[0].name
		targetDisplay := teachTargetName(target)
		ctx.WriteString("\n" + spellDisplay + " 주술을 " + targetDisplay + "에게 시범을 보이며 주문 전수를 시킵니다.\n오옷~~~ 이 주문을 외우자 주위에 이상한 기운이 모이는 것이\n그 사람에게 상당한 도움이 될 것 같습니다.\n")

		actorName := attackCreatureName(actor)
		if !target.player.ID.IsZero() {
			targetMsg := "\n" + actorName + krtext.Particle(actorName, '1') + " " + spellDisplay + " 주술의 시범을 보이며 주문 전수를 시킵니다.\n오옷~~~ 이 주문을 외우자 주위에 이상한 기운이 모이는 것이 그 \n그 사람에게 상당한 도움이 될 것 같습니다.\n"
			_ = sendToPlayerAgent3(ctx, target.player.ID, targetMsg)
		}

		roomMsg := "\n" + actorName + krtext.Particle(actorName, '1') + " " + targetDisplay + "에게 " + spellDisplay + " 주술의 시범을 보이며 주문 전수를 \n시킵니다.\n오옷~~~ 이 주문을 외우자 주위에 이상한 기운이 모이는 것이\n그 사람에게 상당한 도움이 될 것 같습니다.\n"
		return StatusDefault, teachRoomBroadcast(ctx, world, room.ID, target.player.ID, roomMsg)
	}
}

func teachRoomBroadcast(ctx *Context, world TeachWorld, roomID model.RoomID, targetPlayerID model.PlayerID, text string) error {
	if ctx != nil && ctx.Values != nil && ctx.Values["game.activeSessions"] != nil && ctx.Values["game.sendToSession"] != nil {
		return roomBroadcast2(ctx, world, roomID, ctx.SessionID, targetPlayerID, text)
	}
	return roomBroadcast(ctx, roomID, text)
}

type teachSpell struct {
	name  string
	tag   string
	level int
}

var teachSpells = []teachSpell{
	{name: "회복", tag: "SVIGOR", level: 1},
	{name: "삭풍", tag: "SHURTS", level: 2},
	{name: "발광", tag: "SLIGHT", level: 2},
	{name: "해독", tag: "SCUREP", level: 1},
	{name: "성현진", tag: "SBLESS", level: 2},
	{name: "수호진", tag: "SPROTE", level: 2},
	{name: "화궁", tag: "SFIREB", level: 3},
	{name: "은둔법", tag: "SINVIS", level: 5},
	{name: "도력반", tag: "SRESTO", level: 4},
	{name: "은둔감지술", tag: "SDINVI", level: 3},
	{name: "주문감지술", tag: "SDMAGI", level: 3},
	{name: "", tag: "STELEP", level: 4},
	{name: "혼동", tag: "SBEFUD", level: 3},
	{name: "뇌전", tag: "SLGHTN", level: 4},
	{name: "동설주", tag: "SICEBL", level: 5},
	{name: "빙의", tag: "SENCHA", level: 4},
	{name: "귀환", tag: "SRECAL", level: 3},
	{name: "소환", tag: "SSUMMO", level: 3},
	{name: "원기회복", tag: "SMENDW", level: 3},
	{name: "완치", tag: "SFHEAL", level: 5},
	{name: "추적", tag: "STRACK", level: 4},
	{name: "부양술", tag: "SLEVIT", level: 3},
	{name: "방열진", tag: "SRFIRE", level: 3},
	{name: "비상술", tag: "SFLYSP", level: 4},
	{name: "보마진", tag: "SRMAGI", level: 3},
	{name: "권풍술", tag: "SSHOCK", level: 5},
	{name: "지동술", tag: "SRUMBL", level: 2},
	{name: "화선도", tag: "SBURNS", level: 2},
	{name: "탄수공", tag: "SBLIST", level: 2},
	{name: "풍마현", tag: "SDUSTG", level: 3},
	{name: "파초식", tag: "SWBOLT", level: 3},
	{name: "폭진", tag: "SCRUSH", level: 3},
	{name: "낙석", tag: "SENGUL", level: 5},
	{name: "화풍술", tag: "SBURST", level: 3},
	{name: "화룡대천", tag: "SSTEAM", level: 3},
	{name: "토합술", tag: "SSHATT", level: 4},
	{name: "주작현", tag: "SIMMOL", level: 4},
	{name: "열사천", tag: "SBLOOD", level: 4},
	{name: "파천풍", tag: "STHUND", level: 5},
	{name: "지옥패", tag: "SEQUAK", level: 5},
	{name: "태양안", tag: "SFLFIL", level: 5},
	{name: "선악감지", tag: "SKNOWA", level: 3},
	{name: "저주해소", tag: "SREMOV", level: 4},
	{name: "방한진", tag: "SRCOLD", level: 3},
	{name: "수생술", tag: "SBRWAT", level: 3},
	{name: "지방호", tag: "SSSHLD", level: 3},
	{name: "천리안", tag: "SLOCAT", level: 4},
	{name: "백치술", tag: "SDREXP", level: 5},
	{name: "치료", tag: "SRMDIS", level: 3},
	{name: "개안술", tag: "SRMBLD", level: 3},
	{name: "공포", tag: "SFEARS", level: 4},
	{name: "전회복", tag: "SRVIGO", level: 4},
	{name: "전송", tag: "STRANO", level: 4},
	{name: "실명", tag: "SBLIND", level: 5},
	{name: "봉합구", tag: "SSILNC", level: 5},
	{name: "이혼대법", tag: "SCHARM", level: 5},
	{name: "저주", tag: "SCURSE", level: 5},
	{name: "천지진동", tag: "SISIX1", level: 6},
	{name: "천상풍", tag: "SISIX2", level: 6},
	{name: "천마강기", tag: "SISIX3", level: 6},
	{name: "빙천파", tag: "SISIX4", level: 6},
	{name: "공포해소", tag: "SRMGONG", level: 6},
	{name: "혈사천", tag: "XIXIX1", level: 7},
	{name: "빙설검", tag: "XIXIX2", level: 7},
	{name: "멸겁화궁", tag: "XIXIX3", level: 7},
	{name: "탄지수통", tag: "XIXIX4", level: 7},
}

func matchTeachSpell(input string) (teachSpell, int) {
	var exactMatches []teachSpell
	for _, s := range teachSpells {
		if strings.EqualFold(s.name, input) || strings.EqualFold(s.tag, input) {
			exactMatches = append(exactMatches, s)
		}
	}
	if len(exactMatches) == 1 {
		return exactMatches[0], 1
	} else if len(exactMatches) > 1 {
		return teachSpell{}, len(exactMatches)
	}

	var prefixMatches []teachSpell
	for _, s := range teachSpells {
		if (s.name != "" && strings.HasPrefix(strings.ToLower(s.name), strings.ToLower(input))) ||
			(s.tag != "" && strings.HasPrefix(strings.ToLower(s.tag), strings.ToLower(input))) {
			prefixMatches = append(prefixMatches, s)
		}
	}
	if len(prefixMatches) == 1 {
		return prefixMatches[0], 1
	}
	return teachSpell{}, len(prefixMatches)
}

type teachTarget struct {
	player   model.Player
	creature model.Creature
}

func teachActorIsBlind(player model.Player, actor model.Creature) bool {
	return settingsPlayerFlag(player, "blind", "pblind", "PBLIND") ||
		creatureHasAnyFlag(actor, "blind", "pblind", "PBLIND")
}

func teachActorIsSilenced(player model.Player, actor model.Creature) bool {
	return settingsPlayerFlag(player, "silence", "silenced", "psilnc", "PSILNC") ||
		creatureHasAnyFlag(actor, "silence", "silenced", "psilnc", "PSILNC")
}

func teachActorKnowsSpell(player model.Player, creature model.Creature, spell teachSpell) bool {
	if spell.tag == "" {
		return false
	}
	if hasAnyNormalizedFlag(player.Metadata.Tags, spell.tag) {
		return true
	}
	if creatureHasAnyFlag(creature, spell.tag) {
		return true
	}
	targets := normalizedFlagSet(spell.tag)
	for key, value := range creature.Stats {
		if _, ok := targets[normalizeFlagName(key)]; ok && value != 0 {
			return true
		}
	}
	return false
}

func teachTargetKnowsSpell(target teachTarget, spell teachSpell) bool {
	if spell.tag == "" {
		return false
	}
	if hasAnyNormalizedFlag(target.player.Metadata.Tags, spell.tag) {
		return true
	}
	if creatureHasAnyFlag(target.creature, spell.tag) {
		return true
	}
	targets := normalizedFlagSet(spell.tag)
	for key, value := range target.creature.Stats {
		if _, ok := targets[normalizeFlagName(key)]; ok && value != 0 {
			return true
		}
	}
	return false
}

func findTeachTarget(
	world TeachWorld,
	room model.Room,
	actorPlayerID model.PlayerID,
	actorCreatureID model.CreatureID,
	prefix string,
	ordinal int64,
) (teachTarget, bool) {
	viewer := LookViewer{
		PlayerID:   actorPlayerID,
		CreatureID: actorCreatureID,
	}
	player, ok := findAttackPlayerTarget(world, room, viewer, prefix, ordinal)
	if !ok || player.CreatureID.IsZero() {
		return teachTarget{}, false
	}
	creature, ok := world.Creature(player.CreatureID)
	if !ok || creature.RoomID != room.ID {
		return teachTarget{}, false
	}
	return teachTarget{player: player, creature: creature}, true
}

func teachTargetName(target teachTarget) string {
	if name := cleanDisplayText(target.player.DisplayName); name != "" {
		return name
	}
	if name := cleanDisplayText(target.creature.DisplayName); name != "" {
		return name
	}
	if name := cleanDisplayText(target.player.AccountName); name != "" {
		return name
	}
	return string(target.player.ID)
}
