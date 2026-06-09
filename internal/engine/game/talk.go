package game

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/krtext"
	"muhan/internal/persist/legacykr"
	"muhan/internal/session"
	"muhan/internal/world/model"
)

var (
	ErrTalkWorldRequired = errors.New("game: talk world required")
	ErrTalkActorRequired = errors.New("game: talk actor required")
)

type TalkWorld interface {
	PlayerLookup
	Room(model.RoomID) (model.Room, bool)
	Creature(model.CreatureID) (model.Creature, bool)
}

func NewTalkHandler(world TalkWorld) enginecmd.Handler {
	return NewTalkHandlerWithRoot(world, "")
}

func NewTalkHandlerWithRoot(world TalkWorld, root string) enginecmd.Handler {
	return func(ctx *enginecmd.Context, resolved enginecmd.ResolvedCommand) (enginecmd.Status, error) {
		if world == nil {
			return enginecmd.StatusDefault, ErrTalkWorldRequired
		}
		if ctx == nil || ctx.ActorID == "" || ctx.SessionID == "" {
			return enginecmd.StatusDefault, ErrTalkActorRequired
		}
		active, ok := activeSessionsFunc(ctx)
		if !ok {
			return enginecmd.StatusDefault, ErrSocialContextMissing
		}
		send, ok := sendToSessionFunc(ctx)
		if !ok {
			return enginecmd.StatusDefault, ErrSocialContextMissing
		}

		target := strings.TrimSpace(firstTalkArg(resolved))
		if target == "" {
			ctx.WriteString("누구에게 이야기하시려구요?\n")
			return enginecmd.StatusDefault, nil
		}

		actorID := model.PlayerID(ctx.ActorID)
		actorRoomID := playerRoomID(world, actorID)
		if actorRoomID.IsZero() {
			return enginecmd.StatusDefault, fmt.Errorf("talk: actor %q has no room", actorID)
		}
		room, ok := world.Room(actorRoomID)
		if !ok {
			return enginecmd.StatusDefault, fmt.Errorf("talk: room %q not found", actorRoomID)
		}
		creature, targetName, ok := findTalkCreatureTarget(world, room, actorID, target, firstTalkOrdinal(resolved))
		if !ok {
			ctx.WriteString("그런 것은 여기 없습니다.\n")
			return enginecmd.StatusDefault, nil
		}
		if err := revealSocialActor(world, actorID); err != nil {
			return enginecmd.StatusDefault, err
		}

		actorName := playerDisplayName(world, ctx.ActorID)
		if topic := talkTopic(resolved); topic != "" && talkCreatureHasFlag(creature, "talks", "mtalks") {
			entry, loaded, entryOK, err := loadTalkFileEntry(root, room, creature, topic)
			if err != nil {
				return enginecmd.StatusDefault, err
			}
			if !loaded {
				return enginecmd.StatusPrompt, nil
			}
			out := "\n" + actorSubject(actorName) + " " + targetName + "에게 \"" + topic + "\"에 관해 물어봅니다.\n"
			if err := broadcastTalkRoom(ctx, world, active(), send, actorRoomID, actorID, "", out); err != nil {
				return enginecmd.StatusDefault, err
			}
			if entryOK {
				ctx.WriteString("\n" + actorSubject(targetName) + " 당신에게 \"" + entry.Response + "\"라고 이야기합니다.\n")
				responseRoom := "\n" + actorSubject(targetName) + " " + actorName + "에게 \"" + entry.Response + "\"라고 이야기합니다.\n"
				if err := broadcastTalkRoom(ctx, world, active(), send, actorRoomID, actorID, "", responseRoom); err != nil {
					return enginecmd.StatusDefault, err
				}
				if err := executeTalkAction(ctx, world, active(), send, actorRoomID, actorID, creature, targetName, actorName, entry.Action); err != nil {
					return enginecmd.StatusDefault, err
				}
				return enginecmd.StatusDefault, nil
			}
			ctx.WriteString(talkShrugMessage(targetName))
			if err := broadcastTalkRoom(ctx, world, active(), send, actorRoomID, actorID, "", talkShrugMessage(targetName)); err != nil {
				return enginecmd.StatusDefault, err
			}
			if talkCreatureHasFlag(creature, "talkAggressive", "mtlkag") {
				addTalkEnemy(world, creature.ID, actorID)
			}
			return enginecmd.StatusDefault, nil
		}

		roomOut := "\n" + actorSubject(actorName) + " " + targetName + krtext.Particle(targetName, '2') + " 이야기를 합니다.\n"
		if err := broadcastTalkRoom(ctx, world, active(), send, actorRoomID, actorID, "", roomOut); err != nil {
			return enginecmd.StatusDefault, err
		}

		talk := strings.TrimSpace(creature.Properties["legacyTalk"])
		if talk == "" {
			message := "\n" + targetName + krtext.Particle(targetName, '0') + " 단지 당신을 멍하니 바라봅니다.\n"
			ctx.WriteString(message)
			if err := broadcastTalkRoom(ctx, world, active(), send, actorRoomID, actorID, "", message); err != nil {
				return enginecmd.StatusDefault, err
			}
			if talkCreatureHasFlag(creature, "talkAggressive", "mtlkag") {
				addTalkEnemy(world, creature.ID, actorID)
			}
			return enginecmd.StatusDefault, nil
		}

		ctx.WriteString("\n" + actorSubject(targetName) + " 당신에게 \"" + talk + "\"라고 이야기합니다.\n")
		responseRoom := "\n" + actorSubject(targetName) + " " + actorName + "에게 \"" + talk + "\"라고 이야기합니다.\n"
		if err := broadcastTalkRoom(ctx, world, active(), send, actorRoomID, actorID, "", responseRoom); err != nil {
			return enginecmd.StatusDefault, err
		}
		if talkCreatureHasFlag(creature, "talkAggressive", "mtlkag") {
			addTalkEnemy(world, creature.ID, actorID)
		}
		return enginecmd.StatusDefault, nil
	}
}

