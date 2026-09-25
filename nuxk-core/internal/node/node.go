// Package node describes the box nuxk-core runs on and reads its cheap
// counters from /proc: interface bytes, NFQUEUE packets, conntrack, memory.
// Every read is a small in-memory file — safe to call every few seconds on a
// router. Root prefixes /proc and /bin for tests.
package node

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Role is what kind of box this is; the UI says it plainly so a test stand
// is never mistaken for the router.
type Role string

const (
	RoleRouter Role = "router" // Keenetic with Entware
	RoleStand  Role = "stand"  // prototype stand (Docker on the Pi)
	RoleHost   Role = "host"   // anything else
)

// Info is GET /api/v1/info.
type Info struct {
	Role      Role   `json:"role"`
	Hostname  string `json:"hostname"`
	Model     string `json:"model,omitempty"`    // Keenetic model
	Firmware  string `json:"firmware,omitempty"` // KeeneticOS release
	Arch      string `json:"arch"`
	Kernel    string `json:"kernel"`
	UptimeSec int64  `json:"uptime_sec"` // the box, not nuxk-core
	CoreSince int64  `json:"core_since"` // unix seconds nuxk-core started
	Version   string `json:"version"`
	Commit    string `json:"commit"`
}

// Node answers Info and Metrics.
type Node struct {
	Root    string // "" in production
	RoleCfg string // NODE_ROLE from nuxk.conf; "" = detect
	RCI     string // KeeneticOS RCI base for model/firmware
	Version string
	Commit  string

	started time.Time
	mu      sync.Mutex
	model   string
	fw      string
	fwAt    time.Time
}

func New(roleCfg, rci, version, commit string) *Node {
	return &Node{RoleCfg: roleCfg, RCI: rci, Version: version, Commit: commit, started: time.Now()}
}

func (n *Node) p(path string) string { return filepath.Join(n.Root, path) }

func (n *Node) role() Role {
	switch Role(n.RoleCfg) {
	case RoleRouter, RoleStand, RoleHost:
		return Role(n.RoleCfg)
	}
	if _, err := os.Stat(n.p("/bin/ndmc")); err == nil {
		return RoleRouter
	}
	return RoleHost
}

func (n *Node) Info(ctx context.Context) Info {
	host, _ := os.Hostname()
	in := Info{
		Role: n.role(), Hostname: host, Arch: runtime.GOARCH,
		Kernel:    strings.TrimSpace(readFile(n.p("/proc/sys/kernel/osrelease"))),
		CoreSince: n.started.Unix(), Version: n.Version, Commit: n.Commit,
	}
	if f := strings.Fields(readFile(n.p("/proc/uptime"))); len(f) > 0 {
		v, _ := strconv.ParseFloat(f[0], 64)
		in.UptimeSec = int64(v)
	}
	if in.Role == RoleRouter {
		in.Model, in.Firmware = n.keenetic(ctx)
	}
	return in
}

// keenetic reads model and release from RCI once (retried every minute
// while it fails — ndm may still be starting at boot).
func (n *Node) keenetic(ctx context.Context) (string, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fw != "" || time.Since(n.fwAt) < time.Minute || n.RCI == "" {
		return n.model, n.fw
	}
	n.fwAt = time.Now()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(n.RCI, "/")+"/rci/show/version", nil)
	if err != nil {
		return "", ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	var v struct {
		Release string `json:"release"`
		Model   string `json:"model"`
		Device  string `json:"device"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) == nil {
		n.model, n.fw = firstNonEmpty(v.Model, v.Device), v.Release
	}
	return n.model, n.fw
}

// Iface is one network interface's byte counters.
type Iface struct {
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

// Queue is one NFQUEUE (nfqws2) — Packets only ever grows.
type Queue struct {
	Num     int    `json:"num"`
	Packets uint64 `json:"packets"`
	Dropped uint64 `json:"dropped"`
	Waiting uint64 `json:"waiting"`
}

// Metrics is GET /api/v1/metrics: raw counters; rates are the reader's job
// (two samples, divide by the time between them).
type Metrics struct {
	TS        int64            `json:"ts"` // unix milliseconds
	WAN       string           `json:"wan,omitempty"`
	Ifaces    map[string]Iface `json:"ifaces"`
	NFQueues  []Queue          `json:"nfqueues"`
	Conntrack int              `json:"conntrack"`
	ConnMax   int              `json:"conntrack_max"`
	Load1     float64          `json:"load1"`
	MemTotal  int              `json:"mem_total_kb"`
	MemAvail  int              `json:"mem_avail_kb"`
	CoreRSS   int              `json:"core_rss_kb"`
}

func (n *Node) Metrics() Metrics {
	m := Metrics{TS: time.Now().UnixMilli(), Ifaces: map[string]Iface{}, NFQueues: []Queue{}}
	sc := bufio.NewScanner(strings.NewReader(readFile(n.p("/proc/net/dev"))))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		name = strings.TrimSpace(name)
		if !ok || name == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		m.Ifaces[name] = Iface{Rx: atou(f[0]), Tx: atou(f[8])}
	}
	m.WAN = defaultRouteIface(readFile(n.p("/proc/net/route")))
	for _, l := range strings.Split(readFile(n.p("/proc/net/netfilter/nfnetlink_queue")), "\n") {
		// queue_number peer_portid queue_total copy_mode copy_range queue_dropped user_dropped id_sequence 1
		f := strings.Fields(l)
		if len(f) < 8 {
			continue
		}
		m.NFQueues = append(m.NFQueues, Queue{Num: int(atou(f[0])), Waiting: atou(f[2]), Dropped: atou(f[5]) + atou(f[6]), Packets: atou(f[7])})
	}
	m.Conntrack = int(atou(strings.TrimSpace(readFile(n.p("/proc/sys/net/netfilter/nf_conntrack_count")))))
	m.ConnMax = int(atou(strings.TrimSpace(readFile(n.p("/proc/sys/net/netfilter/nf_conntrack_max")))))
	if f := strings.Fields(readFile(n.p("/proc/loadavg"))); len(f) > 0 {
		m.Load1, _ = strconv.ParseFloat(f[0], 64)
	}
	mem := kvKB(readFile(n.p("/proc/meminfo")))
	m.MemTotal, m.MemAvail = mem["MemTotal"], mem["MemAvailable"]
	m.CoreRSS = kvKB(readFile(n.p("/proc/self/status")))["VmRSS"]
	return m
}

// defaultRouteIface picks the IPv4 default route with the lowest metric.
func defaultRouteIface(routes string) string {
	type r struct {
		iface  string
		metric uint64
	}
	var defs []r
	for i, l := range strings.Split(routes, "\n") {
		f := strings.Fields(l)
		if i == 0 || len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		defs = append(defs, r{f[0], atou(f[6])})
	}
	sort.SliceStable(defs, func(i, j int) bool { return defs[i].metric < defs[j].metric })
	if len(defs) == 0 {
		return ""
	}
	return defs[0].iface
}

func kvKB(s string) map[string]int {
	out := map[string]int{}
	for _, l := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		if f := strings.Fields(v); len(f) > 0 {
			out[k] = int(atou(f[0]))
		}
	}
	return out
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func atou(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
