package suggest

import (
	"context"
	"testing"

	"nuxk.dev/horizon/core/internal/probe"
)

func TestUnavailableDirectDoesNotRecommendTunnel(t *testing.T) {
	p := newFake()
	p.skip[probe.ViaDirect] = "не удалось установить метку сокета"
	p.set("example.com", probe.ViaWarp, opened(100, 20000))
	it := decide(context.Background(), p, bar{DefaultSettings(), Reference{}}, true, "example.com")
	if it.Verdict != string(probe.VerdictUnknown) || it.Mode != "" || it.Confirmed {
		t.Errorf("unavailable local probe became a network verdict: %+v", it)
	}
	if len(p.calls) != 1 {
		t.Errorf("measured other paths without a valid baseline: %v", p.calls)
	}
}
