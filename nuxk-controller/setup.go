package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SetupState is GET /ctl/v1/setup: which wizard step the UI shows.
type SetupState struct {
	Admin    bool   `json:"admin"`     // the admin password is set
	Agent    bool   `json:"agent"`     // a router is connected
	LoggedIn bool   `json:"logged_in"` // this browser has a session
	AgentURL string `json:"agent_url,omitempty"`
}

type loginReq struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

type agentReq struct {
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type handlers struct {
	st      *Store
	ses     *Sessions
	ag      *Agent
	version string
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", "invalid JSON")
		return false
	}
	return true
}

func (h handlers) setupState(w http.ResponseWriter, r *http.Request) {
	u, _ := h.ag.Ref()
	st := SetupState{Admin: h.st.HasAdmin(), Agent: u != "", LoggedIn: h.ses.fromRequest(r)}
	if st.LoggedIn {
		st.AgentURL = u
	}
	writeJSON(w, http.StatusOK, st)
}

func (h handlers) startSession(w http.ResponseWriter) {
	id, err := h.ses.New()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session", err.Error())
		return
	}
	setSessionCookie(w, id)
	writeJSON(w, http.StatusOK, map[string]string{"user": adminUser})
}

// setupAdmin: step 1 — the first admin password. Only while none is set;
// whoever opens a fresh controller first sets it (it lives on the home LAN).
func (h handlers) setupAdmin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross_origin", "request from another site")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len([]rune(req.Password)) < minPwLen {
		writeErr(w, http.StatusBadRequest, "weak_password", fmt.Sprintf("пароль — не короче %d символов", minPwLen))
		return
	}
	switch err := h.st.SetAdmin(req.Password); {
	case errors.Is(err, ErrAdminExists):
		writeErr(w, http.StatusConflict, "admin_exists", "пароль администратора уже задан — войдите")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "store", err.Error())
		return
	}
	slog.Info("admin password set", "from", r.RemoteAddr)
	h.startSession(w)
}

func (h handlers) login(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross_origin", "request from another site")
		return
	}
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	ip := clientIP(r.RemoteAddr)
	if wait := h.ses.Locked(ip); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too_many_attempts", "слишком много неудачных попыток, подождите")
		return
	}
	if !h.st.CheckAdmin(req.User, req.Password) {
		h.ses.Fail(ip)
		slog.Warn("login failed", "from", r.RemoteAddr, "user", req.User)
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "неверный логин или пароль")
		return
	}
	h.ses.Succeeded(ip)
	slog.Info("login", "from", r.RemoteAddr)
	h.startSession(w)
}

func (h handlers) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		h.ses.End(c.Value)
	}
	setSessionCookie(w, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// setupAgent: step 2 — connect the router. The root login and password go
// to the agent once (POST /api/v1/auth/pair); only the token it returns is kept.
func (h handlers) setupAgent(w http.ResponseWriter, r *http.Request) {
	var req agentReq
	if !decode(w, r, &req) {
		return
	}
	base, err := agentBase(req.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_url", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, perr := pair(ctx, base, req.User, req.Password)
	if perr != nil {
		writeErr(w, perr.code, perr.kind, perr.msg)
		return
	}
	if err := h.st.SetAgent(AgentRef{URL: base, Token: tok}); err != nil {
		writeErr(w, http.StatusInternalServerError, "store", err.Error())
		return
	}
	h.ag.Configure(base, tok)
	h.ag.poll(ctx)
	slog.Info("router connected", "agent", base, "from", r.RemoteAddr)
	writeJSON(w, http.StatusOK, h.ag.State(h.version))
}

// agentBase turns what a person types ("192.168.1.1", "192.168.1.1:4141",
// "http://router:4141/") into the agent's base URL.
func agentBase(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("укажите адрес роутера")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("не похоже на адрес роутера: %q", s)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "4141")
	}
	return u.Scheme + "://" + host, nil
}

type pairErr struct {
	code      int
	kind, msg string
}

// pair asks the agent for its API token with the box's root credentials and
// translates the agent's refusals into words for the wizard.
func pair(ctx context.Context, base, user, password string) (string, *pairErr) {
	body, _ := json.Marshal(loginReq{User: user, Password: password})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/auth/pair", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", &pairErr{http.StatusBadGateway, "agent_unreachable",
			"роутер не отвечает по адресу " + base + " — проверьте адрес и что nuxk-core запущен (" + err.Error() + ")"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var ok struct {
		Token string `json:"token"`
	}
	var fail struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	switch {
	case resp.StatusCode == http.StatusOK && json.Unmarshal(raw, &ok) == nil && ok.Token != "":
		return ok.Token, nil
	case resp.StatusCode == http.StatusNotFound:
		return "", &pairErr{http.StatusBadGateway, "agent_too_old",
			"на роутере старая версия nuxk-core без входа по паролю — обновите её: на роутере «nuxk update» или здесь «sh nuxk-full.sh router»"}
	}
	json.Unmarshal(raw, &fail)
	msg := fail.Error.Message
	if msg == "" {
		msg = fmt.Sprintf("роутер ответил HTTP %d", resp.StatusCode)
	}
	code := http.StatusBadGateway
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		// not 401: that would read as "your controller session expired"
		code, msg = http.StatusUnprocessableEntity, "роутер не принял логин или пароль root"
	case http.StatusTooManyRequests:
		code = http.StatusTooManyRequests
	}
	return "", &pairErr{code, "pair_failed", msg}
}
