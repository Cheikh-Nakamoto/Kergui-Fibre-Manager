package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Commands is the slice of this module another hub module needs in order to
// run one signed command: the WhatsApp reply path signs a block with the
// hub's narrow key and wants the relay's signed answer back.
//
// It exists so that path does not reach into the queue, and above all so
// the verification of the relay's answer stays here, where it already is:
// a caller must not be able to read a result the hub cannot attribute.
type Commands struct {
	queue  usecase.Queue
	agents agentsdomain.Repository
}

// Commands exposes that slice.
func (m *Module) Commands() *Commands {
	return &Commands{queue: m.Handler.Queue, agents: m.agents}
}

// RunParams is one command to run.
type RunParams struct {
	TenantID string
	// IssuerUserID is the person the command is attributed to — the member
	// who answered in WhatsApp — even though the hub's key signed it.
	IssuerUserID string
	Signed       trust.SignedCommand
	// WantMethod and WantPath are what the caller believes it signed. The
	// queue refuses a command signed for anything else.
	WantMethod string
	WantPath   string
	// Wait is how long to wait for the relay's answer before reporting that
	// nobody answered yet.
	Wait time.Duration
}

// Outcome is what came back.
type Outcome struct {
	CommandID string
	// Answered is false when no relay reported in time. The command may
	// still run: it stays queued until it expires.
	Answered bool
	// Status is what the box answered, as the relay signed it.
	Status int
	Body   json.RawMessage
}

// Run queues a signed command and waits briefly for the relay's signed
// answer.
func (c *Commands) Run(ctx context.Context, p RunParams) (Outcome, error) {
	cmdID, err := c.queue.Submit(ctx, usecase.SubmitParams{
		TenantID: p.TenantID, IssuerUserID: p.IssuerUserID, Signed: p.Signed,
		WantMethod: p.WantMethod, WantPath: p.WantPath,
	})
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{CommandID: cmdID}
	cmd, err := c.queue.AwaitResult(ctx, p.TenantID, cmdID, p.Wait)
	if err != nil {
		return out, err
	}
	if cmd.Status != domain.StatusDone || len(cmd.Result) == 0 {
		return out, nil
	}
	res, err := c.verify(ctx, p.TenantID, cmd)
	if err != nil {
		return out, err
	}
	out.Answered, out.Status, out.Body = true, res.Status, res.Body
	return out, nil
}

// verify checks that the result really comes from the relay that reported
// it. The hub stores results it has already verified once (the relay
// module's Policy does it on the way in); doing it again here means a
// caller reading a result never has to take the storage's word for it.
func (c *Commands) verify(ctx context.Context, tenantID string, cmd *domain.Command) (trust.Result, error) {
	if cmd.AgentID == "" {
		return trust.Result{}, fmt.Errorf("%w: the result names no relay", trust.ErrMalformed)
	}
	agent, err := c.agents.ByID(ctx, tenantID, cmd.AgentID)
	if err != nil {
		return trust.Result{}, err
	}
	var signed trust.SignedResult
	if err := json.Unmarshal(cmd.Result, &signed); err != nil {
		return trust.Result{}, fmt.Errorf("%w: %v", trust.ErrMalformed, err)
	}
	res, err := signed.Open(trust.DomainResult, agent.Ed25519)
	if err != nil {
		return trust.Result{}, err
	}
	if res.TenantID != tenantID || res.CmdID != cmd.ID {
		return trust.Result{}, fmt.Errorf("%w: the result is for another command", trust.ErrMalformed)
	}
	return res, nil
}

// Refused reports whether an outcome is the relay turning the command down
// rather than the box refusing it, which the caller words differently.
func Refused(out Outcome) bool { return out.Answered && out.Status == http.StatusForbidden }
