package release

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// testdata was signed by ssh-keygen itself (OpenSSH 10.2) with a throwaway
// key: SHA256SUMS.sig in the release namespace, wrong-ns.sig in "file",
// other.sig by another key.
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testKeys(t *testing.T) []Key {
	t.Helper()
	ks, err := ParseAllowedSigners(string(read(t, "allowed_signers")))
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestVerifiesWhatSSHKeygenSigned(t *testing.T) {
	k, err := VerifyWith(testKeys(t), read(t, "SHA256SUMS"), read(t, "SHA256SUMS.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if k.Principal != "release@nuxk-horizon" {
		t.Fatalf("principal %q", k.Principal)
	}
	// the fingerprint ssh-keygen printed for the test key
	if got := k.Fingerprint(); got != "SHA256:0CiAMVf9CT800UabS6jccqqJl38farDysLInWlVtpEU" {
		t.Fatalf("fingerprint %s", got)
	}
}

func TestRefuses(t *testing.T) {
	keys := testKeys(t)
	msg, sig := read(t, "SHA256SUMS"), read(t, "SHA256SUMS.sig")
	for _, c := range []struct {
		name     string
		msg, sig []byte
		want     error
	}{
		{"a changed file", append([]byte("0000  nuxk-core-mips\n"), msg...), sig, ErrBadSignature},
		{"one byte", []byte(strings.Replace(string(msg), "abc123", "abc124", 1)), sig, ErrBadSignature},
		{"another namespace", msg, read(t, "wrong-ns.sig"), ErrBadSignature},
		{"another key", msg, read(t, "other.sig"), ErrUnknownKey},
		{"no signature", msg, nil, ErrNoSignature},
		{"garbage", msg, []byte("-----BEGIN SSH SIGNATURE-----\nAAAA\n-----END SSH SIGNATURE-----\n"), ErrBadSignature},
		{"not armored", msg, []byte("hello"), ErrBadSignature},
		{"cut short", msg, sig[:len(sig)/2], ErrBadSignature},
	} {
		if _, err := VerifyWith(keys, c.msg, c.sig); !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", c.name, err, c.want)
		}
	}
}

// The built-in key is a real one and the only key the agent trusts: a test
// signature doesn't pass for a release.
func TestBuiltInKey(t *testing.T) {
	ks := Keys()
	if len(ks) != 1 || ks[0].Principal != "release@nuxk-horizon" {
		t.Fatalf("built-in keys: %+v", ks)
	}
	if _, err := Verify(read(t, "SHA256SUMS"), read(t, "SHA256SUMS.sig")); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a test key passed for the release key: %v", err)
	}
}

// nuxk-full.sh checks releases on the Pi with ssh-keygen and carries the same
// key: a new key goes into both.
func TestFullInstallerHasTheKey(t *testing.T) {
	b, err := os.ReadFile("../../../install/nuxk-full.sh")
	if err != nil {
		t.Fatal(err)
	}
	if line := strings.TrimSpace(allowedSigners); !strings.Contains(string(b), line) {
		t.Fatalf("install/nuxk-full.sh lacks the release key line:\n%s", line)
	}
}

func TestAllowedSignersNamespaces(t *testing.T) {
	ks, err := ParseAllowedSigners(`# comment
a@x namespaces="file" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGZIGLxU1J1lwmgcr2Iqd85lK4K4YrXg50Xek02FxNN5
b@x ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGZIGLxU1J1lwmgcr2Iqd85lK4K4YrXg50Xek02FxNN5 comment
`)
	if err != nil || len(ks) != 1 || ks[0].Principal != "b@x" {
		t.Fatalf("keys %+v, err %v", ks, err)
	}
	if _, err := ParseAllowedSigners("x@y ssh-rsa AAAA"); err == nil {
		t.Fatal("an rsa-only file parsed")
	}
}
