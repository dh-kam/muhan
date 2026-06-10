package command

import (
	"errors"
	"fmt"
	"strings"

	"muhan/internal/commandparse"
	"muhan/internal/commandspec"
	"muhan/internal/metrics"
	"muhan/internal/world/model"
)

type Status int

const (
	StatusDefault    Status = 0
	StatusDisconnect Status = 1
	StatusPrompt     Status = 2
	StatusDoPrompt   Status = 3
)

var ErrUnhandledCommand = errors.New("unhandled command")

type Context struct {
	SessionID string
	ActorID   string
	Values    map[string]any
	Output    []string
}

type Handler func(*Context, ResolvedCommand) (Status, error)

type SpecialHandler func(*Context, int, ResolvedCommand) (Status, error)

const (
	legacyAliasExpandedCommandMaxBytes = 255
	legacyAliasMaxExpandedCommands     = 64
)

type Dispatcher struct {
	Registry       commandspec.Registry
	Handlers       map[string]Handler
	NumberHandlers map[int]Handler
	Special        SpecialHandler
	Privilege      PrivilegePolicy
	AliasStore     AliasStore
}

func (d Dispatcher) DispatchLine(ctx *Context, line string) (Status, error) {
	return d.dispatchLine(ctx, line, true)
}

func (d Dispatcher) dispatchLine(ctx *Context, line string, allowAlias bool) (Status, error) {
	if expanded, ok, err := d.expandAliasLine(ctx, line, allowAlias); err != nil || ok {
		if err != nil {
			return StatusDefault, err
		}
		status := StatusDefault
		for _, expandedLine := range expanded {
			var dispatchErr error
			if ctx != nil {
				ctx.WriteString("\n")
			}
			status, dispatchErr = d.dispatchLine(ctx, expandedLine, false)
			if dispatchErr != nil || status != StatusDefault {
				return status, dispatchErr
			}
		}
		return status, nil
	}

	opts := []Option(nil)
	if d.Privilege != nil {
		opts = append(opts, WithPrivilegePolicy(d.Privilege))
	}
	resolved, err := ParseAndResolveWithOptions(line, d.Registry, opts...)
	if err != nil {
		return StatusDefault, err
	}
	return d.DispatchResolved(ctx, resolved)
}

func (d Dispatcher) expandAliasLine(ctx *Context, line string, allowAlias bool) ([]string, bool, error) {
	if !allowAlias || d.AliasStore == nil || ctx == nil || ctx.ActorID == "" {
		return nil, false, nil
	}
	parsed := commandparse.Parse(line)
	if parsed.Num == 0 || parsed.Str[0] == "" {
		return nil, false, nil
	}
	aliasName := legacyLowerASCII(parsed.Str[0])
	aliases, err := d.AliasStore.ListAliases(model.PlayerID(ctx.ActorID))
	if err != nil {
		return nil, false, err
	}
	for _, alias := range aliases {
		if alias.Alias != aliasName {
			continue
		}
		expanded := legacyExpandAliasCommands(alias.Process, line)
		return expanded, true, nil
	}
	return nil, false, nil
}

func legacyExpandAliasCommands(process, fullstr string) []string {
	argsText := legacyAliasInputBeforeCommand(fullstr)
	args := legacyAliasCommandArgs(argsText)
	var b strings.Builder
	for i := 0; i < len(process); i++ {
		if process[i] != '$' {
			b.WriteByte(process[i])
			continue
		}
		i++
		if i >= len(process) {
			break
		}
		if process[i] == '*' {
			b.WriteString(argsText)
			continue
		}
		num := 0
		for i < len(process) && process[i] >= '0' && process[i] <= '9' {
			num = num*10 + int(process[i]-'0')
			i++
		}
		i--
		if num <= 0 || num > len(args) {
			continue
		}
		b.WriteString(args[num-1])
	}
	return legacySplitAliasCommands(legacyTruncateBytes(b.String(), legacyAliasExpandedCommandMaxBytes))
}

func legacyAliasInputBeforeCommand(fullstr string) string {
	fullstr = strings.TrimSpace(fullstr)
	i := strings.LastIndexByte(fullstr, ' ')
	if i < 0 {
		return ""
	}
	return fullstr[:i]
}

func legacyAliasCommandArgs(text string) []string {
	args := make([]string, 0, 16)
	i := 0
	for len(args) < 16 {
		for i < len(text) && text[i] == ' ' {
			i++
		}
		if i >= len(text) {
			break
		}
		start := i
		for i < len(text) && text[i] != ' ' {
			i++
		}
		args = append(args, text[start:i])
	}
	return args
}

func legacySplitAliasCommands(expanded string) []string {
	parts := strings.Split(expanded, ";")
	commands := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		commands = append(commands, part)
		if len(commands) == legacyAliasMaxExpandedCommands {
			break
		}
	}
	return commands
}

func (d Dispatcher) DispatchResolved(ctx *Context, resolved ResolvedCommand) (Status, error) {
	metrics.CommandsProcessed.WithLabelValues(resolved.Command()).Inc()
	if resolved.Spec.Special {
		if d.Special == nil {
			return StatusDefault, unhandledError(resolved)
		}
		return d.Special(ctx, resolved.Spec.Number, resolved)
	}

	if resolved.Spec.Handler != "" {
		if handler := d.Handlers[resolved.Spec.Handler]; handler != nil {
			return handler(ctx, resolved)
		}
	}
	if handler := d.NumberHandlers[resolved.Spec.Number]; handler != nil {
		return handler(ctx, resolved)
	}
	return StatusDefault, unhandledError(resolved)
}

func unhandledError(resolved ResolvedCommand) error {
	return fmt.Errorf("%w input %q command %q args %v spec %q handler %q number %d", ErrUnhandledCommand, resolved.Input, resolved.Command(), resolved.Args, resolved.Spec.Name, resolved.Spec.Handler, resolved.Spec.Number)
}
