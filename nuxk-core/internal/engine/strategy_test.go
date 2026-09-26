package engine

import (
	"strings"
	"testing"
)

func good() Strategy {
	return Strategy{ID: "gp-1", Protocol: "tls", Domains: []string{"rutracker.org", "browserleaks.com"},
		Args: "--payload=tls_client_hello --lua-desync=multisplit:pos=1,midsld:seqovl=1"}
}

func TestValidateStrategy(t *testing.T) {
	if err := ValidateStrategy(good()); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Strategy){
		"file blob":       func(s *Strategy) { s.Args = "--lua-desync=fake:blob=@/etc/shadow" },
		"shell":           func(s *Strategy) { s.Args = "--lua-desync=fake;reboot" },
		"subshell":        func(s *Strategy) { s.Args = "--lua-desync=$(reboot)" },
		"quote":           func(s *Strategy) { s.Args = `--lua-desync=fake" --user=root "` },
		"glob":            func(s *Strategy) { s.Args = "--lua-desync=fake:*" },
		"path":            func(s *Strategy) { s.Args = "--lua-desync=fake:blob=/tmp/x" },
		"other option":    func(s *Strategy) { s.Args = "--lua-desync=fake --lua-init=@/tmp/evil.lua" },
		"debug":           func(s *Strategy) { s.Args = "--lua-desync=fake --debug=/opt/etc/passwd" },
		"new profile":     func(s *Strategy) { s.Args = "--lua-desync=fake --new --filter-tcp=1-65535" },
		"no desync":       func(s *Strategy) { s.Args = "--payload=tls_client_hello" },
		"empty":           func(s *Strategy) { s.Args = "" },
		"protocol":        func(s *Strategy) { s.Protocol = "udp" },
		"no domains":      func(s *Strategy) { s.Domains = nil },
		"domain with dir": func(s *Strategy) { s.Domains = []string{"../../x"} },
		"domain spaces":   func(s *Strategy) { s.Domains = []string{"a.com b.com"} },
		"id":              func(s *Strategy) { s.ID = "x y" },
	} {
		s := good()
		mut(&s)
		if ValidateStrategy(s) == nil {
			t.Errorf("%s: accepted %q", name, s.Args)
		}
	}
}

func TestNormalizeArgs(t *testing.T) {
	in := "nfqws2 --filter-tcp=443 --filter-l7=tls --hostlist=/opt/x --payload=tls_client_hello  --lua-desync=fake:repeats=6"
	if got := NormalizeArgs(in); got != "--payload=tls_client_hello --lua-desync=fake:repeats=6" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeArgs("/opt/zapret2/nfq2/nfqws2 --lua-desync=fake"); got != "--lua-desync=fake" {
		t.Errorf("path to nfqws2: %q", got)
	}
}

func TestRenderStrategies(t *testing.T) {
	q := good()
	q.ID, q.Protocol, q.Args = "gp-2", "quic", "--payload=quic_initial --lua-desync=fake:repeats=11"
	got := RenderStrategies([]Strategy{good(), q}, "/opt/etc/nfqws2/lists")
	want := "--filter-tcp=443 --filter-l7=tls --hostlist=/opt/etc/nfqws2/lists/nuxk-s1.list --payload=tls_client_hello --lua-desync=multisplit:pos=1,midsld:seqovl=1" +
		" --new " +
		"--filter-udp=443 --filter-l7=quic --hostlist=/opt/etc/nfqws2/lists/nuxk-s2.list --payload=quic_initial --lua-desync=fake:repeats=11"
	if got != want {
		t.Errorf("render:\n got %s\nwant %s", got, want)
	}
	if RenderStrategies(nil, "/x") != "" || strings.Contains(RenderStrategies([]Strategy{good()}, "/x"), "--new") {
		t.Error("no --new for zero or one profile")
	}
}
