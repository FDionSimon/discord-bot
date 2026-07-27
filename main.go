package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/FDionSimon/discord-bot/internal/bot"
	"github.com/FDionSimon/discord-bot/internal/commands"
	"github.com/FDionSimon/discord-bot/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration error", "error", err)
		os.Exit(1)
	}

	// Register every command here. This is the only place that changes when you
	// add a new one.
	registry := commands.NewRegistry(
		commands.NewPing(),
	)

	b, err := bot.New(cfg, registry, log)
	if err != nil {
		log.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	if err := b.Start(); err != nil {
		log.Error("failed to start bot", "error", err)
		os.Exit(1)
	}

	// Block until the process is asked to stop, then close the gateway cleanly
	// so Discord does not keep the session hanging.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	if err := b.Stop(); err != nil {
		log.Error("error during shutdown", "error", err)
		os.Exit(1)
	}
}