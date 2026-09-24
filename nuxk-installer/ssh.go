package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Target is how to reach the router's Entware SSH (not the Keenetic CLI).
type Target struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password,omitempty"`
	Key        string `json:"key,omitempty"`        // private key, PEM
	Passphrase string `json:"passphrase,omitempty"` // for Key
	// HostKey pins the fingerprint seen at detect time (SHA256:…). Install
	// refuses a different key — the password never goes to an impostor twice.
	HostKey string `json:"host_key,omitempty"`

	root string // path prefix for every remote path; tests only
}

func (t Target) addr() string {
	port := t.Port
	if port == 0 {
		port = 222
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(port))
}

// Conn is one SSH connection to the router.
type Conn struct {
	c       *ssh.Client
	HostKey string // fingerprint of the key the router presented
	root    string
}

var errNoAuth = errors.New("укажите пароль или приватный ключ")

func Dial(t Target) (*Conn, error) {
	if t.Host == "" {
		return nil, errors.New("укажите адрес роутера")
	}
	var auths []ssh.AuthMethod
	if strings.TrimSpace(t.Key) != "" {
		var signer ssh.Signer
		var err error
		if t.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(t.Key), []byte(t.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(t.Key))
		}
		if err != nil {
			return nil, fmt.Errorf("приватный ключ не читается: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if t.Password != "" {
		pw := t.Password
		auths = append(auths, ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				ans := make([]string, len(qs))
				for i := range ans {
					ans[i] = pw
				}
				return ans, nil
			}))
	}
	if len(auths) == 0 {
		return nil, errNoAuth
	}

	var seen string
	var mu sync.Mutex
	cfg := &ssh.ClientConfig{
		User: firstNonEmpty(t.User, "root"),
		Auth: auths,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			mu.Lock()
			seen = fp
			mu.Unlock()
			if t.HostKey != "" && fp != t.HostKey {
				return fmt.Errorf("ключ хоста изменился: был %s, сейчас %s — повторите проверку роутера", t.HostKey, fp)
			}
			return nil
		},
		Timeout: 10 * time.Second,
	}
	c, err := ssh.Dial("tcp", t.addr(), cfg)
	if err != nil {
		return nil, explainDial(err, t)
	}
	return &Conn{c: c, HostKey: seen, root: t.root}, nil
}

func explainDial(err error, t Target) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"):
		return fmt.Errorf("SSH не принял логин или пароль (%s@%s). У Entware по умолчанию root / keenetic", firstNonEmpty(t.User, "root"), t.addr())
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("%s не принимает SSH. SSH Entware обычно на порту 222; порт 22 — это CLI Keenetic", t.addr())
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "no route"):
		return fmt.Errorf("%s не отвечает: проверьте адрес и что компьютер в той же сети", t.addr())
	}
	return err
}

func (c *Conn) Close() error { return c.c.Close() }

// Run executes cmd, feeding stdin, streaming combined output lines to out.
// The root prefix is exported as NUXK_ROOT for the scripts that honour it.
func (c *Conn) Run(cmd string, stdin io.Reader, out func(line string)) error {
	s, err := c.c.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	if c.root != "" {
		cmd = "export NUXK_ROOT=" + shQuote(c.root) + "; " + cmd
	}
	if stdin != nil {
		s.Stdin = stdin
	}
	lw := &lineWriter{fn: out}
	s.Stdout, s.Stderr = lw, lw
	err = s.Run(cmd)
	lw.flush()
	return err
}

// Output runs cmd and returns stdout alone (for parsing).
func (c *Conn) Output(cmd string, stdin io.Reader) (string, error) {
	s, err := c.c.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	if c.root != "" {
		cmd = "export NUXK_ROOT=" + shQuote(c.root) + "; " + cmd
	}
	if stdin != nil {
		s.Stdin = stdin
	}
	var so, se bytes.Buffer
	s.Stdout, s.Stderr = &so, &se
	if err := s.Run(cmd); err != nil {
		return so.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(se.String()))
	}
	return so.String(), nil
}

// Upload writes data to a router path atomically (tmp + mv), creating dirs.
func (c *Conn) Upload(path string, data []byte, mode string) error {
	p := shQuote(c.root + path)
	tmp := shQuote(c.root + path + ".nuxk-new")
	cmd := fmt.Sprintf("mkdir -p \"$(dirname %s)\" && cat > %s && chmod %s %s && mv -f %s %s", p, tmp, mode, tmp, tmp, p)
	_, err := c.Output(cmd, bytes.NewReader(data))
	return err
}

// P maps a router path to where it lives for this connection (tests: under root).
func (c *Conn) P(path string) string { return shQuote(c.root + path) }

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

type lineWriter struct {
	fn  func(string)
	buf []byte
	mu  sync.Mutex
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexAny(w.buf, "\r\n")
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(w.buf[:i])); line != "" && w.fn != nil {
			w.fn(line)
		}
		w.buf = w.buf[i+1:]
	}
	return len(b), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if line := strings.TrimSpace(string(w.buf)); line != "" && w.fn != nil {
		w.fn(line)
	}
	w.buf = nil
}
