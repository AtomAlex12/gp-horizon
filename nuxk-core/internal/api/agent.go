package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"nuxk.dev/horizon/core/internal/logbuf"
)

// --- agent: node info, counters, log, live events -----------------------------

func (d Deps) handleInfo(w http.ResponseWriter, r *http.Request) {
	if d.Node == nil {
		writeErr(w, http.StatusNotFound, "not_wired", "node info is not wired")
		return
	}
	writeJSON(w, http.StatusOK, d.Node.Info(r.Context()))
}

func (d Deps) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if d.Node == nil {
		writeErr(w, http.StatusNotFound, "not_wired", "metrics are not wired")
		return
	}
	writeJSON(w, http.StatusOK, d.Node.Metrics())
}

// logDebugReq turns the debug switch on for Minutes (default 30), or off.
type logDebugReq struct {
	On      bool `json:"on"`
	Minutes int  `json:"minutes,omitempty"`
}

func (d Deps) handleLogsDebug(w http.ResponseWriter, r *http.Request) {
	if d.Debug == nil {
		writeErr(w, http.StatusNotFound, "not_wired", "the debug switch is not wired")
		return
	}
	writeJSON(w, http.StatusOK, d.Debug.State())
}

func (d Deps) handleSetLogsDebug(w http.ResponseWriter, r *http.Request) {
	if d.Debug == nil {
		writeErr(w, http.StatusNotFound, "not_wired", "the debug switch is not wired")
		return
	}
	var in logDebugReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil ||
		in.Minutes < 0 || time.Duration(in.Minutes)*time.Minute > logbuf.MaxDebug {
		writeErr(w, http.StatusBadRequest, "bad_body", `want {"on":true,"minutes":30} (minutes 1–240)`)
		return
	}
	if in.Minutes == 0 {
		in.Minutes = 30
	}
	writeJSON(w, http.StatusOK, d.Debug.Set(in.On, time.Duration(in.Minutes)*time.Minute))
}

// handleLogs: ?after=<seq> returns only newer entries; ?limit caps the count.
func (d Deps) handleLogs(w http.ResponseWriter, r *http.Request) {
	if d.Logs == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	writeJSON(w, http.StatusOK, d.Logs.Since(after, limit))
}

// handleEvents is a Server-Sent Events stream: "status" with the full
// snapshot on every change (and once on connect), "log" per log entry, and a
// comment ping every 20 s so proxies keep the connection.
func (d Deps) handleEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil { // the server's WriteTimeout would cut the stream
		writeErr(w, http.StatusInternalServerError, "no_stream", "streaming unsupported: "+err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	upd, unsub := d.Hub.Subscribe()
	defer unsub()
	var logs <-chan logbuf.Entry
	if d.Logs != nil {
		ch, unl := d.Logs.Subscribe()
		defer unl()
		logs = ch
	}
	send := func(event string, v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send("status", d.Hub.Get()) {
		return
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-upd:
			if !ok || !send("status", d.Hub.Get()) {
				return
			}
		case e := <-logs:
			if !send("log", e) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
