package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/engine/game"
	"muhan/internal/session"
)

func TestServerSuicideSinkBroadcastsAndLogsViaHooks(t *testing.T) {
	inputs := serverTestRuntimeInputs(t)
	defer inputs.world.Close()
	if err := inputs.world.SetCreatureStat("creature:alice", "level", 6); err != nil {
		t.Fatalf("raise alice level: %v", err)
	}

	var broadcasts []session.Command
	var logs []string
	sink := serverSuicideSink{
		world: inputs.world,
		root:  inputs.summary.Root,
		now: func() time.Time {
			return time.Unix(123, 0).UTC()
		},
		logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	}
	ctx := &enginecmd.Context{
		ActorID: "player:alice",
		Values: map[string]any{
			game.ContextBroadcastKey: func(cmd session.Command) error {
				broadcasts = append(broadcasts, cmd)
				return nil
			},
		},
	}

	if err := sink.RequestSuicide(ctx, "player:alice"); err != nil {
		t.Fatalf("RequestSuicide() error = %v", err)
	}
	if len(broadcasts) != 1 || !strings.Contains(broadcasts[0].Write, "Alice님이 자살신청을 하였습니다.") {
		t.Fatalf("broadcasts = %#v, want suicide broadcast", broadcasts)
	}
	if !serverSuicideTestLogContains(logs, "[SUICIDE]", "Alice님이 자살신청을 하였습니다.") {
		t.Fatalf("logs = %#v, want suicide log", logs)
	}
	if _, ok := inputs.world.Player("player:alice"); ok {
		t.Fatal("player still exists after suicide sink finalization")
	}
}

func serverSuicideTestLogContains(logs []string, parts ...string) bool {
	for _, line := range logs {
		matched := true
		for _, part := range parts {
			if !strings.Contains(line, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
