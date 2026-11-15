package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bms/internal/app"
	"bms/internal/config"
	"bms/internal/platform/clock"
)

func main() {
	cfg := config.MustLoad()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg, clock.System{})
	if err != nil {
		slog.Error("start", "err", err)
		os.Exit(1)
	}
	defer application.Close()
	application.StartJobs(ctx)
	go func() {
		if err := application.Fiber.Listen(cfg.HTTPAddr); err != nil {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = application.Fiber.ShutdownWithContext(shutdown)
}
