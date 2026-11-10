package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/shabbybastard/knigojor/internal/bot"
	"github.com/shabbybastard/knigojor/internal/config"
)

// version is injected by the Makefile and release workflow.
var version = "0.2.0"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	settings, err := config.Load("")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	switch strings.ToUpper(settings.LogLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "WARNING", "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, err := bot.NewApp(ctx, settings, version)
	if err != nil {
		slog.Error("failed to start", "error", err)
		os.Exit(1)
	}
	defer app.Close(context.Background())

	slog.Info("starting knigojor", "version", version)
	if err := app.Run(ctx); err != nil {
		slog.Error("bot stopped with error", "error", err)
		os.Exit(1)
	}
}
