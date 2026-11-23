// Package handler serves the releases module over HTTP (issue #71).
//
// Two surfaces. /releases/v1 carries no user token: CI declares a build
// with its own bearer token, and the app checks for an update anonymously.
// /api/v1/admin/releases is the back-office's, behind the kernel's JWT and
// for platform administrators only; each of its writes is audited.
package handler

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/usecase"
)

// The audit actions of the back-office's release writes.
const (
	ActionPublished   auditdomain.AuditAction = "RELEASE_PUBLISHED"
	ActionWithdrawn   auditdomain.AuditAction = "RELEASE_WITHDRAWN"
	ActionNotesEdited auditdomain.AuditAction = "RELEASE_NOTES_EDITED"
	ActionMinimumSet  auditdomain.AuditAction = "RELEASE_MINIMUM_SET"
)

// maxBody bounds every request body: a declaration with its notes is far
// smaller.
const maxBody = 16 << 10

// Handler serves the routes.
type Handler struct {
	Svc *usecase.Service
	// Token is CI's declare token; empty turns the declare route off.
	Token string
	Audit audit.EventLogger
}

// RegisterPublicRoutes mounts /releases/v1, outside the kernel's JWT.
func (h *Handler) RegisterPublicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /releases/v1/declare", h.declare)
	mux.HandleFunc("POST /releases/v1/check", h.check)
}

// RegisterRoutes mounts the back-office routes, behind the JWT middleware.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/admin/releases"
	mux.HandleFunc("GET "+p, h.guard(h.list))
	mux.HandleFunc("PUT "+p+"/minimum", h.guard(h.setMinimum))
	mux.HandleFunc("POST "+p+"/{id}/publish", h.guard(h.publish))
	mux.HandleFunc("POST "+p+"/{id}/withdraw", h.guard(h.withdraw))
	mux.HandleFunc("PUT "+p+"/{id}/notes", h.guard(h.notes))
}

// --- JSON -----------------------------------------------------------------

// releaseJSON is a release as the back-office and the app see it.
type releaseJSON struct {
	ID          string     `json:"id"`
	Platform    string     `json:"platform"`
	Channel     string     `json:"channel"`
	VersionName string     `json:"versionName"`
	Build       int64      `json:"build"`
	URL         string     `json:"url"`
	SHA256      string     `json:"sha256"`
	Size        int64      `json:"size"`
	Notes       string     `json:"notes"`
	State       string     `json:"state"`
	DeclaredAt  time.Time  `json:"declaredAt"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	WithdrawnAt *time.Time `json:"withdrawnAt,omitempty"`
	// Installs is only in the back-office's list.
	Installs *int `json:"installs,omitempty"`
}

func toJSON(r domain.Release) releaseJSON {
	return releaseJSON{
		ID: r.ID, Platform: string(r.Platform), Channel: string(r.Channel), VersionName: r.VersionName,
		Build: r.Build, URL: r.URL, SHA256: r.SHA256, Size: r.Size, Notes: r.Notes, State: string(r.State),
		DeclaredAt: r.DeclaredAt, PublishedAt: r.PublishedAt, WithdrawnAt: r.WithdrawnAt,
	}
}

// updateJSON is what the app is offered: the file to download and check.
type updateJSON struct {
	VersionName string     `json:"versionName"`
	Build       int64      `json:"build"`
	URL         string     `json:"url"`
	SHA256      string     `json:"sha256"`
	Size        int64      `json:"size"`
	Notes       string     `json:"notes"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return false
	}
	return true
}

// The 409 codes the back-office tells apart.
const (
	CodeReleaseState       = "RELEASE_STATE"
	CodeMinimumUnpublished = "MINIMUM_NOT_PUBLISHED"
	CodeMinimumUncovered   = "MINIMUM_UNCOVERED"
	CodeFileChanged        = "RELEASE_FILE_CHANGED"
)

func conflict(code, msg string, err error) error {
	return kernelerrors.New(code, msg, http.StatusConflict, err)
}

