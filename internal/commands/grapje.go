package commands

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

// Ping is the minimal command: no upstream API, useful as a health check and as
// the smallest possible template for a new command.
type Grapje struct{}

// NewPing builds the /ping command.
func NewGrapje() *Grapje { return &Grapje{} }

// Definition implements Command.
func (p *Grapje) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "grapje",
		Description: "Is Martijn zijn server gehacked?",
	}
}

// Handle implements Command.
func (p *Grapje) Handle(_ context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return ReplyEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "JA",
		Description: "GROK gaat weer eens los",
		Color:       ColorSuccess,
	})
}