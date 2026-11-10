package archipelago

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Relay forwards multiworld events into a Discord channel.
//
// Events are batched rather than posted one by one. Discord allows roughly
// five messages per five seconds per channel, and an active multiworld can
// produce item sends far faster than that; posting individually would hit the
// rate limiter within seconds and fall permanently behind.
type Relay struct {
	session   *discordgo.Session
	channelID string
	client    *Client
	log       *slog.Logger

	// FlushInterval is how long events accumulate before being posted.
	FlushInterval time.Duration
	// Kinds filters which PrintJSON types are forwarded. Empty means all.
	Kinds map[string]bool
}

// DefaultKinds is a reasonable feed: progress and arrivals, without the chatter
// of command output and tutorial text.
func DefaultKinds() map[string]bool {
	return map[string]bool{
		"ItemSend": true,
		"Hint":     true,
		"Join":     true,
		"Part":     true,
		"Chat":     true,
		"Goal":     true,
		"Release":  true,
		"Collect":  true,
		"Countdown": true,
	}
}

// NewRelay builds a Relay. channelID is a Discord channel ID, which you get by
// right-clicking a channel with Developer Mode enabled.
func NewRelay(s *discordgo.Session, channelID string, client *Client, log *slog.Logger) *Relay {
	return &Relay{
		session:       s,
		channelID:     channelID,
		client:        client,
		log:           log,
		FlushInterval: 3 * time.Second,
		Kinds:         DefaultKinds(),
	}
}

// Run consumes events until ctx is cancelled. It does not return until then,
// so start it on its own goroutine.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.FlushInterval)
	defer ticker.Stop()

	var batch []string

	for {
		select {
		case <-ctx.Done():
			r.flush(batch)
			return

		case ev := <-r.client.Events():
			if len(r.Kinds) > 0 && ev.Kind != "" && !r.Kinds[ev.Kind] {
				continue
			}
			batch = append(batch, format(ev))

			// Flush early when the batch approaches Discord's 2000-character
			// message limit, rather than waiting for the tick and splitting.
			if size(batch) > 1500 {
				r.flush(batch)
				batch = nil
			}

		case <-ticker.C:
			if len(batch) > 0 {
				r.flush(batch)
				batch = nil
			}
		}
	}
}

// flush posts one message, splitting if the batch still exceeds the limit.
func (r *Relay) flush(batch []string) {
	if len(batch) == 0 {
		return
	}

	// The fence costs a few characters, so leave room for it.
	for _, chunk := range chunk(batch, 1850) {
		// Discord only honors ANSI escape codes inside a fence tagged "ansi".
		// Outside one they show as literal garbage, so any message carrying
		// colored names has to be wrapped whole.
		if strings.Contains(chunk, "\u001b[") {
			chunk = "```ansi\n" + chunk + "\n```"
		}
		if _, err := r.session.ChannelMessageSend(r.channelID, chunk); err != nil {
			// A failed post is logged and dropped. Retrying risks duplicating
			// messages that did land, and the feed is informational.
			r.log.Error("failed to post archipelago events", "error", err, "channel", r.channelID)
			return
		}
	}
}

// format renders one event as a Discord line.
func format(ev Event) string {
	text := sanitize(ev.Text)

	switch ev.Kind {
	case "Goal":
		return "🏆 " + text
	case "Join":
		return "➡️ " + text
	case "Part":
		return "⬅️ " + text
	case "Hint":
		return "💡 " + text
	case "Chat":
		return "💬 " + text
	case "Countdown":
		return "⏱️ " + text
	case "Release", "Collect":
		return "📦 " + text
	default:
		return "• " + text
	}
}

// sanitize neutralizes Discord markdown and mentions in game-sourced text.
// Player names and chat come from users, so an unescaped name containing
// "@everyone" or backticks would otherwise ping the server or break layout.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "@everyone", "@​everyone")
	s = strings.ReplaceAll(s, "@here", "@​here")
	// Breaking out of an ansi fence would expose raw escape codes, so a
	// fence in player-supplied text is defanged here.
	s = strings.ReplaceAll(s, "```", "'''")
	return s
}

func size(batch []string) int {
	n := 0
	for _, line := range batch {
		n += len(line) + 1
	}
	return n
}

// chunk packs lines into messages no longer than max characters.
//
// An entry may itself span several lines: a hint lookup is one event holding a
// whole list. Those are split on their own newlines so a long list becomes
// several Discord messages rather than one truncated one. Only a single line
// that exceeds max on its own is cut, which no realistic hint does.
func chunk(entries []string, max int) []string {
	// Flatten to individual lines first, so splits land between them.
	var lines []string
	for _, entry := range entries {
		lines = append(lines, strings.Split(entry, "\n")...)
	}

	var out []string
	var current strings.Builder

	for _, line := range lines {
		if len(line) > max {
			line = line[:max-3] + "..."
		}
		if current.Len()+len(line)+1 > max {
			out = append(out, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}

	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}