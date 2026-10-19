package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/config"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// agentStateID is the vault id the relay's own secrets live under. It is
// not a router id, so it cannot collide with one.
const agentStateID = "hub-agent"

// EnvAgentToken carries a relay token without putting it in the command
// line, where `ps` would show it to every user on the machine.
const EnvAgentToken = "KERGUI_AGENT_TOKEN"

// cmdHub dispatches `kergui hub …`, the PC relay of RFC-005: an optional
// relay for a household that has a computer at home, and the bench the
// integration tests drive.
func (a *App) cmdHub(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.hubUsage()
		return 2
	}
	switch args[0] {
	case "enroll":
		return a.cmdHubEnroll(ctx, args[1:])
	case "status":
		return a.cmdHubStatus(ctx, args[1:])
	case "help", "-h", "--help":
		a.hubUsage()
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown hub command %q\n\n", args[0])
		a.hubUsage()
		return 2
	}
}

func (a *App) hubUsage() {
	fmt.Fprint(a.Err, `kergui hub — join a household on a Kergui hub (RFC-005)

Usage:
  kergui hub enroll <invite> --hub URL [--name NAME] [--kind pc|dedicated]
  kergui hub status

The invite is the QR payload from the owner's app (kgp1.…). Enrolling
generates this relay's keys, joins the household and prints a six-digit
code: it must match the one on the owner's screen. If it does not, stop —
something between you and the owner swapped a key.

Then run the relay with:
  kergui serve --hub URL --router https://192.168.1.1 --router-pin <sha256>
`)
}

func (a *App) cmdHubEnroll(ctx context.Context, args []string) int {
	var hubURL, name, kind string
	fs := flag.NewFlagSet("hub enroll", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.StringVar(&hubURL, "hub", "", "hub base URL, e.g. https://hub.kergui.app")
	fs.StringVar(&name, "name", "", "a name the owner will recognise in the app")
	fs.StringVar(&kind, "kind", "pc", "pc or dedicated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || hubURL == "" {
		a.hubUsage()
		return 2
	}
	invite, err := trust.ParseInvite(fs.Arg(0))
	if err != nil {
		return a.fail(fmt.Errorf("that does not look like a pairing invite: %w", err))
	}

	cfg := RuntimeConfig{DBPath: config.DefaultDBPath, MasterKey: os.Getenv(config.EnvMasterKey)}
	svc, closeFn, err := a.Build(cfg)
	if err != nil {
		return a.fail(err)
	}
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}

	state, res, err := agent.Enroll(ctx, hubURL, invite, name, kind, http.DefaultClient)
	if err != nil {
		return a.fail(err)
	}
	if err := saveAgentState(ctx, svc.Vault, state); err != nil {
		return a.fail(err)
	}
	fmt.Fprintf(a.Out, "Paired with the household %s as relay %s.\n", res.TenantID, res.AgentID)
	fmt.Fprintf(a.Out, "\n  Verification code: %s\n\n", res.Code)
	fmt.Fprintln(a.Out, "It must match the code on the owner's screen. If it differs, revoke this relay.")
	fmt.Fprintln(a.Out, "The owner now signs this relay into the household's trust bundle.")
	return 0
}

func (a *App) cmdHubStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("hub status", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := RuntimeConfig{DBPath: config.DefaultDBPath, MasterKey: os.Getenv(config.EnvMasterKey)}
	svc, closeFn, err := a.Build(cfg)
	if err != nil {
		return a.fail(err)
	}
	if closeFn != nil {
		defer func() { _ = closeFn() }()
	}
	state, err := loadAgentState(ctx, svc.Vault)
	if err != nil {
		return a.fail(err)
	}
	// The token and the private identity are never printed.
	fmt.Fprintf(a.Out, "Hub:       %s\nHousehold: %s\nRelay:     %s\n", state.HubURL, state.TenantID, state.AgentID)
	if b, err := state.Bundle.Unverified(); err == nil {
		fmt.Fprintf(a.Out, "Trust:     bundle version %d\n", b.Version)
	} else {
		fmt.Fprintln(a.Out, "Trust:     no bundle yet — the owner has not published one")
	}
	return 0
}

// vaultState stores the relay's state in the encrypted vault.
type vaultState struct {
	ctx   context.Context
	vault SecretVault
}

func (v vaultState) Load() (agent.StateData, error) {
	return loadAgentState(v.ctx, v.vault)
}

func (v vaultState) Save(s agent.StateData) error {
	return saveAgentState(v.ctx, v.vault, s)
}

func saveAgentState(ctx context.Context, vlt SecretVault, s agent.StateData) error {
	if vlt == nil {
		return fmt.Errorf("no vault: set %s", config.EnvMasterKey)
	}
	blob, err := agent.Marshal(s)
	if err != nil {
		return err
	}
	return vlt.StoreSecret(ctx, agentStateID, blob)
}

func loadAgentState(ctx context.Context, vlt SecretVault) (agent.StateData, error) {
	if vlt == nil {
		return agent.StateData{}, fmt.Errorf("no vault: set %s", config.EnvMasterKey)
	}
	blob, ok, err := vlt.LoadSecret(ctx, agentStateID)
	if err != nil {
		return agent.StateData{}, err
	}
	if !ok {
		return agent.StateData{}, agent.ErrNotEnrolled
	}
	return agent.Unmarshal(blob)
}

// startRelay runs the relay alongside the LAN server, on the same handler
// `kergui serve` already exposes.
func (a *App) startRelay(ctx context.Context, svc *Services, handler http.Handler, cfg RuntimeConfig, hubURL, token, lanIP string) error {
	state, err := loadAgentState(ctx, svc.Vault)
	if err != nil {
		return err
	}
	if token != "" {
		state.Token = token
	}
	if hubURL == "" {
		hubURL = state.HubURL
	}
	store := &overrideState{inner: vaultState{ctx: ctx, vault: svc.Vault}, data: state}

	routerURL := cfg.Router
	ag, err := agent.New(agent.Config{
		HubURL:  hubURL,
		Handler: handler,
		State:   store,
		Router: func(ctx context.Context) (string, error) {
			return httpkit.ProbeFingerprint(ctx, routerURL, cfg.Timeout)
		},
		LANIP:   lanIP,
		Version: a.Version,
		Credentials: func(ctx context.Context, creds domain.Credentials) error {
			return svc.Vault.Store(ctx, usecase.RouterID(routerURL), creds)
		},
		Log: svc.Logger,
	})
	if err != nil {
		return err
	}
	go func() {
		if err := ag.Run(ctx); err != nil && svc.Logger != nil {
			svc.Logger.Errorf("relay stopped: %v", err)
		}
	}()
	if svc.Logger != nil {
		svc.Logger.Infof("relay: connected to the hub %s for household %s", hubURL, state.TenantID)
	}
	return nil
}

// overrideState lets a flag override the stored token for one run without
// writing it back.
type overrideState struct {
	inner vaultState
	data  agent.StateData
}

func (o *overrideState) Load() (agent.StateData, error) { return o.data, nil }

func (o *overrideState) Save(s agent.StateData) error {
	o.data = s
	return o.inner.Save(s)
}
