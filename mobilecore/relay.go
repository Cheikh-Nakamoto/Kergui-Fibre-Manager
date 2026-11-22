package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// A phone left at home can be the household's relay (RFC-005). Pairing it
// is the same protocol as the PC relay's `kergui hub enroll`: the relay
// generates its own keys, redeems the code from the owner's QR, pins the
// owner's key from the fingerprint in it and shows six digits. Its state —
// private identity and hub token included — is kept in the vault under the
// same id the PC relay uses. Running the relay is the Android foreground
// service's job (#45).

// relayStateID is the vault id of this device's relay state.
const relayStateID = "hub-agent"

// relayHTTP is the client the pairing calls use; tests leave it as is.
var relayHTTP = &http.Client{Timeout: requestTimeout}

// RelayEnroll pairs this device as a relay of the household whose owner
// showed invite (the QR payload, kgp1.…). It returns
// {"agentId", "tenantId", "code"}; code must match the owner's screen. The
// hub token and the keys stay in the vault.
func (c *Core) RelayEnroll(hubURL, invite, name string) (string, error) {
	inv, err := trust.ParseInvite(invite)
	if err != nil {
		return "", err
	}
	hubURL = strings.TrimSpace(hubURL)
	if hubURL == "" {
		return "", errors.New("mobilecore: no hub address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	state, res, err := agent.Enroll(ctx, hubURL, inv, strings.TrimSpace(name), "phone", relayHTTP)
	if err != nil {
		return "", err
	}
	if err := c.saveRelay(ctx, state); err != nil {
		return "", err
	}
	c.log.Infof("relay: paired with household %s as %s", res.TenantID, res.AgentID)
	out, err := json.Marshal(map[string]string{"agentId": res.AgentID, "tenantId": res.TenantID, "code": res.Code})
	return string(out), err
}

// RelayEnrolled reports whether this device holds relay state.
func (c *Core) RelayEnrolled() bool {
	_, err := c.loadRelay(context.Background())
	return err == nil
}

// RelayCheckTrust fetches the household's trust bundle, accepts it only if
// the owner key pinned at pairing signed it, and returns
// {"version", "trusted"}: trusted once the owner confirmed the six digits
// and signed this relay in.
func (c *Core) RelayCheckTrust() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	state, err := c.loadRelay(ctx)
	if err != nil {
		return "", err
	}
	next, st, err := agent.CheckTrust(ctx, state, relayHTTP)
	if err != nil {
		return "", err
	}
	if len(next.Bundle.Payload) > 0 && string(next.Bundle.Payload) != string(state.Bundle.Payload) {
		if err := c.saveRelay(ctx, next); err != nil {
			return "", err
		}
	}
	out, err := json.Marshal(map[string]any{"version": st.Version, "trusted": st.Trusted})
	return string(out), err
}

func (c *Core) saveRelay(ctx context.Context, s agent.StateData) error {
	blob, err := agent.Marshal(s)
	if err != nil {
		return err
	}
	if err := c.vault.StoreSecret(ctx, relayStateID, blob); err != nil {
		return fmt.Errorf("mobilecore: keep relay state: %w", err)
	}
	return nil
}

func (c *Core) loadRelay(ctx context.Context) (agent.StateData, error) {
	blob, ok, err := c.vault.LoadSecret(ctx, relayStateID)
	if err != nil {
		return agent.StateData{}, err
	}
	if !ok {
		return agent.StateData{}, agent.ErrNotEnrolled
	}
	return agent.Unmarshal(blob)
}
