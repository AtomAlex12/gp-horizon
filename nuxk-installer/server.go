package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed ui/index.html
var indexHTML string

// Server is the installer's web form + JSON API.
type Server struct {
	Payload *Payload
	Code    string // access code printed at startup; the form asks for it
	Root    string // prefix for every remote path — development against a fake router only

	installing sync.Mutex
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(strings.ReplaceAll(indexHTML, "{{VERSION}}", s.Payload.Version())))
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"version": s.Payload.Version(), "missing": s.Payload.Missing()})
	})
	mux.Handle("POST /api/check", s.guard(http.HandlerFunc(s.handleCheck)))
	mux.Handle("POST /api/install", s.guard(http.HandlerFunc(s.handleInstall)))
	return mux
}

// guard checks the access code. A wrong code costs a second, so guessing a
// 40-bit code from the LAN is pointless.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(r.Header.Get("X-Nuxk-Code")), "-", ""))
		want := strings.ReplaceAll(s.Code, "-", "")
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			time.Sleep(time.Second)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "неверный код доступа — он напечатан в логе инсталлятора при запуске"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		next.ServeHTTP(w, r)
	})
}

type checkResp struct {
	HostKey string `json:"host_key"`
	Report  Report `json:"report"`
	Plan    Plan   `json:"plan"`
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	var t Target
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный запрос: " + err.Error()})
		return
	}
	t.HostKey = "" // first contact: learn the key, the form pins it for install
	t.root = s.Root
	c, err := Dial(t)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	defer c.Close()
	rep, err := Detect(c)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("check %s@%s: arch=%s entware=%v nuxk=%q", firstNonEmpty(t.User, "root"), t.addr(), rep.Arch, rep.Entware, rep.NuxkCore)
	writeJSON(w, http.StatusOK, checkResp{HostKey: c.HostKey, Report: rep, Plan: BuildPlan(rep, s.Payload)})
}

type installReq struct {
	Target   Target   `json:"target"`
	Selected []string `json:"selected"`
}

// handleInstall streams progress as NDJSON: one Event per line, the last one
// of kind "done".
func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req installReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный запрос: " + err.Error()})
		return
	}
	if req.Target.HostKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "сначала проверьте роутер"})
		return
	}
	if !s.installing.TryLock() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "установка уже идёт"})
		return
	}
	defer s.installing.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	emit := func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(e) // a gone browser must not abort a half-done opkg run
		if fl != nil {
			fl.Flush()
		}
	}

	req.Target.root = s.Root
	log.Printf("install %s@%s: %v", firstNonEmpty(req.Target.User, "root"), req.Target.addr(), req.Selected)
	c, err := Dial(req.Target)
	if err != nil {
		emit(Event{Kind: "fail", Text: err.Error()})
		emit(Event{Kind: "done", OK: false})
		return
	}
	defer c.Close()
	res, err := Install(c, s.Payload, req.Selected, emit)
	if err != nil {
		log.Printf("install failed: %v", err)
		emit(Event{Kind: "done", OK: false, Text: err.Error()})
		return
	}
	log.Printf("install done: %s", res.URL)
	emit(Event{Kind: "done", OK: true, URL: res.URL, Token: res.Token, Text: "nuxk установлен"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// newCode is XXXX-XXXX from an alphabet without look-alikes (0/O, 1/I).
func newCode() string {
	const alpha = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 0, 9)
	for i, x := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, alpha[int(x)%len(alpha)])
	}
	return string(out)
}
