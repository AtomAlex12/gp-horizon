package suggest

import (
	"log/slog"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
)

// Discovery: the sites the home actually uses, from the router's DNS
// questions — seen only while protected DNS is on (the router asks nuxk
// then). One new site at most every few minutes, its base domain, measured
// like any other; one that opens fine is kept quietly so it isn't measured
// again for a week. Measuring only: what it finds is a suggestion like any
// other.

const (
	discoverEvery = 3 * time.Minute    // at most one new domain per tick: ≤ 20 an hour
	discoverMin   = 3                  // asked at least this often within the hour
	discoverLook  = 50                 // most asked names looked at per tick
	discoverKeep  = 7 * 24 * time.Hour // a quiet find isn't measured again for this long
	passedFor     = time.Hour          // a name passed over isn't looked at again for this long
)

// discover queues at most one new domain from the router's DNS questions.
func (s *Service) discover() {
	now := s.now()
	if s.DNS == nil || now.Sub(s.lastDiscover) < discoverEvery {
		return
	}
	s.mu.Lock()
	on, busy := s.set.Discover, s.busy
	s.mu.Unlock()
	if !on || busy > 0 || !s.DNS.Watching() { // the person's own checks go first
		return
	}
	s.lastDiscover = now
	s.forget(now)
	names := s.DNS.Frequent(time.Hour, discoverMin)
	if len(names) > discoverLook {
		names = names[:discoverLook]
	}
	for _, name := range names {
		base := BaseDomain(name)
		if base == "" {
			continue
		}
		if _, ok := s.passed[base]; ok {
			continue
		}
		s.passed[base] = now
		if s.known(base) || s.covered(name) != "" || s.covered(base) != "" {
			continue
		}
		if q := s.enqueue([]string{base}, false, SourceDNS); len(q.Queued) > 0 {
			slog.Debug("suggest: found in DNS", "domain", base, "asked", name)
			return
		}
	}
}

// forget drops passed-over names after a while and quiet finds after a week.
func (s *Service) forget(now time.Time) {
	for name, at := range s.passed {
		if now.Sub(at) > passedFor {
			delete(s.passed, name)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := false
	for d, it := range s.items {
		if it.Hidden && it.State == StateReady && now.Sub(time.Unix(it.CheckedAt, 0)) > discoverKeep {
			delete(s.items, d)
			dropped = true
		}
	}
	if dropped {
		s.saveLocked()
	}
}

func (s *Service) known(domain string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.items[domain]
	return ok
}

// Two-label suffixes that are registries, not sites: a base domain under
// them keeps three labels. A short list of what Russian homes meet, not the
// Public Suffix List.
var suffix2 = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
	"com.ru": true, "org.ru": true, "net.ru": true, "msk.ru": true, "spb.ru": true,
	"com.ua": true, "org.ua": true, "net.ua": true, "kiev.ua": true,
	"com.by": true, "com.kz": true, "com.tr": true, "com.br": true, "com.au": true,
	"com.cn": true, "co.jp": true, "co.kr": true, "co.in": true, "co.il": true, "co.za": true,
}

// Not sites: CDN, hosting and infrastructure names, where the base says
// nothing about a site — and routing it would move half the internet.
var notSites = map[string]bool{
	"akamai.net": true, "akamaiedge.net": true, "akamaihd.net": true, "akamaized.net": true,
	"edgekey.net": true, "edgesuite.net": true, "cloudfront.net": true, "amazonaws.com": true,
	"fastly.net": true, "fastlylb.net": true, "cloudflare.net": true, "azureedge.net": true,
	"azurefd.net": true, "trafficmanager.net": true, "googleusercontent.com": true,
	"1e100.net": true, "gvt1.com": true, "gvt2.com": true, "cdn77.org": true,
}

// Local names: never on the internet.
var localTLD = map[string]bool{"arpa": true, "local": true, "lan": true, "home": true, "internal": true, "localdomain": true, "localhost": true, "invalid": true, "test": true}

// BaseDomain reduces a name to the site it belongs to: the last two labels,
// three under a registry suffix (bbc.co.uk). "" = not a site to measure.
func BaseDomain(name string) string {
	n := plane.Normalize([]string{name})
	if len(n) != 1 {
		return ""
	}
	labels := strings.Split(n[0], ".")
	tld := labels[len(labels)-1]
	if localTLD[tld] || strings.Trim(tld, "0123456789") == "" { // a local name or an address
		return ""
	}
	keep := 2
	if suffix2[strings.Join(labels[len(labels)-2:], ".")] {
		keep = 3
	}
	if len(labels) < keep {
		return "" // the registry suffix itself
	}
	base := strings.Join(labels[len(labels)-keep:], ".")
	if notSites[base] {
		return ""
	}
	return base
}
