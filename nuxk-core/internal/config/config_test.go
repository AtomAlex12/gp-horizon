package config

import "testing"

// The DNS forwarder sits on the router's LAN address (KeeneticOS refuses a
// loopback DNS server); DNS_LISTEN / DNS_ROUTER override it.
func TestDNSAddrs(t *testing.T) {
	for _, c := range []struct {
		cfg        Config
		fwd, proxy string
	}{
		{Config{Listen: "192.168.2.1:4141"}, "192.168.2.1:53053", "192.168.2.1:53"},
		{Config{Listen: "127.0.0.1:4141"}, "127.0.0.1:53053", "127.0.0.1:53"},
		{Config{Listen: "0.0.0.0:4141"}, "127.0.0.1:53053", "127.0.0.1:53"},
		{Config{Listen: ":4141"}, "127.0.0.1:53053", "127.0.0.1:53"},
		{Config{Listen: "192.168.2.1:4141", DNSListen: "192.168.2.1:5300", DNSRouter: "192.168.2.1:5353"}, "192.168.2.1:5300", "192.168.2.1:5353"},
	} {
		if got := c.cfg.DNSAddr(); got != c.fwd {
			t.Errorf("%+v: forwarder %s, want %s", c.cfg, got, c.fwd)
		}
		if got := c.cfg.DNSRouterAddr(); got != c.proxy {
			t.Errorf("%+v: proxy %s, want %s", c.cfg, got, c.proxy)
		}
	}
}
