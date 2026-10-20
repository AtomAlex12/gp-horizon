package dns

import (
	"sort"
	"time"
)

// What route suggestions read to find the sites a home uses: the names the
// router asked about, from the same per-minute counts the charts show.
// Read-only; nothing is kept beyond what the charts already keep.

// Watching: the router's DNS questions reach nuxk — protected DNS is on.
func (s *Service) Watching() bool { return s.Settings().Enabled }

// Frequent: names asked at least `least` times within the last since (at most
// the hour the charts keep), most asked first. Nil while protected DNS is
// off: what is counted then is stale.
func (s *Service) Frequent(since time.Duration, least int) []string {
	if !s.Watching() {
		return nil
	}
	return s.rec.frequent(since, least)
}

func (r *recorder) frequent(since time.Duration, least int) []string {
	n := int(since / time.Minute)
	n = max(1, min(n, statMinutes))
	now := r.clock().Unix()
	cur := now - now%60
	counts := map[string]int{}
	r.mu.Lock()
	for i := 0; i < n; i++ {
		t := cur - int64(i)*60
		if b := &r.buckets[(t/60)%statMinutes]; b.t == t {
			for d, c := range b.domains {
				counts[d] += c
			}
		}
	}
	r.mu.Unlock()
	var out []Count
	for d, c := range counts {
		if c >= least {
			out = append(out, Count{d, c})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	names := make([]string, len(out))
	for i, c := range out {
		names[i] = c.Name
	}
	return names
}
