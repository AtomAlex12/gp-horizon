// Package auth logs a person into the agent's web UI with the box's own root
// account (Entware's /opt/etc/shadow — the same login and password as SSH)
// and keeps the resulting browser sessions.
//
// stdlib only: the crypt(3) formats Entware's passwd writes — MD5-crypt ($1$),
// SHA-256-crypt ($5$) and SHA-512-crypt ($6$) — are implemented here.
package auth

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"hash"
	"strconv"
	"strings"
)

// ErrUnsupportedHash: the account's hash is in a format this build can't check
// (yescrypt, bcrypt, DES, ...).
var ErrUnsupportedHash = errors.New("unsupported password hash format")

// Verify reports whether password matches a crypt(3) hash string.
func Verify(password, hashed string) (bool, error) {
	var got string
	switch {
	case strings.HasPrefix(hashed, "$1$"):
		got = md5Crypt([]byte(password), hashed)
	case strings.HasPrefix(hashed, "$5$"):
		got = shaCrypt(sha256.New, "$5$", sha256Perm, []byte(password), hashed)
	case strings.HasPrefix(hashed, "$6$"):
		got = shaCrypt(sha512.New, "$6$", sha512Perm, []byte(password), hashed)
	default:
		return false, ErrUnsupportedHash
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(hashed)) == 1, nil
}

const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// b64 appends the n low 6-bit groups of v, least significant first.
func b64(out []byte, v uint32, n int) []byte {
	for ; n > 0; n-- {
		out = append(out, itoa64[v&0x3f])
		v >>= 6
	}
	return out
}

// saltOf returns the salt field of "$id$[rounds=N$]salt$hash", cut to max.
func saltOf(rest string, max int) string {
	s, _, _ := strings.Cut(rest, "$")
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// md5Crypt is FreeBSD/glibc MD5-crypt; full is the stored "$1$salt$hash".
func md5Crypt(pw []byte, full string) string {
	const magic = "$1$"
	salt := []byte(saltOf(full[len(magic):], 8))

	alt := md5.New()
	alt.Write(pw)
	alt.Write(salt)
	alt.Write(pw)
	altSum := alt.Sum(nil)

	ctx := md5.New()
	ctx.Write(pw)
	ctx.Write([]byte(magic))
	ctx.Write(salt)
	for i := len(pw); i > 0; i -= 16 {
		ctx.Write(altSum[:min(i, 16)])
	}
	for i := len(pw); i > 0; i >>= 1 {
		if i&1 != 0 {
			ctx.Write([]byte{0})
		} else {
			ctx.Write(pw[:1])
		}
	}
	fin := ctx.Sum(nil)

	for i := 0; i < 1000; i++ {
		c := md5.New()
		if i&1 != 0 {
			c.Write(pw)
		} else {
			c.Write(fin)
		}
		if i%3 != 0 {
			c.Write(salt)
		}
		if i%7 != 0 {
			c.Write(pw)
		}
		if i&1 != 0 {
			c.Write(fin)
		} else {
			c.Write(pw)
		}
		fin = c.Sum(nil)
	}

	out := append([]byte(magic), salt...)
	out = append(out, '$')
	for _, g := range [][3]int{{0, 6, 12}, {1, 7, 13}, {2, 8, 14}, {3, 9, 15}, {4, 10, 5}} {
		out = b64(out, uint32(fin[g[0]])<<16|uint32(fin[g[1]])<<8|uint32(fin[g[2]]), 4)
	}
	out = b64(out, uint32(fin[11]), 2)
	return string(out)
}

// Byte order of the final encoding (Drepper's SHA-crypt spec): triples of
// digest indexes, most significant first; -1 is a zero byte.
var sha256Perm = [][3]int{
	{0, 10, 20}, {21, 1, 11}, {12, 22, 2}, {3, 13, 23}, {24, 4, 14},
	{15, 25, 5}, {6, 16, 26}, {27, 7, 17}, {18, 28, 8}, {9, 19, 29}, {-1, 31, 30},
}

var sha512Perm = [][3]int{
	{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26},
	{6, 27, 48}, {28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32},
	{12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38},
	{18, 39, 60}, {40, 61, 19}, {62, 20, 41}, {-1, -1, 63},
}

// shaCrypt is SHA-256/512-crypt; full is the stored "$5$[rounds=N$]salt$hash".
func shaCrypt(newHash func() hash.Hash, magic string, perm [][3]int, pw []byte, full string) string {
	rest := full[len(magic):]
	rounds, custom := 5000, false
	if r, ok := strings.CutPrefix(rest, "rounds="); ok {
		num, after, found := strings.Cut(r, "$")
		if n, err := strconv.Atoi(num); err == nil && found {
			rounds, custom, rest = min(max(n, 1000), 999999999), true, after
		}
	}
	salt := []byte(saltOf(rest, 16))

	b := newHash()
	b.Write(pw)
	b.Write(salt)
	b.Write(pw)
	bSum := b.Sum(nil)
	size := len(bSum)

	a := newHash()
	a.Write(pw)
	a.Write(salt)
	i := len(pw)
	for ; i > size; i -= size {
		a.Write(bSum)
	}
	a.Write(bSum[:i])
	for i := len(pw); i > 0; i >>= 1 {
		if i&1 != 0 {
			a.Write(bSum)
		} else {
			a.Write(pw)
		}
	}
	aSum := a.Sum(nil)

	dp := newHash()
	for range pw {
		dp.Write(pw)
	}
	p := repeat(dp.Sum(nil), len(pw))

	ds := newHash()
	for range 16 + int(aSum[0]) {
		ds.Write(salt)
	}
	s := repeat(ds.Sum(nil), len(salt))

	for r := 0; r < rounds; r++ {
		c := newHash()
		if r&1 != 0 {
			c.Write(p)
		} else {
			c.Write(aSum)
		}
		if r%3 != 0 {
			c.Write(s)
		}
		if r%7 != 0 {
			c.Write(p)
		}
		if r&1 != 0 {
			c.Write(aSum)
		} else {
			c.Write(p)
		}
		aSum = c.Sum(aSum[:0])
	}

	out := []byte(magic)
	if custom {
		out = append(out, "rounds="+strconv.Itoa(rounds)+"$"...)
	}
	out = append(out, salt...)
	out = append(out, '$')
	byteAt := func(i int) uint32 {
		if i < 0 {
			return 0
		}
		return uint32(aSum[i])
	}
	for n, g := range perm {
		chars := 4
		if n == len(perm)-1 {
			chars = (size*8 - (len(perm)-1)*24 + 5) / 6 // 3 for SHA-256, 2 for SHA-512
		}
		out = b64(out, byteAt(g[0])<<16|byteAt(g[1])<<8|byteAt(g[2]), chars)
	}
	return string(out)
}

// repeat cycles d to exactly n bytes.
func repeat(d []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, d[:min(len(d), n-len(out))]...)
	}
	return out
}
