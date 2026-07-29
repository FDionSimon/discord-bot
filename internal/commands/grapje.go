package commands

import (
	"context"
	"os/exec"
	"time"	
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Ping is the minimal command: no upstream API, useful as a health check and as
// the smallest possible template for a new command.
type LocalCommand struct{}

// NewPing builds the /ping command.
func NewCommand() *LocalCommand { return &LocalCommand{} }

// Definition implements Command.
func (p *LocalCommand) Definition() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        "cmd",
		Description: "Local Command Test",
	}
}

// Handle implements Command.
func (p *LocalCommand) Handle(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ls", "-la")
	cmd.Dir = "/mnt/c/repos/me/discord-bot" // working directory; defaults to the bot's

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run ls: %w", err)
	}

	result := strings.TrimSpace(string(out))
	if len(result) > 3900 {
		result = result[:3900] + "\n... (truncated)"
	}
	
	return ReplyEmbed(s, i, &discordgo.MessageEmbed{
		Title:       "Output",
		Description: "Result is " + result,
		Color:       ColorSuccess,
	})
}