package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/inventory/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/snapshot"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Clock is the module's time source; tests move it to close a window.
type Clock func() time.Time

// Ingest absorbs the observations a relay signed into the household's
// inventory.
//
// It takes the snapshot already verified: the relay module checks the
// relay's signature before storing anything, and this runs behind that check.
// What it does not take on faith is the content — a household, an agent and
// an address are all re-read from the signed payload, never from the caller's
// parameters.
type Ingest struct {
	Repo  domain.Repository
	Now   Clock
	Rules coredomain.RecognitionRules
	// Window overrides the 2-minute grouping window; zero takes AlertWindow.
	Window time.Duration
	// Log is optional.
	Log Logger
}

// Logger is the slice of a logger this module uses.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Observations absorbs one observations snapshot. Snapshots of other kinds
// (the device list, the SSIDs) are stored by the relay module and ignored
// here: the inventory reasons about stations, not about Wi-Fi settings.
func (uc Ingest) Observations(ctx context.Context, tenantID, agentID string, signed trust.SignedSnapshot) error {
	snap, err := signed.Unverified()
	if err != nil {
		return err
	}
	if snap.Kind != snapshot.KindObservations {
		return nil
	}
	if snap.TenantID != tenantID || snap.AgentID != agentID {
		return fmt.Errorf("%w: snapshot is signed for another relay", trust.ErrMalformed)
	}
	var body snapshot.Observations
	if err := json.Unmarshal(snap.Body, &body); err != nil {
		return fmt.Errorf("inventory: decoding the observations: %w", err)
	}

	sum := sha256.Sum256(signed.Payload)
	poll := Poll{
		TenantID: snap.TenantID,
		AgentID:  snap.AgentID,
		AsOf:     snap.AsOf,
		SHA256:   hex.EncodeToString(sum[:]),
		RelayMAC: body.RelayMAC,
		Degraded: body.Degraded,
		Hosts:    body.Hosts,
	}
	now := uc.Now()
	rules := uc.Rules
	if rules == (coredomain.RecognitionRules{}) {
		rules = coredomain.DefaultRecognitionRules
	}

	changes, err := uc.Repo.Diff(ctx, tenantID, func(inv domain.Snapshot) (domain.Changes, error) {
		return Diff(inv, poll, now, rules, uc.Window), nil
	})
	if err != nil {
		return err
	}
	if uc.Log != nil && (len(changes.Devices) > 0 || changes.Window != nil) {
		// Never the addresses themselves: an inventory in a log file is a map
		// of someone's home.
		uc.Log.Info("inventory: snapshot absorbed",
			"tenant_id", tenantID, "refreshed", changes.Touched,
			"new_devices", len(changes.Devices), "alerting", changes.Window != nil)
	}
	return nil
}