// fail maps the module's errors to HTTP.
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		response.Error(w, kernelerrors.BadRequest(strings.TrimPrefix(err.Error(), domain.ErrInvalid.Error()+": ")))
	case errors.Is(err, usecase.ErrNotFound):
		response.Error(w, kernelerrors.NotFound("no such release"))
	case errors.Is(err, domain.ErrState):
		response.Error(w, conflict(CodeReleaseState, "the release's state does not allow this", err))
	case errors.Is(err, domain.ErrNotPublished):
		response.Error(w, conflict(CodeMinimumUnpublished, "the minimum must be a published build", err))
	case errors.Is(err, domain.ErrMinimumUncovered):
		response.Error(w, conflict(CodeMinimumUncovered, "the minimum stands on this build: lower the minimum first", err))
	case errors.Is(err, domain.ErrFileChanged):
		response.Error(w, conflict(CodeFileChanged, "this build was already published with another file", err))
	default:
		response.Error(w, kernelerrors.WrapDBError(err))
	}
}

// --- CI -------------------------------------------------------------------

type declareRequest struct {
	Platform    string `json:"platform"`
	Channel     string `json:"channel"`
	VersionName string `json:"versionName"`
	Build       int64  `json:"build"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Notes       string `json:"notes"`
}

// authorized compares the bearer token in constant time, over digests so
// the length does not leak either.
func (h *Handler) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || got == "" {
		return false
	}
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(h.Token))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// declare records a build CI built: 201 when new, 200 when it was known.
func (h *Handler) declare(w http.ResponseWriter, r *http.Request) {
	if h.Token == "" {
		http.NotFound(w, r)
		return
	}
	if !h.authorized(r) {
		response.Error(w, kernelerrors.Unauthorized("a valid release token is required"))
		return
	}
	var req declareRequest
	if !decode(w, r, &req) {
		return
	}
	rel, created, err := h.Svc.Declare(r.Context(), domain.Declaration{
		Target:      domain.Target{Platform: domain.Platform(req.Platform), Channel: domain.Channel(req.Channel)},
		VersionName: req.VersionName, Build: req.Build, URL: req.URL, SHA256: req.SHA256, Size: req.Size, Notes: req.Notes,
	})
	if err != nil {
		fail(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	response.JSON(w, status, toJSON(rel))
}

// --- the app --------------------------------------------------------------

type checkRequest struct {
	Platform       string `json:"platform"`
	Channel        string `json:"channel"`
	Build          int64  `json:"build"`
	InstallationID string `json:"installationId"`
}

type checkResponse struct {
	Update       *updateJSON `json:"update"`
	MinimumBuild int64       `json:"minimumBuild"`
	Required     bool        `json:"required"`
}

// check answers the app: the newest published build above its own, if
// any, and the minimum.
func (h *Handler) check(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := h.Svc.Check(r.Context(), usecase.CheckInput{
		Target: domain.Target{Platform: domain.Platform(req.Platform), Channel: domain.Channel(req.Channel)},
		Build:  req.Build, InstallationID: req.InstallationID,
	})
	if err != nil {
		fail(w, err)
		return
	}
	out := checkResponse{MinimumBuild: res.Minimum, Required: res.Required}
	if u := res.Update; u != nil {
		out.Update = &updateJSON{VersionName: u.VersionName, Build: u.Build, URL: u.URL, SHA256: u.SHA256,
			Size: u.Size, Notes: u.Notes, PublishedAt: u.PublishedAt}
	}
	response.JSON(w, http.StatusOK, out)
}

// --- the back-office ------------------------------------------------------

// guard lets platform administrators through, and nobody else: not a
// household's owner, whatever its role in its household.
func (h *Handler) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if kernelauth.UserIDFromContext(r.Context()) == "" {
			response.Error(w, kernelerrors.Unauthorized("authentication required"))
			return
		}
		if !kernelauth.IsPlatformAdmin(r.Context()) {
			response.Error(w, kernelerrors.Forbidden("platform administrators only"))
			return
		}
		next(w, r)
	}
}

// target reads ?platform= and ?channel=, the website APK by default.
func target(r *http.Request) domain.Target {
	t := domain.Target{Platform: domain.Android, Channel: domain.Website}
	if p := r.URL.Query().Get("platform"); p != "" {
		t.Platform = domain.Platform(p)
	}
	if c := r.URL.Query().Get("channel"); c != "" {
		t.Channel = domain.Channel(c)
	}
	return t
}

type versionCount struct {
	Build    int64 `json:"build"`
	Installs int   `json:"installs"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	l, err := h.Svc.List(r.Context(), target(r))
	if err != nil {
		fail(w, err)
		return
	}
	releases := make([]releaseJSON, 0, len(l.Releases))
	for _, rel := range l.Releases {
		j := toJSON(rel)
		n := l.Installs[rel.Build]
		j.Installs = &n
		releases = append(releases, j)
	}
	versions := make([]versionCount, 0, len(l.Installs))
	for b, n := range l.Installs {
		versions = append(versions, versionCount{Build: b, Installs: n})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Build > versions[j].Build })
	response.JSON(w, http.StatusOK, map[string]any{
		"platform": l.Target.Platform, "channel": l.Target.Channel, "minimumBuild": l.Minimum,
		"windowDays": int(domain.CountWindow / (24 * time.Hour)), "releases": releases, "versions": versions,
	})
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	rel, err := h.Svc.Publish(r.Context(), r.PathValue("id"))
	h.answer(w, r, rel, err, ActionPublished, nil)
}

