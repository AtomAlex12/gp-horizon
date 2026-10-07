// Package keenetic drives KeeneticOS's built-in DNS routing through RCI, the
// firmware's local JSON API (http://127.0.0.1:79/rci/). From the router
// itself RCI needs no password — verified on KeeneticOS 5.01, 25.09 — so the
// agent never stores the admin credentials.
//
// Writes use the same structure reads return, "no": true deletes, and every
// action answers with status entries (code + message); an "error" status
// fails the op. IPv6: the firmware fills _NDM_OGDN_6_@<group> but routes
// nothing through it, so v6 to nuxk groups is refused (REJECT) to make
// clients fall back to IPv4, which is routed.
package keenetic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"nuxk.dev/horizon/core/internal/plane"
)

// Runner runs a command on the router (ip6tables/ipset). Tests fake it.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

func execRunner(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// Backend is the Keenetic plane backend.
type Backend struct {
	URL  string // RCI base, default http://127.0.0.1:79
	HTTP *http.Client
	Run  Runner
	// Addrs lists this box's addresses (net.InterfaceAddrs; tests replace it)
	Addrs func() ([]net.Addr, error)
}

func New(url string) *Backend {
	if url == "" {
		url = "http://127.0.0.1:79"
	}
	return &Backend{URL: strings.TrimRight(url, "/"), HTTP: &http.Client{Timeout: 15 * time.Second}, Run: execRunner}
}

func (b *Backend) Name() string { return "keenetic" }

// --- RCI transport ------------------------------------------------------------

func (b *Backend) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL+"/rci/"+path, nil)
	if err != nil {
		return err
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("RCI %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RCI %s: HTTP %d", path, resp.StatusCode)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

// post sends one change and fails if any status in the answer is an error.
func (b *Backend) post(ctx context.Context, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.URL+"/rci/", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("RCI POST: %w", err)
	}
	defer resp.Body.Close()
	ans, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RCI POST: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(ans)))
	}
	var tree any
	if err := json.Unmarshal(ans, &tree); err != nil {
		return fmt.Errorf("RCI POST: bad answer: %w", err)
	}
	if msgs := statusErrors(tree); len(msgs) > 0 {
		return errors.New("RCI: " + strings.Join(msgs, "; "))
	}
	return nil
}

// statusErrors walks an RCI answer and collects messages of error statuses.
func statusErrors(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		if st, _ := t["status"].(string); st == "error" {
			msg, _ := t["message"].(string)
			code, _ := t["code"].(string)
			out = append(out, strings.TrimSpace(msg+" ("+code+")"))
		}
		for _, c := range t {
			out = append(out, statusErrors(c)...)
		}
	case []any:
		for _, c := range t {
			out = append(out, statusErrors(c)...)
		}
	}
	return out
}

// --- observe ------------------------------------------------------------------

type rciGroup struct {
	Description string `json:"description"`
	Include     []struct {
		Address string `json:"address"`
	} `json:"include"`
}

type rciDNSProxy struct {
	Route []plane.Route `json:"route"`
}

// Version returns the KeeneticOS release (e.g. "5.01.C.6.0-1").
func (b *Backend) Version(ctx context.Context) (string, error) {
	var v struct {
		Release string `json:"release"`
	}
	if err := b.get(ctx, "show/version", &v); err != nil {
		return "", err
	}
	return v.Release, nil
}

// Supported: built-in DNS routing exists from KeeneticOS 4; 4.x and 5.x are
// the versions it's known on. Anything else is refused rather than guessed at.
func Supported(release string) bool {
	return strings.HasPrefix(release, "4.") || strings.HasPrefix(release, "5.")
}