type talkFileAction struct {
	Type   string
	Name   string
	Target string
}

type talkFileResult struct {
	Response string
	Action   talkFileAction
}

func loadTalkFileEntry(root string, room model.Room, creature model.Creature, topic string) (talkFileResult, bool, bool, error) {
	root = strings.TrimSpace(root)
	topic = strings.TrimSpace(topic)
	if root == "" || topic == "" || creature.Level <= 0 {
		return talkFileResult{}, false, false, nil
	}
	filenames := legacyTalkFilenameCandidates(room, creature)
	if len(filenames) == 0 {
		return talkFileResult{}, false, false, nil
	}
	var data []byte
	var path string
	for _, filename := range filenames {
		path = filepath.Join(root, "objmon", "talk", filename)
		var err error
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return talkFileResult{}, false, false, fmt.Errorf("read talk file %q: %w", path, err)
		}
		data = nil
	}
	if data == nil {
		return talkFileResult{}, false, false, nil
	}
	text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Path: path, Field: "talk"}, data)
	if err != nil {
		return talkFileResult{}, false, false, err
	}
	lines := talkFileLines(text)
	loaded := false
	for i := 0; i+1 < len(lines); i += 2 {
		loaded = true
		key := talkFileKey(lines[i])
		if key == topic {
			return talkFileResult{
				Response: lines[i+1],
				Action:   talkFileActionFromLine(lines[i]),
			}, true, true, nil
		}
	}
	return talkFileResult{}, loaded, false, nil
}

func talkFileResponse(root string, creature model.Creature, topic string) (string, bool, error) {
	entry, _, ok, err := loadTalkFileEntry(root, model.Room{}, creature, topic)
	return entry.Response, ok, err
}

func legacyTalkFilename(name string, level int) (string, bool) {
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "_")
	if name == "" || level <= 0 {
		return "", false
	}
	return name + "-" + strconv.Itoa(level), true
}

func legacyTalkFilenameCandidates(room model.Room, creature model.Creature) []string {
	if creature.Level <= 0 {
		return nil
	}
	names := []string{creature.DisplayName}
	for _, key := range []string{"name", "legacyRecordName", "key[0]", "key[1]", "key[2]", "key/1", "key/2", "key/3"} {
		names = append(names, creature.Properties[key])
	}
	if keywords := strings.TrimSpace(creature.Properties["keywords"]); keywords != "" {
		names = append(names, strings.Split(keywords, "\n")...)
	}
	if roomName := strings.TrimSpace(room.DisplayName); roomName != "" {
		names = append(names, roomName+" 주인")
	}

	seen := map[string]struct{}{}
	filenames := make([]string, 0, len(names))
	for _, name := range names {
		filename, ok := legacyTalkFilename(name, creature.Level)
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

func talkFileLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func talkFileKey(line string) string {
	words := talkFileWords(line, 1)
	if len(words) == 0 {
		return ""
	}
	return words[0]
}

func talkFileActionFromLine(line string) talkFileAction {
	words := talkFileWords(line, 4)
	if len(words) < 2 {
		return talkFileAction{}
	}
	switch words[1] {
	case "ATTACK":
		return talkFileAction{Type: "ATTACK"}
	case "ACTION":
		if len(words) < 3 {
			return talkFileAction{}
		}
		action := talkFileAction{Type: "ACTION", Name: words[2]}
		if len(words) > 3 {
			action.Target = words[3]
		}
		return action
	case "CAST":
		if len(words) < 3 {
			return talkFileAction{}
		}
		action := talkFileAction{Type: "CAST", Name: words[2]}
		if len(words) > 3 {
			action.Target = words[3]
		}
		return action
	case "GIVE":
		if len(words) < 3 {
			return talkFileAction{}
		}
		return talkFileAction{Type: "GIVE", Name: words[2]}
	default:
		return talkFileAction{}
	}
}

func talkFileWords(line string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	words := make([]string, 0, limit)
	for pos := 0; pos < len(line) && len(words) < limit; {
		for pos < len(line) && legacyTalkSpaceByte(line[pos]) {
			pos++
		}
		if pos >= len(line) {
			break
		}
		start := pos
		for pos < len(line) {
			r, size := utf8.DecodeRuneInString(line[pos:])
			if size == 0 || !legacyTalkWordRune(r) {
				break
			}
			pos += size
		}
		if pos == start {
			_, size := utf8.DecodeRuneInString(line[pos:])
			if size <= 0 {
				break
			}
			pos += size
			continue
		}
		words = append(words, line[start:pos])
	}
	return words
}

func legacyTalkSpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func legacyTalkWordRune(r rune) bool {
	if r > 127 {
		return true
	}
	return r == '-' ||
		(r >= '0' && r <= '9') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z')
}

func firstTalkArg(resolved enginecmd.ResolvedCommand) string {
	if len(resolved.Args) == 0 {
		return ""
	}
	return strings.TrimSpace(resolved.Args[0])
}

func firstTalkOrdinal(resolved enginecmd.ResolvedCommand) int64 {
	if len(resolved.Values) == 0 || resolved.Values[0] < 1 {
		return 1
	}
	return resolved.Values[0]
}

func talkTopic(resolved enginecmd.ResolvedCommand) string {
	if len(resolved.Args) < 2 {
		return ""
	}
	for _, arg := range resolved.Args[1:] {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			return arg
		}
	}
	return ""
}

func findTalkCreatureTarget(world TalkWorld, room model.Room, actorID model.PlayerID, target string, ordinal int64) (model.Creature, string, bool) {
	target = strings.TrimSpace(target)
	if len(target) < 2 {
		return model.Creature{}, "", false
	}
	if ordinal < 1 {
		ordinal = 1
	}

	var seen int64
	for _, creatureID := range room.CreatureIDs {
		if creatureID.IsZero() {
			continue
		}
		creature, ok := world.Creature(creatureID)
		if !ok || creature.RoomID != room.ID || creature.Kind == model.CreatureKindPlayer || creature.PlayerID == actorID {
			continue
		}
		if !talkCreatureMatches(creature, target) {
			continue
		}
		seen++
		if seen == ordinal {
			name := strings.TrimSpace(creature.DisplayName)
			if name == "" {
				name = string(creature.ID)
			}
			return creature, name, true
		}
	}
	return model.Creature{}, "", false
}

func talkCreatureMatches(creature model.Creature, target string) bool {
	if strings.HasPrefix(cleanTalkText(creature.DisplayName), target) {
		return true
	}
	for _, key := range []string{"name", "key[0]", "key[1]", "key[2]", "key/1", "key/2", "key/3"} {
		if strings.HasPrefix(cleanTalkText(creature.Properties[key]), target) {
			return true
		}
	}
	if keywords := strings.TrimSpace(creature.Properties["keywords"]); keywords != "" {
		for _, keyword := range strings.Split(keywords, "\n") {
			if strings.HasPrefix(cleanTalkText(keyword), target) {
				return true
			}
		}
	}
	return false
}

func cleanTalkText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\x00", ""))
}

