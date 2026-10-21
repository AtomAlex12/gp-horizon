// Package suggest measures how a domain opens and recommends a way out the
// user accepts or dismisses. It advises only: Accept is the only way a
// suggestion reaches the plane, and the plane's own apply switch (PLANE_APPLY)
// still decides whether the router is touched. Rechecks and discovery only
// measure.
package suggest

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
	"nuxk.dev/horizon/core/internal/probe"
)

// Catalog is checked by Scan: popular services that are known to refuse
// Russian addresses. A list of names to look at, not a claim about any of them.
var Catalog = []string{
	"chatgpt.com", "openai.com", "claude.ai", "anthropic.com",
	"perplexity.ai", "gemini.google.com", "copilot.microsoft.com",
	"netflix.com", "spotify.com", "udemy.com",
}

const (
	maxQueue = 20
	maxItems = 300
	stateKey = "suggest"
	// a scan doesn't redo what was checked recently
	scanFresh = time.Hour
	// one domain's whole ladder, every way's samples included
	probeBudget = 5 * time.Minute
	// verifyAfter: the plane reconciles an accepted domain within this
	verifyAfter = 2 * time.Minute
	// accepted ways are measured again this often
	reverifyEvery = 24 * time.Hour
	// verifyWait: how soon to look again while the plane hasn't applied it
	verifyWait = 10 * time.Minute
	// the housekeeping tick: due verifications, discovery
	tickEvery = 30 * time.Second
)

type State string

const (
	StateChecking  State = "checking"
	StateReady     State = "ready"
	StateAccepted  State = "accepted"
	StateDismissed State = "dismissed"
)

// Where a domain came from.
const (
	SourceUser = "user" // typed in by the person
	SourceScan = "scan" // the built-in catalog
	SourceDNS  = "dns"  // asked often in the router's DNS (discovery)
)

// NextSearch: DPI with the known strategies didn't help — a strategy search
// (GP on the Raspberry Pi) is the next step.
const NextSearch = "search"

// Path is one way out as it was measured, in words for the user.
type Path struct {
	Via        string `json:"via"`
	Result     string `json:"result"`
	Ms         int    `json:"ms,omitempty"`
	Tries      int    `json:"tries"`
	Opened     int    `json:"opened"`
	TTFBMs     int    `json:"ttfb_ms,omitempty"` // 0 = unknown
	Kbps       int    `json:"kbps,omitempty"`    // 0 = not measurable
	Frozen     bool   `json:"frozen,omitempty"`
	Acceptable bool   `json:"acceptable"` // good enough by the settings
}

// Item is one domain's suggestion.
type Item struct {
	Domain    string `json:"domain"`
	State     State  `json:"state"`
	Source    string `json:"source"` // user | scan | dns
	Verdict   string `json:"verdict,omitempty"`
	Mode      string `json:"mode,omitempty"` // desync | warp | vless; empty = nothing to route
	Alt       string `json:"alt,omitempty"`  // another way that was measured and is good enough
	Confirmed bool   `json:"confirmed"`
	// Interim: the way to use while a strategy search runs (Next = search).
	Interim   string `json:"interim,omitempty"`
	Next      string `json:"next,omitempty"` // "search" | ""
	Reason    string `json:"reason,omitempty"`
	Paths     []Path `json:"paths,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"`
	// Hidden: found in DNS and nothing to tell (it opens); kept so it isn't
	// measured again for a week.
	Hidden bool `json:"hidden,omitempty"`

	// after acceptance
	Accepted   string `json:"accepted,omitempty"` // the mode the person put it in (Mode or Alt)
	AcceptedAt int64  `json:"accepted_at,omitempty"`
	Verified   bool   `json:"verified"`              // the accepted way was measured good enough
	VerifiedAt int64  `json:"verified_at,omitempty"` // last measured (unix); 0 = not yet
	VerifyDue  int64  `json:"verify_due,omitempty"`  // next measurement (unix)
	VerifyNote string `json:"verify_note,omitempty"` // what it found, for the user
}

// PathInfo is a way out and whether it can be probed right now.
type PathInfo struct {
	Via    string `json:"via"`
	Iface  string `json:"iface,omitempty"`
	Usable bool   `json:"usable"`
	Why    string `json:"why,omitempty"`
}

