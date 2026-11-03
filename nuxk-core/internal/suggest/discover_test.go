package suggest

import (
	"testing"
	"time"

	"nuxk.dev/horizon/core/internal/probe"
)

type fakeAsked struct {
	watching bool
	names    []string
	since    time.Duration
	least    int
}

func (f *fakeAsked) Watching() bool { return f.watching }
func (f *fakeAsked) Frequent(since time.Duration, least int) []string {
	f.since, f.least = since, least
	if !f.watching {
		return nil
	}
	return f.names
}

func TestBaseDomain(t *testing.T) {
	for in, want := range map[string]string{
		"api.openai.com":             "openai.com",
		"openai.com":                 "openai.com",
		"A.B.Example.COM.":           "example.com",
		"www.bbc.co.uk":              "bbc.co.uk",
		"news.bbc.co.uk":             "bbc.co.uk",
		"shop.example.com.ru":        "example.com.ru",
		"co.uk":                      "",
		"d111.cloudfront.net":        "",
		"1.2.3.4":                    "",
		"4.3.2.1.in-addr.arpa":       "",
		"printer.lan":                "",
		"localhost":                  "",
		"not a name":                 "",
		"rr1---sn-x.googlevideo.com": "googlevideo.com",
	} {
		if got := BaseDomain(in); got != want {
			t.Errorf("BaseDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDiscover(t *testing.T) {
	p := newFake()
	p.set("youtube.com", probe.ViaDirect, opened(300, 0))
	asked := &fakeAsked{watching: true, names: []string{
		"printer.lan",         // not a site
		"api.mine.com",        // its domain is in a list
		"known.com",           // already checked
		"i.ytimg.com",         // → ytimg.com
		"www.youtube.com",     // → youtube.com
		"rr1.googlevideo.com", // → googlevideo.com
	}}
	rt := &fakeRouter{covered: map[string]string{"mine.com": "список «Мои»", "api.mine.com": "список «Мои»"}}
	s, _, c := newService(t, p, rt, false)
	s.DNS = asked
	s.Check([]string{"known.com"}, true)
	drain(s)

	s.discover()
	if asked.since != time.Hour || asked.least != 3 {
		t.Errorf("asked Frequent(%v, %d)", asked.since, asked.least)
	}
	ov := s.Overview()
	if ov.Busy != 1 || !ov.Discovering {
		t.Fatalf("one domain must be queued: %+v", ov)
	}
	if it := item(t, s, "ytimg.com"); it.Source != SourceDNS || it.State != StateChecking {
		t.Fatalf("%+v", it)
	}
	drain(s)

	s.discover() // within the 3 minutes: nothing
	if s.Overview().Busy != 0 {
		t.Fatal("more than one domain per tick")
	}
	c.advance(discoverEvery)
	s.discover()
	if it := item(t, s, "youtube.com"); it.State != StateChecking || it.Source != SourceDNS {
		t.Fatalf("next tick: %+v", it)
	}
	drain(s)
	// it opens: kept quietly, without the details
	if it := item(t, s, "youtube.com"); !it.Hidden || it.Verdict != "ok" || it.Paths != nil {
		t.Errorf("quiet find: %+v", it)
	}
	// ytimg.com doesn't open anywhere: nothing to say, kept quietly too
	if it := item(t, s, "ytimg.com"); !it.Hidden {
		t.Errorf("unknown find: %+v", it)
	}

	c.advance(discoverEvery)
	s.discover()
	drain(s)
	if it := item(t, s, "googlevideo.com"); it.Source != SourceDNS {
		t.Fatalf("%+v", it)
	}
	// everything looked at: an hour of quiet ticks, no re-measuring
	p.reset()
	for i := 0; i < 5; i++ {
		c.advance(discoverEvery)
		s.discover()
		drain(s)
	}
	if len(p.calls) != 0 {
		t.Errorf("re-measured known names: %v", p.calls)
	}
	// a week later the quiet ones are forgotten and may be measured again
	c.advance(discoverKeep)
	s.discover()
	if s.Overview().Busy != 1 {
		t.Errorf("a week later nothing was queued")
	}
	if _, ok := s.items["mine.com"]; ok {
		t.Error("a covered domain was queued")
	}
}

func TestDiscoverOff(t *testing.T) {
	asked := &fakeAsked{watching: true, names: []string{"www.youtube.com"}}
	s, _, c := newService(t, newFake(), &fakeRouter{}, false)
	s.DNS = asked

	set := s.Settings()
	set.Discover = false
	if _, err := s.SetSettings(set); err != nil {
		t.Fatal(err)
	}
	s.discover()
	if s.Overview().Busy != 0 || s.Overview().Discovering {
		t.Fatal("discovery turned off still queued")
	}

	set.Discover = true
	_, _ = s.SetSettings(set)
	asked.watching = false // protected DNS off: the router doesn't ask nuxk
	c.advance(discoverEvery)
	s.discover()
	if ov := s.Overview(); ov.Busy != 0 || ov.Discovering {
		t.Fatal("discovery without protected DNS")
	}

	asked.watching = true
	s.Check([]string{"user.example"}, true) // the person's own check goes first
	c.advance(discoverEvery)
	s.discover()
	if s.Overview().Busy != 1 {
		t.Fatal("discovery queued while the person's check waits")
	}
}
