package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Vectors from `openssl passwd -1/-5/-6` and Drepper's SHA-crypt spec.
func TestVerify(t *testing.T) {
	for _, c := range []struct {
		pw, hash string
	}{
		{"Hello world!", "$1$saltstri$YMyguxXMBpd2TEZ.vS/3q1"},
		{"Hello world!", "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
		{"Hello world!", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"Hello world!", "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"Hello world!", "$5$rounds=10000$saltstringsaltst$3xv.VbSHBb41AL9AvLeujZkZRBAwqFMz2.opqey6IcA"},
		{"This is just a test", "$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"},
		{"we have a short salt string but not a short password", "$6$rounds=77777$short$WuQyW2YR.hBNpjjRhpYD/ifIw05xdfeEyQoMxIXbkvr0gge1a1x3yRULJ5CCaUeOxFmtlcGZelFl5CxtgfiAc0"},
		{"the minimum number is still observed", "$5$rounds=1000$roundstoolow$yfvwcWrQ8l/K0DAWyuPMDNHpIVlTQebY9l/gL972bIC"},
		{"", "$1$ab$rn6aQS/o7141mj179E/zA."},
	} {
		ok, err := Verify(c.pw, c.hash)
		if err != nil || !ok {
			t.Errorf("Verify(%q, %s) = %v, %v", c.pw, c.hash, ok, err)
		}
		if ok, _ := Verify(c.pw+"x", c.hash); ok {
			t.Errorf("wrong password accepted for %s", c.hash)
		}
	}
	if _, err := Verify("x", "$y$j9T$abc$def"); !errors.Is(err, ErrUnsupportedHash) {
		t.Errorf("yescrypt: want ErrUnsupportedHash, got %v", err)
	}
}

func testGuard(t *testing.T, shadow string) *Guard {
	t.Helper()
	dir := t.TempDir()
	sh := filepath.Join(dir, "shadow")
	pw := filepath.Join(dir, "passwd")
	os.WriteFile(sh, []byte(shadow), 0o600)
	os.WriteFile(pw, []byte("root:x:0:0:root:/opt/root:/bin/sh\n"), 0o644)
	return New("root", sh, pw)
}

const helloShadow = "daemon:*:0:0:99999:7:::\nroot:$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1:19000:0:99999:7:::\n"

func TestCheck(t *testing.T) {
	g := testGuard(t, helloShadow)
	if err := g.Check("a", "root", "Hello world!"); err != nil {
		t.Fatalf("right password: %v", err)
	}
	if err := g.Check("a", "admin", "Hello world!"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("other user: %v", err)
	}
	if err := g.Check("a", "root", "nope"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("wrong password: %v", err)
	}

	locked := testGuard(t, "root:!:19000::::::\n")
	if err := locked.Check("a", "root", ""); !errors.Is(err, ErrNoPassword) {
		t.Errorf("locked account: %v", err)
	}
	empty := testGuard(t, "root::19000::::::\n")
	if err := empty.Check("a", "root", ""); !errors.Is(err, ErrNoPassword) {
		t.Errorf("empty hash must never log in: %v", err)
	}

	// passwd with the hash inline, no shadow entry
	dir := t.TempDir()
	pw := filepath.Join(dir, "passwd")
	os.WriteFile(pw, []byte("root:$1$saltstri$YMyguxXMBpd2TEZ.vS/3q1:0:0::/opt/root:/bin/sh\n"), 0o644)
	if err := New("root", filepath.Join(dir, "shadow"), pw).Check("a", "root", "Hello world!"); err != nil {
		t.Errorf("inline passwd hash: %v", err)
	}
}

func TestLockout(t *testing.T) {
	g := testGuard(t, helloShadow)
	now := time.Unix(1_700_000_000, 0)
	g.now = func() time.Time { return now }
	for i := 0; i < freeFails; i++ {
		g.Check("1.2.3.4", "root", "bad")
	}
	var tm TooMany
	if err := g.Check("1.2.3.4", "root", "Hello world!"); !errors.As(err, &tm) {
		t.Fatalf("after %d failures the right password must wait, got %v", freeFails, err)
	}
	if err := g.Check("5.6.7.8", "root", "Hello world!"); err != nil {
		t.Errorf("another address is not locked: %v", err)
	}
	now = now.Add(tm.Wait + time.Second)
	if err := g.Check("1.2.3.4", "root", "Hello world!"); err != nil {
		t.Errorf("after the wait: %v", err)
	}
	if err := g.Check("1.2.3.4", "root", "bad"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("success resets the counter: %v", err)
	}
}

func TestSessions(t *testing.T) {
	g := New("root")
	now := time.Unix(1_700_000_000, 0)
	g.now = func() time.Time { return now }
	id, err := g.NewSession("root")
	if err != nil || len(id) != 64 {
		t.Fatalf("id %q err %v", id, err)
	}
	if u, ok := g.Session(id); !ok || u != "root" {
		t.Fatalf("fresh session: %q %v", u, ok)
	}
	now = now.Add(sessionIdle - time.Minute)
	if _, ok := g.Session(id); !ok {
		t.Fatal("use keeps a session alive")
	}
	now = now.Add(sessionIdle + time.Minute)
	if _, ok := g.Session(id); ok {
		t.Fatal("idle session must expire")
	}
	id, _ = g.NewSession("root")
	g.EndSession(id)
	if _, ok := g.Session(id); ok {
		t.Fatal("ended session is gone")
	}
	first, _ := g.NewSession("root")
	for i := 0; i < maxSessions; i++ {
		now = now.Add(time.Second)
		g.NewSession("root")
	}
	if _, ok := g.Session(first); ok {
		t.Error("the oldest session is dropped past the cap")
	}
	if _, ok := g.Session(""); ok {
		t.Error("empty id")
	}
}