func (h *Handler) withdraw(w http.ResponseWriter, r *http.Request) {
	rel, err := h.Svc.Withdraw(r.Context(), r.PathValue("id"))
	h.answer(w, r, rel, err, ActionWithdrawn, nil)
}

func (h *Handler) notes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Notes string `json:"notes"`
	}
	if !decode(w, r, &req) {
		return
	}
	notes := strings.TrimSpace(req.Notes)
	rel, err := h.Svc.EditNotes(r.Context(), r.PathValue("id"), notes)
	h.answer(w, r, rel, err, ActionNotesEdited, map[string]any{"notes": notes})
}

// answer writes a release back and audits the write that changed it.
func (h *Handler) answer(w http.ResponseWriter, r *http.Request, rel domain.Release, err error,
	action auditdomain.AuditAction, extra map[string]any) {
	if err != nil {
		fail(w, err)
		return
	}
	meta := map[string]any{"platform": rel.Platform, "channel": rel.Channel, "build": rel.Build, "versionName": rel.VersionName}
	for k, v := range extra {
		meta[k] = v
	}
	h.audit(r.Context(), action, rel.ID, meta)
	response.JSON(w, http.StatusOK, toJSON(rel))
}

type minimumRequest struct {
	Platform string `json:"platform"`
	Channel  string `json:"channel"`
	Build    int64  `json:"build"`
}

// setMinimum sets the build below which the app blocks; 0 clears it.
func (h *Handler) setMinimum(w http.ResponseWriter, r *http.Request) {
	var req minimumRequest
	if !decode(w, r, &req) {
		return
	}
	t := domain.Target{Platform: domain.Platform(req.Platform), Channel: domain.Channel(req.Channel)}
	if err := h.Svc.SetMinimum(r.Context(), t, req.Build); err != nil {
		fail(w, err)
		return
	}
	h.audit(r.Context(), ActionMinimumSet, "", map[string]any{"platform": t.Platform, "channel": t.Channel, "build": req.Build})
	response.JSON(w, http.StatusOK, map[string]any{"platform": t.Platform, "channel": t.Channel, "minimumBuild": req.Build})
}

func (h *Handler) audit(ctx context.Context, action auditdomain.AuditAction, target string, meta map[string]any) {
	if h.Audit == nil {
		return
	}
	h.Audit.Execute(ctx, audit.LogEventParams{
		ActorID: kernelauth.UserIDFromContext(ctx), Action: action,
		TargetType: "release", TargetID: target, Metadata: meta,
	})
}
