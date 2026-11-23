package mobilecore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
)

// GET /api/export (owner request, 2026-11-23): the phone's own backup, the
// same archive as `kergui export`, base64 in {"archive", "name"}: the app
// shares it (Drive, Files, WhatsApp) and imports it again on a new phone.
func (c *Core) exportBackup(w http.ResponseWriter, r *http.Request) {
	archive, err := sqlite.ExportBackup(r.Context(), c.db)
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	name := "kergui-sauvegarde-" + c.clock.Now().Format("2006-01-02") + ".zip"
	c.log.Infof("core: sauvegarde créée (%d octets)", len(archive))
	writeJSONResponse(w, http.StatusOK, map[string]string{"archive": base64.StdEncoding.EncodeToString(archive), "name": name})
}

// POST /api/import (owner request, 2026-11-23): a backup — made by the app
// (GET /api/export) or by `kergui export` on the PC — a zip of appareils.csv and conso.csv, sent base64 in
// {"archive": "…"} — applied to the box the core points at. The backup's
// names and notes win; a device it names is approved, as a name given in
// the app is. The box's password is never in it.
func (c *Core) importBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Archive string `json:"archive"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20)).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(req.Archive)
	if err != nil || len(raw) == 0 {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "archive: not base64", "code": "bad_backup"})
		return
	}
	box := c.boxID()
	if box == "" {
		writeJSONResponse(w, http.StatusConflict, map[string]string{"error": "no box configured", "code": "no_box"})
		return
	}
	res, err := sqlite.ImportBackup(r.Context(), c.db, box, raw)
	if errors.Is(err, sqlite.ErrBadBackup) {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "bad_backup"})
		return
	}
	if err != nil {
		writeJSONResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.approvals.approve(box, res.NamedMACs...)
	c.log.Infof("core: sauvegarde importée : %d appareils (%d nommés), %d mois de conso", res.Devices, res.Named, res.Usage)
	writeJSONResponse(w, http.StatusOK, res)
}
