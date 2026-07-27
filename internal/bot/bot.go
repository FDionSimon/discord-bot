// Package bot wires a Discord session to the command registry and owns the
// lifecycle: connect, register commands, dispatch interactions, shut down.
package bot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"

	"github.com/FDionSimon/discord-bot/internal/commands"
	"github.com/FDionSimon/discord-bot/internal/config"
)

// Bot is the running application.
type Bot struct {
	cfg      *config.Config
	session  *discordgo.Session
	registry *commands.Registry
	log      *slog.Logger
}

// New creates a Bot and its Discord session, but does not connect yet.
func New(cfg *config.Config, registry *commands.Registry, log *slog.Logger) (*Bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}

	// Slash commands arrive over the interactions gateway, so the privileged
	// message-content intent is not needed. Ask for as little as possible.
	session.Identify.Intents = discordgo.IntentsGuilds

	b := &Bot{cfg: cfg, session: session, registry: registry, log: log}

	session.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		b.log.Info("connected to discord",
			"user", r.User.Username+"#"+r.User.Discriminator,
			"guilds", len(r.Guilds),
		)
	})
	session.AddHandler(b.onInteraction)

	return b, nil
}

// Start opens the gateway connection and registers the slash commands.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return fmt.Errorf("open gateway: %w", err)
	}

	// BulkOverwrite makes the registered set exactly match the code: commands
	// deleted from the registry disappear from Discord too, which avoids stale
	// commands lingering after a refactor.
	scope := "globally"
	if b.cfg.GuildID != "" {
		scope = "for guild " + b.cfg.GuildID
	}

	registered, err := b.session.ApplicationCommandBulkOverwrite(
		b.cfg.AppID, b.cfg.GuildID, b.registry.Definitions(),
	)
	if err != nil {
		return fmt.Errorf("register commands: %w", err)
	}

	b.log.Info("registered slash commands", "count", len(registered), "scope", scope, "names", b.registry.Names())
	return nil
}

// Stop closes the gateway connection.
func (b *Bot) Stop() error {
	b.log.Info("shutting down")
	return b.session.Close()
}

// onInteraction is the single entry point for every slash command.
func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	name := i.ApplicationCommandData().Name
	log := b.log.With("command", name, "user", userID(i))

	cmd, ok := b.registry.Get(name)
	if !ok {
		log.Warn("received unknown command")
		return
	}

	// Discord closes the interaction if it is not acknowledged within three
	// seconds, which is far too short for an upstream API call. Defer first,
	// then reply with followup messages once the work is done.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Error("failed to acknowledge interaction", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), b.cfg.CommandTimeout)
	defer cancel()

	// A panic in one command must not take the whole bot down.
	defer func() {
		if r := recover(); r != nil {
			log.Error("command panicked", "panic", r)
			_ = commands.ReplyError(s, i, "Something went badly wrong while running that command.")
		}
	}()

	if err := cmd.Handle(ctx, s, i); err != nil {
		// Log the real cause, show the user something harmless: upstream error
		// bodies can contain URLs with tokens in them.
		log.Error("command failed", "error", err)
		if ctx.Err() != nil {
			_ = commands.ReplyError(s, i, "That took too long and timed out. Please try again.")
			return
		}
		_ = commands.ReplyError(s, i, "Sorry, that command failed. Please try again shortly.")
		return
	}

	log.Info("command handled")
}

func userID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return "unknown"
}
