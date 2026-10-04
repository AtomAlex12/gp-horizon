package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Browser sessions and the failed-login lockout — the same rules as the
// agent's (nuxk-core/internal/auth), kept in memory: a restart logs out,
// except an update's — the sessions go to the next controller (Handoff),
// as hashes only.
const (
	sessionCookie = "nuxk_ctl" // the agent's is nuxk_agent: no collision in one browser
	sessionIdle   = 12 * time.Hour
	maxSessions   = 32
	freeFails     = 5
	maxLockout    = 15 * time.Minute
)

type Sessions struct {
	now func() time.Time

	mu     sync.Mutex
	live   map[string]sessionEntry
	handed map[string]sessionEntry // from the controller before an update, by sha256(id)
	fails  map[string]*failEntry
}

const handoffFresh = 15 * time.Minute

func sessionHash(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// Handoff writes the live sessions — their hashes, never the ids — for the
// controller that replaces this one.
func (s *Sessions) Handoff(path string) error {
	s.mu.Lock()
	var b strings.Builder
	for id, e := range s.live {
		fmt.Fprintf(&b, "%s %d %d\n", sessionHash(id), e.created.Unix(), e.seen.Unix())
	}
	s.mu.Unlock()
	if err := os.WriteFile(path+".tmp", []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// TakeHandoff reads what the controller before left, if it's fresh, and
// removes it: a session shown by its id continues here.
func (s *Sessions) TakeHandoff(path string) int {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	defer os.Remove(path)
	if s.now().Sub(st.ModTime()) > handoffFresh {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() && len(s.handed) < maxSessions {
		p := strings.Fields(sc.Text())
		if len(p) != 3 || len(p[0]) != 64 {
			continue
		}
		c, _ := strconv.ParseInt(p[1], 10, 64)
		e, _ := strconv.ParseInt(p[2], 10, 64)
		s.handed[p[0]] = sessionEntry{created: time.Unix(c, 0), seen: time.Unix(e, 0)}
		n++
	}
	return n
}

type sessionEntry struct{ created, seen time.Time }

type failEntry struct {
	n           int
	last, until time.Time
}

func NewSessions() *Sessions {
	return &Sessions{now: time.Now, live: map[string]sessionEntry{}, handed: map[string]sessionEntry{}, fails: map[string]*failEntry{}}
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
	if !ok {
		// handed over by the controller before an update
		if h, was := s.handed[sessionHash(id)]; was {
			delete(s.handed, sessionHash(id))
			e, ok = h, true
		}
	}
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
