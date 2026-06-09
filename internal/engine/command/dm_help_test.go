package command

import (
	"strings"
	"testing"

	"muhan/internal/commandparse"
	"muhan/internal/commandspec"
	"muhan/internal/world/model"
)

type mockDMHelpWorld struct {
	players   map[model.PlayerID]model.Player
	creatures map[model.CreatureID]model.Creature
}

func (w mockDMHelpWorld) Player(id model.PlayerID) (model.Player, bool) {
	player, ok := w.players[id]
	return player, ok
}

func (w mockDMHelpWorld) Creature(id model.CreatureID) (model.Creature, bool) {
	creature, ok := w.creatures[id]
	return creature, ok
}

func TestDMHelpHandlerAuthorization(t *testing.T) {
	world := mockDMHelpWorld{
		players: map[model.PlayerID]model.Player{
			"player:user": {ID: "player:user", CreatureID: "creature:user"},
			"player:dm":   {ID: "player:dm", CreatureID: "creature:dm"},
		},
		creatures: map[model.CreatureID]model.Creature{
			"creature:user": {ID: "creature:user", Stats: map[string]int{"class": 1}}, // Not DM
			"creature:dm":   {ID: "creature:dm", Stats: map[string]int{"class": 13}},  // DM
		},
	}

	root := t.TempDir()
	writeLegacyHelpFixture(t, root, "dm_helpfile", "DM 도움말\n")

	handler := NewDMHelpHandler(root, world)

	t.Run("Rejects Non-DM Caster", func(t *testing.T) {
		ctx := &Context{ActorID: "player:user"}
		resolved := ResolvedCommand{
			Input: "*dmhelp",
			Spec:  commandspec.CommandSpec{Name: "*dmhelp", Handler: "dm_help"},
		}
		status, err := handler(ctx, resolved)
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if status != StatusDefault {
			t.Errorf("status = %v, want StatusDefault", status)
		}
		if got := ctx.OutputString(); got != "" {
			t.Errorf("output = %q, want no permission output", got)
		}
	})

	t.Run("Allows DM Caster", func(t *testing.T) {
		ctx := &Context{ActorID: "player:dm"}
		resolved := ResolvedCommand{
			Input: "*dmhelp",
			Spec:  commandspec.CommandSpec{Name: "*dmhelp", Handler: "dm_help"},
		}
		status, err := handler(ctx, resolved)
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		if status != StatusDoPrompt {
			t.Errorf("status = %v, want StatusDoPrompt", status)
		}
		if got := ctx.OutputString(); got != "DM 도움말\n" {
			t.Errorf("output = %q, want %q", got, "DM 도움말\n")
		}
	})
}

func TestDMHelpHandlerTopics(t *testing.T) {
	world := mockDMHelpWorld{
		players: map[model.PlayerID]model.Player{
			"player:dm": {ID: "player:dm", CreatureID: "creature:dm"},
		},
		creatures: map[model.CreatureID]model.Creature{
			"creature:dm": {ID: "creature:dm", Stats: map[string]int{"class": 13}},
		},
	}

	root := t.TempDir()
	writeLegacyHelpFixture(t, root, "dm_helpfile", "기본 DM 도움말\n")
	writeLegacyHelpFixture(t, root, "mflags", "몬스터 플래그 도움말\n")

	writeLegacyHelpFixture(t, root, "관리", "관리 도움말\n")

	handler := NewDMHelpHandler(root, world)

	tests := []struct {
		name       string
		args       []string
		wantStatus Status
		wantOutput string
	}{
		{
			name:       "No arguments (defaults to dm_helpfile)",
			args:       []string{},
			wantStatus: StatusDoPrompt,
			wantOutput: "기본 DM 도움말\n",
		},
		{
			name:       "Empty argument (defaults to dm_helpfile)",
			args:       []string{""},
			wantStatus: StatusDoPrompt,
			wantOutput: "기본 DM 도움말\n",
		},
		{
			name:       "Valid ASCII topic",
			args:       []string{"mflags"},
			wantStatus: StatusDoPrompt,
			wantOutput: "몬스터 플래그 도움말\n",
		},
		{
			name:       "ASCII topic remains case-sensitive like C strcmp",
			args:       []string{"MFlags"},
			wantStatus: StatusDefault,
			wantOutput: "That dm help file does not exist.\n",
		},
		{
			name:       "Valid Korean topic",
			args:       []string{"관리"},
			wantStatus: StatusDoPrompt,
			wantOutput: "관리 도움말\n",
		},
		{
			name:       "Invalid topic",
			args:       []string{"invalid_topic"},
			wantStatus: StatusDefault,
			wantOutput: "That dm help file does not exist.\n",
		},
		{
			name:       "Valid topic but file missing",
			args:       []string{"oflags"},
			wantStatus: StatusDoPrompt,
			wantOutput: "화일을 읽을 수 없습니다.\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{ActorID: "player:dm"}
			resolved := ResolvedCommand{
				Input: "*dmhelp " + strings.Join(tt.args, " "),
				Args:  tt.args,
				Spec:  commandspec.CommandSpec{Name: "*dmhelp", Handler: "dm_help"},
			}
			status, err := handler(ctx, resolved)
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %v, want %v", status, tt.wantStatus)
			}
			if got := ctx.OutputString(); got != tt.wantOutput {
				t.Errorf("output = %q, want %q", got, tt.wantOutput)
			}
		})
	}
}

