package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"nuxk.dev/horizon/core/internal/engine/amneziawg"
	"nuxk.dev/horizon/core/internal/engine/warp"
	"nuxk.dev/horizon/core/internal/plane"
)

func (d Deps) handleWarpTransport(w http.ResponseWriter, _ *http.Request) {
	status := warp.Status{Transport: plane.WarpUSQUE}
	configured := false
	if d.Warp != nil {
		status = d.Warp.Status()
		configured = d.Warp.ProfileConfigured()
	}
	installed := amneziawg.Installed()
	status.AWGInstalled, status.ProfileConfigured = &installed, &configured
	writeJSON(w, http.StatusOK, status)
}

func (d Deps) handleSetWarpProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if d.Warp == nil {
		writeErr(w, http.StatusConflict, "warp_unavailable", warp.ErrProfileUnavailable.Error())
		return
	}
	var body struct {
		Config string `json:"config"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*amneziawg.MaxProfileSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeErr(w, http.StatusBadRequest, "bad_body", "ожидается текст профиля WG / AWG")
		return
	}
	switch err := d.Warp.SaveProfile(body.Config); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case errors.Is(err, amneziawg.ErrInvalidProfile):
		writeErr(w, http.StatusBadRequest, "bad_warp_profile", err.Error())
	case errors.Is(err, warp.ErrProfileBusy), errors.Is(err, warp.ErrProfileUnavailable):
		writeErr(w, http.StatusConflict, "warp_unavailable", err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, "warp_profile_save_failed", err.Error())
	}
}

func (d Deps) handleSetWarpTransport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Transport string `json:"transport"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeErr(w, http.StatusBadRequest, "bad_body", "ожидается объект с транспортом WARP")
		return
	}
	if d.Warp == nil || d.Plane == nil {
		writeErr(w, http.StatusConflict, "warp_unavailable", "AmneziaWG не настроен на роутере")
		return
	}
	life := d.Life
	if life == nil {
		life = context.Background()
	}
	ctx, cancel := context.WithTimeout(life, 90*time.Second)
	if err := d.Warp.Queue(ctx, d.Plane, body.Transport, func() {
		cancel()
		if d.Ctl != nil {
			d.Ctl.Kick()
		}
	}); err != nil {
		cancel()
		if errors.Is(err, warp.ErrTransport) {
			writeErr(w, http.StatusBadRequest, "bad_transport", err.Error())
		} else {
			writeErr(w, http.StatusConflict, "warp_unavailable", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, d.Warp.Status())
}
