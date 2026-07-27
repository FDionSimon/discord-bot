package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

// Discord colour constants used by the embed helpers.
const (
	ColorSuccess = 0x2ECC71
	ColorError   = 0xE74C3C
	ColorInfo    = 0x3498DB
)

// ReplyText sends a plain-text followup message for an already-deferred
// interaction.
func ReplyText(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})
	return err
}

// ReplyEmbed sends an embed as a followup message.
func ReplyEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) error {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{embed},
	})
	return err
}

// ReplyError sends an ephemeral error message, visible only to the invoker so
// failures do not clutter the channel.
func ReplyError(s *discordgo.Session, i *discordgo.InteractionCreate, message string) error {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Flags: discordgo.MessageFlagsEphemeral,
		Embeds: []*discordgo.MessageEmbed{{
			Description: fmt.Sprintf("⚠️ %s", message),
			Color:       ColorError,
		}},
	})
	return err
}