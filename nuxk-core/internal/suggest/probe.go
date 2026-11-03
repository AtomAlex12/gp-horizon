package suggest

import (
	"context"
	"net"

	"nuxk.dev/horizon/core/internal/probe"
)

// Prober is the measuring side of the ladder: internal/probe's Prober, or a
// test's own.
type Prober interface {
	Resolve(ctx context.Context, domain string) (net.IP, string) // ip, or why not: nxdomain | timeout | bogus
	Way(ctx context.Context, domain string, ip net.IP, via probe.Via) probe.Result
	Paths() []probe.PathState
}

var _ Prober = (*probe.Prober)(nil)
