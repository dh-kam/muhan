package main

import (
	"errors"
	"fmt"
	"strings"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

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
