package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/suggest"
)

// --- route suggestions ----------------------------------------------------------
//
// Probes run in the background, one domain at a time; the screen polls GET
// /suggest. Nothing here touches routing: accept only adds the domain to the
// plane's «Подбор» list, and the plane's apply switch still decides the rest.

func (d Deps) suggestOff(w http.ResponseWriter) bool {
	if d.Suggest == nil {
		writeErr(w, http.StatusNotFound, "suggest_off", "route suggestions are off")
		return true
	}
	return false
}

func (d Deps) handleSuggest(w http.ResponseWriter, r *http.Request) {
	if d.suggestOff(w) {
		return
	}
	writeJSON(w, http.StatusOK, d.Suggest.Overview())
}

func (d Deps) handleSuggestCheck(w http.ResponseWriter, r *http.Request) {
	if d.suggestOff(w) {
		return
	}
	var req struct {
		Domains []string `json:"domains"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || len(req.Domains) == 0 || len(req.Domains) > 20 {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"domains":["chatgpt.com"]} with 1..20 names`)
		return
	}
	writeJSON(w, http.StatusAccepted, d.Suggest.Check(req.Domains, true))
}

func (d Deps) handleSuggestScan(w http.ResponseWriter, r *http.Request) {
	if d.suggestOff(w) {
		return
	}
	writeJSON(w, http.StatusAccepted, d.Suggest.Scan())
}

func (d Deps) handleSuggestAccept(w http.ResponseWriter, r *http.Request) {
	if d.suggestOff(w) {
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "bad_body", `want {} or {"mode":"vless"}`)
			return
		}
	}
	switch err := d.Suggest.Accept(r.PathValue("domain"), req.Mode); {
	case errors.Is(err, suggest.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "no such suggestion (already accepted, dismissed or still being checked)")
	case errors.Is(err, suggest.ErrNoMode):
		writeErr(w, http.StatusConflict, "nothing_to_route", "routing would not help for this domain")
	case errors.Is(err, suggest.ErrBadMode), errors.Is(err, plane.ErrBadList):
		writeErr(w, http.StatusBadRequest, "bad_mode", err.Error())
	case errors.Is(err, suggest.ErrNoPlane):
		writeErr(w, http.StatusNotFound, "plane_off", "routing plane is off (PLANE= in nuxk.conf)")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "state_error", err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func (d Deps) handleSuggestDismiss(w http.ResponseWriter, r *http.Request) {
	if d.suggestOff(w) {
		return
	}
	if err := d.Suggest.Dismiss(r.PathValue("domain")); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "no such suggestion")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
