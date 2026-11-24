package admin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/google/uuid"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
)

// The action log's read side (issue #77):
//
//	GET /api/v1/admin/audit?admin=&kind=&from=&to=&limit=&before=
//
// admin is an admin's user id or e-mail; kind one of actionlog.Kinds; from
// and to are days (2006-01-02, Dakar is UTC, to included) or RFC 3339
// times; before is the previous page's next. Newest first.

type logAdmin struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type logTarget struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type logEntry struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Admin    logAdmin  `json:"admin"`
	Kind     string    `json:"kind"`
	Target   logTarget `json:"target"`
	TenantID string    `json:"tenantId,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Summary  string    `json:"summary"`
}

type logResponse struct {
	Entries []logEntry `json:"entries"`
	Next    string     `json:"next,omitempty"`
	// Admins and Kinds feed the page's filters.
	Admins []logAdmin `json:"admins"`
	Kinds  []string   `json:"kinds"`
}

func (m *Module) actionLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	limit := intParam(r, "limit", 50, 200)

	kinds := make([]string, len(actionlog.Kinds))
	for i, k := range actionlog.Kinds {
		kinds[i] = string(k)
	}
	where := []string{"l.action = ANY($1::audit_action[])"}
	args := []any{kinds}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}

	if kind := strings.TrimSpace(q.Get("kind")); kind != "" {
		if !actionlog.IsKind(kind) {
			response.Error(w, kernelerrors.BadRequest("unknown kind"))
			return
		}
		add("l.action = $%d::audit_action", kind)
	}
	if a := strings.TrimSpace(q.Get("admin")); a != "" {
		if _, err := uuid.Parse(a); err == nil {
			add("l.actor_id = $%d::uuid", a)
		} else {
			add("lower(u.email) = lower($%d)", a)
		}
	}
	if s := q.Get("from"); s != "" {
		t, err := parseDay(s, false)
		if err != nil {
			response.Error(w, kernelerrors.BadRequest("from: a day (2006-01-02) or an RFC 3339 time"))
			return
		}
		add("l.occurred_at >= $%d", t)
	}
	if s := q.Get("to"); s != "" {
		t, err := parseDay(s, true)
		if err != nil {
			response.Error(w, kernelerrors.BadRequest("to: a day (2006-01-02) or an RFC 3339 time"))
			return
		}
		add("l.occurred_at < $%d", t)
	}
	if s := q.Get("before"); s != "" {
		at, id, err := decodeCursor(s)
		if err != nil {
			response.Error(w, kernelerrors.BadRequest("before: not a cursor"))
			return
		}
		args = append(args, at, id)
		where = append(where, fmt.Sprintf("(l.occurred_at, l.id) < ($%d, $%d::uuid)", len(args)-1, len(args)))
	}
	args = append(args, limit+1)

	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT l.id::text, l.occurred_at, COALESCE(l.actor_id::text, ''), COALESCE(u.email, ''), l.action::text,
		       COALESCE(l.target_type, ''), COALESCE(l.target_id::text, ''), COALESCE(l.tenant_id::text, ''), l.metadata
		FROM audit_logs l LEFT JOIN users u ON u.id = l.actor_id
		WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(`
		ORDER BY l.occurred_at DESC, l.id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	out := logResponse{Entries: []logEntry{}, Admins: []logAdmin{}, Kinds: kinds}
	for rows.Next() {
		var e logEntry
		var meta []byte
		if err := rows.Scan(&e.ID, &e.At, &e.Admin.ID, &e.Admin.Email, &e.Kind,
			&e.Target.Type, &e.Target.ID, &e.TenantID, &meta); err != nil {
			rows.Close()
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		var md map[string]any
		_ = json.Unmarshal(meta, &md)
		e.Reason, _ = md[actionlog.MetaReason].(string)
		e.Summary, _ = md[actionlog.MetaSummary].(string)
		if e.Summary == "" {
			e.Summary = fallbackSummary(md)
		}
		if e.Target.Type == "licensing_account" { // rows written before the log
			e.Target.Type = actionlog.TargetAccount
		}
		out.Entries = append(out.Entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	if len(out.Entries) > limit {
		out.Entries = out.Entries[:limit]
		last := out.Entries[limit-1]
		out.Next = encodeCursor(last.At, last.ID)
	}

	arows, err := m.cfg.Pool.Query(ctx, `SELECT id::text, email FROM users WHERE role::text = 'ROLE_ADMIN' ORDER BY lower(email)`)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer arows.Close()
	for arows.Next() {
		var a logAdmin
		if err := arows.Scan(&a.ID, &a.Email); err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		out.Admins = append(out.Admins, a)
	}
	response.JSON(w, http.StatusOK, out)
}

// parseDay reads a day or a time; a day as an upper bound means its end.
func parseDay(s string, end bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	if end {
		t = t.Add(24 * time.Hour)
	}
	return t, nil
}

// A cursor is the last entry's time and id, opaque to the caller.
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(s string) (time.Time, string, error) {
	t, id, err := decodeKeyCursor(s)
	if err != nil {
		return time.Time{}, "", err
	}
	if _, err := uuid.Parse(id); err != nil {
		return time.Time{}, "", err
	}
	return t, id, nil
}

// decodeKeyCursor reads a cursor whose key is any string (the household
// history's keys are not UUIDs).
func decodeKeyCursor(s string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	at, key, ok := strings.Cut(string(b), "|")
	if !ok || key == "" {
		return time.Time{}, "", fmt.Errorf("no separator")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", err
	}
	return t, key, nil
}

// fallbackSummary describes rows written before gestures carried a summary.
func fallbackSummary(md map[string]any) string {
	if v, ok := md["versionName"].(string); ok && v != "" {
		return fmt.Sprintf("%s (build %v)", v, md["build"])
	}
	if b, ok := md["build"]; ok {
		return fmt.Sprintf("minimum : build %v", b)
	}
	if p, ok := md["pass"].(string); ok {
		return "Pass " + p
	}
	return ""
}
