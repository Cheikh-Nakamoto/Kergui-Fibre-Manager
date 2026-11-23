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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
)

// version is overridable via -ldflags "-X main.version=...".
var version = "0.1.0-h0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version", "version":
			fmt.Println(version)
			return
		case "admin":
			// kergui-hub admin create <email>: a back-office account
			// (platform administrator). The password comes from
			// KERGUI_ADMIN_PASSWORD, never from the command line.
			os.Exit(adminCommand(os.Args[2:]))
		case "-healthcheck", "healthcheck":
			// The image is distroless (no shell, no curl): compose's
			// healthcheck runs the binary itself against /ready.
			os.Exit(healthcheck(os.Getenv("KERGUI_HUB_ADDR")))
		}
	}
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

// healthcheck asks the local hub's /ready (database reachable) and returns the
// process exit code: 0 ready, 1 not.
func healthcheck(addr string) int {
	if addr == "" {
		addr = ":8090"
	}
	if addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/ready")
	if err != nil {
		fmt.Fprintln(os.Stderr, "kergui-hub healthcheck:", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "kergui-hub healthcheck: /ready answered", resp.Status)
		return 1
	}
	return 0
}

// adminCommand creates or promotes a platform administrator, the account the
// back-office signs in with (issue #62). It only writes to the database, so
// it may run next to the running hub (docker exec, or Dokploy's terminal).
func adminCommand(args []string) int {
	if len(args) != 2 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, "usage: KERGUI_ADMIN_PASSWORD=... kergui-hub admin create <email>")
		return 2
	}
	cfg, err := config.FromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kergui-hub:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kergui-hub:", err)
		return 1
	}
	defer pool.Close()
	created, err := admin.CreateAdmin(ctx, pool, args[1], os.Getenv("KERGUI_ADMIN_PASSWORD"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kergui-hub:", err)
		return 1
	}
	if created {
		fmt.Println("platform administrator created:", args[1])
	} else {
		fmt.Println("platform administrator granted:", args[1])
	}
	return 0
}