func talkCreatureHasFlag(creature model.Creature, names ...string) bool {
	targets := normalizedFlagSet(names...)
	if hasAnyNormalizedFlag(creature.Metadata.Tags, names...) {
		return true
	}
	for key, value := range creature.Stats {
		if value != 0 {
			if _, ok := targets[normalizeFlagName(key)]; ok {
				return true
			}
		}
	}
	return talkPropertiesHaveAnyFlag(creature.Properties, targets)
}

func talkPropertiesHaveAnyFlag(properties map[string]string, targets map[string]struct{}) bool {
	for key, value := range properties {
		if _, ok := targets[normalizeFlagName(key)]; ok && propertyFlagEnabled(value) {
			return true
		}
		if objectFlagContainerProperty(key) && propertyFlagValueHasAnyToken(value, targets) {
			return true
		}
	}
	return false
}

func objectFlagContainerProperty(key string) bool {
	switch normalizeFlagName(key) {
	case "flag", "flags":
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

func talkTagMatches(tag string, names ...string) bool {
	return hasAnyNormalizedFlag([]string{tag}, names...)
}

func normalizeTalkTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", "")
	tag = strings.ReplaceAll(tag, "-", "")
	return tag
}

func talkShrugMessage(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "누군가"
	}
	return "\n" + actorSubject(name) + " 어깨를 으쓱 거립니다.\n"
}

func broadcastTalkRoom(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	targetID model.PlayerID,
	text string,
) error {
	for _, activeSession := range sessions {
		if activeSession.ActorID == "" {
			continue
		}
		playerID := model.PlayerID(activeSession.ActorID)
		if playerID == actorID || (!targetID.IsZero() && playerID == targetID) {
			continue
		}
		if playerRoomID(world, playerID) != roomID {
			continue
		}
		if string(activeSession.ID) == ctx.SessionID {
			ctx.WriteString(text)
			continue
		}
		_ = send(activeSession.ID, session.Command{Write: text})
	}
	return nil
}

func executeTalkAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	creature model.Creature,
	creatureName string,
	playerName string,
	action talkFileAction,
) error {
	switch action.Type {
	case "ACTION":
		return executeTalkSocialAction(ctx, world, sessions, send, roomID, actorID, creatureName, playerName, action)
	case "ATTACK":
		return executeTalkAttackAction(ctx, world, sessions, send, roomID, actorID, creature.ID, creatureName, playerName)
	case "CAST":
		return executeTalkCastAction(ctx, world, sessions, send, roomID, actorID, creature, creatureName, playerName, action)
	case "GIVE":
		return executeTalkGiveAction(ctx, world, sessions, send, roomID, actorID, creature, creatureName, playerName, action)
	default:
		return nil
	}
}

func executeTalkAttackAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	attackerID model.CreatureID,
	creatureName string,
	playerName string,
) error {
	targetOut := "\n" + actorSubject(creatureName) + " 당신을 공격합니다.\n"
	roomOut := "\n" + actorSubject(creatureName) + " " + strings.TrimSpace(playerName) + krtext.Particle(playerName, '3') + " 공격합니다.\n"
	// Legacy fidelity (C command8.c: talk_action case ATTACK): add_enm_crt(ply name to crt enemy list) + broadcast.
	addTalkEnemy(world, attackerID, actorID)
	if err := primeTalkAttackCombat(world, attackerID); err != nil {
		return err
	}
	return broadcastTalkTargetedAction(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut)
}

func addTalkEnemy(world TalkWorld, attackerID model.CreatureID, actorID model.PlayerID) {
	if world == nil || attackerID.IsZero() || actorID.IsZero() {
		return
	}
	player, ok := world.Player(actorID)
	if !ok || player.CreatureID.IsZero() {
		return
	}
	registrar, ok := world.(interface {
		AddEnemy(model.CreatureID, model.CreatureID) (bool, error)
	})
	if !ok {
		return
	}
	_, _ = registrar.AddEnemy(attackerID, player.CreatureID)
}

func primeTalkAttackCombat(world TalkWorld, attackerID model.CreatureID) error {
	if world == nil || attackerID.IsZero() {
		return nil
	}
	if updater, ok := world.(interface {
		UpdateCreatureTags(model.CreatureID, []string, []string) (model.Creature, error)
	}); ok {
		if _, err := updater.UpdateCreatureTags(attackerID, []string{"was_attacked"}, nil); err != nil {
			return err
		}
	}
	if cooldowns, ok := world.(interface {
		SetCreatureCooldown(model.CreatureID, string, int64, int64) error
	}); ok {
		// UpdateActiveMonsters consumes the was_attacked tag on the next tick and
		// can attack immediately when any existing attack cooldown has expired.
		if err := cooldowns.SetCreatureCooldown(attackerID, "attack", 0, 1); err != nil {
			return err
		}
	}
	return nil
}

func executeTalkSocialAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	creatureName string,
	playerName string,
	action talkFileAction,
) error {
	name, ok := legacyTalkActionName(action.Name)
	if !ok {
		return nil
	}

	targeted := strings.TrimSpace(action.Target) == "PLAYER"
	message := renderActionMessages(name, creatureName, playerName, targeted, "")
	for _, activeSession := range sessions {
		if activeSession.ActorID == "" {
			continue
		}
		playerID := model.PlayerID(activeSession.ActorID)
		if playerRoomID(world, playerID) != roomID {
			continue
		}
		out := message.Room
		if targeted && playerID == actorID && message.Target != "" {
			out = message.Target
		}
		if string(activeSession.ID) == ctx.SessionID {
			ctx.WriteString(out)
			continue
		}
		_ = send(activeSession.ID, session.Command{Write: out})
	}
	return nil
}

var legacyTalkActionNames = []string{
	"감정표현",
	"보아",
	"노려봐",
	"끄덕",
	"응",
	"아니",
	"감",
	"감사",
	"미소",
	"떨어",
	"해",
	"하품",
	"청혼",
	"웃어",
	"미안",
	"악수",
	"하이파이브",
	"박수",
	"흡연",
	"담배",
	"절",
	"찔러",
	"춤",
	"노래",
	"울어",
	"달래",
	"당황",
	"윙크",
	"뽀뽀",
	"바이",
	"잘가",
	"안녕",
	"설레",
	"놀려",
	"생각",
	"부끄러",
	"구걸",
	"구박",
	"안아",
	"껴안아",
	"니다",
}

func legacyTalkActionName(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	for _, name := range legacyTalkActionNames {
		if name == raw {
			return name, true
		}
	}
	for _, name := range legacyTalkActionNames {
		if strings.HasPrefix(name, raw) {
			return name, true
		}
	}
	return "", false
}

type talkCastEffect struct {
	AddTags    []string
	RemoveTags []string
	HealAmount int
	FullHeal   bool
	MPCost     int
}

type talkCastRuntime interface {
	CastTalkSpell(caster model.Creature, target model.Creature, player model.Player, spell string) (bool, error)
}

type talkCastCombatWorld interface {
	ApplyCreatureDamage(model.CreatureID, int) (model.Creature, int, bool, error)
	RecordCreatureDamage(model.CreatureID, model.CreatureID, int) error
	AddEnemy(model.CreatureID, model.CreatureID) (bool, error)
}

func executeTalkCastAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	caster model.Creature,
	creatureName string,
	playerName string,
	action talkFileAction,
) error {
	spell := strings.TrimSpace(action.Name)
	if spell == "" {
		return nil
	}
	player, ok := world.Player(actorID)
	if !ok || player.CreatureID.IsZero() {
		return nil
	}
	target, ok := world.Creature(player.CreatureID)
	if !ok {
		return nil
	}
	if spellInfo, damageInfo, ok := talkOffensiveSpellForName(spell); ok {
		return executeTalkOffensiveCastAction(ctx, world, sessions, send, roomID, actorID, caster, creatureName, playerName, player, target, spellInfo, damageInfo)
	}
	if runtime, ok := world.(talkCastRuntime); ok {
		handled, err := runtime.CastTalkSpell(caster, target, player, spell)
		if err != nil {
			return err
		}
		if handled {
			targetOut := "\n" + actorSubject(creatureName) + " 당신에게 " + spell + " 주문을 겁니다.\n"
			roomOut := "\n" + actorSubject(creatureName) + " " + strings.TrimSpace(playerName) + "에게 " + spell + " 주문을 겁니다.\n"
			return broadcastTalkTargetedAction(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut)
		}
	}
	effect, ok := talkCastEffectForSpell(spell)
	if !ok {
		return nil
	}
	if talkCasterHasPlayerEnemy(world, caster.ID, actorID) {
		ctx.WriteString(talkCastEnemyRefusalMessage(creatureName))
		return nil
	}
	spent, err := spendTalkCastMP(world, caster, effect.MPCost)
	if err != nil {
		return err
	}
	if !spent {
		ctx.WriteString(talkCastUnableMessage(creatureName))
		return nil
	}
	if err := applyTalkCastEffect(world, player, target, effect); err != nil {
		return err
	}

	targetOut := "\n" + actorSubject(creatureName) + " 당신에게 " + spell + " 주문을 겁니다.\n"
	roomOut := "\n" + actorSubject(creatureName) + " " + strings.TrimSpace(playerName) + "에게 " + spell + " 주문을 겁니다.\n"
	return broadcastTalkTargetedAction(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut)
}

func executeTalkOffensiveCastAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	caster model.Creature,
	creatureName string,
	playerName string,
	player model.Player,
	target model.Creature,
	spell studySpell,
	damageInfo osp_t,
) error {
	combatWorld, ok := world.(talkCastCombatWorld)
	if !ok {
		targetOut := "\n" + actorSubject(creatureName) + " " + spell.name + " 주문을 외우지만 아직 아무 일도 일어나지 않습니다.\n"
		roomOut := "\n" + actorSubject(creatureName) + " " + strings.TrimSpace(playerName) + "에게 " + spell.name + " 주문을 외우지만 아무 일도 일어나지 않습니다.\n"
		return broadcastTalkTargetedAction(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut)
	}
	spent, err := spendTalkCastMP(world, caster, damageInfo.mp)
	if err != nil {
		return err
	}
	if !spent {
		ctx.WriteString(talkCastUnableMessage(creatureName))
		return nil
	}
	if legacyMonsterSpellFails(caster) {
		return nil
	}

	damage := rollDice(damageInfo.ndice, damageInfo.sdice, damageInfo.pdice)
	_, applied, dead, err := combatWorld.ApplyCreatureDamage(target.ID, damage)
	if err != nil {
		return err
	}
	if err := combatWorld.RecordCreatureDamage(target.ID, caster.ID, applied); err != nil {
		return err
	}

	casterSubject := actorSubject(creatureName)
	targetOut := fmt.Sprintf("\n%s 당신에게 %s 주문을 외웠습니다.\n%s 당신에게 %d만큼의 상처를 입혔습니다.\n", casterSubject, spell.name, casterSubject, applied)
	roomOut := fmt.Sprintf("\n%s %s에게 %s 주문을 외웠습니다.\n%s %s에게 %d만큼의 피해를 입힙니다.\n", casterSubject, strings.TrimSpace(playerName), spell.name, casterSubject, strings.TrimSpace(playerName), applied)
	if dead {
		targetOut += "\n당신은 쓰러졌습니다.\n당신은 죽으면서 몇가지 물건을 떨어뜨렸습니다.\n"
	}
	if err := broadcastTalkTargetedAction(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut); err != nil {
		return err
	}

	if dead {
		return finalizeTalkCastPlayerDeath(world, player, target, caster)
	}
	_, _ = combatWorld.AddEnemy(caster.ID, target.ID)
	_, _ = combatWorld.AddEnemy(target.ID, caster.ID)
	_ = primeTalkAttackCombat(world, caster.ID)
	if recalculator, ok := world.(interface {
		RecalculateAC(model.CreatureID) error
		RecalculateTHACO(model.CreatureID) error
	}); ok {
		_ = recalculator.RecalculateAC(caster.ID)
		_ = recalculator.RecalculateTHACO(target.ID)
	}
	return nil
}

