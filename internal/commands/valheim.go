package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/FDionSimon/discord-bot/internal/rcon"
)

var valhiemActions = map[string]string{
	"players":      "player list",
	"findPlayer":   "player info",
	"eventsList":   "event list",
	"currentEvent": "current event",
	"serverStats":  "server stats",
	"globalKeys":   "boss info",
}

type Valheim struct {
	client *rcon.Client
}

// NewMinecraft builds the /mc command.
func NewValheim(client *rcon.Client) *Valheim {
	return &Valheim{client: client}
}

// Definition implements Command.
func (m *Valheim) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "vh",
		Description: "Query Shon's Valheim server",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "action",
				Description: "What to ask the server",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "List players", Value: "player list"},
					{Name: "Locate player", Value: "player info"},
					{Name: "List events", Value: "event list"},
				},
			},
		},
	}
}

// Handle implements Command.
func (m *Valheim) Handle(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if !m.client.Configured() {
		return ReplyError(s, i, "RCON is not configured on this bot.")
	}

	opts := OptionMap(i.ApplicationCommandData().Options)
	action := StringOption(opts, "action", "")

	command, ok := valhiemActions[action]
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
		Title: out,
		Color: ColorSuccess,
	})
}
