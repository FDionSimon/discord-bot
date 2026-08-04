package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/FDionSimon/discord-bot/internal/minecraft"
)

// safeActions maps a user-facing choice to the RCON command it runs.
// Discord validates choices server-side, so /mc can never send anything that
// is not in this map — no input sanitising needed.
var safeActions = map[string]string{
	"players":    "list",
	"time":       "time query daytime",
	"difficulty": "difficulty",
	"whitelist":  "whitelist list",
}

// Minecraft exposes read-only server queries to everyone in the guild.
type Minecraft struct {
	client *minecraft.Client
}

// NewMinecraft builds the /mc command.
func NewMinecraft(client *minecraft.Client) *Minecraft {
	return &Minecraft{client: client}
}

// Definition implements Command.
func (m *Minecraft) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "mc",
		Description: "Query the Minecraft server",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "action",
				Description: "What to ask the server",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Who is online", Value: "players"},
					{Name: "In-game time", Value: "time"},
					{Name: "Difficulty", Value: "difficulty"},
					{Name: "Whitelist", Value: "whitelist"},
				},
			},
		},
	}
}

// Handle implements Command.
func (m *Minecraft) Handle(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !m.client.Configured() {
		return ReplyError(s, i, "RCON is not configured on this bot.")
	}

	opts := OptionMap(i.ApplicationCommandData().Options)
	action := StringOption(opts, "action", "")

	command, ok := safeActions[action]
	if !ok {
		// Only reachable if the choices and the map fall out of sync.
		return ReplyError(s, i, "Unknown action.")
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	out, err := m.client.Execute(ctx, command)
	if err != nil {
		if ctx.Err() != nil {
			return ReplyError(s, i, "The server did not respond in time. It may be offline or lagging.")
		}
		return fmt.Errorf("rcon %q: %w", command, err)
	}

	if out == "" {
		out = "_(the server returned nothing)_"
	}

	return ReplyEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "Minecraft Server",
		Description: out,
		Color:       ColorSuccess,
		Footer:      &discordgo.MessageEmbedFooter{Text: "via RCON: " + command},
	})
}