// Overview is GET /suggest.
type Overview struct {
	Items     []Item     `json:"items"`
	Paths     []PathInfo `json:"paths"`
	Busy      int        `json:"busy"` // domains queued or being probed
	Desync    bool       `json:"desync"`
	Settings  Settings   `json:"settings"`
	Reference Reference  `json:"reference"`
	// Discovering: discovery is on and sees the router's DNS questions
	// (protected DNS is on).
	Discovering bool `json:"discovering"`
}

// Skip is a domain that was not queued, and why.
type Skip struct {
	Domain string `json:"domain"`
	Reason string `json:"reason"`
}

// Queued is the answer to a check or scan request.
type Queued struct {
	Queued  []string `json:"queued"`
	Skipped []Skip   `json:"skipped"`
}

// The errors' words reach the person as they are (the screen shows the
// API's message).
var (
	ErrNotFound = errors.New("такого предложения нет: оно уже принято, скрыто или ещё проверяется")
	ErrNoMode   = errors.New("для этого сайта маршрут не поможет — принимать нечего")
	ErrBadMode  = errors.New("этот путь не предлагался: можно принять предложенный или проверенный запасной")
	ErrNoPlane  = errors.New("маршрутизация выключена (PLANE= в nuxk.conf)")
)

// Store persists the suggestions and settings (state/suggest.json).
type Store interface {
	LoadJSON(name string, v any) error
	SaveJSON(name string, v any) error
}

// Router is the plane as the service sees it. nil = plane off.
type Router interface {
	Covered(domain string) string
	ModeOf(domain string) plane.Mode // the mode of nuxk's list that carries it; "" = none
	AddAuto(mode plane.Mode, domain string) error
	Status() plane.Status
}

// Asked is protected DNS as discovery sees it.
type Asked interface {
	Watching() bool                                   // the router's DNS questions reach nuxk
	Frequent(since time.Duration, least int) []string // names asked at least that often, most asked first
}

type Service struct {
	P      Prober
	Store  Store
	Router Router
	Desync func() bool // nfqws2 is wired
	DNS    Asked       // discovery's source; nil = no discovery

	now func() time.Time

	queue     chan string
	mu        sync.Mutex
	items     map[string]*Item
	busy      int
	set       Settings
	ref       Reference
	accepting map[string]bool // an Accept is between its checks and the plane

	// the worker's own (Run's goroutine)
	lastDiscover time.Time
	passed       map[string]time.Time // names discovery looked at and passed over
}

type file struct {
	Settings Settings `json:"settings"`
	Items    []Item   `json:"items"`
}

// New loads the stored suggestions. A domain that was mid-probe when the agent
// stopped is dropped, not shown as stuck.
func New(p Prober, st Store, rt Router, desync func() bool) *Service {
	s := &Service{P: p, Store: st, Router: rt, Desync: desync, now: time.Now,
		queue: make(chan string, maxQueue), items: map[string]*Item{}, accepting: map[string]bool{}, passed: map[string]time.Time{}}
	f := file{Settings: DefaultSettings()} // what the file leaves out keeps its default
	if err := st.LoadJSON(stateKey, &f); err != nil {
		slog.Warn("suggest: stored suggestions unreadable, starting empty", "err", err)
	}
	if set, err := f.Settings.check(); err == nil {
		s.set = set
	} else {
		s.set = DefaultSettings()
	}
	s.ref = Reference{Domain: s.set.Reference}
	now := s.now()
	for _, it := range f.Items {
		if it.State == StateChecking || it.Domain == "" {
			continue
		}
		it := it
		if it.Source == "" {
			it.Source = SourceUser
		}
		if it.State == StateAccepted && it.Accepted == "" {
			// accepted before verification existed, maybe the alternative: the
			// check reads the mode off the plane's lists
			it.VerifyDue = now.Add(verifyAfter).Unix()
		}
		s.items[it.Domain] = &it
	}
	return s
}

// Run measures queued domains one at a time (a router is small) and, between
// them, verifies accepted ways and looks for new domains, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-s.queue:
			s.process(ctx, d)
		case <-t.C:
			s.verifyDue(ctx)
			s.discover()
		}
	}
}