func finalizeTalkCastPlayerDeath(world TalkWorld, player model.Player, target model.Creature, caster model.Creature) error {
	deathWorld, ok := world.(interface {
		SetCreatureStat(model.CreatureID, string, int) error
		UpdatePlayerTags(model.PlayerID, []string, []string) (model.Player, error)
		MovePlayerToRoom(model.PlayerID, model.RoomID) error
		SavePlayer(model.PlayerID) error
		BroadcastAll(string) error
	})
	if !ok {
		return nil
	}
	hpMax := target.Stats["hpMax"]
	mpMax := target.Stats["mpMax"]
	mpCurrent := target.Stats["mpCurrent"]
	if hpMax > 0 {
		if err := deathWorld.SetCreatureStat(target.ID, "hpCurrent", hpMax); err != nil {
			return err
		}
	}
	nextMP := mpCurrent
	if mpMax/10 > nextMP {
		nextMP = mpMax / 10
	}
	if err := deathWorld.SetCreatureStat(target.ID, "mpCurrent", nextMP); err != nil {
		return err
	}
	if _, err := deathWorld.UpdatePlayerTags(player.ID, nil, []string{"PPOISN", "poison", "PDISEA", "disease"}); err != nil {
		return err
	}
	if err := deathWorld.MovePlayerToRoom(player.ID, model.RoomID("room:1008")); err != nil {
		return err
	}
	_ = deathWorld.SavePlayer(player.ID)
	if room, ok := world.Room(target.RoomID); ok && !roomHasAnyFlag(room, "RSUVIV", "survival") {
		playerName := strings.TrimSpace(target.DisplayName)
		if playerName == "" {
			playerName = strings.TrimSpace(player.DisplayName)
		}
		_ = deathWorld.BroadcastAll(fmt.Sprintf("\n### 애석하게도 %s님이 %s에게 죽었습니다.", playerName, caster.DisplayName))
	}
	return nil
}

func talkOffensiveSpellForName(spell string) (studySpell, osp_t, bool) {
	normalized := normalizeTalkSpellName(spell)
	if normalized == "" {
		return studySpell{}, osp_t{}, false
	}
	for _, known := range studySpells {
		if normalizeTalkSpellName(known.name) != normalized && normalizeTalkSpellName(known.tag) != normalized {
			continue
		}
		for _, damage := range ospell {
			if damage.tag == known.tag {
				if known.name == "" {
					known.name = damage.name
				}
				return known, damage, true
			}
		}
	}
	for _, damage := range ospell {
		if normalizeTalkSpellName(damage.name) == normalized || normalizeTalkSpellName(damage.tag) == normalized {
			return studySpell{name: damage.name, tag: damage.tag}, damage, true
		}
	}
	return studySpell{}, osp_t{}, false
}

func talkCasterHasPlayerEnemy(world TalkWorld, casterID model.CreatureID, playerID model.PlayerID) bool {
	if world == nil || casterID.IsZero() || playerID.IsZero() {
		return false
	}
	player, ok := world.Player(playerID)
	if !ok {
		return false
	}
	lister, ok := world.(interface {
		CreatureEnemies(model.CreatureID) ([]string, error)
	})
	if !ok {
		return false
	}
	enemies, err := lister.CreatureEnemies(casterID)
	if err != nil || len(enemies) == 0 {
		return false
	}

	names := []string{player.DisplayName}
	if !player.CreatureID.IsZero() {
		if creature, ok := world.Creature(player.CreatureID); ok {
			names = append(names, creature.DisplayName)
		}
	}
	for _, enemy := range enemies {
		enemy = normalizeTalkEnemyName(enemy)
		for _, name := range names {
			if enemy != "" && enemy == normalizeTalkEnemyName(name) {
				return true
			}
		}
	}
	return false
}

func normalizeTalkEnemyName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func talkCastEnemyRefusalMessage(creatureName string) string {
	creatureName = strings.TrimSpace(creatureName)
	if creatureName == "" {
		creatureName = "그"
	}
	return "\n" + actorSubject(creatureName) + " 당신에게 어떤 주문을 거는것을 거부했습니다.\n"
}

func talkCastUnableMessage(creatureName string) string {
	creatureName = strings.TrimSpace(creatureName)
	if creatureName == "" {
		creatureName = "그"
	}
	return "\n" + actorSubject(creatureName) + " 지금은 당신에게 주문을 걸어줄 수 없다고 사과합니다.\n"
}

