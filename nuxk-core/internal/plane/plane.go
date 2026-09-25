// Package plane is nuxk's routing plane: which domains go which way.
//
// The data plane itself is not ours. On a Keenetic the firmware already binds
// DNS answers to routes ("маршрутизация по доменам": object-group fqdn +
// dns-proxy route → ipset → mark → table), in the kernel, at DNS-answer time.
// The plane only keeps the firmware's nuxk-* objects equal to the desired
// lists — observe, diff, apply — and never touches anything else.
package plane

import (
	"context"
	"sort"
	"strings"
)

// Mode is where a list's traffic goes. Desync is not a route (nfqws2 works on
// the WAN path), so only routed modes get firmware objects.
type Mode string

const (
	ModeWarp  Mode = "warp"  // usque tunnel (OpkgTun0)
	ModeVless Mode = "vless" // xray tunnel (OpkgTun1)
)

func (m Mode) Valid() bool { return m == ModeWarp || m == ModeVless }

// List is one desired domain list.
type List struct {
	Name    string   `json:"name"`             // shown in UI; free text
	Mode    Mode     `json:"mode"`             // warp | vless
	Domains []string `json:"domains"`          // hosts; subdomains are covered by the firmware
	Source  string   `json:"source,omitempty"` // manual | imported:<group> | preset:<id>
}

// Desired is the whole plane intent, stored as state/plane.json.
type Desired struct {
	Lists []List `json:"lists"`
}

// Group is the firmware-side object nuxk wants for one mode: one group per
// routed mode keeps the object count (and route count) tiny.
type Group struct {
	Name      string   `json:"name"`      // nuxk-<mode>
	Mode      Mode     `json:"mode"`      //
	Interface string   `json:"interface"` // route target
	Domains   []string `json:"domains"`   // normalised, deduplicated, subdomains folded
}

// GroupPrefix marks objects nuxk owns. Everything else is the user's.
const GroupPrefix = "nuxk-"

// Owned reports whether a firmware object name belongs to nuxk.
func Owned(name string) bool { return strings.HasPrefix(name, GroupPrefix) }

// Build turns desired lists into one firmware group per routed mode.
// ifaces maps a mode to its route target interface; a mode without an
// interface is skipped (its tunnel isn't installed yet).
func Build(d Desired, ifaces map[Mode]string) []Group {
	byMode := map[Mode][]string{}
	for _, l := range d.Lists {
		if l.Mode.Valid() {
			byMode[l.Mode] = append(byMode[l.Mode], l.Domains...)
		}
	}
	var out []Group
	for _, m := range []Mode{ModeWarp, ModeVless} {
		doms := Normalize(byMode[m])
		iface := ifaces[m]
		if len(doms) == 0 || iface == "" {
			continue
		}
		out = append(out, Group{Name: GroupPrefix + string(m), Mode: m, Interface: iface, Domains: doms})
	}
	return out
}