func (s *Service) process(ctx context.Context, domain string) {
	defer func() {
		s.mu.Lock()
		s.busy--
		s.mu.Unlock()
	}()
	if why := s.covered(domain); why != "" { // listed while it waited
		s.mu.Lock()
		delete(s.items, domain)
		s.saveLocked()
		s.mu.Unlock()
		return
	}
	pctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	it := decide(pctx, s.P, s.bar(pctx), s.desync(), domain)
	it.State, it.CheckedAt = StateReady, s.now().Unix()
	if pctx.Err() != nil {
		// a ladder cut short proves nothing: say so, keep what was measured
		it.Verdict, it.Mode, it.Alt, it.Confirmed = string(probe.VerdictUnknown), "", "", false
		it.Reason = "Проверка не уложилась в 5 минут — сайт отвечает слишком медленно. Проверьте ещё раз позже."
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.items[domain]
	if ctx.Err() != nil { // the agent is stopping: no verdict, and in-flight checks aren't kept
		delete(s.items, domain)
		return
	}
	if old == nil || old.State != StateChecking {
		return // dismissed or removed while probing
	}
	it.Source = old.Source
	if it.Source == SourceDNS && quiet(it) {
		it.Hidden, it.Paths = true, nil // kept small: only "don't measure again"
	}
	s.items[domain] = &it
	s.saveLocked()
	slog.Info("suggest: checked", "domain", domain, "source", it.Source, "verdict", it.Verdict, "mode", it.Mode, "alt", it.Alt, "confirmed", it.Confirmed)
}

// quiet: nothing in it for the person — it opens, or it is slow or unclear
// with nothing to route. A domain they didn't ask about stays out of sight
// then. A block with no way around stays in view: it says what to install.
func quiet(it Item) bool {
	return it.Verdict == string(probe.VerdictOK) ||
		it.Mode == "" && (it.Verdict == string(probe.VerdictUnknown) || it.Verdict == string(probe.VerdictSlow))
}

func (s *Service) desync() bool { return s.Desync != nil && s.Desync() }

func (s *Service) covered(domain string) string {
	if s.Router == nil {
		return ""
	}
	return s.Router.Covered(domain)
}

// Check queues the given domains for the person. force=false leaves alone what
// was checked recently or already decided; force=true redoes it.
func (s *Service) Check(domains []string, force bool) Queued {
	return s.enqueue(domains, force, SourceUser)
}

// Scan checks the built-in catalog.
func (s *Service) Scan() Queued { return s.enqueue(Catalog, false, SourceScan) }

func (s *Service) enqueue(domains []string, force bool, source string) Queued {
	out := Queued{Queued: []string{}, Skipped: []Skip{}}
	seen := map[string]bool{}
	for _, raw := range domains {
		n := plane.Normalize([]string{raw})
		if len(n) != 1 {
			out.Skipped = append(out.Skipped, Skip{raw, "не похоже на имя сайта"})
			continue
		}
		d := n[0]
		if seen[d] {
			continue
		}
		seen[d] = true
		if net.ParseIP(d) != nil {
			out.Skipped = append(out.Skipped, Skip{d, "это адрес, а не имя сайта — проверяются имена"})
			continue
		}
		if why := s.covered(d); why != "" {
			out.Skipped = append(out.Skipped, Skip{d, "уже маршрутизируется: " + why})
			continue
		}
		s.mu.Lock()
		old := s.items[d]
		switch {
		case old != nil && old.State == StateChecking:
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, "уже проверяется"})
			continue
		case s.accepting[d]:
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, "предложение как раз принимается"})
			continue
		case old != nil && !force && (old.State == StateDismissed || old.State == StateAccepted):
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, map[State]string{StateDismissed: "вы отклонили это предложение", StateAccepted: "предложение уже принято"}[old.State]})
			continue
		case old != nil && !force && s.now().Sub(time.Unix(old.CheckedAt, 0)) < scanFresh:
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, "проверен недавно"})
			continue
		}
		select {
		case s.queue <- d:
			s.items[d] = &Item{Domain: d, State: StateChecking, Source: source}
			s.busy++
			s.mu.Unlock()
			out.Queued = append(out.Queued, d)
		default:
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, "очередь заполнена, повторите позже"})
		}
	}
	return out
}

