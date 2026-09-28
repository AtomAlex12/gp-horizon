package dns

import (
	"context"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Canaries: sites blocked by the registry, whose names providers answer with
// stubs — the same kind a DNS-substitution test checks. The person's own list
// domains are checked next to them.
var Canaries = []string{"rutor.info", "flibusta.is", "rezka.ag"}

// PlainDNS: an ordinary resolver asked the ordinary way (UDP/53) — the kind
// of question providers intercept on the way.
const PlainDNS = "8.8.8.8:53"

const (
	VerdictOK      = "ok"      // an address the resolver behind the tunnel gives too
	VerdictSpoofed = "spoofed" // a stub, a made-up «no such site», a private address
	VerdictDiffers = "differs" // other addresses; CDNs do that — not proven either way
	VerdictError   = "error"   // no answer
)

// CheckItem: one domain, three answers.
type CheckItem struct {
	Domain        string   `json:"domain"`
	Truth         []string `json:"truth"`          // through the tunnel (or DoH): the reference
	Router        []string `json:"router"`         // what devices get from the router
	RouterVerdict string   `json:"router_verdict"` // ok | spoofed | differs | error
	RouterNote    string   `json:"router_note,omitempty"`
	Plain         []string `json:"plain"`         // 8.8.8.8 asked plainly, over the provider's network
	PlainVerdict  string   `json:"plain_verdict"` // ok | spoofed | differs | error
	PlainNote     string   `json:"plain_note,omitempty"`
}

// Check is POST /api/v1/dns/check.
type Check struct {
	At     int64       `json:"at"`
	Path   string      `json:"path"` // how the reference answers came: vless | warp | direct
	Items  []CheckItem `json:"items"`
	Router int         `json:"router_spoofed"` // domains the router answered with a stub
	Plain  int         `json:"plain_spoofed"`  // domains plain DNS answered with a stub: the provider intercepts
}

type answer struct {
	a   Answer
	err error
}

// Check compares, for each domain: the router's answer, a plain resolver's
// over the provider's network, and DoH through the tunnel.
func (s *Service) Check(ctx context.Context, domains []string) Check {
	if len(domains) == 0 {
		domains = s.checkDomains()
	}
	res := Check{At: time.Now().Unix(), Items: make([]CheckItem, len(domains))}
	truth := make([]answer, len(domains))
	router := make([]answer, len(domains))
	plain := make([]answer, len(domains))
	var wg sync.WaitGroup
	lim := make(chan struct{}, 6)
	var pmu sync.Mutex
	for i, d := range domains {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lim <- struct{}{}
			defer func() { <-lim }()
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if q, err := Query(randID(), d, TypeA); err != nil {
				truth[i].err = err
			} else if resp, path, err := s.resolve(cctx, q); err != nil {
				truth[i].err = err
			} else {
				truth[i].a, truth[i].err = Parse(resp)
				pmu.Lock()
				res.Path = path
				pmu.Unlock()
			}
			router[i].a, router[i].err = s.routerLookup(cctx, d)
			plain[i].a, plain[i].err = udpLookup(cctx, s.o.PlainDNS, d)
		}()
	}
	wg.Wait()
	rv := verdicts(truth, router)
	pv := verdicts(truth, plain)
	for i, d := range domains {
		it := CheckItem{
			Domain: d, Truth: addrs(truth[i]), Router: addrs(router[i]), Plain: addrs(plain[i]),
			RouterVerdict: rv[i].v, RouterNote: rv[i].note, PlainVerdict: pv[i].v, PlainNote: pv[i].note,
		}
		if it.RouterVerdict == VerdictSpoofed {
			res.Router++
		}
		if it.PlainVerdict == VerdictSpoofed {
			res.Plain++
		}
		res.Items[i] = it
	}
	return res
}

// checkDomains: the canaries, then the person's list domains (up to 12).
func (s *Service) checkDomains() []string {
	out := append([]string{}, Canaries...)
	if s.o.Domains == nil {
		return out
	}
	seen := map[string]bool{}
	for _, d := range out {
		seen[d] = true
	}
	for _, d := range s.o.Domains() {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] || strings.ContainsAny(d, "*/ ") {
			continue
		}
		seen[d] = true
		out = append(out, d)
		if len(out) >= len(Canaries)+12 {
			break
		}
	}
	return out
}

func addrs(a answer) []string {
	out := []string{}
	if a.err != nil {
		return out
	}
	for _, x := range a.a.Addrs {
		if x.Is4() {
			out = append(out, x.String())
		}
	}
	return out
}

type verdict struct{ v, note string }

// verdicts: each answer against the reference. A stub shows as the same
// addresses for different sites, a private address, or «no such site» where
// the site exists; other addresses alone prove nothing (CDNs answer by place).
func verdicts(truth, got []answer) []verdict {
	out := make([]verdict, len(got))
	// the same set of addresses for sites that really live apart: a stub
	sets := map[string][]int{}
	for i, g := range got {
		if g.err == nil && len(addrs(g)) > 0 {
			k := key(addrs(g))
			sets[k] = append(sets[k], i)
		}
	}
	for i, g := range got {
		t := truth[i]
		switch {
		case g.err != nil:
			out[i] = verdict{VerdictError, "нет ответа: " + g.err.Error()}
		case t.err != nil:
			out[i] = verdict{VerdictError, "не с чем сравнить: через туннель нет ответа"}
		case len(addrs(t)) > 0 && (g.a.Rcode == RcodeNXDomain || len(addrs(g)) == 0):
			out[i] = verdict{VerdictSpoofed, "говорит, что сайта нет, а он есть"}
		case privateOnly(addrs(g)) && !privateOnly(addrs(t)):
			out[i] = verdict{VerdictSpoofed, "заглушка: частный адрес"}
		case overlap(addrs(g), addrs(t)):
			out[i] = verdict{VerdictOK, ""}
		case stubShared(sets[key(addrs(g))], i, truth):
			out[i] = verdict{VerdictSpoofed, "один и тот же адрес у разных сайтов — заглушка"}
		case len(addrs(t)) == 0 && len(addrs(g)) == 0:
			out[i] = verdict{VerdictOK, "адресов нет ни там, ни там"}
		default:
			out[i] = verdict{VerdictDiffers, "адреса другие — у CDN так бывает, подмена не доказана"}
		}
	}
	return out
}

// stubShared: others got this very answer while the reference answers for
// them don't overlap — one address standing in for unrelated sites.
func stubShared(group []int, i int, truth []answer) bool {
	for _, j := range group {
		if j != i && !overlap(addrs(truth[i]), addrs(truth[j])) && len(addrs(truth[j])) > 0 {
			return true
		}
	}
	return false
}

func key(a []string) string {
	b := slices.Clone(a)
	sort.Strings(b)
	return strings.Join(b, ",")
}

func overlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func privateOnly(a []string) bool {
	if len(a) == 0 {
		return false
	}
	for _, s := range a {
		ip, err := netip.ParseAddr(s)
		if err != nil {
			return false
		}
		if !(ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
			netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("0.0.0.0/8").Contains(ip)) {
			return false
		}
	}
	return true
}
