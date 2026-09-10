package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/FDionSimon/discord-bot/internal/apiclient"
)

type valheimAction struct {
	title string
	path  string
}

var valheimActions = map[string]valheimAction{
	"server":  {title: "Server Info", path: "v1/status"},
	"players": {title: "Online Players", path: "v1/players"},
	"world":   {title: "World Info", path: "v1/world"},
	"bosses":  {title: "Boss Info", path: "v1/bosses"},
}

type Valheim struct {
	client *apiclient.Client
}

func NewValheim(baseURL, token string, timeout time.Duration) *Valheim {
	opts := []apiclient.Option{}
	if token != "" {
		opts = append(opts, apiclient.WithHeader("Authorization", "Bearer "+token))
	}
	return &Valheim{client: apiclient.New(baseURL, timeout, opts...)}
}

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
					{Name: "List Players", Value: "players"},
					{Name: "Boss Info", Value: "bosses"},
					{Name: "Server Info", Value: "server"},
					{Name: "World Info", Value: "world"},
				},
			},
		},
	}
}

func (m *Valheim) Handle(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	opts := OptionMap(i.ApplicationCommandData().Options)
	action := StringOption(opts, "action", "")

	spec, ok := valheimActions[action]
	if !ok {
		// Only reachable if the choices and the map fall out of sync.
		return ReplyError(s, i, "Unknown action.")
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Decoding into `any` rather than a struct keeps this endpoint-agnostic:
	// the renderer below walks whatever shape comes back.
	data, err := apiclient.GetJSON[any](ctx, m.client, spec.path, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ReplyError(s, i, "The server did not respond in time. It may be offline or lagging.")
		}
		return fmt.Errorf("call %q: %w", spec.path, err)
	}

	return ReplyEmbed(s, i, renderJSON(spec.title, data))
}

func renderJSON(title string, data any) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{Title: title, Color: ColorSuccess}

	switch v := data.(type) {
	case map[string]any:
		embed.Fields = objectFields(v)
		if len(embed.Fields) == 0 {
			embed.Description = "_(empty response)_"
		}

	case []any:
		if len(v) == 0 {
			embed.Description = "_(nothing to show)_"
			break
		}
		lines := make([]string, 0, len(v))
		for idx, item := range v {
			if idx >= 20 {
				lines = append(lines, fmt.Sprintf("_...and %d more_", len(v)-idx))
				break
			}
			lines = append(lines, fmt.Sprintf("%d. %s", idx+1, formatValue(item)))
		}
		embed.Description = truncateField(strings.Join(lines, "\n"), 4000)

	default:
		embed.Description = formatValue(v)
	}

	return embed
}

func objectFields(obj map[string]any) []*discordgo.MessageEmbedField {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fields := make([]*discordgo.MessageEmbedField, 0, len(keys))
	for _, k := range keys {
		if len(fields) >= 25 { // Discord's hard cap on embed fields
			break
		}
		value := formatValue(obj[k])
		if value == "" {
			value = "—" // Discord rejects empty field values
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   truncateField(prettifyKey(k), 256),
			Value:  truncateField(value, 1024),
			Inline: len(value) < 30,
		})
	}
	return fields
}

func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "—"
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%.2f", t)
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for idx, item := range t {
			if idx >= 10 {
				parts = append(parts, fmt.Sprintf("...+%d", len(t)-idx))
				break
			}
			parts = append(parts, formatValue(item))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		// Nested objects: show them inline as key=value pairs rather than
		// recursing into more fields, which would blow the 25-field cap.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, formatValue(t[k])))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", t)
	}
}

func prettifyKey(k string) string {
	k = strings.ReplaceAll(k, "_", " ")
	k = strings.ReplaceAll(k, "-", " ")
	if k == "" {
		return "field"
	}
	return strings.ToUpper(k[:1]) + k[1:]
}

func truncateField(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
