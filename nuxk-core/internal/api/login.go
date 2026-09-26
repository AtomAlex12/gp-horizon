package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"nuxk.dev/horizon/core/internal/auth"
)

// sessionCookie carries a browser session on the agent (the controller on the
// Pi uses its own name, so the two never collide in one browser).
const sessionCookie = "nuxk_agent"

type credentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// SessionInfo is POST /auth/login's answer.
type SessionInfo struct {
	User string `json:"user"`
}

// PairInfo is POST /auth/pair's answer: the agent's API token for a
// controller that proved it knows the box's root password.
type PairInfo struct {
	Token string `json:"token"`
}

// checkLogin decodes credentials and checks them; it writes the error itself.
func (d Deps) checkLogin(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	if d.Auth == nil {
		writeErr(w, http.StatusNotFound, "login_off", "login is not configured on this node")
		return credentials{}, false
	}
	var c credentials
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_body", "want {\"user\",\"password\"}")
		return c, false
	}
	err := d.Auth.Check(clientIP(r.RemoteAddr), c.User, c.Password)
	var tm auth.TooMany
	switch {
	case err == nil:
		return c, true
	case errors.As(err, &tm):
		w.Header().Set("Retry-After", strconv.Itoa(int(tm.Wait.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too_many_attempts", "слишком много неудачных попыток, подождите")
	case errors.Is(err, auth.ErrBadCredentials), errors.Is(err, auth.ErrNoPassword):
		slog.Warn("login failed", "from", r.RemoteAddr, "user", c.User)
		writeErr(w, http.StatusUnauthorized, "bad_credentials", "неверный логин или пароль")
	default:
		// the account file is missing or its hash format is unknown — the
		// person can't fix that by retyping, so say what's wrong
		slog.Error("login: can't check the password", "err", err)
		writeErr(w, http.StatusServiceUnavailable, "login_unavailable", "не удалось проверить пароль на роутере: "+err.Error())
	}
	return c, false
}

func (d Deps) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross_origin", "request from another site")
		return
	}
	c, ok := d.checkLogin(w, r)
	if !ok {
		return
	}
	id, err := d.Auth.NewSession(c.User)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "session", err.Error())
		return
	}
	slog.Info("login", "user", c.User, "from", r.RemoteAddr)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, SessionInfo{User: c.User})
}

func (d Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && d.Auth != nil {
		d.Auth.EndSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePair hands the API token to a controller that knows the root
// password, so nobody has to copy the token out of nuxk.conf by hand.
func (d Deps) handlePair(w http.ResponseWriter, r *http.Request) {
	if _, ok := d.checkLogin(w, r); !ok {
		return
	}
	if d.Token == "" {
		writeErr(w, http.StatusConflict, "no_token", "в /opt/etc/nuxk/nuxk.conf пустой API_TOKEN — задайте его и перезапустите nuxk-core")
		return
	}
	slog.Info("paired a controller", "from", r.RemoteAddr)
	writeJSON(w, http.StatusOK, PairInfo{Token: d.Token})
}

// sessionUser returns the logged-in user of a request's session cookie.
func (d Deps) sessionUser(r *http.Request) (string, bool) {
	if d.Auth == nil {
		return "", false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return d.Auth.Session(c.Value)
}

// sameOrigin guards cookie-authenticated writes against other sites: a
// browser always sends Origin on a cross-origin POST, and a page on another
// site can't add X-Forwarded-Host (non-simple header → preflight → denied).
// Behind a dev proxy the browser's host arrives in X-Forwarded-Host.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		s := r.Header.Get("Sec-Fetch-Site")
		return s == "" || s == "same-origin" || s == "none"
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return u.Host == r.Host || (u.Host != "" && u.Host == r.Header.Get("X-Forwarded-Host"))
}

func clientIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
