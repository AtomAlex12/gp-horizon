package auth

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrBadCredentials = errors.New("wrong login or password")
	ErrNoPassword     = errors.New("the account has no password set")
)

// TooMany is returned while an address is locked out after failed logins.
type TooMany struct{ Wait time.Duration }

func (e TooMany) Error() string {
	return fmt.Sprintf("too many failed logins, retry in %s", e.Wait.Round(time.Second))
}

const (
	sessionIdle = 12 * time.Hour // a session unused this long is gone
	maxSessions = 32             // oldest dropped beyond this
	freeFails   = 5              // failed logins before the lockout starts
	maxLockout  = 15 * time.Minute
)

// Guard checks the box account's password and holds browser sessions (in
// memory: a restart of nuxk-core logs everyone out, by design).
type Guard struct {
	User  string   // the one account that may log in (root)
	Files []string // shadow first, then passwd — the first entry for User wins

	now func() time.Time

	mu       sync.Mutex
	sessions map[string]session
	fails    map[string]*failure
}

type session struct {
	user    string
	created time.Time
	seen    time.Time
}

type failure struct {
	n     int
	last  time.Time
	until time.Time
}

func New(user string, files ...string) *Guard {
	return &Guard{
		User: user, Files: files, now: time.Now,
		sessions: map[string]session{}, fails: map[string]*failure{},
	}
}

// Check verifies user/password for a client address, with a lockout that
// doubles after every failure past the free ones.
func (g *Guard) Check(addr, user, password string) error {
	if wait := g.locked(addr); wait > 0 {
		return TooMany{wait}
	}
	err := g.verify(user, password)
	if errors.Is(err, ErrBadCredentials) || errors.Is(err, ErrNoPassword) {
		g.fail(addr)
	} else if err == nil {
		g.mu.Lock()
		delete(g.fails, addr)
		g.mu.Unlock()
	}
	return err
}

func (g *Guard) verify(user, password string) error {
	hashed, err := g.lookup()
	if err != nil {
		return err
	}
	if hashed == "" || hashed[0] == '!' || hashed[0] == '*' {
		// locked account, or no password at all — never a way in
		_, _ = Verify(password, "$6$nuxk$") // same work as a real check
		return ErrNoPassword
	}
	ok, err := Verify(password, hashed)
	if err != nil {
		return err
	}
	if !ok || user != g.User {
		return ErrBadCredentials
	}
	return nil
}

// lookup finds the account's hash: the shadow file, or passwd when it keeps
// the hash itself (no "x" placeholder).
func (g *Guard) lookup() (string, error) {
	var lastErr error
	for _, f := range g.Files {
		h, err := hashIn(f, g.User)
		if err != nil {
			lastErr = err
			continue
		}
		if h != "x" {
			return h, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no entry for %q in %s", g.User, strings.Join(g.Files, ", "))
	}
	return "", lastErr
}

func hashIn(path, user string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if ok && name == user {
			h, _, _ := strings.Cut(rest, ":")
			return h, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no entry for %q in %s", user, path)
}

func (g *Guard) locked(addr string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f := g.fails[addr]; f != nil {
		return f.until.Sub(g.now())
	}
	return 0
}

func (g *Guard) fail(addr string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for a, f := range g.fails { // forget stale entries
		if now.Sub(f.last) > time.Hour {
			delete(g.fails, a)
		}
	}
	f := g.fails[addr]
	if f == nil {
		f = &failure{}
		g.fails[addr] = f
	}
	f.n++
	f.last = now
	if f.n >= freeFails {
		f.until = now.Add(min(30*time.Second<<min(f.n-freeFails, 10), maxLockout))
	}
}

// NewSession starts a session and returns its id (the cookie value).
func (g *Guard) NewSession(user string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for k, s := range g.sessions {
		if now.Sub(s.seen) > sessionIdle {
			delete(g.sessions, k)
		}
	}
	for len(g.sessions) >= maxSessions {
		oldest := ""
		for k, s := range g.sessions {
			if oldest == "" || s.created.Before(g.sessions[oldest].created) {
				oldest = k
			}
		}
		delete(g.sessions, oldest)
	}
	g.sessions[id] = session{user: user, created: now, seen: now}
	return id, nil
}

// Session returns the user of a live session and refreshes it.
func (g *Guard) Session(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessions[id]
	now := g.now()
	if !ok || now.Sub(s.seen) > sessionIdle {
		delete(g.sessions, id)
		return "", false
	}
	s.seen = now
	g.sessions[id] = s
	return s.user, true
}

func (g *Guard) EndSession(id string) {
	g.mu.Lock()
	delete(g.sessions, id)
	g.mu.Unlock()
}
