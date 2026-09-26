package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Browser sessions and the failed-login lockout — the same rules as the
// agent's (nuxk-core/internal/auth), kept in memory: a restart logs out.
const (
	sessionCookie = "nuxk_ctl" // the agent's is nuxk_agent: no collision in one browser
	sessionIdle   = 12 * time.Hour
	maxSessions   = 32
	freeFails     = 5
	maxLockout    = 15 * time.Minute
)

type Sessions struct {
	now func() time.Time

	mu    sync.Mutex
	live  map[string]sessionEntry
	fails map[string]*failEntry
}

type sessionEntry struct{ created, seen time.Time }

type failEntry struct {
	n           int
	last, until time.Time
}

func NewSessions() *Sessions {
	return &Sessions{now: time.Now, live: map[string]sessionEntry{}, fails: map[string]*failEntry{}}
}

func (s *Sessions) New() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.live {
		if now.Sub(e.seen) > sessionIdle {
			delete(s.live, k)
		}
	}
	for len(s.live) >= maxSessions {
		oldest := ""
		for k, e := range s.live {
			if oldest == "" || e.created.Before(s.live[oldest].created) {
				oldest = k
			}
		}
		delete(s.live, oldest)
	}
	s.live[id] = sessionEntry{created: now, seen: now}
	return id, nil
}

// Valid reports whether id is a live session and refreshes it.
func (s *Sessions) Valid(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live[id]
	now := s.now()
	if !ok || now.Sub(e.seen) > sessionIdle {
		delete(s.live, id)
		return false
	}
	e.seen = now
	s.live[id] = e
	return true
}

func (s *Sessions) End(id string) {
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
}

// Locked returns how long addr must still wait before the next attempt.
func (s *Sessions) Locked(addr string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.fails[addr]; f != nil {
		return f.until.Sub(s.now())
	}
	return 0
}

func (s *Sessions) Fail(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for a, f := range s.fails {
		if now.Sub(f.last) > time.Hour {
			delete(s.fails, a)
		}
	}
	f := s.fails[addr]
	if f == nil {
		f = &failEntry{}
		s.fails[addr] = f
	}
	f.n++
	f.last = now
	if f.n >= freeFails {
		f.until = now.Add(min(30*time.Second<<min(f.n-freeFails, 10), maxLockout))
	}
}

func (s *Sessions) Succeeded(addr string) {
	s.mu.Lock()
	delete(s.fails, addr)
	s.mu.Unlock()
}

// fromRequest: the session id in the request's cookie, if live.
func (s *Sessions) fromRequest(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && s.Valid(c.Value)
}

func setSessionCookie(w http.ResponseWriter, id string) {
	c := &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}
	if id == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// sameOrigin guards cookie-authenticated writes against other sites (see the
// agent's twin in nuxk-core/internal/api/login.go).
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
