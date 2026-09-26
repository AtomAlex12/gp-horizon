package main

import (
	"strings"
	"sync"
)

// metrics mirrors the agent's Metrics schema (only what the history needs).
type metrics struct {
	TS        int64            `json:"ts"` // unix ms
	WAN       string           `json:"wan"`
	Ifaces    map[string]iface `json:"ifaces"`
	NFQueues  []nfqueue        `json:"nfqueues"`
	Conntrack int              `json:"conntrack"`
	Load1     float64          `json:"load1"`
	MemAvail  int              `json:"mem_avail_kb"`
}

type iface struct {
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

type nfqueue struct {
	Packets uint64 `json:"packets"`
}

// isTunnel: the interfaces the WARP/VLESS engines create (opkgtunN, tun-xray…)
// — not the kernel's own IPIP fallback tunl0, which never carries traffic.
func isTunnel(name string) bool {
	return strings.HasPrefix(name, "opkgtun") || (strings.HasPrefix(name, "tun") && !strings.HasPrefix(name, "tunl"))
}

// point is one sample turned into rates (per second) against the previous one.
type point struct {
	TS        int64
	WanRx     float64 // bits/s
	WanTx     float64
	NFQ       float64 // packets/s
	Tun       map[string][2]float64
	Conntrack int
	Load1     float64
}

// History keeps the last N rate points computed from raw agent counters.
type History struct {
	mu   sync.Mutex
	max  int
	prev *metrics
	pts  []point
}

func NewHistory(max int) *History { return &History{max: max} }

// Add turns a new sample into a rate point. Counters that went backwards
// (router reboot, interface re-created) give 0 for that step.
func (h *History) Add(m metrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.prev
	h.prev = &m
	if p == nil || m.TS <= p.TS {
		return
	}
	dt := float64(m.TS-p.TS) / 1000
	rate := func(now, before uint64) float64 {
		if now < before {
			return 0
		}
		return float64(now-before) / dt
	}
	pt := point{TS: m.TS, Tun: map[string][2]float64{}, Conntrack: m.Conntrack, Load1: m.Load1}
	if m.WAN != "" && m.WAN == p.WAN {
		pt.WanRx = rate(m.Ifaces[m.WAN].Rx, p.Ifaces[m.WAN].Rx) * 8
		pt.WanTx = rate(m.Ifaces[m.WAN].Tx, p.Ifaces[m.WAN].Tx) * 8
	}
	var pk, ppk uint64
	for _, q := range m.NFQueues {
		pk += q.Packets
	}
	for _, q := range p.NFQueues {
		ppk += q.Packets
	}
	pt.NFQ = rate(pk, ppk)
	for name, c := range m.Ifaces {
		if !isTunnel(name) {
			continue
		}
		if b, ok := p.Ifaces[name]; ok {
			pt.Tun[name] = [2]float64{rate(c.Rx, b.Rx) * 8, rate(c.Tx, b.Tx) * 8}
		}
	}
	h.pts = append(h.pts, pt)
	if len(h.pts) > h.max {
		h.pts = h.pts[len(h.pts)-h.max:]
	}
}

// Series is GET /ctl/v1/history: columns, oldest first.
type Series struct {
	WAN       string                          `json:"wan"`
	TS        []int64                         `json:"ts"`
	WanRx     []float64                       `json:"wan_rx_bps"`
	WanTx     []float64                       `json:"wan_tx_bps"`
	NFQ       []float64                       `json:"nfq_pps"`
	Conntrack []int                           `json:"conntrack"`
	Load1     []float64                       `json:"load1"`
	Tunnels   map[string]map[string][]float64 `json:"tunnels"` // iface → rx_bps / tx_bps
}

func (h *History) Series() Series {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Series{TS: []int64{}, WanRx: []float64{}, WanTx: []float64{}, NFQ: []float64{}, Conntrack: []int{}, Load1: []float64{}, Tunnels: map[string]map[string][]float64{}}
	if h.prev != nil {
		s.WAN = h.prev.WAN
	}
	names := map[string]bool{}
	for _, p := range h.pts {
		for n := range p.Tun {
			names[n] = true
		}
	}
	for n := range names {
		s.Tunnels[n] = map[string][]float64{"rx_bps": {}, "tx_bps": {}}
	}
	for _, p := range h.pts {
		s.TS = append(s.TS, p.TS)
		s.WanRx = append(s.WanRx, p.WanRx)
		s.WanTx = append(s.WanTx, p.WanTx)
		s.NFQ = append(s.NFQ, p.NFQ)
		s.Conntrack = append(s.Conntrack, p.Conntrack)
		s.Load1 = append(s.Load1, p.Load1)
		for n := range names {
			v := p.Tun[n] // missing → 0 (tunnel down at that moment)
			s.Tunnels[n]["rx_bps"] = append(s.Tunnels[n]["rx_bps"], v[0])
			s.Tunnels[n]["tx_bps"] = append(s.Tunnels[n]["tx_bps"], v[1])
		}
	}
	return s
}