func (b *Backend) Observe(ctx context.Context) (plane.Observed, error) {
	obs := plane.Observed{Groups: map[string][]string{}, Descr: map[string]string{}}
	rel, err := b.Version(ctx)
	if err != nil {
		return obs, err
	}
	if !Supported(rel) {
		return obs, fmt.Errorf("KeeneticOS %q: the plane only knows 4.x/5.x — refusing to change it", rel)
	}
	groups := map[string]rciGroup{}
	if err := b.get(ctx, "object-group/fqdn", &groups); err != nil {
		return obs, err
	}
	for name, g := range groups {
		var doms []string
		for _, i := range g.Include {
			doms = append(doms, strings.ToLower(i.Address))
		}
		sort.Strings(doms)
		obs.Groups[name] = doms
		obs.Descr[name] = g.Description
	}
	var dp rciDNSProxy
	if err := b.get(ctx, "dns-proxy", &dp); err != nil {
		return obs, err
	}
	obs.Routes = dp.Route
	var ifs map[string]json.RawMessage
	if err := b.get(ctx, "show/interface", &ifs); err == nil && len(ifs) > 0 {
		obs.Interfaces = map[string]bool{}
		for name := range ifs {
			obs.Interfaces[name] = true
		}
	}
	return obs, nil
}

// --- apply --------------------------------------------------------------------

func addrs(doms []string, no bool) []map[string]any {
	out := make([]map[string]any, 0, len(doms))
	for _, d := range doms {
		e := map[string]any{"address": d}
		if no {
			e["no"] = true
		}
		out = append(out, e)
	}
	return out
}

func fqdn(group string, body map[string]any) map[string]any {
	return map[string]any{"object-group": map[string]any{"fqdn": map[string]any{group: body}}}
}

// route: "auto" makes the firmware skip the route while the interface is
// down (traffic goes direct); without it traffic stays on the dead tunnel.
func route(group, iface string, no, block bool) map[string]any {
	r := map[string]any{"group": group, "interface": iface}
	if no {
		r["no"] = true
	} else if !block {
		r["auto"] = true
	}
	return map[string]any{"dns-proxy": map[string]any{"route": r}}
}

// Apply performs one op. Only nuxk-* objects are accepted — a last line of
// defence under plane.Plan, which never emits anything else.
func (b *Backend) Apply(ctx context.Context, op plane.Op) error {
	if op.Group != "" && !plane.Owned(op.Group) {
		return fmt.Errorf("refusing to touch %q: not a nuxk object", op.Group)
	}
	switch op.Kind {
	case plane.OpCreateGroup:
		if err := b.post(ctx, fqdn(op.Group, map[string]any{"include": addrs(op.Domains, false)})); err != nil {
			return err
		}
		// cosmetic, and not verified on every firmware: never fails the op
		// the old name on purpose: the groups already on routers carry it
		_ = b.post(ctx, fqdn(op.Group, map[string]any{"description": "nuxk Horizon — managed, do not edit"}))
		return nil
	case plane.OpAddDomains:
		return b.post(ctx, fqdn(op.Group, map[string]any{"include": addrs(op.Domains, false)}))
	case plane.OpDelDomains:
		return b.post(ctx, fqdn(op.Group, map[string]any{"include": addrs(op.Domains, true)}))
	case plane.OpAddRoute:
		return b.post(ctx, route(op.Group, op.Interface, false, op.Block))
	case plane.OpDelRoute:
		return b.post(ctx, route(op.Group, op.Interface, true, false))
	case plane.OpDeleteGroup:
		return b.post(ctx, fqdn(op.Group, map[string]any{"no": true}))
	case plane.OpEnsureV6Deny:
		return b.ensureV6Deny(ctx, op.Groups)
	}
	return fmt.Errorf("unknown op %q", op.Kind)
}

// V6Chain is nuxk's own ip6tables chain, jumped to from FORWARD.
const V6Chain = "NUXK_V6_DENY"

func v6Set(group string) string { return "_NDM_OGDN_6_@" + group }

// v6Rules is the wanted content of V6Chain for these groups: TCP gets a reset
// (instant fallback), everything else an ICMPv6 refusal.
func v6Rules(groups []string) []string {
	var out []string
	for _, g := range groups {
		s := v6Set(g)
		out = append(out,
			"-A "+V6Chain+" -p tcp -m set --match-set "+s+" dst -j REJECT --reject-with tcp-reset",
			"-A "+V6Chain+" -m set --match-set "+s+" dst -j REJECT --reject-with icmp6-adm-prohibited",
		)
	}
	return out
}

