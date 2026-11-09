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
	// Discord Info
	DiscordToken string	// DiscordToken is the bot token from the Discord developer portal.
	AppID string // AppID is the application (client) ID. Required to register slash commands.
	GuildID string // GuildID scopes command registration to a single guild. Guild commands update instantly, global commands can take up to an hour to propagate, so set this during development and leave it empty in production.

	//Timeouts
	HTTPTimeout time.Duration // HTTPTimeout bounds a single outbound HTTP request to a third-party API.
	CommandTimeout time.Duration // CommandTimeout bounds the total handling of one slash command, including retries. Keep it under 15 minutes: that is Discord's followup window.

	// Minecraft RCON
	RCONAddressMC  string // localhost:port
	RCONPasswordMC string // rcon pw
	
	// Valheim RCON
	RCONAddressVH  string // localhost:port
	RCONPasswordVH string // rcon pw

	// Archipelago Info
	APAddress string // Server URL, default: archipelago.gg
	APSlot string // User slot, select existing user
	APPassword string // Slot password, default: empty
	APChannelID string // Discord channel for server output
	APTLS bool // true: ws:// | false: wss://
}

// MC Bot enabled
func (c *Config) MCEnabled() bool {
	return c.RCONAddressMC != "" && c.RCONPasswordMC != ""
}

// VH Bot enabled
func (c *Config) VHEnabled() bool {
	return c.RCONAddressVH != "" && c.RCONPasswordVH != ""
}

// AP Bot enabled
func (c *Config) APEnabled() bool {
	return c.APAddress != "" && c.APSlot != "" && c.APChannelID != ""
}

// Load reads configuration from the environment and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		DiscordToken:   strings.TrimSpace(os.Getenv("DISCORD_TOKEN")),
		AppID:          strings.TrimSpace(os.Getenv("DISCORD_APP_ID")),
		GuildID:        strings.TrimSpace(os.Getenv("DISCORD_GUILD_ID")),
		RCONAddressMC:  strings.TrimSpace(os.Getenv("RCON_ADDRESS_MC")),
		RCONPasswordMC: strings.TrimSpace(os.Getenv("RCON_PASSWORD_MC")),
		RCONAddressVH:  strings.TrimSpace(os.Getenv("RCON_ADDRESS_VH")),
		RCONPasswordVH: strings.TrimSpace(os.Getenv("RCON_PASSWORD_VH")),
		APAddress:		strings.TrimSpace(os.Getenv("AP_ADDRESS")),
		APSlot:			strings.TrimSpace(os.Getenv("AP_SLOT")),
		APPassword:		os.Getenv("AP_PASSWORD"),
		APChannelID:	strings.TrimSpace(os.Getenv("AP_CHANNEL_ID")),
		APTLS: 			os.Getenv("AP_TLS"), true,
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
