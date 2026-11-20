package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"nuxk.dev/horizon/core/internal/engine/warp"
	"nuxk.dev/horizon/core/internal/plane"
)

func (d Deps) handleWarpTransport(w http.ResponseWriter, _ *http.Request) {
	status := warp.Status{Transport: plane.WarpUSQUE}
	if d.Warp != nil {
		status = d.Warp.Status()
	}
	writeJSON(w, http.StatusOK, status)
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