// ensureV6Deny makes V6Chain hold exactly the rules for the given groups and
// be jumped to from FORWARD. Sets the firmware hasn't created yet are skipped
// (next pass picks them up). Idempotent: rewrites only on a difference.
// Keenetic rebuilds its firewall and drops foreign rules — the next reconcile
// pass puts them back.
func (b *Backend) ensureV6Deny(ctx context.Context, groups []string) error {
	sets, _ := b.Run(ctx, "ipset", "list", "-n")
	var ready []string
	for _, g := range groups {
		if strings.Contains("\n"+sets+"\n", "\n"+v6Set(g)+"\n") {
			ready = append(ready, g)
		}
	}
	want := v6Rules(ready)

	cur, err := b.Run(ctx, "ip6tables", "-w", "-S", V6Chain)
	if err != nil {
		if len(want) == 0 {
			return nil // nothing to deny and nothing of ours to clean up
		}
		if _, err := b.Run(ctx, "ip6tables", "-w", "-N", V6Chain); err != nil {
			return fmt.Errorf("ip6tables -N %s: %w", V6Chain, err)
		}
		cur = ""
	}
	var have []string
	for _, l := range strings.Split(cur, "\n") {
		if strings.HasPrefix(l, "-A "+V6Chain+" ") {
			have = append(have, strings.TrimSpace(l))
		}
	}
	if strings.Join(have, "\n") != strings.Join(want, "\n") {
		if _, err := b.Run(ctx, "ip6tables", "-w", "-F", V6Chain); err != nil {
			return fmt.Errorf("ip6tables -F %s: %w", V6Chain, err)
		}
		for _, r := range want {
			if _, err := b.Run(ctx, "ip6tables", append([]string{"-w"}, strings.Fields(r)...)...); err != nil {
				return fmt.Errorf("ip6tables %s: %w", r, err)
			}
		}
	}
	if _, err := b.Run(ctx, "ip6tables", "-w", "-C", "FORWARD", "-j", V6Chain); err != nil {
		if _, err := b.Run(ctx, "ip6tables", "-w", "-I", "FORWARD", "1", "-j", V6Chain); err != nil {
			return fmt.Errorf("ip6tables -I FORWARD: %w", err)
		}
	}
	return nil
}

// --- DNS ----------------------------------------------------------------------

// NameServer adds (on) or takes back one server of the firmware's DNS proxy
// through RCI's command parser — only nuxk's own forwarder, on an address of
// this very router (KeeneticOS refuses loopback ones: the LAN address, in
// practice), never anyone else's. It lives in the running config: never
// saved, a reboot drops it and the agent adds it again.
func (b *Backend) NameServer(ctx context.Context, addr string, on bool) error {
	host, port, err := net.SplitHostPort(addr)
	ip := net.ParseIP(host)
	n, perr := strconv.Atoi(port)
	if err != nil || ip == nil || ip.To4() == nil || perr != nil || n <= 0 || n > 65535 || !b.local(ip) {
		return fmt.Errorf("refusing name-server %q: only nuxk's own, on this router's address", addr)
	}
	cmd := "ip name-server " + net.JoinHostPort(ip.String(), strconv.Itoa(n))
	if !on {
		cmd = "no " + cmd
	}
	return b.post(ctx, map[string]any{"parse": cmd})
}

// DNSHook adds nuxk's DNS forwarder to the DNS proxy (dns.Hook). Attach first
// takes back an old entry — after the agent's restart it's still there — so
// adding is never refused as a duplicate.
type DNSHook struct{ B *Backend }

func (h DNSHook) Attach(ctx context.Context, addr string) error {
	_ = h.B.NameServer(ctx, addr, false)
	return h.B.NameServer(ctx, addr, true)
}

func (h DNSHook) Detach(ctx context.Context, addr string) error {
	return h.B.NameServer(ctx, addr, false)
}

// local: ip is loopback or one of this box's own addresses.
func (b *Backend) local(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	addrs := b.Addrs
	if addrs == nil {
		addrs = net.InterfaceAddrs
	}
	as, err := addrs()
	if err != nil {
		return false
	}
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}
