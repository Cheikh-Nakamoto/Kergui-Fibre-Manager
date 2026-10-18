// Command kergui-hub is the central node of the semi-distributed Kergui system
// (RFC-005): accounts, households (tenants), members, and — in later
// milestones — the relays that reach each household's router from its LAN.
// It needs Postgres; the kergui CLI and the mobile core never link it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
)

// version is overridable via -ldflags "-X main.version=...".
var version = "0.1.0-h0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "kergui-hub:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		return err
	}
	// The kernel reads APP_ENV itself: anything but production/staging adds
	// the failing source file and line to every error response.
	if err := os.Setenv("APP_ENV", cfg.Env); err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log) // the kernel migrator logs through the default logger

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, version, log)
	if err != nil {
		return err
	}
	defer a.Close()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: relays hold long-poll requests open (RFC-005).
		IdleTimeout: 2 * time.Minute,
	}
	busErr := make(chan error, 1)
	go func() { busErr <- a.Run(ctx) }()
	srvErr := make(chan error, 1)
	go func() {
		log.Info("kergui-hub listening", "addr", cfg.Addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err = <-srvErr:
	case err = <-busErr:
		busErr = nil
	}
	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if serr := srv.Shutdown(shutdownCtx); serr != nil {
		log.Error("shutdown", "error", serr)
	}
	// Let the dispatcher finish before the deferred Close drops the pool.
	if busErr != nil {
		if berr := <-busErr; err == nil {
			err = berr
		}
	}
	return err
}
