package main

// A copy of nuxk-core/internal/update/version.go (a module of its own here).

import (
	"regexp"
	"strconv"
	"strings"
)

var releaseRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// Valid: X.Y.Z or X.Y.Z-pre (a "v" in front is allowed).
func Valid(v string) bool { return releaseRe.MatchString(strings.TrimPrefix(v, "v")) }

// Newer: a is a later version than b. X.Y.Z by number; a release is later
// than its pre-releases (0.4.0 > 0.4.0-beta.2), pre-releases compare part by
// part (beta.10 > beta.9).
func Newer(a, b string) bool { return compare(a, b) > 0 }

func compare(a, b string) int {
	ac, ap, _ := strings.Cut(strings.TrimPrefix(a, "v"), "-")
	bc, bp, _ := strings.Cut(strings.TrimPrefix(b, "v"), "-")
	an, bn := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < 3; i++ {
		if c := cmpInt(verPart(an, i), verPart(bn, i)); c != 0 {
			return c
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	as, bs := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, xerr := strconv.Atoi(as[i])
		y, yerr := strconv.Atoi(bs[i])
		var c int
		switch {
		case xerr == nil && yerr == nil:
			c = cmpInt(x, y)
		case xerr == nil:
			c = -1 // numbers before words
		case yerr == nil:
			c = 1
		default:
			c = strings.Compare(as[i], bs[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmpInt(len(as), len(bs))
}

func verPart(p []string, i int) int {
	if i >= len(p) {
		return 0
	}
	n, _ := strconv.Atoi(p[i])
	return n
}

func cmpInt(x, y int) int {
	switch {
	case x > y:
		return 1
	case x < y:
		return -1
	}
	return 0
}
