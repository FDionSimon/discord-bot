// Package commands defines the slash-command contract and the registry that
// maps command names to their handlers.
package commands

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// Command is everything the bot needs to know about one slash command:
// how to declare it to Discord, and what to do when someone invokes it.
//
// Handle is called after the interaction has already been acknowledged with a
// deferred response, so implementations have up to 15 minutes and must reply
// using followup messages (see the reply helpers in package bot).
type Command interface {
	// Definition is the payload registered with Discord's API.
	Definition() *discordgo.ApplicationCommand
	// Handle executes the command. Returning an error makes the dispatcher
	// send a generic failure message and log the details.
	Handle(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error
}

// Registry holds the commands the bot serves, keyed by command name.
type Registry struct {
	commands map[string]Command
}

// NewRegistry builds a registry from the given commands.
// It panics on duplicate names, since that is a programming error that would
// otherwise silently drop a command at startup.
func NewRegistry(cmds ...Command) *Registry {
	r := &Registry{commands: make(map[string]Command, len(cmds))}
	for _, c := range cmds {
		name := c.Definition().Name
		if _, exists := r.commands[name]; exists {
			panic(fmt.Sprintf("commands: duplicate command name %q", name))
		}
		r.commands[name] = c
	}
	return r
}

// Get returns the handler for a command name.
func (r *Registry) Get(name string) (Command, bool) {
	c, ok := r.commands[name]
	return c, ok
}

// Definitions returns every command definition, for bulk registration.
func (r *Registry) Definitions() []*discordgo.ApplicationCommand {
	defs := make([]*discordgo.ApplicationCommand, 0, len(r.commands))
	for _, c := range r.commands {
		defs = append(defs, c.Definition())
	}
	return defs
}

// Names lists the registered command names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	return names
}

// OptionMap flattens interaction options into a lookup by option name.
// Discord only sends options the user actually filled in, so always check the
// boolean before using a value.
func OptionMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(opts))
	for _, opt := range opts {
		m[opt.Name] = opt
	}
	return m
}

// StringOption returns a string option's value, or fallback when it is absent.
func StringOption(opts map[string]*discordgo.ApplicationCommandInteractionDataOption, name, fallback string) string {
	if opt, ok := opts[name]; ok {
		return opt.StringValue()
	}
	return fallback
}