func talkCastEffectForSpell(spell string) (talkCastEffect, bool) {
	switch normalizeTalkSpellName(spell) {
	case "vigor", "회복":
		return talkCastEffect{HealAmount: 10, MPCost: 5}, true
	case "mend", "mend wounds", "mend wound", "원기회복":
		return talkCastEffect{HealAmount: 25, MPCost: 10}, true
	case "heal", "full heal", "완치", "전회복":
		return talkCastEffect{FullHeal: true, MPCost: 50}, true
	case "cure poison", "curepoison", "해독":
		return talkCastEffect{RemoveTags: []string{"poison", "poisoned", "ppoisn"}, MPCost: 6}, true
	case "bless", "성현진", "성현":
		return talkCastEffect{AddTags: []string{"blessed"}, MPCost: 10}, true
	case "protection", "protect", "수호진":
		return talkCastEffect{AddTags: []string{"protection"}, MPCost: 10}, true
	case "invisibility", "invisible", "은둔법", "은둔술":
		return talkCastEffect{AddTags: []string{"invisible"}, MPCost: 15}, true
	case "detect invisible", "detectinvisible", "은둔감지술":
		return talkCastEffect{AddTags: []string{"detectInvisible"}, MPCost: 10}, true
	case "detect magic", "detectmagic", "주문감지술", "주문감지":
		return talkCastEffect{AddTags: []string{"detectMagic"}, MPCost: 10}, true
	case "levitate", "부양술":
		return talkCastEffect{AddTags: []string{"levitate"}, MPCost: 10}, true
	case "resist fire", "resistfire", "방열진":
		return talkCastEffect{AddTags: []string{"resistFire"}, MPCost: 12}, true
	case "fly", "비상술":
		return talkCastEffect{AddTags: []string{"fly"}, MPCost: 15}, true
	case "resist magic", "resistmagic", "보마진":
		return talkCastEffect{AddTags: []string{"resistMagic"}, MPCost: 12}, true
	case "know alignment", "knowalignment", "선악감지":
		return talkCastEffect{AddTags: []string{"knowAlignment"}, MPCost: 6}, true
	case "resist cold", "resistcold", "방한진", "추위보호":
		return talkCastEffect{AddTags: []string{"resistCold"}, MPCost: 12}, true
	case "breathe water", "breathewater", "수생술":
		return talkCastEffect{AddTags: []string{"breatheWater"}, MPCost: 12}, true
	case "earth shield", "earthshield", "지방호":
		return talkCastEffect{AddTags: []string{"earthShield"}, MPCost: 12}, true
	case "remove disease", "removedisease", "rm disease", "rmdisease", "치료", "질병치료":
		return talkCastEffect{RemoveTags: []string{"disease", "diseased", "pdisea", "mdisea"}, MPCost: 12}, true
	case "remove blindness", "remove blind", "removeblindness", "removeblind", "rm blind", "rmblind", "개안술":
		return talkCastEffect{RemoveTags: []string{"blind", "blinded", "pblind", "mblind"}, MPCost: 12}, true
	case "fear", "공포":
		return talkCastEffect{AddTags: []string{"fearful"}, MPCost: 15}, true
	case "blind", "실명":
		return talkCastEffect{AddTags: []string{"blind"}, MPCost: 15}, true
	case "silence", "봉합구":
		return talkCastEffect{AddTags: []string{"silenced"}, MPCost: 12}, true
	case "remove fear", "removefear", "공포해소":
		return talkCastEffect{RemoveTags: []string{"fear", "fearful", "pfears", "mfears", "sfears"}, MPCost: 10}, true
	default:
		return talkCastEffect{}, false
	}
}

func normalizeTalkSpellName(spell string) string {
	spell = strings.ToLower(strings.TrimSpace(spell))
	spell = strings.NewReplacer("_", " ", "-", " ").Replace(spell)
	return strings.Join(strings.Fields(spell), " ")
}

func spendTalkCastMP(world TalkWorld, caster model.Creature, cost int) (bool, error) {
	if cost <= 0 {
		return true, nil
	}
	if caster.ID.IsZero() || caster.Stats == nil {
		return false, nil
	}
	current, ok := caster.Stats["mpCurrent"]
	if !ok {
		return false, nil
	}
	if current < cost {
		return false, nil
	}
	updater, ok := world.(interface {
		SetCreatureStat(model.CreatureID, string, int) error
	})
	if !ok {
		return false, nil
	}
	if err := updater.SetCreatureStat(caster.ID, "mpCurrent", current-cost); err != nil {
		return false, err
	}
	return true, nil
}

