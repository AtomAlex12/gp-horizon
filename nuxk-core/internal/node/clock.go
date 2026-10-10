package node

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ClockURL answers a plain HTTP request with its Date: no DNS (an address)
// and no TLS — both need the right time, so neither may stand in the way of
// finding out that it's wrong.
const ClockURL = "http://1.1.1.1/cdn-cgi/trace"

// clockOff: off by more than this, the router's clock is wrong.
const clockOff = 2 * time.Minute

// Clock checks the box's time against a reference. A wrong clock breaks what
// checks certificates and times (DoH, subscriptions, VLESS Reality,
// WireGuard handshakes) — on 10.10.2026 the router ran 5 days behind after a
// reboot and nothing said so.
type Clock struct {
	URL   string
	HTTP  *http.Client
	Every time.Duration

	mu    sync.Mutex
	skew  time.Duration // the box minus the reference
	at    time.Time
	known bool
}

func NewClock() *Clock {
	return &Clock{
		URL:   ClockURL,
		Every: 30 * time.Minute,
		HTTP: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Run checks a minute after the start (the network is up by then), then
// every Every.
func (c *Clock) Run(ctx context.Context) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		c.Check(ctx)
		t.Reset(c.Every)
	}
}

// Check measures once; a failure leaves the last measurement as it is.
func (c *Clock) Check(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.URL, nil)
	if err != nil {
		return
	}
	sent := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		slog.Debug("clock: reference unreachable", "url", c.URL, "err", err)
		return
	}
	resp.Body.Close()
	got := time.Now()
	ref, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		slog.Debug("clock: no Date in the answer", "url", c.URL)
		return
	}
	// the Date was written about halfway through; it has whole seconds only
	skew := sent.Add(got.Sub(sent) / 2).Sub(ref).Round(time.Second)
	c.mu.Lock()
	wasOff := c.known && off(c.skew)
	c.skew, c.at, c.known = skew, got, true
	c.mu.Unlock()
	switch {
	case off(skew) && !wasOff:
		slog.Warn("clock: the router's time is wrong — TLS, VLESS Reality and WireGuard may fail; check the time sync in Keenetic",
			"skew", skew.String(), "reference", ref.UTC().Format(time.RFC3339))
	case !off(skew) && wasOff:
		slog.Info("clock: the router's time is right again", "skew", skew.String())
	default:
		slog.Debug("clock", "skew", skew.String())
	}
}

func off(d time.Duration) bool { return d > clockOff || d < -clockOff }

// Skew: the box's clock minus the reference, and when it was measured; ok is
// false until a measurement succeeded.
func (c *Clock) Skew() (skew time.Duration, at time.Time, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.skew, c.at, c.known
}
