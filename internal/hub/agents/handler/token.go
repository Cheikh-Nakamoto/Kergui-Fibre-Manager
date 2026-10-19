package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
)

// TokenResolver authenticates a relay from its kga_ token. The relay module
// uses the same one, so a revoked agent loses the lease, the long-poll and
// the credentials in one go.
type TokenResolver struct {
	Repo domain.Repository
	Now  func() time.Time
}

type agentKey struct{}

// Middleware resolves the bearer token and puts the agent in the context.
//
// Every failure answers the same 401: a caller probing tokens must not learn
// whether one existed, belonged to another household or was revoked.
func (t *TokenResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if !strings.HasPrefix(token, domain.TokenPrefix) {
			response.Error(w, kernelerrors.Unauthorized("relay token required"))
			return
		}
		agent, err := t.Repo.ByTokenHash(r.Context(), domain.HashSecret(token))
		if err != nil || agent.Revoked() {
			response.Error(w, kernelerrors.Unauthorized("relay token is not valid"))
			return
		}
		// Best-effort bookkeeping: a failed touch must not fail the call.
		_ = t.Repo.TouchSeen(r.Context(), agent.ID, t.Now().UTC())
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentKey{}, agent)))
	})
}

// AgentFrom returns the relay resolved by Middleware. It is only ever called
// from a handler the middleware wrapped, so a missing agent is a wiring bug,
// not a request the caller can provoke.
func AgentFrom(ctx context.Context) *domain.Agent {
	a, _ := ctx.Value(agentKey{}).(*domain.Agent)
	if a == nil {
		return &domain.Agent{}
	}
	return a
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}
