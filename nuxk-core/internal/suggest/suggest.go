// Package suggest turns probe results into route suggestions the user accepts
// or dismisses. It never changes routing on its own: Accept is the only way a
// suggestion reaches the plane, and the plane's own apply switch (PLANE_APPLY)
// still decides whether the router is touched.
package suggest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	"perplexity.ai", "netflix.com", "spotify.com",
}

const (
	maxQueue = 20
	maxItems = 200
	stateKey = "suggest"
	// a scan doesn't redo what was checked recently
	scanFresh = time.Hour
)

type State string

const (
	StateChecking  State = "checking"
	StateReady     State = "ready"
	StateAccepted  State = "accepted"
	StateDismissed State = "dismissed"
)

// Path is one way out as it was probed, in words for the user.
type Path struct {
	Via    string `json:"via"`
	Result string `json:"result"`
	Ms     int    `json:"ms,omitempty"`
}

// Item is one domain's suggestion.
type Item struct {
	Domain    string `json:"domain"`
	State     State  `json:"state"`
	Verdict   string `json:"verdict,omitempty"`
	Mode      string `json:"mode,omitempty"` // desync | warp | vless; empty = nothing to route
	Alt       string `json:"alt,omitempty"`  // another way that was tried and works
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason,omitempty"`
	Paths     []Path `json:"paths,omitempty"`
	CheckedAt int64  `json:"checked_at,omitempty"`
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
	Items  []Item     `json:"items"`
	Paths  []PathInfo `json:"paths"`
	Busy   int        `json:"busy"` // domains queued or being probed
	Desync bool       `json:"desync"`
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

var (
	ErrNotFound = errors.New("no such suggestion")
	ErrNoMode   = errors.New("nothing to route for this domain")
	ErrBadMode  = errors.New("mode is neither the suggested one nor the verified alternative")
	ErrNoPlane  = errors.New("routing plane is off")
)

// Store persists the suggestions (state/suggest.json).
type Store interface {
	LoadJSON(name string, v any) error
	SaveJSON(name string, v any) error
}

// Router is the plane as the service sees it. nil = plane off.
type Router interface {
	Covered(domain string) string
	AddAuto(mode plane.Mode, domain string) error
}

type Service struct {
	P      *probe.Prober
	Store  Store
	Router Router
	Desync func() bool // nfqws2 is wired

	queue chan string
	mu    sync.Mutex
	items map[string]*Item
	busy  int
}

type file struct {
	Items []Item `json:"items"`
}

// New loads the stored suggestions. A domain that was mid-probe when the agent
// stopped is dropped, not shown as stuck.
func New(p *probe.Prober, st Store, rt Router, desync func() bool) *Service {
	s := &Service{P: p, Store: st, Router: rt, Desync: desync, queue: make(chan string, maxQueue), items: map[string]*Item{}}
	var f file
	if err := st.LoadJSON(stateKey, &f); err != nil {
		slog.Warn("suggest: stored suggestions unreadable, starting empty", "err", err)
	}
	for _, it := range f.Items {
		if it.State == StateChecking || it.Domain == "" {
			continue
		}
		it := it
		s.items[it.Domain] = &it
	}
	return s
}

// Run probes queued domains one at a time (a router is small) until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-s.queue:
			s.process(ctx, d)
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
	pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	rep := s.P.Check(pctx, domain)
	sug := probe.Classify(rep, probe.Options{Desync: s.desync()})

	it := Item{
		Domain: domain, State: StateReady, Verdict: string(sug.Verdict), Mode: sug.Mode, Alt: sug.Alt,
		Confirmed: sug.Confirmed, Reason: sug.Reason, Paths: describe(rep), CheckedAt: time.Now().Unix(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.items[domain]; old == nil || old.State != StateChecking {
		return // dismissed or removed while probing
	}
	s.items[domain] = &it
	s.saveLocked()
	slog.Info("suggest: checked", "domain", domain, "verdict", it.Verdict, "mode", it.Mode, "confirmed", it.Confirmed)
}

func (s *Service) desync() bool { return s.Desync != nil && s.Desync() }

func (s *Service) covered(domain string) string {
	if s.Router == nil {
		return ""
	}
	return s.Router.Covered(domain)
}

// Check queues the given domains. force=false (a scan) leaves alone what was
// checked recently or already decided; force=true (the user asked) redoes it.
func (s *Service) Check(domains []string, force bool) Queued {
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
		case old != nil && !force && (old.State == StateDismissed || old.State == StateAccepted):
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, map[State]string{StateDismissed: "вы отклонили это предложение", StateAccepted: "предложение уже принято"}[old.State]})
			continue
		case old != nil && !force && time.Since(time.Unix(old.CheckedAt, 0)) < scanFresh:
			s.mu.Unlock()
			out.Skipped = append(out.Skipped, Skip{d, "проверен недавно"})
			continue
		}
		select {
		case s.queue <- d:
			s.items[d] = &Item{Domain: d, State: StateChecking}
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

// Scan checks the built-in catalog.
func (s *Service) Scan() Queued { return s.Check(Catalog, false) }

// Overview is the current picture.
func (s *Service) Overview() Overview {
	s.mu.Lock()
	items := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		items = append(items, *it)
	}
	busy := s.busy
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
	for _, p := range s.P.Paths() {
		paths = append(paths, PathInfo{Via: string(p.Via), Iface: p.Iface, Usable: p.Usable, Why: p.Why})
	}
	return Overview{Items: items, Paths: paths, Busy: busy, Desync: s.desync()}
}