func TestDMHelpHandlerTopicFromParsedSlotWithoutSyntheticArgs(t *testing.T) {
	world := mockDMHelpWorld{
		players: map[model.PlayerID]model.Player{
			"player:dm": {ID: "player:dm", CreatureID: "creature:dm"},
		},
		creatures: map[model.CreatureID]model.Creature{
			"creature:dm": {ID: "creature:dm", Stats: map[string]int{"class": legacyClassDM}},
		},
	}

	root := t.TempDir()
	writeLegacyHelpFixture(t, root, "mflags", "몬스터 플래그 도움말\n")

	resolved := ResolvedCommand{
		Input: "*dmhelp mflags",
		Parsed: commandparse.Command{
			Num: 2,
			Str: [commandparse.CommandMax]string{"*dmhelp", "mflags"},
		},
		Spec: commandspec.CommandSpec{Name: "*dmhelp", Handler: "dm_help"},
	}
	ctx := &Context{ActorID: "player:dm"}

	status, err := NewDMHelpHandler(root, world)(ctx, resolved)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDoPrompt {
		t.Fatalf("status = %v, want %v", status, StatusDoPrompt)
	}
	if got := ctx.OutputString(); got != "몬스터 플래그 도움말\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestDMHelpPaginatesLongHelpLikeLegacyViewFile(t *testing.T) {
	world := mockDMHelpWorld{
		players: map[model.PlayerID]model.Player{
			"player:dm": {ID: "player:dm", CreatureID: "creature:dm"},
		},
		creatures: map[model.CreatureID]model.Creature{
			"creature:dm": {ID: "creature:dm", Stats: map[string]int{"class": legacyClassDM}},
		},
	}
	root := t.TempDir()
	writeLegacyHelpFixture(t, root, "dm_helpfile", helpLongLines(24))

	var pending PendingLineHandler
	ctx := &Context{
		ActorID: "player:dm",
		Values: map[string]any{
			ContextPendingLineKey: func(handler PendingLineHandler) {
				pending = handler
			},
		},
	}
	resolved := ResolvedCommand{
		Input: "*dmhelp",
		Spec:  commandspec.CommandSpec{Name: "*dmhelp", Handler: "dm_help"},
	}

	status, err := NewDMHelpHandler(root, world)(ctx, resolved)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if status != StatusDoPrompt {
		t.Fatalf("first page status = %d, want StatusDoPrompt", status)
	}
	out := ctx.OutputString()
	if !strings.Contains(out, "line S\n") {
		t.Fatalf("first page output missing final first-page line:\n%s", out)
	}
	if strings.Contains(out, "line T\n") {
		t.Fatalf("first page output included continuation line:\n%s", out)
	}
	if !strings.Contains(out, postReadContinuePrompt) {
		t.Fatalf("first page output missing continue prompt:\n%s", out)
	}
	if pending == nil {
		t.Fatal("pending continuation handler was not installed")
	}

	ctx.Output = nil
	status, err = pending(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusDefault {
		t.Fatalf("cancel status = %d, want StatusDefault", status)
	}
	if got := ctx.OutputString(); got != "중단합니다.\n" {
		t.Fatalf("cancel output = %q", got)
	}
	if pending != nil {
		t.Fatal("pending continuation handler was not cleared")
	}
}
