package suggest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
)

// Settings: what counts as "good enough", and discovery. Wide on purpose:
// the ladder takes the first way that clears them (the cheapest), not the
// fastest one.
type Settings struct {
	TTFBMaxMs  int     `json:"ttfb_max_ms"` // the first byte within this, always
	TTFBFactor float64 `json:"ttfb_factor"` // ...and within factor × the reference's + 500 ms, when known
	KbpsMin    int     `json:"kbps_min"`    // speed, when measurable: at least this
	KbpsShare  int     `json:"kbps_share"`  // ...and at least this % of the reference's, when known
	Reference  string  `json:"reference"`   // a well-reachable site measured directly
	Discover   bool    `json:"discover"`    // look for sites in the router's DNS questions
}

func DefaultSettings() Settings {
	return Settings{TTFBMaxMs: 5000, TTFBFactor: 3, KbpsMin: 2000, KbpsShare: 30, Reference: "ya.ru", Discover: true}
}

// ttfbSlack is added to the reference-relative limit: a site a continent away
// is not "slow" next to one across town.
const ttfbSlack = 500

// A short download's speed is bound by the round trip (TCP slow start), so a
// site across the world can't match one across town: the share of the
// reference's speed applies only to a site about as near as the reference —
// its TLS handshake within 2× the reference's plus nearSlack. The handshake,
// not the TCP connect: a tunnel's own network stack (xray's TUN) completes
// TCP locally, while TLS still goes all the way to the site.
const nearSlack = 20 // ms

func near(siteMs, refMs int) bool { return refMs > 0 && siteMs > 0 && siteMs <= 2*refMs+nearSlack }

// refEvery: the reference is measured again at most this often.
const refEvery = 10 * time.Minute

var ErrBadSettings = errors.New("недопустимые настройки")

func (s Settings) check() (Settings, error) {
	bad := func(what string) (Settings, error) { return s, fmt.Errorf("%w: %s", ErrBadSettings, what) }
	switch {
	case s.TTFBMaxMs < 500 || s.TTFBMaxMs > 30000:
		return bad("предел первого байта — от 500 до 30000 мс")
	case s.TTFBFactor < 1 || s.TTFBFactor > 20:
		return bad("множитель к эталону — от 1 до 20")
	case s.KbpsMin < 0 || s.KbpsMin > 1000000:
		return bad("наименьшая скорость — от 0 до 1000000 кбит/с")
	case s.KbpsShare < 0 || s.KbpsShare > 100:
		return bad("доля скорости эталона — от 0 до 100 %")
	}
	n := plane.Normalize([]string{s.Reference})
	if len(n) != 1 {
		return bad("эталон — имя сайта, например ya.ru")
	}
	s.Reference = n[0]
	return s, nil
}

// Reference is the well-reachable site's direct measurement: the yardstick
// for "slow" that moves with the line itself.
type Reference struct {
	Domain string `json:"domain"`
	OK     bool   `json:"ok"` // it opened directly when last measured
	TLSMs  int    `json:"tls_ms,omitempty"`
	TTFBMs int    `json:"ttfb_ms,omitempty"`
	Kbps   int    `json:"kbps,omitempty"`
	At     int64  `json:"at,omitempty"` // when measured (unix); 0 = not yet
}

// Settings now.
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

// SetSettings validates and keeps them. A new reference is measured with the
// next check.
func (s *Service) SetSettings(set Settings) (Settings, error) {
	set, err := set.check()
	if err != nil {
		return s.Settings(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if set.Reference != s.set.Reference {
		s.ref = Reference{Domain: set.Reference}
	}
	s.set = set
	s.saveLocked()
	slog.Info("suggest: settings", "ttfb_max_ms", set.TTFBMaxMs, "ttfb_factor", set.TTFBFactor,
		"kbps_min", set.KbpsMin, "kbps_share", set.KbpsShare, "reference", set.Reference, "discover", set.Discover)
	return set, nil
}

// bar: the settings and the reference, as one check is judged against them.
func (s *Service) bar(ctx context.Context) bar {
	s.mu.Lock()
	set, ref := s.set, s.ref
	s.mu.Unlock()
	now := s.now()
	if ref.Domain == set.Reference && ref.At != 0 && now.Sub(time.Unix(ref.At, 0)) < refEvery {
		return bar{set, ref}
	}
	ref = Reference{Domain: set.Reference, At: now.Unix()}
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if ip, why := s.P.Resolve(rctx, ref.Domain); why == "" {
		if m := s.P.Way(rctx, ref.Domain, ip, probe.ViaDirect); m.Clean() {
			ref.OK, ref.TLSMs, ref.TTFBMs, ref.Kbps = true, m.TLSMs, m.TTFBMs, m.Kbps
		}
	}
	if !ref.OK {
		slog.Warn("suggest: the reference doesn't open directly; judging by the fixed limits only", "reference", ref.Domain)
	}
	s.mu.Lock()
	if s.set.Reference == ref.Domain { // not changed meanwhile
		s.ref = ref
	}
	s.mu.Unlock()
	return bar{set, ref}
}

type bar struct {
	Settings
	ref Reference
}

// judge: is the way good enough to recommend; if not, why — for the user.
func (b bar) judge(m probe.Result) (bool, string) {
	switch {
	case m.Skipped != "":
		return false, "не проверялся: " + m.Skipped
	case m.Tries == 0:
		return false, "не измерен"
	case m.Frozen:
		return false, "соединение замирает после первых ~16 КБ"
	case m.Opened*5 < m.Tries*4:
		// one refusal among good tries is the 4-in-5 rule's to forgive; only
		// a way that never opened is "refuses"
		if m.Opened == 0 && (m.Signal != probe.SignalNone || m.Status >= 500) {
			return false, "сайт отвечает отказом или ошибкой"
		}
		return false, fmt.Sprintf("открылся %d из %d раз", m.Opened, m.Tries)
	}
	if m.TTFBMs > 0 {
		if m.TTFBMs > b.TTFBMaxMs {
			return false, fmt.Sprintf("первый байт через %d мс (предел %d мс)", m.TTFBMs, b.TTFBMaxMs)
		}
		if b.ref.OK && b.ref.TTFBMs > 0 {
			if lim := int(b.TTFBFactor*float64(b.ref.TTFBMs)) + ttfbSlack; m.TTFBMs > lim {
				return false, fmt.Sprintf("первый байт через %d мс — дольше %d мс (эталон %s: %d мс)", m.TTFBMs, lim, b.ref.Domain, b.ref.TTFBMs)
			}
		}
	}
	if m.Kbps > 0 { // speed only when it could be measured
		if m.Kbps < b.KbpsMin {
			return false, "скорость " + speed(m.Kbps) + " (нужно от " + speed(b.KbpsMin) + ")"
		}
		if b.ref.OK && b.ref.Kbps > 0 && near(m.TLSMs, b.ref.TLSMs) && m.Kbps*100 < b.ref.Kbps*b.KbpsShare {
			return false, fmt.Sprintf("скорость %s — меньше %d%% эталона (%s: %s)", speed(m.Kbps), b.KbpsShare, b.ref.Domain, speed(b.ref.Kbps))
		}
	}
	return true, ""
}

func speed(kbps int) string {
	if kbps >= 1000 {
		return strings.Replace(fmt.Sprintf("%.1f Мбит/с", float64(kbps)/1000), ".", ",", 1)
	}
	return fmt.Sprintf("%d кбит/с", kbps)
}
