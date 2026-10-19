package handler

import (
	"context"
	"fmt"
	"net/http"

	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Policy is the hub's own, second opinion on a command.
//
// RFC-005 is explicit that the relay decides: it verifies every signature
// against the bundle the owner signed, and it does not trust this hub. The
// checks here exist anyway, because a hub that forwards obvious nonsense is
// a hub that lets a bug or a stolen session waste a household's time — and
// because refusing early gives the app a readable error instead of a
// timeout. Nothing here can authorise what the relay would refuse.
type Policy struct {
	Trust interface {
		GetBundle(ctx context.Context, tenantID string) (agentsdomain.TrustBundle, error)
	}
	Agents agentsdomain.Repository
}

// CheckCommand refuses a command the relay would certainly refuse: an
// unsigned one, a viewer trying to write, or a key the household's bundle
// does not delegate.
func (p *Policy) CheckCommand(ctx context.Context, caller tenantaccess.Caller, signed trust.SignedCommand) error {
	if len(signed.Sig) == 0 || len(signed.Payload) == 0 {
		return trust.ErrUnsigned
	}
	// A VIEWER may read the household but never act on the box. The
	// signature would stop them anyway — they hold no control delegation —
	// but the hub need not carry the request that far.
	if caller.Role == memberdomain.MEMBER_ROLE_VIEWER {
		return fmt.Errorf("%w: viewers may not act on the router", trust.ErrScope)
	}
	cmd, err := signed.Unverified()
	if err != nil {
		return err
	}
	bundle, err := p.bundle(ctx, caller.TenantID)
	if err != nil {
		return err
	}
	if bundle.isRevoked(cmd.KeyID) {
		return fmt.Errorf("%w: %s", trust.ErrRevoked, cmd.KeyID)
	}
	if !bundle.mayCommand(cmd) {
		return fmt.Errorf("%w: this key is not delegated for %s %s", trust.ErrScope, cmd.Method, cmd.Path)
	}
	return nil
}

// VerifyResult checks that a result really comes from the relay that sends
// it, so the hub never stores one it cannot attribute. The app verifies it
// again against the bundle.
func (p *Policy) VerifyResult(ctx context.Context, agent *agentsdomain.Agent, signed trust.SignedResult, cmdID string) (trust.Result, error) {
	res, err := signed.Open(trust.DomainResult, agent.Ed25519)
	if err != nil {
		return trust.Result{}, err
	}
	if res.TenantID != agent.TenantID {
		return trust.Result{}, fmt.Errorf("%w: result is for another household", trust.ErrTenant)
	}
	if res.CmdID != cmdID {
		return trust.Result{}, fmt.Errorf("%w: result is for command %s", trust.ErrMalformed, res.CmdID)
	}
	return res, nil
}

// VerifySnapshot checks a snapshot's signature the same way.
func (p *Policy) VerifySnapshot(ctx context.Context, agent *agentsdomain.Agent, signed trust.SignedSnapshot) error {
	snap, err := signed.Open(trust.DomainSnapshot, agent.Ed25519)
	if err != nil {
		return err
	}
	if snap.TenantID != agent.TenantID || snap.AgentID != agent.ID {
		return fmt.Errorf("%w: snapshot is signed for another relay", trust.ErrMalformed)
	}
	return nil
}

// bundleView is the part of a household's bundle the hub reads. It parses
// the bundle without verifying it: the hub cannot check the owner's
// signature usefully (it would be checking a key it stores itself), and
// everything here is advisory.
type bundleView struct{ b trust.Bundle }

func (p *Policy) bundle(ctx context.Context, tenantID string) (bundleView, error) {
	stored, err := p.Trust.GetBundle(ctx, tenantID)
	if err != nil {
		return bundleView{}, err
	}
	signed := trust.Signed[trust.Bundle]{Payload: stored.Payload, Sig: stored.Sig}
	b, err := signed.Unverified()
	if err != nil {
		return bundleView{}, err
	}
	return bundleView{b: b}, nil
}

func (v bundleView) isRevoked(keyID string) bool {
	for _, r := range v.b.Revoked {
		if r == keyID {
			return true
		}
	}
	return false
}

// mayCommand mirrors the relay's scope rules closely enough to catch an
// obviously wrong key. The relay's Verifier remains the authority.
func (v bundleView) mayCommand(cmd trust.Command) bool {
	if cmd.KeyID == "" {
		return false
	}
	if cmd.KeyID == v.b.Owner.KeyID() {
		return true
	}
	if v.b.OwnerOnly {
		return false
	}
	for _, d := range v.b.Delegations {
		if d.KeyID() != cmd.KeyID {
			continue
		}
		switch d.Scope {
		case trust.ScopeControl:
			return true
		case trust.ScopeView:
			return cmd.Method == http.MethodGet
		case trust.ScopeHubBlock:
			mac, ok := trust.BlockTargetMAC(cmd.Path)
			return ok && cmd.Method == http.MethodPost && !v.b.IsProtected(mac)
		}
	}
	return false
}
