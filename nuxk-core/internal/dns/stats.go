package dns

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The statistics behind the panel's DNS charts: every answer the router's DNS
// proxy got from nuxk — from the built-in forwarder as it answers, or from
// SmartDNS's audit log (smartdns.go) — counted by minute for the last hour,
// with the most asked names and the last questions. Bounded memory, nothing
// forked: a few counters per answer.

// Where an answer came from.
const (
	SrcCache    = "cache"    // kept: no question went out
	SrcUpstream = "upstream" // asked a DoH server
	SrcStale    = "stale"    // an expired answer: no server answered in time
	SrcFailed   = "failed"   // no answer
)

const (
	statMinutes   = 60  // the hour on the router; the controller keeps longer
	statSamples   = 256 // upstream times kept per minute for the 95th percentile
	statDomains   = 200 // names counted per minute; later new ones aren't
	statRecent    = 200 // the last questions
	statTopShown  = 10
	statTypeShown = 6
)

// Minute: one minute's answers. T is its start (unix).
type Minute struct {
	T        int64   `json:"t"`
	Cache    int     `json:"cache"`
	Upstream int     `json:"upstream"`
	Stale    int     `json:"stale"`
	Failed   int     `json:"failed"`
	AvgMs    float64 `json:"avg_ms,omitempty"` // upstream answers only
	P95Ms    float64 `json:"p95_ms,omitempty"`
}

// Count: a name (or a query type) and how often it was asked.
type Count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// LogEntry is one question and its answer (GET /api/v1/dns/log).
type LogEntry struct {
	At     int64  `json:"at"`
	Domain string `json:"domain"`
	Type   string `json:"type"`
	Answer string `json:"answer,omitempty"` // the first addresses, or soa / empty
	Source string `json:"source"`           // cache | upstream | stale | failed
	Ms     int    `json:"ms"`
}

// Stats is GET /api/v1/dns/stats.
type Stats struct {
	Engine   string   `json:"engine"`
	Since    int64    `json:"since,omitempty"` // counting since (unix): the agent's or SmartDNS's start
	Queries  int      `json:"queries"`         // in the last hour
	Cache    int      `json:"cache"`
	Upstream int      `json:"upstream"`
	Stale    int      `json:"stale"`
	Failed   int      `json:"failed"`
	AvgMs    float64  `json:"avg_ms,omitempty"` // upstream answers in the last hour
	Minutes  []Minute `json:"minutes"`          // oldest first, the current one last
	Top      []Count  `json:"top"`              // the most asked names in the hour
	Types    []Count  `json:"types"`
	// what this engine can't tell apart, said plainly in the panel
	StaleKnown bool   `json:"stale_known"` // stale answers are counted separately
	Note       string `json:"note,omitempty"`
}

type bucket struct {
	t                        int64
	cache, up, stale, failed int
	msSum                    int
	ms                       []uint16
	domains                  map[string]int
	types                    map[string]int
}

type recorder struct {
	mu      sync.Mutex
	clock   func() time.Time
	since   int64
	buckets [statMinutes]bucket
	recent  []LogEntry // ring
	next    int
}

func newRecorder(clock func() time.Time) *recorder {
	return &recorder{clock: clock, since: clock().Unix()}
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets = [statMinutes]bucket{}
	r.recent, r.next = nil, 0
	r.since = r.clock().Unix()
}

// add counts one answer; at is when it was given.
func (r *recorder) add(q LogEntry) {
	m := q.At - q.At%60
	r.mu.Lock()
	defer r.mu.Unlock()
	b := &r.buckets[(m/60)%statMinutes]
	if b.t != m {
		*b = bucket{t: m}
	}
	switch q.Source {
	case SrcCache:
		b.cache++
	case SrcStale:
		b.stale++
	case SrcFailed:
		b.failed++
	default:
		b.up++
		b.msSum += q.Ms
		if len(b.ms) < statSamples {
			b.ms = append(b.ms, uint16(min(q.Ms, 65535)))
		}
	}
	if q.Domain != "" {
		if b.domains == nil {
			b.domains = map[string]int{}
		}
		if _, ok := b.domains[q.Domain]; ok || len(b.domains) < statDomains {
			b.domains[q.Domain]++
		}
	}
	if q.Type != "" {
		if b.types == nil {
			b.types = map[string]int{}
		}
		b.types[q.Type]++
	}
	if len(r.recent) < statRecent {
		r.recent = append(r.recent, q)
	} else {
		r.recent[r.next] = q
	}
	r.next = (r.next + 1) % statRecent
}

// stats: the last hour, minute by minute.
func (r *recorder) stats() Stats {
	now := r.clock().Unix()
	cur := now - now%60
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Stats{Since: r.since, Minutes: make([]Minute, 0, statMinutes)}
	doms := map[string]int{}
	types := map[string]int{}
	msSum, ups := 0, 0
	for i := statMinutes - 1; i >= 0; i-- {
		t := cur - int64(i)*60
		m := Minute{T: t}
		if b := &r.buckets[(t/60)%statMinutes]; b.t == t {
			m.Cache, m.Upstream, m.Stale, m.Failed = b.cache, b.up, b.stale, b.failed
			if b.up > 0 {
				m.AvgMs = round1(float64(b.msSum) / float64(b.up))
				m.P95Ms = p95(b.ms)
			}
			msSum += b.msSum
			ups += b.up
			for d, n := range b.domains {
				doms[d] += n
			}
			for k, n := range b.types {
				types[k] += n
			}
		}
		st.Cache += m.Cache
		st.Upstream += m.Upstream
		st.Stale += m.Stale
		st.Failed += m.Failed
		st.Minutes = append(st.Minutes, m)
	}
	st.Queries = st.Cache + st.Upstream + st.Stale + st.Failed
	if ups > 0 {
		st.AvgMs = round1(float64(msSum) / float64(ups))
	}
	st.Top = top(doms, statTopShown)
	st.Types = top(types, statTypeShown)
	return st
}

// log: the last questions, newest first; filter by part of the name.
func (r *recorder) log(match string, n int) []LogEntry {
	match = strings.ToLower(strings.TrimSpace(match))
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []LogEntry{}
	l := len(r.recent)
	for i := 1; i <= l && len(out) < n; i++ {
		q := r.recent[(r.next-i+l)%l] // r.next is one past the newest
		if match == "" || strings.Contains(q.Domain, match) {
			out = append(out, q)
		}
	}
	return out
}

func top(m map[string]int, n int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func p95(ms []uint16) float64 {
	if len(ms) == 0 {
		return 0
	}
	s := slices.Clone(ms)
	slices.Sort(s)
	return float64(s[(len(s)*95-1)/100])
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// typeName: the query types people see; the rest by number.
func typeName(t uint16) string {
	switch t {
	case TypeA:
		return "A"
	case TypeAAAA:
		return "AAAA"
	case 65:
		return "HTTPS"
	case 5:
		return "CNAME"
	case 12:
		return "PTR"
	case 15:
		return "MX"
	case 16:
		return "TXT"
	case 33:
		return "SRV"
	case typeSOA:
		return "SOA"
	}
	return "TYPE" + itoa(int(t))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 && i > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
