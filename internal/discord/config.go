package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds every runtime setting the bot needs. Everything comes from the
// environment so the binary stays deployable without a config file.
type Config struct {
	// DiscordToken is the bot token from the Discord developer portal.
	DiscordToken string
	// AppID is the application (client) ID. Required to register slash commands.
	AppID string
	// GuildID scopes command registration to a single guild. Guild commands
	// update instantly, global commands can take up to an hour to propagate,
	// so set this during development and leave it empty in production.
	GuildID string

	// HTTPTimeout bounds a single outbound HTTP request to a third-party API.
	HTTPTimeout time.Duration
	// CommandTimeout bounds the total handling of one slash command, including
	// retries. Keep it under 15 minutes: that is Discord's followup window.
	CommandTimeout time.Duration

	// GitHubToken is optional. Without it the GitHub API allows 60 requests per
	// hour per IP; with it, 5000.
	GitHubToken string
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		DiscordToken:   strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		AppID:          strings.TrimSpace(os.Getenv("DISCORD_APP_ID")),
		GuildID:        strings.TrimSpace(os.Getenv("DISCORD_GUILD_ID")),
		GitHubToken:    strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		HTTPTimeout:    durationEnv("HTTP_TIMEOUT", 10*time.Second),
		CommandTimeout: durationEnv("COMMAND_TIMEOUT", 30*time.Second),
	}

	var missing []string
	if cfg.DiscordToken == "" {
		missing = append(missing, "DISCORD_TOKEN")
	}
	if cfg.AppID == "" {
		missing = append(missing, "DISCORD_APP_ID")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}