// Accept puts the domain into the plane's «Подбор» list for mode ("" = the
// suggested one; the verified alternative is also allowed).
func (s *Service) Accept(domain, mode string) error {
	s.mu.Lock()
	it, ok := s.items[domain]
	if !ok || it.State != StateReady {
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
	}
	s.mu.Unlock()
	if s.Router == nil {
		return ErrNoPlane
	}
	if err := s.Router.AddAuto(plane.Mode(mode), domain); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if it := s.items[domain]; it != nil {
		it.State = StateAccepted
		s.saveLocked()
	}
	slog.Info("suggest: accepted", "domain", domain, "mode", mode)
	return nil
}

// Dismiss hides a suggestion; a later scan won't bring it back.
func (s *Service) Dismiss(domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[domain]
	if !ok || it.State == StateChecking {
		return ErrNotFound
	}
	it.State = StateDismissed
	s.saveLocked()
	return nil
}

// saveLocked persists everything but in-flight checks, oldest decided items
// dropped past maxItems. Caller holds s.mu.
func (s *Service) saveLocked() {
	var keep []Item
	for _, it := range s.items {
		if it.State != StateChecking {
			keep = append(keep, *it)
		}
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].CheckedAt > keep[j].CheckedAt })
	if len(keep) > maxItems {
		for _, it := range keep[maxItems:] {
			delete(s.items, it.Domain)
		}
		keep = keep[:maxItems]
	}
	if err := s.Store.SaveJSON(stateKey, file{Items: keep}); err != nil {
		slog.Warn("suggest: save failed", "err", err)
	}
}

// describe renders what each probed path did, for the details row.
func describe(r probe.Report) []Path {
	if r.DNS != "" {
		return []Path{{Via: "dns", Result: map[string]string{"bogus": "подменённый ответ", "nxdomain": "имя не найдено", "timeout": "нет ответа"}[r.DNS]}}
	}
	paths := []Path{describeOne(r.Direct)}
	for _, t := range r.Tunnels {
		paths = append(paths, describeOne(t))
	}
	return paths
}

var failText = map[probe.Fail]string{
	probe.FailTimeout: "тишина (таймаут)", probe.FailReset: "обрыв соединения (RST)", probe.FailEOF: "соединение закрыто",
	probe.FailRefused: "отказ в соединении", probe.FailUnreachable: "нет маршрута", probe.FailAlert: "сервер отверг TLS", probe.FailOther: "ошибка",
}

func describeOne(r probe.Result) Path {
	p := Path{Via: string(r.Via), Ms: r.Ms}
	switch {
	case r.Skipped != "":
		p.Result, p.Ms = "не проверялся: "+r.Skipped, 0
	case !r.TCP:
		p.Result = "не подключается: " + failText[r.TCPFail]
	case !r.TLS:
		p.Result = "TLS: " + failText[r.TLSFail]
		if r.Neutral != nil {
			if *r.Neutral {
				p.Result += "; на другое имя тот же адрес отвечает"
			} else {
				p.Result += "; на другое имя тоже молчит"
			}
		}
	case r.Status == 0:
		p.Result = "HTTP: " + failText[r.HTTPFail]
	case r.Signal == probe.SignalExplicit:
		p.Result = fmt.Sprintf("отказ по стране (HTTP %d)", r.Status)
	case r.Signal == probe.SignalChallenge:
		p.Result = fmt.Sprintf("проверка «Just a moment» (HTTP %d)", r.Status)
	case r.Signal == probe.SignalDenied:
		p.Result = "отказ (HTTP 403)"
	case r.Status >= 500:
		p.Result = fmt.Sprintf("ошибка сервера (HTTP %d)", r.Status)
	default:
		p.Result = fmt.Sprintf("открывается (HTTP %d)", r.Status)
	}
	return p
}