// Normalize lower-cases, trims, drops junk and duplicates, and folds a
// subdomain into its parent when the parent is present (the firmware covers
// subdomains itself — verified on the router, 25.09). Sorted output.
func Normalize(in []string) []string {
	set := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimSuffix(d, ".")
		if d == "" || strings.HasPrefix(d, "#") || !validHost(d) {
			continue
		}
		set[d] = true
	}
	var out []string
	for d := range set {
		if !hasParent(d, set) {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

func hasParent(d string, set map[string]bool) bool {
	for i := strings.IndexByte(d, '.'); i >= 0; {
		p := d[i+1:]
		if strings.Contains(p, ".") && set[p] {
			return true
		}
		j := strings.IndexByte(p, '.')
		if j < 0 {
			break
		}
		i += j + 1
	}
	return false
}

func validHost(d string) bool {
	if len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, r := range d {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return !strings.Contains(d, "..") && !strings.HasPrefix(d, ".") && !strings.HasPrefix(d, "-")
}

// Observed is what the firmware has right now.
type Observed struct {
	Groups map[string][]string // fqdn group name → includes (all groups, ours and the user's)
	Descr  map[string]string   // group name → description
	Routes []Route             // all dns-proxy routes
	// Interfaces known to the firmware; nil = backend can't tell (no check).
	Interfaces map[string]bool
}

// Route is one dns-proxy route (group → interface).
type Route struct {
	Group     string `json:"group"`
	Interface string `json:"interface"`
	Auto      bool   `json:"auto"`
	Index     string `json:"index,omitempty"`
}

// OpKind is one kind of firmware change.
type OpKind string

const (
	OpCreateGroup  OpKind = "create-group"
	OpAddDomains   OpKind = "add-domains"
	OpDelDomains   OpKind = "del-domains"
	OpAddRoute     OpKind = "add-route"
	OpDelRoute     OpKind = "del-route"
	OpDeleteGroup  OpKind = "delete-group"
	OpEnsureV6Deny OpKind = "ensure-v6-deny" // Keenetic doesn't route IPv6 for these groups: refuse it
)

// Op is one step of a plan. Ops run in order; the order is the safety.
type Op struct {
	Kind      OpKind   `json:"kind"`
	Group     string   `json:"group,omitempty"`
	Interface string   `json:"interface,omitempty"`
	Domains   []string `json:"domains,omitempty"`
	Groups    []string `json:"groups,omitempty"` // ensure-v6-deny: the full set of groups to deny
}

// Conflict is a domain nuxk wants that a user group already routes. Two
// routes for one name is undefined, so it is held back until the user
// removes it from their list (migration is list by list).
type Conflict struct {
	Domain    string `json:"domain"`
	Group     string `json:"group"`      // nuxk group that wants it
	UserGroup string `json:"user_group"` // user group that already has it
}

// Plan computes the ops that turn observed into desired. Only nuxk-* objects
// are ever created, changed or removed.
//
// Order: create/extend groups → add routes → v6 deny for the final set →
// drop stale routes → drop stale domains → delete stale groups (a group can't
// go while a route or our v6 rule still references it).
func Plan(want []Group, obs Observed, v6deny bool) ([]Op, []Conflict) {
	// who else already routes what
	userHas := map[string]string{}
	routed := map[string]bool{}
	for _, r := range obs.Routes {
		routed[r.Group] = true
	}
	for g, doms := range obs.Groups {
		if Owned(g) || !routed[g] {
			continue
		}
		for _, d := range doms {
			userHas[d] = g
		}
	}
	// d conflicts if the user routes d, a parent of d, or a subdomain of d
	coveredByUser := func(d string) string {
		for h, g := range userHas {
			if strings.HasSuffix(h, "."+d) {
				return g
			}
		}
		for h := d; ; {
			if g, ok := userHas[h]; ok {
				return g
			}
			i := strings.IndexByte(h, '.')
			if i < 0 || !strings.Contains(h[i+1:], ".") {
				return ""
			}
			h = h[i+1:]
		}
	}

	var ops, tail []Op
	var conflicts []Conflict
	wantNames := map[string]bool{}
	for _, g := range want {
		wantNames[g.Name] = true
		var doms []string
		for _, d := range g.Domains {
			if ug := coveredByUser(d); ug != "" {
				conflicts = append(conflicts, Conflict{Domain: d, Group: g.Name, UserGroup: ug})
				continue
			}
			doms = append(doms, d)
		}
		have, exists := obs.Groups[g.Name]
		switch {
		case !exists && len(doms) > 0:
			ops = append(ops, Op{Kind: OpCreateGroup, Group: g.Name, Domains: doms})
		case exists:
			add, del := diff(doms, have)
			if len(add) > 0 {
				ops = append(ops, Op{Kind: OpAddDomains, Group: g.Name, Domains: add})
			}
			if len(del) > 0 {
				tail = append(tail, Op{Kind: OpDelDomains, Group: g.Name, Domains: del})
			}
		}
		if len(doms) == 0 && !exists {
			continue
		}
		hasRoute := false
		for _, r := range obs.Routes {
			if r.Group != g.Name {
				continue
			}
			if r.Interface == g.Interface {
				hasRoute = true
			} else {
				tail = append(tail, Op{Kind: OpDelRoute, Group: r.Group, Interface: r.Interface})
			}
		}
		if !hasRoute {
			ops = append(ops, Op{Kind: OpAddRoute, Group: g.Name, Interface: g.Interface})
		}
	}

	// stale nuxk objects: routes first, then groups
	var stale []string
	for name := range obs.Groups {
		if Owned(name) && !wantNames[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		for _, r := range obs.Routes {
			if r.Group == name {
				tail = append(tail, Op{Kind: OpDelRoute, Group: r.Group, Interface: r.Interface})
			}
		}
	}

	// Always emitted: with v6deny off it carries no groups and only clears
	// rules of ours that would otherwise pin a stale group's ipset.
	var keep []string
	if v6deny {
		for _, g := range want {
			if _, exists := obs.Groups[g.Name]; exists || hasCreate(ops, g.Name) {
				keep = append(keep, g.Name)
			}
		}
	}
	ops = append(ops, Op{Kind: OpEnsureV6Deny, Groups: keep})
	ops = append(ops, tail...)
	for _, name := range stale {
		ops = append(ops, Op{Kind: OpDeleteGroup, Group: name})
	}
	return ops, conflicts
}

func hasCreate(ops []Op, g string) bool {
	for _, o := range ops {
		if o.Kind == OpCreateGroup && o.Group == g {
			return true
		}
	}
	return false
}

// diff returns what to add to have and what to drop from it to reach want.
func diff(want, have []string) (add, del []string) {
	w, h := map[string]bool{}, map[string]bool{}
	for _, d := range want {
		w[d] = true
	}
	for _, d := range have {
		h[d] = true
		if !w[d] {
			del = append(del, d)
		}
	}
	for _, d := range want {
		if !h[d] {
			add = append(add, d)
		}
	}
	sort.Strings(add)
	sort.Strings(del)
	return add, del
}

// Changes reports whether a plan does anything beyond the idempotent v6 check.
func Changes(ops []Op) int {
	n := 0
	for _, o := range ops {
		if o.Kind != OpEnsureV6Deny {
			n++
		}
	}
	return n
}

// Backend talks to one kind of data plane.
type Backend interface {
	Name() string
	Observe(ctx context.Context) (Observed, error)
	Apply(ctx context.Context, op Op) error
}
