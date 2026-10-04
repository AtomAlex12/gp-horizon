package api

import (
	"errors"
	"net/http"

	"nuxk.dev/horizon/core/internal/update"
)

// handleComponents: WARP, VLESS, SmartDNS, nfqws2 — which are on the router,
// which the panel can add, how the last install went.
func (d Deps) handleComponents(w http.ResponseWriter, r *http.Request) {
	if d.updateOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, d.Update.Components())
}

// handleComponentInstall starts the router's own `nuxk <component> --yes`.
// It answers at once; the agent restarts on the way, so the panel follows
// the run with GET /components.
func (d Deps) handleComponentInstall(w http.ResponseWriter, r *http.Request) {
	if d.updateOff(w) {
		return
	}
	c, err := d.Update.Install(r.PathValue("id"))
	switch {
	case errors.Is(err, update.ErrUnknownComponent):
		writeErr(w, http.StatusNotFound, "unknown_component", err.Error())
	case errors.Is(err, update.ErrInstalled):
		writeErr(w, http.StatusConflict, "component_installed", err.Error())
	case errors.Is(err, update.ErrBusy):
		writeErr(w, http.StatusConflict, "update_busy", err.Error())
	case errors.Is(err, update.ErrNoInstall):
		writeErr(w, http.StatusServiceUnavailable, "component_unavailable", err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "component_failed", err.Error())
	default:
		writeJSON(w, http.StatusAccepted, c)
	}
}
