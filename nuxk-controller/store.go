package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Settings is what the setup wizard writes: the admin's password hash and
// the router's agent. DATA_DIR/controller.json, mode 0600.
type Settings struct {
	Admin *Admin    `json:"admin,omitempty"`
	Agent *AgentRef `json:"agent,omitempty"`
}

// Admin: PBKDF2-SHA256 (stdlib since Go 1.24) — the password itself is never stored.
type Admin struct {
	User string `json:"user"`
	Salt []byte `json:"salt"`
	Hash []byte `json:"hash"`
	Iter int    `json:"iter"`
}

// AgentRef: where the router's agent is and the API token it handed over
// when paired (never the root password).
type AgentRef struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

const (
	adminUser  = "admin"
	pbkdf2Iter = 600_000 // OWASP 2023 for PBKDF2-SHA256; ~0.3 s on a Pi 5
	minPwLen   = 8
)

var ErrAdminExists = errors.New("the admin password is already set")

type Store struct {
	path string
	mu   sync.Mutex
	s    Settings
}

func OpenStore(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "controller.json")}
	b, err := os.ReadFile(st.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &st.s); err != nil {
		return nil, err
	}
	return st, nil
}

func (st *Store) save() error {
	b, err := json.MarshalIndent(st.s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func (st *Store) HasAdmin() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.Admin != nil
}

// SetAdmin sets the first admin password; it can't overwrite an existing one.
func (st *Store) SetAdmin(password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	h, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, 32)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.Admin != nil {
		return ErrAdminExists
	}
	st.s.Admin = &Admin{User: adminUser, Salt: salt, Hash: h, Iter: pbkdf2Iter}
	if err := st.save(); err != nil {
		st.s.Admin = nil
		return err
	}
	return nil
}

// CheckAdmin verifies the admin's login and password.
func (st *Store) CheckAdmin(user, password string) bool {
	st.mu.Lock()
	a := st.s.Admin
	st.mu.Unlock()
	if a == nil {
		return false
	}
	h, err := pbkdf2.Key(sha256.New, password, a.Salt, a.Iter, len(a.Hash))
	return err == nil && subtle.ConstantTimeCompare(h, a.Hash) == 1 && user == a.User
}

func (st *Store) Agent() *AgentRef {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.Agent == nil {
		return nil
	}
	a := *st.s.Agent
	return &a
}

func (st *Store) SetAgent(a AgentRef) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	prev := st.s.Agent
	st.s.Agent = &a
	if err := st.save(); err != nil {
		st.s.Agent = prev
		return err
	}
	return nil
}
