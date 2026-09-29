package keenetic

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The DNS hook speaks RCI's command parser, and only about nuxk's own server.
func TestNameServer(t *testing.T) {
	var mu sync.Mutex
	var cmds []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body struct {
			Parse string `json:"parse"`
		}
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		cmds = append(cmds, body.Parse)
		mu.Unlock()
		if strings.HasPrefix(body.Parse, "no ") && len(cmds) == 1 {
			// taking back what isn't there: an error status, as the firmware answers
			_, _ = w.Write([]byte(`{"parse":{"status":[{"status":"error","code":"7405600","message":"no such name server"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"parse":{"status":[{"status":"message","code":"0","message":"ok"}]}}`))
	}))
	defer srv.Close()
	b := New(srv.URL)
	// this router's own LAN address, as on the real one
	b.Addrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.2.1"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	h := DNSHook{B: b}
	ctx := context.Background()
	if err := h.Attach(ctx, "192.168.2.1:53053"); err != nil {
		t.Fatalf("attach over a missing entry: %v", err)
	}
	if err := h.Detach(ctx, "192.168.2.1:53053"); err != nil {
		t.Fatal(err)
	}
	want := []string{"no ip name-server 192.168.2.1:53053", "ip name-server 192.168.2.1:53053", "no ip name-server 192.168.2.1:53053"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands %q", cmds)
	}
	// someone else's server, no port, not an address at all, a command smuggled in
	for _, bad := range []string{"8.8.8.8:53", "192.168.2.1", "192.0.2.1:53053", "router:53", "192.168.2.1:53; system configuration save", "192.168.2.1:99999"} {
		if err := h.B.NameServer(ctx, bad, true); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if len(cmds) != 3 {
		t.Fatalf("a refused one reached the router: %q", cmds)
	}
}
