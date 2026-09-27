package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"nuxk.dev/horizon/core/internal/update"
)

func (d Deps) updateOff(w http.ResponseWriter) bool {
	if d.Update == nil {
		writeErr(w, http.StatusNotFound, "update_off", "updates are off")
		return true
	}
	return false
}

// handleUpdate: this version, the newest release found, whether the panel
// can update, the last update started from it.
func (d Deps) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if d.updateOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, d.Update.Status())
}

// handleUpdateCheck looks on GitHub now (at most once a minute; more often
// answers with the last look).
func (d Deps) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if d.updateOff(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, d.Update.CheckNow(ctx))
}

func (d Deps) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if d.updateOff(w) {
		return
	}
	var s update.Settings
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&s); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"check":true,"channel":"stable"}`)
		return
	}
	was := d.Update.Settings()
	st, err := d.Update.SetSettings(s)
	switch {
	case errors.Is(err, update.ErrBadSettings):
		writeErr(w, http.StatusBadRequest, "bad_settings", err.Error())
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "state", err.Error())
		return
	}
	// another channel, or checks just turned on: look now, not tomorrow
	if s.Check && (s.Channel != was.Channel || !was.Check) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		st = d.Update.Check(ctx)
	}
	writeJSON(w, http.StatusOK, st)
}

// handleUpdateStart starts `nuxk update` to the version the panel showed. It
// answers at once; the agent restarts on the way, so the panel follows the
// run with GET /update.
func (d Deps) handleUpdateStart(w http.ResponseWriter, r *http.Request) {
	if d.updateOff(w) {
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || !update.Valid(in.Version) {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"version":"X.Y.Z"}`)
		return
	}
	st, err := d.Update.Start(in.Version)
	switch {
	case errors.Is(err, update.ErrBusy):
		writeErr(w, http.StatusConflict, "update_busy", err.Error())
	case errors.Is(err, update.ErrNotNewer), errors.Is(err, update.ErrUnknownVersion):
		writeErr(w, http.StatusConflict, "update_stale", err.Error())
	case errors.Is(err, update.ErrCannot):
		writeErr(w, http.StatusServiceUnavailable, "update_unavailable", err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "update_failed", err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}