// Overview is the current picture.
func (s *Service) Overview() Overview {
	s.mu.Lock()
	items := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		items = append(items, *it)
	}
	busy, set, ref := s.busy, s.set, s.ref
	s.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { // checking first, then the freshest
		if (items[i].State == StateChecking) != (items[j].State == StateChecking) {
			return items[i].State == StateChecking
		}
		if items[i].CheckedAt != items[j].CheckedAt {
			return items[i].CheckedAt > items[j].CheckedAt
		}
		return items[i].Domain < items[j].Domain
	})
	var paths []PathInfo
	if s.P != nil {
		for _, p := range s.P.Paths() {
			paths = append(paths, PathInfo{Via: string(p.Via), Iface: p.Iface, Usable: p.Usable, Why: p.Why})
		}
	}
	return Overview{Items: items, Paths: paths, Busy: busy, Desync: s.desync(), Settings: set, Reference: ref,
		Discovering: set.Discover && s.DNS != nil && s.DNS.Watching()}
}

// Accept puts the domain into the plane's «Подбор» list for mode ("" = the
// suggested one; the verified alternative is also allowed). The way is
// measured again once the plane has had time to apply it.
func (s *Service) Accept(domain, mode string) error {
	s.mu.Lock()
	it, ok := s.items[domain]
	if !ok || it.State != StateReady || s.accepting[domain] {
		s.mu.Unlock()
		return ErrNotFound
	}
	if mode == "" {
		mode = it.Mode
	}
	switch {
	case mode == "":
		s.mu.Unlock()
		return ErrNoMode
	case mode != it.Mode && mode != it.Alt:
		s.mu.Unlock()
		return ErrBadMode
	case s.Router == nil:
		s.mu.Unlock()
		return ErrNoPlane
	}
	// held until the item says accepted: a second Accept, a dismiss or a
	// recheck can't slip in while the plane is being written
	s.accepting[domain] = true
	s.mu.Unlock()
	err := s.Router.AddAuto(plane.Mode(mode), domain)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accepting, domain)
	if err != nil {
		return err
	}
	if it := s.items[domain]; it != nil {
		now := s.now()
		it.State, it.Hidden = StateAccepted, false
		it.Accepted, it.AcceptedAt = mode, now.Unix()
		it.Verified, it.VerifiedAt, it.VerifyNote = false, 0, ""
		it.VerifyDue = now.Add(verifyAfter).Unix()
		s.saveLocked()
	}
	slog.Info("suggest: accepted", "domain", domain, "mode", mode)
	return nil
}

// Dismiss hides a suggestion; a later scan won't bring it back, and it is
// never measured again on its own.
func (s *Service) Dismiss(domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[domain]
	if !ok || it.State == StateChecking || s.accepting[domain] {
		return ErrNotFound
	}
	it.State, it.VerifyDue = StateDismissed, 0
	s.saveLocked()
	return nil
}

// keepRank: what goes first when there are too many — quiet finds from DNS,
// then dismissed, then the rest; accepted ones last.
func keepRank(it *Item) int {
	switch {
	case it.State == StateAccepted:
		return 0
	case it.Hidden:
		return 3
	case it.State == StateDismissed:
		return 2
	}
	return 1
}

// saveLocked persists everything but in-flight checks; past maxItems the
// least needed go (see keepRank), oldest first. Caller holds s.mu.
func (s *Service) saveLocked() {
	var keep []*Item
	for _, it := range s.items {
		if it.State != StateChecking {
			keep = append(keep, it)
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		if a, b := keepRank(keep[i]), keepRank(keep[j]); a != b {
			return a < b
		}
		return keep[i].CheckedAt > keep[j].CheckedAt
	})
	if len(keep) > maxItems {
		for _, it := range keep[maxItems:] {
			delete(s.items, it.Domain)
		}
		keep = keep[:maxItems]
	}
	f := file{Settings: s.set, Items: make([]Item, 0, len(keep))}
	for _, it := range keep {
		f.Items = append(f.Items, *it)
	}
	if err := s.Store.SaveJSON(stateKey, f); err != nil {
		slog.Warn("suggest: save failed", "err", err)
	}
}
