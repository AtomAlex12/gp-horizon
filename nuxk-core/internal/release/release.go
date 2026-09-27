// Package release checks that a release's SHA256SUMS comes from nuxk Horizon:
// the release workflow signs it with the release key (an SSH signature,
// `ssh-keygen -Y sign -n nuxk-release`), and the agent already on the router
// checks it before an update touches anything. A new agent can't vouch for
// itself — the one being replaced does.
//
// The public key is built in (allowed_signers, the ssh-keygen format), so
// anyone can check a release by hand too:
//
//	ssh-keygen -Y verify -f allowed_signers -I release@nuxk-horizon \
//	    -n nuxk-release -s SHA256SUMS.sig < SHA256SUMS
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	_ "embed"
)

// Namespace keeps a release signature from passing for any other SSH
// signature made with the same key (and the other way round).
const Namespace = "nuxk-release"

//go:embed allowed_signers
var allowedSigners string

// Key is one trusted signer.
type Key struct {
	Principal string
	Pub       ed25519.PublicKey
}

// Fingerprint is how ssh-keygen shows the key: SHA256:… of its wire form.
func (k Key) Fingerprint() string {
	sum := sha256.Sum256(wireKey(k.Pub))
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Keys are the built-in trusted release keys.
func Keys() []Key {
	ks, err := ParseAllowedSigners(allowedSigners)
	if err != nil {
		panic("release: built-in allowed_signers: " + err.Error())
	}
	return ks
}

var (
	ErrNoSignature  = errors.New("нет подписи")
	ErrBadSignature = errors.New("подпись не сходится")
	ErrUnknownKey   = errors.New("подписано не ключом nuxk Horizon")
)

// Verify checks msg against an armored SSH signature, with the built-in keys.
func Verify(msg, sig []byte) (Key, error) { return VerifyWith(Keys(), msg, sig) }

// VerifyFile checks the file at path against path.sig, with the built-in keys.
func VerifyFile(path string) (Key, error) {
	msg, err := os.ReadFile(path)
	if err != nil {
		return Key{}, err
	}
	sig, err := os.ReadFile(path + ".sig")
	if errors.Is(err, fs.ErrNotExist) {
		return Key{}, ErrNoSignature
	} else if err != nil {
		return Key{}, err
	}
	return Verify(msg, sig)
}

// VerifyWith checks msg against an armored SSH signature made in Namespace by
// one of keys.
func VerifyWith(keys []Key, msg, sig []byte) (Key, error) {
	s, err := parseSig(sig)
	if err != nil {
		return Key{}, err
	}
	if s.namespace != Namespace {
		return Key{}, fmt.Errorf("%w: подпись для «%s», а не для релиза", ErrBadSignature, s.namespace)
	}
	var h []byte
	switch s.hashAlg {
	case "sha512":
		sum := sha512.Sum512(msg)
		h = sum[:]
	case "sha256":
		sum := sha256.Sum256(msg)
		h = sum[:]
	default:
		return Key{}, fmt.Errorf("%w: хеш %q", ErrBadSignature, s.hashAlg)
	}
	var key *Key
	for i := range keys {
		if bytes.Equal(keys[i].Pub, s.pub) {
			key = &keys[i]
			break
		}
	}
	if key == nil {
		return Key{}, ErrUnknownKey
	}
	// what was signed (PROTOCOL.sshsig): the preamble, then the namespace,
	// reserved, hash algorithm and the message's hash as SSH strings
	var signed []byte
	signed = append(signed, "SSHSIG"...)
	for _, f := range [][]byte{[]byte(s.namespace), s.reserved, []byte(s.hashAlg), h} {
		signed = appendString(signed, f)
	}
	if !ed25519.Verify(key.Pub, signed, s.sig) {
		return Key{}, ErrBadSignature
	}
	return *key, nil
}

type sshsig struct {
	pub       ed25519.PublicKey
	namespace string
	reserved  []byte
	hashAlg   string
	sig       []byte
}

const (
	armorBegin = "-----BEGIN SSH SIGNATURE-----"
	armorEnd   = "-----END SSH SIGNATURE-----"
)

func parseSig(armored []byte) (sshsig, error) {
	var s sshsig
	txt := strings.TrimSpace(strings.ReplaceAll(string(armored), "\r", ""))
	if txt == "" {
		return s, ErrNoSignature
	}
	body, ok := strings.CutPrefix(txt, armorBegin)
	if !ok {
		return s, fmt.Errorf("%w: не SSH-подпись", ErrBadSignature)
	}
	body, ok = strings.CutSuffix(body, armorEnd)
	if !ok {
		return s, fmt.Errorf("%w: подпись обрезана", ErrBadSignature)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(body), ""))
	if err != nil {
		return s, fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	r := reader{b: raw}
	if magic := r.fixed(6); string(magic) != "SSHSIG" {
		return s, fmt.Errorf("%w: не SSHSIG", ErrBadSignature)
	}
	if v := r.uint32(); v != 1 {
		return s, fmt.Errorf("%w: версия %d", ErrBadSignature, v)
	}
	pubBlob := r.string()
	s.namespace = string(r.string())
	s.reserved = r.string()
	s.hashAlg = string(r.string())
	sigBlob := r.string()
	if r.err != nil || len(r.b) != 0 {
		return s, fmt.Errorf("%w: формат", ErrBadSignature)
	}
	if s.pub, err = parseWireKey(pubBlob); err != nil {
		return s, err
	}
	sr := reader{b: sigBlob}
	typ, sig := sr.string(), sr.string()
	if sr.err != nil || string(typ) != "ssh-ed25519" || len(sig) != ed25519.SignatureSize {
		return s, fmt.Errorf("%w: не ed25519", ErrBadSignature)
	}
	s.sig = sig
	return s, nil
}

// ParseAllowedSigners reads ssh-keygen's allowed_signers: "principal
// [options] ssh-ed25519 BASE64 [comment]" per line. A key limited to other
// namespaces is left out; only ed25519 keys are taken.
func ParseAllowedSigners(s string) ([]Key, error) {
	var keys []Key
	for n, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		i := 1
		for i < len(f) && f[i] != "ssh-ed25519" {
			i++
		}
		if len(f) < 3 || i+1 >= len(f) {
			return nil, fmt.Errorf("строка %d: нужен «principal [options] ssh-ed25519 KEY»", n+1)
		}
		if opts := strings.Join(f[1:i], " "); strings.Contains(opts, "namespaces=") && !strings.Contains(opts, Namespace) {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(f[i+1])
		if err != nil {
			return nil, fmt.Errorf("строка %d: %v", n+1, err)
		}
		pub, err := parseWireKey(blob)
		if err != nil {
			return nil, fmt.Errorf("строка %d: %v", n+1, err)
		}
		keys = append(keys, Key{Principal: f[0], Pub: pub})
	}
	if len(keys) == 0 {
		return nil, errors.New("нет ни одного ключа ed25519")
	}
	return keys, nil
}

func parseWireKey(b []byte) (ed25519.PublicKey, error) {
	r := reader{b: b}
	typ, pub := r.string(), r.string()
	if r.err != nil || len(r.b) != 0 || string(typ) != "ssh-ed25519" || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: ключ не ed25519", ErrBadSignature)
	}
	return ed25519.PublicKey(pub), nil
}

func wireKey(pub ed25519.PublicKey) []byte {
	return appendString(appendString(nil, []byte("ssh-ed25519")), pub)
}

func appendString(b, s []byte) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// reader walks SSH wire format; the first short read sticks as err.
type reader struct {
	b   []byte
	err error
}

func (r *reader) fixed(n int) []byte {
	if r.err != nil || len(r.b) < n {
		r.err = errors.New("short")
		return nil
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v
}

func (r *reader) uint32() uint32 {
	b := r.fixed(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *reader) string() []byte {
	n := r.uint32()
	if r.err != nil || uint64(n) > uint64(len(r.b)) {
		r.err = errors.New("short")
		return nil
	}
	return r.fixed(int(n))
}