func applyTalkCastEffect(world TalkWorld, player model.Player, target model.Creature, effect talkCastEffect) error {
	if effect.HealAmount > 0 || effect.FullHeal {
		if err := applyTalkCastHeal(world, target, effect.HealAmount, effect.FullHeal); err != nil {
			return err
		}
	}
	if len(effect.AddTags) == 0 && len(effect.RemoveTags) == 0 {
		return nil
	}
	if !target.ID.IsZero() {
		updater, ok := world.(interface {
			UpdateCreatureTags(model.CreatureID, []string, []string) (model.Creature, error)
		})
		if ok {
			if _, err := updater.UpdateCreatureTags(target.ID, effect.AddTags, effect.RemoveTags); err != nil {
				return err
			}
		}
	}
	if !player.ID.IsZero() {
		updater, ok := world.(interface {
			UpdatePlayerTags(model.PlayerID, []string, []string) (model.Player, error)
		})
		if ok {
			if _, err := updater.UpdatePlayerTags(player.ID, effect.AddTags, effect.RemoveTags); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyTalkCastHeal(world TalkWorld, target model.Creature, amount int, full bool) error {
	if target.ID.IsZero() || target.Stats == nil {
		return nil
	}
	current, ok := target.Stats["hpCurrent"]
	if !ok {
		return nil
	}
	maxHP, ok := target.Stats["hpMax"]
	if !ok || maxHP < 1 {
		return nil
	}
	next := current
	if full {
		next = maxHP
	} else {
		if amount < 1 {
			amount = 1
		}
		next = current + amount
		if next > maxHP {
			next = maxHP
		}
	}
	if next == current {
		return nil
	}
	updater, ok := world.(interface {
		SetCreatureStat(model.CreatureID, string, int) error
	})
	if !ok {
		return nil
	}
	return updater.SetCreatureStat(target.ID, "hpCurrent", next)
}

func executeTalkGiveAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	npcCreature model.Creature,
	creatureName string,
	playerName string,
	action talkFileAction,
) error {
	item := strings.TrimSpace(action.Name)
	if item == "" {
		return nil
	}
	giveWorld, ok := world.(GiveWorld)
	if !ok {
		return nil
	}
	player, ok := world.Player(actorID)
	if !ok || player.CreatureID.IsZero() {
		return nil
	}
	playerCreature, ok := world.Creature(player.CreatureID)
	if !ok {
		return nil
	}

	if number, numeric := parseTalkGiveObjectNumber(item); numeric {
		return executeTalkGivePrototypeObject(ctx, giveWorld, sessions, send, roomID, actorID, player, playerCreature, number, creatureName, playerName)
	}
	return nil
}

func executeTalkGivePrototypeObject(
	ctx *enginecmd.Context,
	world GiveWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	player model.Player,
	playerCreature model.Creature,
	number int,
	creatureName string,
	playerName string,
) error {
	protoID := legacyTalkGivePrototypeID(number)
	proto, ok := world.ObjectPrototype(protoID)
	if !ok {
		ctx.WriteString(renderTalkGiveNothingMessage(creatureName))
		return nil
	}
	objectID, err := createTalkGiftObjectFromPrototype(world, protoID, playerCreature.ID)
	if err != nil {
		return fmt.Errorf("talk give prototype %q to %q: %w", protoID, playerCreature.ID, err)
	}
	if objectID.IsZero() {
		return nil
	}
	object, ok := world.Object(objectID)
	if !ok {
		if rollbackErr := rollbackTalkGiftObject(world, objectID); rollbackErr != nil {
			return rollbackErr
		}
		return fmt.Errorf("talk give prototype %q to %q: created object %q not found", protoID, playerCreature.ID, objectID)
	}
	if talkGiveCapacityExceeded(world, playerCreature, talkObjectTotalWeight(world, object)) {
		if err := rollbackTalkGiftObject(world, objectID); err != nil {
			return err
		}
		ctx.WriteString(talkGiveInventoryFullMessage())
		return nil
	}

	questNum, _ := givePrototypeQuestNumber(proto)
	objectName := cleanGiveText(proto.DisplayName)
	objectName = giveObjectDisplayName(world, object)
	if questNum == 0 {
		questNum, _ = giveObjectQuestNumber(world, object)
	}
	if questNum != 0 && creatureQuestCompleted(playerCreature, questNum) {
		if err := rollbackTalkGiftObject(world, objectID); err != nil {
			return err
		}
		ctx.WriteString(questAlreadyCompletedMessage())
		return nil
	}
	if giveObjectHasFlag(world, object, "event", "oevent") {
		setTalkGiftObjectOwner(world, object.ID, playerName)
	}
	if objectName == "" {
		objectName = string(protoID)
	}
	if questNum != 0 {
		completeQuestDelivery(ctx, world, playerCreature, player, questNum, objectName)
	}
	targetOut := renderGiveObjectTarget(creatureName, objectName)
	roomOut := renderGiveObjectRoom(creatureName, playerName, objectName)
	return broadcastTalkGive(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut)
}

func parseTalkGiveObjectNumber(item string) (int, bool) {
	item = strings.TrimSpace(item)
	if item == "" {
		return 0, false
	}
	sign := 1
	if item[0] == '-' {
		sign = -1
		item = item[1:]
	} else if item[0] == '+' {
		item = item[1:]
	}
	value := 0
	digits := 0
	for _, r := range item {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
		digits++
	}
	if digits == 0 || sign*value <= 0 {
		return 0, false
	}
	return sign * value, true
}

func legacyTalkGivePrototypeID(number int) model.PrototypeID {
	return model.PrototypeID(fmt.Sprintf("object:o%02d:%d", number/100, number%100))
}

func createTalkGiftObjectFromPrototype(world GiveWorld, protoID model.PrototypeID, creatureID model.CreatureID) (model.ObjectInstanceID, error) {
	if cloner, ok := world.(interface {
		CloneObjectToCreatureInventory(model.ObjectInstanceID, model.CreatureID) (model.ObjectInstanceID, error)
	}); ok {
		return cloner.CloneObjectToCreatureInventory(model.ObjectInstanceID(protoID), creatureID)
	}
	if creator, ok := world.(interface {
		CreateObjectFromPrototype(model.PrototypeID, model.CreatureID) (model.ObjectInstanceID, error)
	}); ok {
		return creator.CreateObjectFromPrototype(protoID, creatureID)
	}
	return "", nil
}

func rollbackTalkGiftObject(world GiveWorld, objectID model.ObjectInstanceID) error {
	if objectID.IsZero() {
		return nil
	}
	destroyer, ok := world.(interface {
		DestroyObject(model.ObjectInstanceID) error
	})
	if !ok {
		return fmt.Errorf("talk give object %q rollback: destroy hook unavailable", objectID)
	}
	if err := destroyer.DestroyObject(objectID); err != nil {
		return fmt.Errorf("talk give object %q rollback: %w", objectID, err)
	}
	return nil
}

func givePrototypeQuestNumber(proto model.ObjectPrototype) (int, bool) {
	for _, key := range []string{"questNumber", "questnum", "questNum"} {
		if value, ok := parseGiveInt(proto.Properties[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func talkGiveInventoryFullMessage() string {
	return "당신은 더이상 가질 수 없습니다.\n"
}

func renderTalkGiveNothingMessage(creatureName string) string {
	creatureName = strings.TrimSpace(creatureName)
	if creatureName == "" {
		creatureName = "그"
	}
	return creatureName + krtext.Particle(creatureName, '0') + " 당신에게 줄 물건을 아무것도 가지고 있지 않습니다.\n"
}

func talkGiveCapacityExceeded(world GiveWorld, creature model.Creature, incomingWeight int) bool {
	return talkCreatureHeldCount(world, creature) > giveInventoryLimit ||
		talkCreatureCarriedWeight(world, creature)+incomingWeight > talkCreatureMaxWeight(creature)
}

func talkCreatureHeldCount(world GiveWorld, creature model.Creature) int {
	count := 0
	for _, objectID := range creature.Equipment {
		if !objectID.IsZero() {
			count++
		}
	}
	inventoryCount := 0
	for _, objectID := range creature.Inventory.ObjectIDs {
		if objectID.IsZero() {
			continue
		}
		inventoryCount++
		if object, ok := world.Object(objectID); ok && talkObjectIsContainer(world, object) {
			inventoryCount += talkObjectContainerCount(world, object)
		}
	}
	if inventoryCount > 200 {
		inventoryCount = 200
	}
	return count + inventoryCount
}

func talkObjectContainerCount(world GiveWorld, object model.ObjectInstance) int {
	for _, key := range []string{"shotsCurrent", "shotscur", "shotsCur", "contentsCount"} {
		if count, ok := giveObjectIntProperty(world, object, key); ok {
			return count
		}
	}
	return len(object.Contents.ObjectIDs)
}

func talkCreatureCarriedWeight(world GiveWorld, creature model.Creature) int {
	seen := map[model.ObjectInstanceID]struct{}{}
	weight := 0
	for _, objectID := range creature.Inventory.ObjectIDs {
		weight += talkCarriedObjectWeight(world, objectID, true, seen)
	}
	for _, objectID := range creature.Equipment {
		weight += talkCarriedObjectWeight(world, objectID, false, seen)
	}
	return weight
}

func talkCarriedObjectWeight(world GiveWorld, objectID model.ObjectInstanceID, skipWeightless bool, seen map[model.ObjectInstanceID]struct{}) int {
	if objectID.IsZero() {
		return 0
	}
	if _, exists := seen[objectID]; exists {
		return 0
	}
	seen[objectID] = struct{}{}
	object, ok := world.Object(objectID)
	if !ok {
		return 0
	}
	if skipWeightless && talkObjectHasFlag(world, object, "weightless", "owtles") {
		return 0
	}
	weight := talkObjectOwnWeight(world, object)
	for _, childID := range object.Contents.ObjectIDs {
		weight += talkCarriedObjectWeight(world, childID, true, seen)
	}
	return weight
}

func talkObjectTotalWeight(world GiveWorld, object model.ObjectInstance) int {
	seen := map[model.ObjectInstanceID]struct{}{}
	weight := talkObjectOwnWeight(world, object)
	for _, childID := range object.Contents.ObjectIDs {
		weight += talkCarriedObjectWeight(world, childID, true, seen)
	}
	return weight
}

func talkObjectOwnWeight(world GiveWorld, object model.ObjectInstance) int {
	if weight, ok := giveObjectIntProperty(world, object, "weight"); ok {
		return weight
	}
	return 0
}

func talkObjectIsContainer(world GiveWorld, object model.ObjectInstance) bool {
	if talkObjectHasFlag(world, object, "container", "ocontn") {
		return true
	}
	if !object.PrototypeID.IsZero() {
		if proto, ok := world.ObjectPrototype(object.PrototypeID); ok {
			return proto.Kind == model.ObjectKindContainer
		}
	}
	return false
}

func talkObjectHasFlag(world GiveWorld, object model.ObjectInstance, names ...string) bool {
	targets := normalizedFlagSet(names...)
	if hasAnyNormalizedFlag(object.Metadata.Tags, names...) || talkPropertiesHaveAnyFlag(object.Properties, targets) {
		return true
	}
	if object.PrototypeID.IsZero() {
		return false
	}
	proto, ok := world.ObjectPrototype(object.PrototypeID)
	if !ok {
		return false
	}
	return hasAnyNormalizedFlag(proto.Metadata.Tags, names...) || talkPropertiesHaveAnyFlag(proto.Properties, targets)
}

func objectPropertyEnabled(properties map[string]string, key string) bool {
	if properties == nil {
		return false
	}
	normalizedKey := normalizeGiveTag(key)
	for propertyKey, value := range properties {
		if normalizeGiveTag(propertyKey) != normalizedKey {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "1", "t", "true", "y", "yes", "on":
			return true
		default:
			return false
		}
	}
	return false
}

func talkCreatureMaxWeight(creature model.Creature) int {
	strength := creatureStat(creature, "strength")
	level := creature.Level
	if level == 0 {
		level = creatureStat(creature, "level")
	}
	maxWeight := 20 + strength*10
	if creatureStat(creature, "class") == legacyClassBarbarian {
		maxWeight += ((level + 3) / 4) * 10
	}
	return maxWeight
}

func setTalkGiftObjectOwner(world GiveWorld, objectID model.ObjectInstanceID, playerName string) {
	setter, ok := world.(interface {
		SetObjectProperty(model.ObjectInstanceID, string, string) (model.ObjectInstance, error)
	})
	if !ok {
		return
	}
	_, _ = setter.SetObjectProperty(objectID, "key[2]", strings.TrimSpace(playerName))
}

func broadcastTalkGive(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	targetOut string,
	roomOut string,
) error {
	return broadcastTalkTargetedAction(ctx, world, sessions, send, roomID, actorID, targetOut, roomOut)
}

func broadcastTalkTargetedAction(
	ctx *enginecmd.Context,
	world TalkWorld,
	sessions []ActiveSession,
	send func(session.ID, session.Command) error,
	roomID model.RoomID,
	actorID model.PlayerID,
	targetOut string,
	roomOut string,
) error {
	for _, activeSession := range sessions {
		if activeSession.ActorID == "" {
			continue
		}
		playerID := model.PlayerID(activeSession.ActorID)
		if playerRoomID(world, playerID) != roomID {
			continue
		}
		out := roomOut
		if playerID == actorID {
			out = targetOut
		}
		if string(activeSession.ID) == ctx.SessionID {
			ctx.WriteString(out)
			continue
		}
		_ = send(activeSession.ID, session.Command{Write: out})
	}
	return nil
}
