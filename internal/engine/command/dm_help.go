package command

import (
	"os"
	"path/filepath"
	"strings"

	"muhan/internal/persist/legacykr"
	"muhan/internal/textfmt"
	"muhan/internal/world/model"
)

// DMHelpWorld defines the data access interface needed for dm_help.
type DMHelpWorld interface {
	Player(model.PlayerID) (model.Player, bool)
	Creature(model.CreatureID) (model.Creature, bool)
}

// NewDMHelpHandler creates a handler for the dm_help command.
func NewDMHelpHandler(root string, world DMHelpWorld) Handler {
	return func(ctx *Context, resolved ResolvedCommand) (Status, error) {
		return dmHelp(ctx, resolved, root, world)
	}
}

func dmHelp(ctx *Context, resolved ResolvedCommand, root string, world DMHelpWorld) (Status, error) {
	if ctx == nil || strings.TrimSpace(ctx.ActorID) == "" || world == nil {
		return StatusDefault, nil
	}

	playerID := model.PlayerID(ctx.ActorID)
	var creatureID model.CreatureID
	var ok bool
	if player, ok := world.Player(playerID); ok {
		creatureID = player.CreatureID
	} else {
		creatureID = model.CreatureID(ctx.ActorID)
	}

	creature, ok := world.Creature(creatureID)
	if !ok {
		return StatusDefault, nil
	}

	class := creatureClass(creature)
	if class < legacyClassDM {
		return StatusDefault, nil
	}

	topic, hasTopic := dmHelpTopicArg(resolved)

	validTopics := map[string]bool{
		"mflags":  true,
		"oflags":  true,
		"pflags":  true,
		"rflags":  true,
		"xflags":  true,
		"sflags":  true,
		"titles":  true,
		"oset":    true,
		"pset":    true,
		"char":    true,
		"wear":    true,
		"otypes":  true,
		"realms":  true,
		"exp":     true,
		"scrolls": true,
		"관리":      true,
	}

	var filename string
	if !hasTopic {
		filename = "dm_helpfile"
	} else {
		if !validTopics[topic] {
			ctx.WriteString("That dm help file does not exist.\n")
			return StatusDefault, nil
		}
		filename = topic
	}

	// Read and format the file
	text, err := readDMHelpFile(root, filename, ctx)
	if err != nil {
		ctx.WriteString("화일을 읽을 수 없습니다.\n")
		return StatusDoPrompt, nil
	}

	return renderLegacyViewFile(ctx, text, "DM 도움말 읽기 상태를 시작할 수 없습니다")
}

func dmHelpTopicArg(resolved ResolvedCommand) (string, bool) {
	if resolved.Parsed.Num > 0 {
		if resolved.Parsed.Num < 2 {
			return "", false
		}
		return resolved.Parsed.Str[1], true
	}
	if len(resolved.Args) > 0 {
		topic := strings.TrimSpace(resolved.Args[0])
		if topic != "" {
			return topic, true
		}
	}
	return "", false
}

func readDMHelpFile(root, name string, ctx *Context) (string, error) {
	path := filepath.Join(root, "help", filepath.Clean(name))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{
		Path:  path,
		Field: "dm help text",
	}, data)
	if err != nil {
		return "", err
	}
	text = textfmt.RenderLegacyColors(text, textOptionsFromContext(ctx))
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, nil
}
