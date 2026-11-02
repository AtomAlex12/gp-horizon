package dns

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Hold the direct DoH path while WARP recovers. A running interface alone is
// not evidence of a working DNS path. This state is deliberately reset on
// every agent startup: old SmartDNS configuration must not block bootstrap.
const smartWarpHold = 5 * time.Minute
const smartWarpProbeGap = 30 * time.Second

func (s *Service) holdSmartWarp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.smartDirectUntil = s.clock().Add(smartWarpHold)
	s.smartWarpGood = 0
	s.smartWarpCandidate = ""
	s.smartWarpProbeAt = time.Time{}
}

func (s *Service) smartWarpIface() string {
	s.mu.Lock()
	held := !s.smartDirectUntil.IsZero()
	s.mu.Unlock()
	if held || s.sd == nil {
		return ""
	}
	return s.sd.o.WarpIface()
}

// Switch the configuration before restarting an unresponsive SmartDNS on
// the same broken path. Direct DoH uses fixed IPs and verified TLS already.
func (s *Service) fallbackSmart(ctx context.Context, cause error) error {
	if s.smartWarpIface() == "" || s.Settings().Via == ViaDirect {
		return cause
	}
	s.holdSmartWarp()
	slog.Warn("dns: DNS через WARP не отвечает — перехожу на прямой DoH", "err", cause)
	if err := s.startSmart(ctx); err != nil {
		return fmt.Errorf("прямой DoH не включился: %w", err)
	}
	return s.healthy(ctx)
}

// Probe independently of the router's DNS proxy: DoH's fixed addresses do
// not need a DNS lookup through the tunnel being repaired. Two successful
// checks on separate ticks are required; failure starts another hold period.
func (s *Service) recoverSmartWarp(ctx context.Context) {
	if s.sd == nil || s.Settings().Via == ViaDirect {
		return
	}
	now := s.clock()
	s.mu.Lock()
	due := !s.smartDirectUntil.IsZero() && !now.Before(s.smartDirectUntil) &&
		(s.smartWarpProbeAt.IsZero() || now.Sub(s.smartWarpProbeAt) >= smartWarpProbeGap)
	s.mu.Unlock()
	if !due {
		return
	}
	iface := s.sd.o.WarpIface()
	if iface == "" {
		return
	}
	s.mu.Lock()
	if s.smartWarpCandidate != iface {
		s.smartWarpCandidate, s.smartWarpGood = iface, 0
	}
	s.smartWarpProbeAt = now
	s.mu.Unlock()
	q, _ := Query(randID(), fmt.Sprintf("n%08x.nuxk-check.example.com", randID32()), TypeA)
	pctx, cancel := context.WithTimeout(ctx, slowAnswer)
	defer cancel()
	ok := false
	for _, r := range s.resolvers() {
		resp, err := doh(pctx, s.client(Path{Name: ViaWarp, Iface: iface}), r, q)
		if err == nil {
			a, err := Parse(resp)
			if err == nil && pctx.Err() == nil && (a.Rcode == 0 || a.Rcode == 3) {
				ok = true
				break
			}
		}
		if pctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if ok {
		s.smartWarpGood++
	} else {
		s.smartWarpGood = 0
		s.smartDirectUntil = now.Add(smartWarpHold)
	}
	recovered := s.smartWarpGood >= 2
	if recovered {
		s.smartDirectUntil = time.Time{}
		s.smartWarpGood = 0
	}
	s.mu.Unlock()
	if recovered {
		slog.Info("dns: DNS через WARP снова отвечает — возвращаю SmartDNS в туннель")
	}
}
