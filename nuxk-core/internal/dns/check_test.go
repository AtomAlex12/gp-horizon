package dns

import (
	"errors"
	"net/netip"
	"testing"
)

func ans(ips ...string) answer {
	a := answer{}
	for _, ip := range ips {
		a.a.Addrs = append(a.a.Addrs, netip.MustParseAddr(ip))
	}
	return a
}

func nx() answer { return answer{a: Answer{Rcode: RcodeNXDomain}} }

// What counts as substitution: a made-up «no such site», a private address,
// one address for unrelated sites. Other addresses alone don't.
func TestVerdicts(t *testing.T) {
	truth := []answer{
		ans("104.21.8.8"),                 // rutor.info
		ans("104.21.9.9"),                 // flibusta.is
		ans("93.184.215.14"),              // a CDN site
		ans("198.51.100.1"),               // a site
		ans("203.0.113.5", "203.0.113.6"), // ok
		ans("192.0.2.77"),                 // private stub
	}
	got := []answer{
		ans("95.167.13.50"),  // the same stub…
		ans("95.167.13.50"),  // …for another site
		ans("93.184.216.34"), // CDN elsewhere
		nx(),                 // «no such site»
		ans("203.0.113.6"),   // one of the real ones
		ans("10.10.34.34"),   // a private address
	}
	want := []string{VerdictSpoofed, VerdictSpoofed, VerdictDiffers, VerdictSpoofed, VerdictOK, VerdictSpoofed}
	for i, v := range verdicts(truth, got) {
		if v.v != want[i] {
			t.Errorf("%d: %s (%s), want %s", i, v.v, v.note, want[i])
		}
	}
	// no reference: can't tell
	if v := verdicts([]answer{{err: errTest}}, []answer{ans("1.2.3.4")}); v[0].v != VerdictError {
		t.Fatalf("%+v", v)
	}
}

var errTest = errors.New("timeout")
