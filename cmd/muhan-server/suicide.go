package main

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/engine/game"
	"muhan/internal/krtext"
	"muhan/internal/session"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

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
