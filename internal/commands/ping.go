package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Ping is the minimal command: no upstream API, useful as a health check and as
// the smallest possible template for a new command.
type Ping struct{}

// NewPing builds the /ping command.
func NewPing() *Ping { return &Ping{} }

// Definition implements Command.
func (p *Ping) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "ping",
		Description: "Check whether the bot is alive and how fast the gateway is",
	}
}

// Handle implements Command.
func (p *Ping) Handle(_ context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	latency := s.HeartbeatLatency().Round(time.Millisecond)
	return ReplyEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "Pong",
		Description: fmt.Sprintf("Gateway latency: **%s**", latency),
		Color:       ColorSuccess,
	})
}